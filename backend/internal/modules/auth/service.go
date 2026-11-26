package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/argon2"

	apphttp "github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/http"
)

const (
	jwtIssuer            = "futbol-digibyte-peru"
	jwtTokenTypeAccess   = "access"
	refreshKeyPrefix     = "auth:refresh:"
	userSessionsPrefix   = "auth:user_sessions:"
	minPasswordLength    = 8
	maxPasswordLength    = 128
	argonMemoryKiB       = 47104 // 46 MiB (OWASP Argon2id)
	argonIterations      = 1
	argonParallelism     = 1
	argonSaltLength      = 16
	argonKeyLength       = 32
	refreshTokenNumBytes = 32
)

// dummyArgon2Hash se usa en login fallido por usuario inexistente para reducir
// diferencias de tiempo (mitigación de enumeración de correos).
var dummyArgon2Hash string

func init() {
	hash, err := HashPassword("dummy-password-for-timing-mitigation")
	if err != nil {
		panic(fmt.Sprintf("error inicializando hash dummy argon2id: %v", err))
	}
	dummyArgon2Hash = hash
}

// SessionStore abstrae el almacenamiento de Refresh Tokens opacos en Valkey.
type SessionStore interface {
	SaveRefreshToken(ctx context.Context, tokenHash string, userID uuid.UUID, ttl time.Duration) error
	ConsumeRefreshToken(ctx context.Context, tokenHash string) (uuid.UUID, error)
	DeleteRefreshToken(ctx context.Context, tokenHash string) error
	RevokeAllUserRefreshTokens(ctx context.Context, userID uuid.UUID) error
}

// ValkeySessionStore almacena sesiones de refresh en Valkey con TTL automático
// y un índice secundario por usuario para revocación masiva (ej. cambio de contraseña).
type ValkeySessionStore struct {
	client *redis.Client
}

// NewValkeySessionStore crea un almacén de sesiones respaldado por Valkey.
func NewValkeySessionStore(client *redis.Client) *ValkeySessionStore {
	return &ValkeySessionStore{client: client}
}

func (s *ValkeySessionStore) SaveRefreshToken(ctx context.Context, tokenHash string, userID uuid.UUID, ttl time.Duration) error {
	key := refreshKeyPrefix + tokenHash
	userSetKey := userSessionsPrefix + userID.String()

	pipe := s.client.Pipeline()
	pipe.Set(ctx, key, userID.String(), ttl)
	pipe.SAdd(ctx, userSetKey, tokenHash)
	pipe.Expire(ctx, userSetKey, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("error guardando refresh token en Valkey: %w", err)
	}
	return nil
}

// ConsumeRefreshToken lee y elimina atómicamente (GETDEL) el refresh token para garantizar rotación de un solo uso.
func (s *ValkeySessionStore) ConsumeRefreshToken(ctx context.Context, tokenHash string) (uuid.UUID, error) {
	key := refreshKeyPrefix + tokenHash
	rawUserID, err := s.client.GetDel(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return uuid.Nil, ErrInvalidToken
		}
		return uuid.Nil, fmt.Errorf("error consumiendo refresh token en Valkey: %w", err)
	}

	userID, err := uuid.Parse(rawUserID)
	if err != nil {
		return uuid.Nil, ErrInvalidToken
	}

	_ = s.client.SRem(ctx, userSessionsPrefix+userID.String(), tokenHash).Err()
	return userID, nil
}

func (s *ValkeySessionStore) DeleteRefreshToken(ctx context.Context, tokenHash string) error {
	key := refreshKeyPrefix + tokenHash
	rawUserID, err := s.client.GetDel(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil
		}
		return fmt.Errorf("error revocando refresh token en Valkey: %w", err)
	}
	if strings.TrimSpace(rawUserID) != "" {
		_ = s.client.SRem(ctx, userSessionsPrefix+strings.TrimSpace(rawUserID), tokenHash).Err()
	}
	return nil
}

// RevokeAllUserRefreshTokens invalida todos los refresh tokens activos de un usuario en Valkey.
func (s *ValkeySessionStore) RevokeAllUserRefreshTokens(ctx context.Context, userID uuid.UUID) error {
	userSetKey := userSessionsPrefix + userID.String()
	hashes, err := s.client.SMembers(ctx, userSetKey).Result()
	if err != nil {
		return fmt.Errorf("error listando sesiones activas en Valkey: %w", err)
	}

	keys := make([]string, 0, len(hashes)+1)
	for _, h := range hashes {
		if strings.TrimSpace(h) != "" {
			keys = append(keys, refreshKeyPrefix+h)
		}
	}
	keys = append(keys, userSetKey)

	if err := s.client.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("error revocando sesiones activas en Valkey: %w", err)
	}
	return nil
}

// AccessClaims define las claims JWT del Access Token.
type AccessClaims struct {
	Rol        string  `json:"rol"`
	ComplejoID *string `json:"complejo_id,omitempty"`
	TokenType  string  `json:"typ"`
	jwt.RegisteredClaims
}

// Service encapsula la lógica de autenticación, contraseñas, JWT y sesiones en Valkey.
type Service struct {
	repo       Repository
	sessions   SessionStore
	jwtSecret  []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
	logger     *slog.Logger
	now        func() time.Time
}

// NewService crea una instancia del servicio de autenticación.
func NewService(repo Repository, sessions SessionStore, jwtSecret string, accessTTL, refreshTTL time.Duration) *Service {
	return &Service{
		repo:       repo,
		sessions:   sessions,
		jwtSecret:  []byte(jwtSecret),
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
		logger:     slog.Default(),
		now:        time.Now,
	}
}

// WithLogger configura un logger estructurado para avisos operativos del módulo auth.
func (s *Service) WithLogger(logger *slog.Logger) *Service {
	if logger != nil {
		s.logger = logger
	}
	return s
}

// Register registra un nuevo JUGADOR con correo normalizado, password Argon2id y teléfono WhatsApp.
func (s *Service) Register(ctx context.Context, req RegisterRequest) (UserProfile, error) {
	email := NormalizeEmail(req.Email)
	nombre := strings.TrimSpace(req.NombreCompleto)
	telefono := strings.TrimSpace(req.Telefono)

	fields := make(map[string]string)
	if !ValidateEmail(email) {
		fields["email"] = "Ingresa un correo electrónico válido"
	}
	if errMsg := ValidatePassword(req.Password); errMsg != "" {
		fields["password"] = errMsg
	}
	if len([]rune(nombre)) < 2 || len([]rune(nombre)) > 150 {
		fields["nombre_completo"] = "El nombre completo debe tener entre 2 y 150 caracteres"
	}
	if !ValidatePhone(telefono) {
		fields["telefono"] = "Ingresa un número de celular WhatsApp válido (7 a 20 caracteres)"
	}
	if len(fields) > 0 {
		return UserProfile{}, &ValidationError{
			Message: "Revisa los datos de registro",
			Fields:  fields,
		}
	}

	passwordHash, err := HashPassword(req.Password)
	if err != nil {
		return UserProfile{}, fmt.Errorf("error generando hash de contraseña: %w", err)
	}

	user, err := s.repo.CreateJugador(ctx, email, passwordHash, nombre, telefono)
	if err != nil {
		return UserProfile{}, err
	}

	return toUserProfile(user, nil), nil
}

// Login valida credenciales, actualiza ultimo_login_at y emite Access Token JWT + Refresh Token en Valkey.
func (s *Service) Login(ctx context.Context, req LoginRequest) (AuthTokensResponse, error) {
	email := NormalizeEmail(req.Email)

	fields := make(map[string]string)
	if email == "" {
		fields["email"] = "El correo electrónico es obligatorio"
	}
	if req.Password == "" {
		fields["password"] = "La contraseña es obligatoria"
	}
	if len(fields) > 0 {
		return AuthTokensResponse{}, &ValidationError{
			Message: "Credenciales incompletas",
			Fields:  fields,
		}
	}

	user, err := s.repo.FindByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			_, _ = VerifyPassword(req.Password, dummyArgon2Hash)
			return AuthTokensResponse{}, ErrInvalidCredentials
		}
		return AuthTokensResponse{}, err
	}

	valid, err := VerifyPassword(req.Password, user.PasswordHash)
	if err != nil || !valid {
		return AuthTokensResponse{}, ErrInvalidCredentials
	}

	if !user.Activo {
		return AuthTokensResponse{}, ErrAccountInactive
	}

	lastLogin, err := s.repo.UpdateLastLogin(ctx, user.ID)
	if err != nil {
		return AuthTokensResponse{}, err
	}
	user.UltimoLoginAt = &lastLogin

	return s.issueSession(ctx, user)
}

// Refresh valida y rota atómicamente el Refresh Token en Valkey emitiendo un nuevo par de tokens.
func (s *Service) Refresh(ctx context.Context, req RefreshRequest) (AuthTokensResponse, error) {
	rawToken := strings.TrimSpace(req.RefreshToken)
	if rawToken == "" {
		return AuthTokensResponse{}, &ValidationError{
			Message: "El refresh_token es obligatorio",
			Fields:  map[string]string{"refresh_token": "El refresh_token es obligatorio"},
		}
	}

	userID, err := s.sessions.ConsumeRefreshToken(ctx, hashToken(rawToken))
	if err != nil {
		return AuthTokensResponse{}, err
	}

	user, err := s.repo.FindByID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return AuthTokensResponse{}, ErrInvalidToken
		}
		return AuthTokensResponse{}, err
	}

	if !user.Activo {
		return AuthTokensResponse{}, ErrAccountInactive
	}

	return s.issueSession(ctx, user)
}

// Logout revoca el Refresh Token en Valkey de manera idempotente.
func (s *Service) Logout(ctx context.Context, req LogoutRequest) error {
	rawToken := strings.TrimSpace(req.RefreshToken)
	if rawToken == "" {
		return &ValidationError{
			Message: "El refresh_token es obligatorio",
			Fields:  map[string]string{"refresh_token": "El refresh_token es obligatorio"},
		}
	}

	return s.sessions.DeleteRefreshToken(ctx, hashToken(rawToken))
}

// Me obtiene el perfil actualizado del usuario autenticado (nunca expone password_hash).
func (s *Service) Me(ctx context.Context, userID uuid.UUID) (UserProfile, error) {
	user, err := s.repo.FindByID(ctx, userID)
	if err != nil {
		return UserProfile{}, err
	}
	if !user.Activo {
		return UserProfile{}, ErrAccountInactive
	}

	complejoID, err := s.resolveComplejoID(ctx, user)
	if err != nil {
		return UserProfile{}, err
	}

	return toUserProfile(user, complejoID), nil
}

// UpdateProfile actualiza nombre_completo y/o telefono del usuario autenticado con validación estricta.
func (s *Service) UpdateProfile(ctx context.Context, userID uuid.UUID, req UpdateProfileRequest) (UserProfile, error) {
	if req.NombreCompleto == nil && req.Telefono == nil {
		return UserProfile{}, &ValidationError{
			Message: "Debe enviar al menos un campo para actualizar el perfil",
			Fields:  map[string]string{"body": "Envía nombre_completo y/o telefono"},
		}
	}

	user, err := s.repo.FindByID(ctx, userID)
	if err != nil {
		return UserProfile{}, err
	}
	if !user.Activo {
		return UserProfile{}, ErrAccountInactive
	}

	nombre := user.NombreCompleto
	telefono := user.Telefono
	fields := make(map[string]string)

	if req.NombreCompleto != nil {
		trimmed := strings.TrimSpace(*req.NombreCompleto)
		if n := utf8.RuneCountInString(trimmed); n < 2 || n > 150 {
			fields["nombre_completo"] = "El nombre completo debe tener entre 2 y 150 caracteres"
		} else {
			nombre = trimmed
		}
	}

	if req.Telefono != nil {
		trimmed := strings.TrimSpace(*req.Telefono)
		if trimmed == "" {
			if user.Rol == apphttp.RoleSuperAdmin {
				telefono = nil
			} else {
				fields["telefono"] = "El número de celular WhatsApp es obligatorio"
			}
		} else if !ValidatePhone(trimmed) {
			fields["telefono"] = "Ingresa un número de celular WhatsApp válido (7 a 20 caracteres)"
		} else {
			t := trimmed
			telefono = &t
		}
	}

	if len(fields) > 0 {
		return UserProfile{}, &ValidationError{
			Message: "Revisa los datos de perfil",
			Fields:  fields,
		}
	}

	updated, err := s.repo.UpdateProfile(ctx, userID, nombre, telefono)
	if err != nil {
		return UserProfile{}, err
	}

	complejoID, err := s.resolveComplejoID(ctx, updated)
	if err != nil {
		return UserProfile{}, err
	}

	return toUserProfile(updated, complejoID), nil
}

// ChangePassword verifica la contraseña actual con Argon2id, valida la nueva política de contraseña,
// actualiza password_hash en PostgreSQL y revoca todos los refresh tokens activos en Valkey.
// Nota: los access tokens JWT emitidos previamente son stateless de corta duración (JWT_ACCESS_TTL)
// y expirarán naturalmente; ningún dispositivo podrá refrescar sesión tras el cambio.
func (s *Service) ChangePassword(ctx context.Context, userID uuid.UUID, req ChangePasswordRequest) (ChangePasswordResponse, error) {
	fields := make(map[string]string)
	if req.PasswordActual == "" {
		fields["password_actual"] = "La contraseña actual es obligatoria"
	}
	if errMsg := ValidatePassword(req.PasswordNueva); errMsg != "" {
		fields["password_nueva"] = errMsg
	} else if req.PasswordActual != "" && req.PasswordNueva == req.PasswordActual {
		fields["password_nueva"] = "La nueva contraseña debe ser diferente a la contraseña actual"
	}
	if len(fields) > 0 {
		return ChangePasswordResponse{}, &ValidationError{
			Message: "Revisa los datos para cambiar tu contraseña",
			Fields:  fields,
		}
	}

	user, err := s.repo.FindByID(ctx, userID)
	if err != nil {
		return ChangePasswordResponse{}, err
	}
	if !user.Activo {
		return ChangePasswordResponse{}, ErrAccountInactive
	}

	valid, err := VerifyPassword(req.PasswordActual, user.PasswordHash)
	if err != nil || !valid {
		return ChangePasswordResponse{}, ErrInvalidCurrentPassword
	}

	newHash, err := HashPassword(req.PasswordNueva)
	if err != nil {
		return ChangePasswordResponse{}, fmt.Errorf("error generando hash Argon2id: %w", err)
	}

	if err := s.repo.UpdatePasswordHash(ctx, userID, newHash); err != nil {
		return ChangePasswordResponse{}, err
	}

	revoked := false
	if s.sessions != nil {
		revokeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if revokeErr := s.sessions.RevokeAllUserRefreshTokens(revokeCtx, userID); revokeErr != nil {
			if s.logger != nil {
				s.logger.Warn("auth_change_password_revoke_sessions_error",
					slog.String("user_id", userID.String()),
					slog.String("error", revokeErr.Error()),
				)
			}
		} else {
			revoked = true
		}
	}

	msg := "Contraseña actualizada correctamente. Se revocaron las sesiones activas."
	if !revoked {
		msg = "Contraseña actualizada correctamente."
	}

	return ChangePasswordResponse{
		Message:           msg,
		SesionesRevocadas: revoked,
	}, nil
}

// GenerateAccessToken firma un JWT HS256 de corta duración con {UserID, Rol, ComplejoID}.
func (s *Service) GenerateAccessToken(userID uuid.UUID, rol string, complejoID *uuid.UUID) (string, error) {
	now := s.now().UTC()

	var complejoStr *string
	if complejoID != nil {
		v := complejoID.String()
		complejoStr = &v
	}

	claims := AccessClaims{
		Rol:        rol,
		ComplejoID: complejoStr,
		TokenType:  jwtTokenTypeAccess,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    jwtIssuer,
			Subject:   userID.String(),
			ID:        uuid.NewString(),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.accessTTL)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(s.jwtSecret)
	if err != nil {
		return "", fmt.Errorf("error firmando access token JWT: %w", err)
	}
	return signed, nil
}

// ParseAccessToken valida firma HS256, vigencia, emisor y claims del Access Token JWT.
func (s *Service) ParseAccessToken(tokenString string) (apphttp.AuthUser, error) {
	claims := &AccessClaims{}

	parsed, err := jwt.ParseWithClaims(
		tokenString,
		claims,
		func(token *jwt.Token) (any, error) {
			if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
				return nil, fmt.Errorf("algoritmo JWT no permitido: %s", token.Method.Alg())
			}
			return s.jwtSecret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(jwtIssuer),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(s.now),
	)
	if err != nil || !parsed.Valid {
		return apphttp.AuthUser{}, ErrInvalidToken
	}

	if claims.TokenType != jwtTokenTypeAccess {
		return apphttp.AuthUser{}, ErrInvalidToken
	}
	if !isValidRole(claims.Rol) {
		return apphttp.AuthUser{}, ErrInvalidToken
	}

	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return apphttp.AuthUser{}, ErrInvalidToken
	}

	var complejoID *uuid.UUID
	if claims.ComplejoID != nil && strings.TrimSpace(*claims.ComplejoID) != "" {
		cid, parseErr := uuid.Parse(*claims.ComplejoID)
		if parseErr != nil {
			return apphttp.AuthUser{}, ErrInvalidToken
		}
		complejoID = &cid
	}

	return apphttp.AuthUser{
		UserID:     userID,
		Rol:        claims.Rol,
		ComplejoID: complejoID,
	}, nil
}

func (s *Service) issueSession(ctx context.Context, user User) (AuthTokensResponse, error) {
	complejoID, err := s.resolveComplejoID(ctx, user)
	if err != nil {
		return AuthTokensResponse{}, err
	}

	accessToken, err := s.GenerateAccessToken(user.ID, user.Rol, complejoID)
	if err != nil {
		return AuthTokensResponse{}, err
	}

	refreshToken, err := generateOpaqueToken()
	if err != nil {
		return AuthTokensResponse{}, fmt.Errorf("error generando refresh token: %w", err)
	}

	if err := s.sessions.SaveRefreshToken(ctx, hashToken(refreshToken), user.ID, s.refreshTTL); err != nil {
		return AuthTokensResponse{}, err
	}

	return AuthTokensResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    int64(s.accessTTL.Seconds()),
		User:         toUserProfile(user, complejoID),
	}, nil
}

func (s *Service) resolveComplejoID(ctx context.Context, user User) (*uuid.UUID, error) {
	if user.Rol != apphttp.RoleDueno {
		return nil, nil
	}
	return s.repo.FindPrimaryComplejoIDByOwner(ctx, user.ID)
}

// HashPassword genera un hash Argon2id en formato estándar PHC.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("error generando salt aleatorio: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, argonIterations, argonMemoryKiB, argonParallelism, argonKeyLength)

	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonIterations, argonParallelism, b64Salt, b64Hash,
	), nil
}

// VerifyPassword compara en tiempo constante una contraseña contra un hash Argon2id en formato PHC.
func VerifyPassword(password, encodedHash string) (bool, error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("formato de hash argon2id inválido")
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errors.New("versión de argon2id no soportada")
	}

	var memory uint32
	var iterations uint32
	var parallelism uint8
	if err := parseArgonParams(parts[3], &memory, &iterations, &parallelism); err != nil {
		return false, err
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, errors.New("salt inválido en hash argon2id")
	}

	expectedHash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expectedHash) == 0 {
		return false, errors.New("digest inválido en hash argon2id")
	}

	derived := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(expectedHash)))
	return subtle.ConstantTimeCompare(derived, expectedHash) == 1, nil
}

func parseArgonParams(raw string, memory, iterations *uint32, parallelism *uint8) error {
	kv := strings.Split(raw, ",")
	if len(kv) != 3 {
		return errors.New("parámetros argon2id inválidos")
	}

	var hasM, hasT, hasP bool
	for _, item := range kv {
		pair := strings.SplitN(item, "=", 2)
		if len(pair) != 2 {
			return errors.New("parámetro argon2id malformado")
		}
		val, err := strconv.ParseUint(pair[1], 10, 32)
		if err != nil || val == 0 {
			return errors.New("valor de parámetro argon2id inválido")
		}
		switch pair[0] {
		case "m":
			*memory = uint32(val)
			hasM = true
		case "t":
			*iterations = uint32(val)
			hasT = true
		case "p":
			if val > 255 {
				return errors.New("paralelismo argon2id fuera de rango")
			}
			*parallelism = uint8(val)
			hasP = true
		default:
			return errors.New("parámetro argon2id desconocido")
		}
	}

	if !hasM || !hasT || !hasP {
		return errors.New("parámetros argon2id incompletos")
	}
	return nil
}

// NormalizeEmail recorta espacios y convierte el correo a minúsculas.
func NormalizeEmail(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// ValidateEmail verifica un formato de correo limpio sin espacios ni nombre embebido.
func ValidateEmail(email string) bool {
	if email == "" || len(email) > 255 || strings.ContainsAny(email, " \t\r\n") {
		return false
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return false
	}
	atIdx := strings.LastIndex(email, "@")
	if atIdx <= 0 || atIdx >= len(email)-1 {
		return false
	}
	domain := email[atIdx+1:]
	return strings.Contains(domain, ".") && !strings.HasPrefix(domain, ".") && !strings.HasSuffix(domain, ".")
}

// ValidatePhone valida números de WhatsApp/celular (7 a 20 caracteres, al menos 7 dígitos).
func ValidatePhone(phone string) bool {
	trimmed := strings.TrimSpace(phone)
	if len(trimmed) < 7 || len(trimmed) > 20 {
		return false
	}
	digits := 0
	for i, r := range trimmed {
		switch {
		case unicode.IsDigit(r):
			digits++
		case r == '+' && i == 0:
			continue
		case r == ' ' || r == '-':
			continue
		default:
			return false
		}
	}
	return digits >= 7 && digits <= 15
}

// ValidatePassword valida las reglas mínimas de longitud y contenido para contraseñas.
func ValidatePassword(password string) string {
	if len(password) < minPasswordLength {
		return fmt.Sprintf("La contraseña debe tener al menos %d caracteres", minPasswordLength)
	}
	if len(password) > maxPasswordLength {
		return fmt.Sprintf("La contraseña no debe exceder %d caracteres", maxPasswordLength)
	}
	if strings.TrimSpace(password) == "" {
		return "La contraseña no puede contener solo espacios"
	}
	return ""
}

func isValidRole(role string) bool {
	return role == apphttp.RoleSuperAdmin || role == apphttp.RoleDueno || role == apphttp.RoleJugador
}

func generateOpaqueToken() (string, error) {
	buf := make([]byte, refreshTokenNumBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
