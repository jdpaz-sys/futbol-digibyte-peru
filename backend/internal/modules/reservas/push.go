package reservas

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	webPushDefaultTimeout     = 10 * time.Second
	webPushMaxConcurrency     = 16
	webPushRecordSize         = 4096
	webPushTTLSeconds         = "3600"
	webPushVAPIDExpiry        = 12 * time.Hour
	webPushUncompressedPubLen = 65
	webPushAuthSecretLen      = 16
	webPushPrivKeyLen         = 32
)

// PushConfigResponse define la respuesta pública de GET /api/v1/public/push-config.
type PushConfigResponse struct {
	Enabled        bool   `json:"enabled"`
	VAPIDPublicKey string `json:"vapid_public_key"`
}

// PushMessage representa el contenido a despachar vía Web Push PWA a un usuario.
type PushMessage struct {
	UsuarioID  uuid.UUID  `json:"-"`
	ComplejoID *uuid.UUID `json:"complejo_id,omitempty"`
	ReservaID  *uuid.UUID `json:"reserva_id,omitempty"`
	Tipo       string     `json:"tipo"`
	Titulo     string     `json:"titulo"`
	Mensaje    string     `json:"mensaje"`
}

// PushSubscriptionRecord contiene las credenciales W3C necesarias para enviar un Web Push.
type PushSubscriptionRecord struct {
	ID       uuid.UUID
	Endpoint string
	P256dh   string
	Auth     string
}

// PushSubscriptionStore abstrae la lectura y limpieza de suscripciones Web Push en PostgreSQL.
type PushSubscriptionStore interface {
	ListActivePushSubscriptions(ctx context.Context, usuarioID uuid.UUID) ([]PushSubscriptionRecord, error)
	DeactivatePushSubscriptionByID(ctx context.Context, id uuid.UUID) error
	MarkPushSubscriptionSent(ctx context.Context, id uuid.UUID) error
}

// PushDispatcher define la interfaz ligera e inyectable para envío asíncrono de notificaciones Web Push.
type PushDispatcher interface {
	Enabled() bool
	VAPIDPublicKey() string
	DispatchAsync(ctx context.Context, msg PushMessage)
}

// WebPushConfig configura el despachador Web Push VAPID (RFC 8292 + RFC 8291 aes128gcm).
type WebPushConfig struct {
	Enabled        bool
	VAPIDPublicKey string
	VAPIDPrivate   string
	VAPIDSubject   string
	HTTPClient     *http.Client
	Now            func() time.Time
}

// WebPushDispatcher implementa PushDispatcher. Si Enabled es false (o faltan claves VAPID),
// opera como un no-op limpio sin errores ni tráfico de red.
type WebPushDispatcher struct {
	enabled    bool
	publicKey  string
	subject    string
	privateKey *ecdsa.PrivateKey
	store      PushSubscriptionStore
	httpClient *http.Client
	logger     *slog.Logger
	now        func() time.Time

	sem    chan struct{}
	mu     sync.RWMutex
	closed bool
	wg     sync.WaitGroup
}

// NewWebPushDispatcher construye un despachador Web Push. Si las claves VAPID faltan o son inválidas,
// degrada de forma segura a push deshabilitado (no-op) registrando un aviso en slog.
func NewWebPushDispatcher(cfg WebPushConfig, store PushSubscriptionStore, logger *slog.Logger) *WebPushDispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: webPushDefaultTimeout}
	}

	d := &WebPushDispatcher{
		store:      store,
		httpClient: client,
		logger:     logger,
		now:        nowFn,
		sem:        make(chan struct{}, webPushMaxConcurrency),
	}

	pubKey := strings.TrimSpace(cfg.VAPIDPublicKey)
	privRaw := strings.TrimSpace(cfg.VAPIDPrivate)
	subject := strings.TrimSpace(cfg.VAPIDSubject)

	if !cfg.Enabled || pubKey == "" || privRaw == "" || subject == "" {
		return d
	}

	ecdsaPriv, err := parseVAPIDPrivateKey(privRaw)
	if err != nil {
		logger.Warn("webpush_invalid_vapid_private_key_disabled", slog.String("error", err.Error()))
		return d
	}

	pubBytes, err := decodeBase64Flex(pubKey)
	if err != nil || len(pubBytes) != webPushUncompressedPubLen || pubBytes[0] != 0x04 {
		logger.Warn("webpush_invalid_vapid_public_key_disabled")
		return d
	}

	d.enabled = true
	d.publicKey = pubKey
	d.subject = subject
	d.privateKey = ecdsaPriv
	return d
}

// Enabled informa si Web Push VAPID se encuentra activo.
func (d *WebPushDispatcher) Enabled() bool {
	return d != nil && d.enabled
}

// VAPIDPublicKey devuelve la clave pública VAPID si está habilitado, o cadena vacía si está deshabilitado.
func (d *WebPushDispatcher) VAPIDPublicKey() string {
	if !d.Enabled() {
		return ""
	}
	return d.publicKey
}

// DispatchAsync encola el envío Web Push de forma asíncrona sin bloquear la petición HTTP ni el worker.
// Si Web Push está deshabilitado, es un no-op inmediato.
func (d *WebPushDispatcher) DispatchAsync(ctx context.Context, msg PushMessage) {
	if !d.Enabled() || d.store == nil || msg.UsuarioID == uuid.Nil {
		return
	}

	d.mu.RLock()
	if d.closed {
		d.mu.RUnlock()
		return
	}
	d.wg.Add(1)
	d.mu.RUnlock()

	baseCtx := context.WithoutCancel(ctx)
	go func() {
		defer d.wg.Done()

		sendCtx, cancel := context.WithTimeout(baseCtx, webPushDefaultTimeout)
		defer cancel()

		select {
		case d.sem <- struct{}{}:
			defer func() { <-d.sem }()
		case <-sendCtx.Done():
			return
		}

		d.dispatchToUser(sendCtx, msg)
	}()
}

// Wait detiene la aceptación de nuevos envíos y espera a que finalicen los envíos en curso o expire ctx.
func (d *WebPushDispatcher) Wait(ctx context.Context) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.closed = true
	d.mu.Unlock()

	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
	}
}

func (d *WebPushDispatcher) dispatchToUser(ctx context.Context, msg PushMessage) {
	subs, err := d.store.ListActivePushSubscriptions(ctx, msg.UsuarioID)
	if err != nil {
		d.logger.Warn("webpush_list_subscriptions_error",
			slog.String("usuario_id", msg.UsuarioID.String()),
			slog.String("error", err.Error()),
		)
		return
	}
	if len(subs) == 0 {
		return
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		d.logger.Warn("webpush_marshal_payload_error", slog.String("error", err.Error()))
		return
	}

	for _, sub := range subs {
		if ctx.Err() != nil {
			return
		}
		d.sendOne(ctx, sub, payload)
	}
}

func (d *WebPushDispatcher) sendOne(ctx context.Context, sub PushSubscriptionRecord, payload []byte) {
	encrypted, err := encryptWebPushAES128GCM(payload, sub.P256dh, sub.Auth)
	if err != nil {
		d.logger.Warn("webpush_encrypt_error",
			slog.String("subscription_id", sub.ID.String()),
			slog.String("error", err.Error()),
		)
		return
	}

	authHeader, err := d.buildVAPIDAuthHeader(sub.Endpoint)
	if err != nil {
		d.logger.Warn("webpush_vapid_sign_error",
			slog.String("subscription_id", sub.ID.String()),
			slog.String("error", err.Error()),
		)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(encrypted))
	if err != nil {
		d.logger.Warn("webpush_request_build_error",
			slog.String("subscription_id", sub.ID.String()),
			slog.String("error", err.Error()),
		)
		return
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("TTL", webPushTTLSeconds)
	req.Header.Set("Authorization", authHeader)

	resp, err := d.httpClient.Do(req)
	if err != nil {
		d.logger.Warn("webpush_send_error",
			slog.String("subscription_id", sub.ID.String()),
			slog.String("error", err.Error()),
		)
		return
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()

	switch {
	case resp.StatusCode == http.StatusGone:
		if deactErr := d.store.DeactivatePushSubscriptionByID(ctx, sub.ID); deactErr != nil {
			d.logger.Warn("webpush_deactivate_gone_error",
				slog.String("subscription_id", sub.ID.String()),
				slog.String("error", deactErr.Error()),
			)
		} else {
			d.logger.Info("webpush_subscription_gone_deactivated",
				slog.String("subscription_id", sub.ID.String()),
			)
		}
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		if markErr := d.store.MarkPushSubscriptionSent(ctx, sub.ID); markErr != nil {
			d.logger.Warn("webpush_mark_sent_error",
				slog.String("subscription_id", sub.ID.String()),
				slog.String("error", markErr.Error()),
			)
		}
	default:
		d.logger.Warn("webpush_unexpected_status",
			slog.String("subscription_id", sub.ID.String()),
			slog.Int("status", resp.StatusCode),
		)
	}
}

func (d *WebPushDispatcher) buildVAPIDAuthHeader(endpoint string) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("endpoint push inválido")
	}
	aud := parsed.Scheme + "://" + parsed.Host

	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"aud": aud,
		"exp": d.now().UTC().Add(webPushVAPIDExpiry).Unix(),
		"sub": d.subject,
	})

	signed, err := token.SignedString(d.privateKey)
	if err != nil {
		return "", fmt.Errorf("error firmando JWT VAPID ES256: %w", err)
	}
	return fmt.Sprintf("vapid t=%s, k=%s", signed, d.publicKey), nil
}

// encryptWebPushAES128GCM cifra el payload JSON conforme a RFC 8291 + RFC 8188 (aes128gcm).
func encryptWebPushAES128GCM(plaintext []byte, p256dhB64, authB64 string) ([]byte, error) {
	uaPubBytes, err := decodeBase64Flex(p256dhB64)
	if err != nil || len(uaPubBytes) != webPushUncompressedPubLen {
		return nil, errors.New("clave pública p256dh de suscripción inválida")
	}
	uaPub, err := ecdh.P256().NewPublicKey(uaPubBytes)
	if err != nil {
		return nil, fmt.Errorf("clave p256dh no válida en P-256: %w", err)
	}

	authSecret, err := decodeBase64Flex(authB64)
	if err != nil || len(authSecret) != webPushAuthSecretLen {
		return nil, errors.New("secreto auth de suscripción inválido")
	}

	asPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("error generando par efímero ECDH: %w", err)
	}
	asPubBytes := asPriv.PublicKey().Bytes()

	ecdhSecret, err := asPriv.ECDH(uaPub)
	if err != nil {
		return nil, fmt.Errorf("error calculando secreto compartido ECDH: %w", err)
	}

	prkKey, err := hkdf.Extract(sha256.New, ecdhSecret, authSecret)
	if err != nil {
		return nil, fmt.Errorf("error HKDF extract (auth): %w", err)
	}

	keyInfo := make([]byte, 0, len("WebPush: info\x00")+len(uaPubBytes)+len(asPubBytes))
	keyInfo = append(keyInfo, []byte("WebPush: info\x00")...)
	keyInfo = append(keyInfo, uaPubBytes...)
	keyInfo = append(keyInfo, asPubBytes...)

	ikm, err := hkdf.Expand(sha256.New, prkKey, string(keyInfo), 32)
	if err != nil {
		return nil, fmt.Errorf("error HKDF expand (ikm): %w", err)
	}

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("error generando salt aleatorio: %w", err)
	}

	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, fmt.Errorf("error HKDF extract (salt): %w", err)
	}

	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, fmt.Errorf("error HKDF expand (cek): %w", err)
	}

	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, fmt.Errorf("error HKDF expand (nonce): %w", err)
	}

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, fmt.Errorf("error creando cifrador AES-128: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("error creando GCM: %w", err)
	}

	record := make([]byte, len(plaintext)+1)
	copy(record, plaintext)
	record[len(plaintext)] = 0x02 // Delimitador final de registro RFC 8188

	ciphertext := aead.Seal(nil, nonce, record, nil)

	// Cabecera RFC 8188: salt (16) || rs (4) || idlen (1) || asPublic (65)
	header := make([]byte, 16+4+1+len(asPubBytes))
	copy(header[0:16], salt)
	binary.BigEndian.PutUint32(header[16:20], webPushRecordSize)
	header[20] = byte(len(asPubBytes))
	copy(header[21:], asPubBytes)

	out := make([]byte, 0, len(header)+len(ciphertext))
	out = append(out, header...)
	out = append(out, ciphertext...)
	return out, nil
}

func parseVAPIDPrivateKey(raw string) (*ecdsa.PrivateKey, error) {
	privBytes, err := decodeBase64Flex(raw)
	if err != nil || len(privBytes) != webPushPrivKeyLen {
		return nil, errors.New("la clave privada VAPID debe ser un escalar P-256 de 32 bytes en base64url")
	}

	ecdhPriv, err := ecdh.P256().NewPrivateKey(privBytes)
	if err != nil {
		return nil, fmt.Errorf("clave privada VAPID P-256 inválida: %w", err)
	}
	pubBytes := ecdhPriv.PublicKey().Bytes()

	return &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{
			Curve: elliptic.P256(),
			X:     new(big.Int).SetBytes(pubBytes[1:33]),
			Y:     new(big.Int).SetBytes(pubBytes[33:65]),
		},
		D: new(big.Int).SetBytes(privBytes),
	}, nil
}

func decodeBase64Flex(raw string) ([]byte, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errors.New("cadena base64 vacía")
	}
	for _, enc := range []*base64.Encoding{
		base64.RawURLEncoding,
		base64.URLEncoding,
		base64.RawStdEncoding,
		base64.StdEncoding,
	} {
		if b, err := enc.DecodeString(trimmed); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("formato base64 inválido")
}

// ---------- Repositorio PostgreSQL: Push Subscriptions ----------

func (r *PostgresRepository) ListActivePushSubscriptions(ctx context.Context, usuarioID uuid.UUID) ([]PushSubscriptionRecord, error) {
	const sql = `
		SELECT id, endpoint, p256dh, auth
		FROM push_subscriptions
		WHERE usuario_id = $1 AND activa = true
		ORDER BY updated_at DESC`

	rows, err := r.pool.Query(ctx, sql, usuarioID)
	if err != nil {
		return nil, fmt.Errorf("error listando suscripciones push activas: %w", err)
	}
	defer rows.Close()

	out := make([]PushSubscriptionRecord, 0)
	for rows.Next() {
		var rec PushSubscriptionRecord
		if scanErr := rows.Scan(&rec.ID, &rec.Endpoint, &rec.P256dh, &rec.Auth); scanErr != nil {
			return nil, fmt.Errorf("error escaneando suscripción push: %w", scanErr)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterando suscripciones push: %w", err)
	}
	return out, nil
}

func (r *PostgresRepository) DeactivatePushSubscriptionByID(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE push_subscriptions SET activa = false, updated_at = NOW() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("error desactivando suscripción push por ID: %w", err)
	}
	return nil
}

func (r *PostgresRepository) MarkPushSubscriptionSent(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE push_subscriptions SET ultimo_envio_at = NOW() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("error actualizando ultimo_envio_at de suscripción push: %w", err)
	}
	return nil
}
