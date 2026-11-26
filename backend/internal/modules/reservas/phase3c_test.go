package reservas

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	apphttp "github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/http"
)

type stubMaintenanceStore struct {
	mu                     sync.Mutex
	expiredDone            bool
	expiringDone           bool
	remindersDone          bool
	expiredBatch           []ExpiredSubscriptionResult
	expiringBatch          []PushMessage
	remindersBatch         []PushMessage
	expireCalls            int
	expiringCalls          int
	remindersCalls         int
	lastExpiringWithin     time.Duration
	lastRemindersWithin    time.Duration
}

func (s *stubMaintenanceStore) ExpireOverdueSubscriptions(_ context.Context, _ int) ([]ExpiredSubscriptionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireCalls++
	if s.expiredDone {
		return nil, nil
	}
	s.expiredDone = true
	return s.expiredBatch, nil
}

func (s *stubMaintenanceStore) NotifyExpiringSubscriptions(_ context.Context, within time.Duration, _ int) ([]PushMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expiringCalls++
	s.lastExpiringWithin = within
	if s.expiringDone {
		return nil, nil
	}
	s.expiringDone = true
	return s.expiringBatch, nil
}

func (s *stubMaintenanceStore) NotifyUpcomingMatchReminders(_ context.Context, within time.Duration, _ int) ([]PushMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.remindersCalls++
	s.lastRemindersWithin = within
	if s.remindersDone {
		return nil, nil
	}
	s.remindersDone = true
	return s.remindersBatch, nil
}

type spySlugInvalidator struct {
	mu    sync.Mutex
	slugs []string
	err   error
}

func (s *spySlugInvalidator) InvalidatePublicComplejo(_ context.Context, slug string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.slugs = append(s.slugs, slug)
	return s.err
}

type spyPushDispatcher struct {
	enabled bool
	pubKey  string
	mu      sync.Mutex
	msgs    []PushMessage
}

func (s *spyPushDispatcher) Enabled() bool          { return s.enabled }
func (s *spyPushDispatcher) VAPIDPublicKey() string { return s.pubKey }
func (s *spyPushDispatcher) DispatchAsync(_ context.Context, msg PushMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, msg)
}

type stubHoldExpirer struct {
	calls atomic.Int32
}

func (s *stubHoldExpirer) ExpireStaleHolds(_ context.Context, _ int) (int, error) {
	s.calls.Add(1)
	return 0, nil
}

func TestCU14MaintenanceCycleAndLifecycle(t *testing.T) {
	ctx := context.Background()
	dueno1 := uuid.New()
	dueno2 := uuid.New()
	jugador1 := uuid.New()
	comp1 := uuid.New()
	comp2 := uuid.New()
	res1 := uuid.New()

	maintStore := &stubMaintenanceStore{
		expiredBatch: []ExpiredSubscriptionResult{
			{
				ComplejoID: comp1,
				Slug:       "complejo-vencido",
				Push: PushMessage{
					UsuarioID:  dueno1,
					ComplejoID: &comp1,
					Tipo:       NotifSuscripcionVencida,
					Titulo:     "Tu suscripción ha vencido",
					Mensaje:    "Renueva tu plan",
				},
			},
		},
		expiringBatch: []PushMessage{
			{
				UsuarioID:  dueno2,
				ComplejoID: &comp2,
				Tipo:       NotifSuscripcionPorVencer,
				Titulo:     "Tu suscripción vence pronto",
				Mensaje:    "Vence en 2 días",
			},
		},
		remindersBatch: []PushMessage{
			{
				UsuarioID:  jugador1,
				ComplejoID: &comp2,
				ReservaID:  &res1,
				Tipo:       NotifRecordatorioPartido,
				Titulo:     "Tu partido comienza pronto",
				Mensaje:    "Recuerda tu QR",
			},
		},
	}

	inv := &spySlugInvalidator{err: errors.New("valkey timeout simulado")}
	pushSpy := &spyPushDispatcher{enabled: true, pubKey: "pub"}
	silentLogger := slog.New(slog.NewTextHandler(io.Discard, nil))

	worker := NewExpirationWorker(&stubHoldExpirer{}, 20*time.Millisecond, silentLogger).
		WithMaintenance(maintStore, 25*time.Millisecond).
		WithPublicCacheInvalidator(inv).
		WithPushDispatcher(pushSpy)

	// 1. Primera ejecución: procesa los 3 bloques y tolera fallo puntual de Valkey al invalidar
	res, err := worker.RunMaintenanceOnce(ctx)
	if err != nil {
		t.Fatalf("RunMaintenanceOnce falló: %v", err)
	}
	if res.SuscripcionesVencidas != 1 || res.AvisosVencimiento != 1 || res.RecordatoriosPartido != 1 {
		t.Fatalf("resultados CU-14 inesperados: %+v", res)
	}
	if len(inv.slugs) != 1 || inv.slugs[0] != "complejo-vencido" {
		t.Fatalf("esperaba invalidación de complejo-vencido, obtuvo %v", inv.slugs)
	}
	if len(pushSpy.msgs) != 3 {
		t.Fatalf("esperaba 3 notificaciones push despachadas, obtuvo %d", len(pushSpy.msgs))
	}
	if maintStore.lastExpiringWithin != 3*24*time.Hour {
		t.Fatalf("ventana aviso vencimiento = %v, esperado 72h", maintStore.lastExpiringWithin)
	}
	if maintStore.lastRemindersWithin != 2*time.Hour {
		t.Fatalf("ventana recordatorio partido = %v, esperado 2h", maintStore.lastRemindersWithin)
	}

	// 2. Segunda ejecución: idempotente (cero duplicados)
	res2, err := worker.RunMaintenanceOnce(ctx)
	if err != nil {
		t.Fatalf("RunMaintenanceOnce #2 falló: %v", err)
	}
	if res2.SuscripcionesVencidas != 0 || res2.AvisosVencimiento != 0 || res2.RecordatoriosPartido != 0 {
		t.Fatalf("segunda ejecución debía ser idempotente (0 nuevos), obtuvo: %+v", res2)
	}

	// 3. Ciclo Start con cancelación limpia por context (verifica -race)
	runCtx, cancel := context.WithTimeout(ctx, 60*time.Millisecond)
	defer cancel()
	worker.Start(runCtx)
}

type memoryPushStore struct {
	mu          sync.Mutex
	subsByUser  map[uuid.UUID][]PushSubscriptionRecord
	deactivated map[uuid.UUID]bool
	sent        map[uuid.UUID]bool
}

func (m *memoryPushStore) ListActivePushSubscriptions(_ context.Context, usuarioID uuid.UUID) ([]PushSubscriptionRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]PushSubscriptionRecord(nil), m.subsByUser[usuarioID]...), nil
}

func (m *memoryPushStore) DeactivatePushSubscriptionByID(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deactivated[id] = true
	return nil
}

func (m *memoryPushStore) MarkPushSubscriptionSent(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent[id] = true
	return nil
}

func generateTestVAPIDKeys(t *testing.T) (pubB64, privB64 string, ecdsaPub *ecdsa.PublicKey) {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey P-256 falló: %v", err)
	}
	pubBytes := priv.PublicKey().Bytes()
	privBytes := priv.Bytes()
	ecdsaPub = &ecdsa.PublicKey{
		Curve: elliptic.P256(),
		X:     new(big.Int).SetBytes(pubBytes[1:33]),
		Y:     new(big.Int).SetBytes(pubBytes[33:65]),
	}
	return base64.RawURLEncoding.EncodeToString(pubBytes), base64.RawURLEncoding.EncodeToString(privBytes), ecdsaPub
}

func generateTestSubscriberKeys(t *testing.T) (p256dhB64, authB64 string) {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey suscripción falló: %v", err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatalf("rand auth falló: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes()), base64.RawURLEncoding.EncodeToString(auth)
}

func TestWebPushDispatcherAndPublicPushConfigEndpoint(t *testing.T) {
	silentLogger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("deshabilitado es no-op limpio y /public/push-config devuelve enabled=false", func(t *testing.T) {
		dispatcher := NewWebPushDispatcher(WebPushConfig{Enabled: false}, nil, silentLogger)
		if dispatcher.Enabled() || dispatcher.VAPIDPublicKey() != "" {
			t.Fatalf("dispatcher debía estar deshabilitado")
		}
		dispatcher.DispatchAsync(context.Background(), PushMessage{UsuarioID: uuid.New(), Titulo: "noop"})
		dispatcher.Wait(context.Background())

		svc := NewService(nil, nil, silentLogger).WithPushDispatcher(dispatcher)
		h := NewHandler(svc, nil, silentLogger)
		router := apphttp.NewRouter(apphttp.RouterConfig{
			Logger:      silentLogger,
			MountPublic: func(public chi.Router) { h.RegisterPublicRoutes(public) },
		})

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/public/push-config", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /public/push-config status=%d body=%s", rec.Code, rec.Body.String())
		}
		var env struct {
			Data PushConfigResponse `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("JSON inválido: %v", err)
		}
		if env.Data.Enabled || env.Data.VAPIDPublicKey != "" {
			t.Fatalf("esperaba enabled=false y vapid_public_key vacío, obtuvo %+v", env.Data)
		}
	})

	t.Run("habilitado firma VAPID ES256, cifra aes128gcm y desactiva 410 Gone", func(t *testing.T) {
		pubB64, privB64, ecdsaPub := generateTestVAPIDKeys(t)
		p256dhOK, authOK := generateTestSubscriberKeys(t)
		p256dhGone, authGone := generateTestSubscriberKeys(t)

		var reqCount atomic.Int32
		var authCheckErr atomic.Value

		pushServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqCount.Add(1)
			if r.Header.Get("Content-Encoding") != "aes128gcm" {
				authCheckErr.Store("Content-Encoding debía ser aes128gcm")
			}
			authHdr := r.Header.Get("Authorization")
			if !strings.HasPrefix(authHdr, "vapid t=") || !strings.Contains(authHdr, ", k="+pubB64) {
				authCheckErr.Store("cabecera VAPID inválida: " + authHdr)
			} else {
				tokenPart := strings.TrimPrefix(strings.Split(authHdr, ",")[0], "vapid t=")
				parsed, err := jwt.Parse(tokenPart, func(tok *jwt.Token) (any, error) {
					if tok.Method.Alg() != jwt.SigningMethodES256.Alg() {
						return nil, errors.New("algoritmo inesperado")
					}
					return ecdsaPub, nil
				})
				if err != nil || !parsed.Valid {
					authCheckErr.Store("firma JWT VAPID inválida")
				}
			}
			bodyBytes, _ := io.ReadAll(r.Body)
			if len(bodyBytes) <= 86 {
				authCheckErr.Store("payload cifrado aes128gcm demasiado corto")
			}

			if strings.HasSuffix(r.URL.Path, "/gone") {
				w.WriteHeader(http.StatusGone)
				return
			}
			w.WriteHeader(http.StatusCreated)
		}))
		defer pushServer.Close()

		userID := uuid.New()
		subOKID := uuid.New()
		subGoneID := uuid.New()
		store := &memoryPushStore{
			subsByUser: map[uuid.UUID][]PushSubscriptionRecord{
				userID: {
					{ID: subOKID, Endpoint: pushServer.URL + "/ok", P256dh: p256dhOK, Auth: authOK},
					{ID: subGoneID, Endpoint: pushServer.URL + "/gone", P256dh: p256dhGone, Auth: authGone},
				},
			},
			deactivated: make(map[uuid.UUID]bool),
			sent:        make(map[uuid.UUID]bool),
		}

		dispatcher := NewWebPushDispatcher(WebPushConfig{
			Enabled:        true,
			VAPIDPublicKey: pubB64,
			VAPIDPrivate:   privB64,
			VAPIDSubject:   "mailto:soporte@digibyte.pe",
			HTTPClient:     pushServer.Client(),
		}, store, silentLogger)

		if !dispatcher.Enabled() || dispatcher.VAPIDPublicKey() != pubB64 {
			t.Fatalf("dispatcher debía estar habilitado con pubKey=%s", pubB64)
		}

		// Verifica GET /api/v1/public/push-config habilitado
		svc := NewService(nil, nil, silentLogger).WithPushDispatcher(dispatcher)
		h := NewHandler(svc, nil, silentLogger)
		router := apphttp.NewRouter(apphttp.RouterConfig{
			Logger:      silentLogger,
			MountPublic: func(public chi.Router) { h.RegisterPublicRoutes(public) },
		})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/public/push-config", nil))
		var env struct {
			Data PushConfigResponse `json:"data"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		if !env.Data.Enabled || env.Data.VAPIDPublicKey != pubB64 {
			t.Fatalf("push-config habilitado inesperado: %+v", env.Data)
		}

		// Despacho asíncrono + espera limpia
		dispatcher.DispatchAsync(context.Background(), PushMessage{
			UsuarioID: userID,
			Tipo:      NotifRecordatorioPartido,
			Titulo:    "Tu partido comienza pronto",
			Mensaje:   "Ten listo tu QR",
		})
		waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		dispatcher.Wait(waitCtx)

		if v := authCheckErr.Load(); v != nil {
			t.Fatalf("falló verificación de petición Web Push: %v", v)
		}
		if reqCount.Load() != 2 {
			t.Fatalf("esperaba 2 envíos Web Push, obtuvo %d", reqCount.Load())
		}
		if !store.sent[subOKID] {
			t.Fatal("la suscripción 201 Created debía actualizar ultimo_envio_at")
		}
		if !store.deactivated[subGoneID] {
			t.Fatal("la suscripción 410 Gone debía desactivarse automáticamente")
		}
	})
}
