package auth

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	apphttp "github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/http"
)

func TestHashAndVerifyPassword(t *testing.T) {
	password := "DigibytePeru2026!"

	hash1, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword falló: %v", err)
	}
	hash2, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword (2) falló: %v", err)
	}

	if !strings.HasPrefix(hash1, "$argon2id$v=19$") {
		t.Fatalf("formato inesperado de hash: %s", hash1)
	}
	if hash1 == hash2 {
		t.Fatal("dos hashes de la misma contraseña deben usar salts aleatorios distintos")
	}

	ok, err := VerifyPassword(password, hash1)
	if err != nil || !ok {
		t.Fatalf("VerifyPassword correcto devolvió ok=%v, err=%v", ok, err)
	}

	ok, err = VerifyPassword("OtraClaveIncorrecta!", hash1)
	if err != nil {
		t.Fatalf("VerifyPassword incorrecto no debe fallar con error: %v", err)
	}
	if ok {
		t.Fatal("VerifyPassword aceptó una contraseña incorrecta")
	}

	if _, err := VerifyPassword(password, "hash-malformado"); err == nil {
		t.Fatal("VerifyPassword debía rechazar hash malformado")
	}
}

func TestAccessTokenGenerateAndParse(t *testing.T) {
	secret := "super-secret-jwt-key-for-unit-tests-2026-fdp"
	fixedNow := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)

	svc := NewService(nil, nil, secret, 15*time.Minute, 7*24*time.Hour)
	svc.now = func() time.Time { return fixedNow }

	userID := uuid.New()
	complejoID := uuid.New()

	t.Run("token válido de DUENO con complejo_id", func(t *testing.T) {
		token, err := svc.GenerateAccessToken(userID, apphttp.RoleDueno, &complejoID)
		if err != nil {
			t.Fatalf("GenerateAccessToken falló: %v", err)
		}

		parsed, err := svc.ParseAccessToken(token)
		if err != nil {
			t.Fatalf("ParseAccessToken falló: %v", err)
		}
		if parsed.UserID != userID {
			t.Errorf("UserID = %s, esperado %s", parsed.UserID, userID)
		}
		if parsed.Rol != apphttp.RoleDueno {
			t.Errorf("Rol = %s, esperado %s", parsed.Rol, apphttp.RoleDueno)
		}
		if parsed.ComplejoID == nil || *parsed.ComplejoID != complejoID {
			t.Errorf("ComplejoID = %v, esperado %s", parsed.ComplejoID, complejoID)
		}
	})

	t.Run("token válido de JUGADOR sin complejo_id", func(t *testing.T) {
		token, err := svc.GenerateAccessToken(userID, apphttp.RoleJugador, nil)
		if err != nil {
			t.Fatalf("GenerateAccessToken falló: %v", err)
		}

		parsed, err := svc.ParseAccessToken(token)
		if err != nil {
			t.Fatalf("ParseAccessToken falló: %v", err)
		}
		if parsed.ComplejoID != nil {
			t.Errorf("ComplejoID = %v, esperado nil", parsed.ComplejoID)
		}
	})

	t.Run("rechaza firma con secreto distinto", func(t *testing.T) {
		otherSvc := NewService(nil, nil, "otro-secreto-completamente-distinto-32chars!", 15*time.Minute, 7*24*time.Hour)
		otherSvc.now = svc.now

		token, err := otherSvc.GenerateAccessToken(userID, apphttp.RoleJugador, nil)
		if err != nil {
			t.Fatalf("GenerateAccessToken falló: %v", err)
		}

		if _, err := svc.ParseAccessToken(token); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("esperado ErrInvalidToken, obtenido %v", err)
		}
	})

	t.Run("rechaza token expirado", func(t *testing.T) {
		token, err := svc.GenerateAccessToken(userID, apphttp.RoleJugador, nil)
		if err != nil {
			t.Fatalf("GenerateAccessToken falló: %v", err)
		}

		laterSvc := NewService(nil, nil, secret, 15*time.Minute, 7*24*time.Hour)
		laterSvc.now = func() time.Time { return fixedNow.Add(16 * time.Minute) }

		if _, err := laterSvc.ParseAccessToken(token); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("esperado ErrInvalidToken para token expirado, obtenido %v", err)
		}
	})

	t.Run("rechaza token con algoritmo none", func(t *testing.T) {
		claims := AccessClaims{
			Rol:       apphttp.RoleSuperAdmin,
			TokenType: jwtTokenTypeAccess,
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    jwtIssuer,
				Subject:   userID.String(),
				ExpiresAt: jwt.NewNumericDate(fixedNow.Add(10 * time.Minute)),
			},
		}
		unsigned := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
		tokenStr, err := unsigned.SignedString(jwt.UnsafeAllowNoneSignatureType)
		if err != nil {
			t.Fatalf("error generando token alg=none: %v", err)
		}

		if _, err := svc.ParseAccessToken(tokenStr); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("esperado ErrInvalidToken para alg=none, obtenido %v", err)
		}
	})
}

func TestEmailAndPhoneValidation(t *testing.T) {
	if got := NormalizeEmail("  Jugador.Test@Ejemplo.PE  "); got != "jugador.test@ejemplo.pe" {
		t.Fatalf("NormalizeEmail = %q", got)
	}
	if !ValidateEmail("jugador@digibyte.pe") {
		t.Fatal("correo válido rechazado")
	}
	if ValidateEmail("correo-invalido") || ValidateEmail("a@localhost") || ValidateEmail("Nombre <a@b.pe>") {
		t.Fatal("correo inválido aceptado")
	}
	if !ValidatePhone("+51 987 654 321") || !ValidatePhone("987654321") {
		t.Fatal("teléfono válido rechazado")
	}
	if ValidatePhone("123") || ValidatePhone("abc987654321") {
		t.Fatal("teléfono inválido aceptado")
	}
}

type memoryRepo struct {
	usersByEmail map[string]User
	usersByID    map[uuid.UUID]User
}

func newMemoryRepo() *memoryRepo {
	return &memoryRepo{
		usersByEmail: make(map[string]User),
		usersByID:    make(map[uuid.UUID]User),
	}
}

func (m *memoryRepo) CreateJugador(_ context.Context, email, passwordHash, nombreCompleto, telefono string) (User, error) {
	if _, exists := m.usersByEmail[email]; exists {
		return User{}, ErrEmailAlreadyExists
	}
	now := time.Now().UTC()
	tel := telefono
	u := User{
		ID:             uuid.New(),
		Rol:            apphttp.RoleJugador,
		Email:          email,
		PasswordHash:   passwordHash,
		NombreCompleto: nombreCompleto,
		Telefono:       &tel,
		Activo:         true,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	m.usersByEmail[email] = u
	m.usersByID[u.ID] = u
	return u, nil
}

func (m *memoryRepo) FindByEmail(_ context.Context, email string) (User, error) {
	u, ok := m.usersByEmail[email]
	if !ok {
		return User{}, ErrUserNotFound
	}
	return u, nil
}

func (m *memoryRepo) FindByID(_ context.Context, id uuid.UUID) (User, error) {
	u, ok := m.usersByID[id]
	if !ok {
		return User{}, ErrUserNotFound
	}
	return u, nil
}

func (m *memoryRepo) UpdateLastLogin(_ context.Context, id uuid.UUID) (time.Time, error) {
	now := time.Now().UTC()
	u := m.usersByID[id]
	u.UltimoLoginAt = &now
	m.usersByID[id] = u
	m.usersByEmail[u.Email] = u
	return now, nil
}

func (m *memoryRepo) FindPrimaryComplejoIDByOwner(_ context.Context, _ uuid.UUID) (*uuid.UUID, error) {
	return nil, nil
}

func (m *memoryRepo) UpdateProfile(_ context.Context, id uuid.UUID, nombreCompleto string, telefono *string) (User, error) {
	u, ok := m.usersByID[id]
	if !ok {
		return User{}, ErrUserNotFound
	}
	u.NombreCompleto = nombreCompleto
	u.Telefono = telefono
	u.UpdatedAt = time.Now().UTC()
	m.usersByID[id] = u
	m.usersByEmail[u.Email] = u
	return u, nil
}

func (m *memoryRepo) UpdatePasswordHash(_ context.Context, id uuid.UUID, passwordHash string) error {
	u, ok := m.usersByID[id]
	if !ok {
		return ErrUserNotFound
	}
	u.PasswordHash = passwordHash
	u.UpdatedAt = time.Now().UTC()
	m.usersByID[id] = u
	m.usersByEmail[u.Email] = u
	return nil
}

type memorySessionStore struct {
	tokens map[string]uuid.UUID
}

func newMemorySessionStore() *memorySessionStore {
	return &memorySessionStore{tokens: make(map[string]uuid.UUID)}
}

func (s *memorySessionStore) SaveRefreshToken(_ context.Context, tokenHash string, userID uuid.UUID, _ time.Duration) error {
	s.tokens[tokenHash] = userID
	return nil
}

func (s *memorySessionStore) ConsumeRefreshToken(_ context.Context, tokenHash string) (uuid.UUID, error) {
	uid, ok := s.tokens[tokenHash]
	if !ok {
		return uuid.Nil, ErrInvalidToken
	}
	delete(s.tokens, tokenHash)
	return uid, nil
}

func (s *memorySessionStore) DeleteRefreshToken(_ context.Context, tokenHash string) error {
	delete(s.tokens, tokenHash)
	return nil
}

func (s *memorySessionStore) RevokeAllUserRefreshTokens(_ context.Context, userID uuid.UUID) error {
	for h, uid := range s.tokens {
		if uid == userID {
			delete(s.tokens, h)
		}
	}
	return nil
}

func TestAuthServiceFlowAndRefreshRotation(t *testing.T) {
	repo := newMemoryRepo()
	sessions := newMemorySessionStore()
	svc := NewService(repo, sessions, "secreto-de-pruebas-unitario-con-mas-de-32-chars", 15*time.Minute, 7*24*time.Hour)
	ctx := context.Background()

	profile, err := svc.Register(ctx, RegisterRequest{
		Email:          "  Jugador.Huacho@Digibyte.PE ",
		Password:       "ClaveSegura2026!",
		NombreCompleto: "Carlos Quispe",
		Telefono:       "987654321",
	})
	if err != nil {
		t.Fatalf("Register falló: %v", err)
	}
	if profile.Email != "jugador.huacho@digibyte.pe" {
		t.Fatalf("email no normalizado: %q", profile.Email)
	}
	if profile.Rol != apphttp.RoleJugador {
		t.Fatalf("rol = %q, esperado JUGADOR", profile.Rol)
	}

	loginRes, err := svc.Login(ctx, LoginRequest{
		Email:    "JUGADOR.HUACHO@DIGIBYTE.PE",
		Password: "ClaveSegura2026!",
	})
	if err != nil {
		t.Fatalf("Login falló: %v", err)
	}
	if loginRes.AccessToken == "" || loginRes.RefreshToken == "" {
		t.Fatal("Login debe devolver access_token y refresh_token")
	}
	if loginRes.User.UltimoLoginAt == nil {
		t.Fatal("Login debe actualizar ultimo_login_at")
	}

	// Rotación de refresh token
	refreshRes, err := svc.Refresh(ctx, RefreshRequest{RefreshToken: loginRes.RefreshToken})
	if err != nil {
		t.Fatalf("Refresh falló: %v", err)
	}
	if refreshRes.RefreshToken == loginRes.RefreshToken {
		t.Fatal("Refresh debe emitir un nuevo refresh_token rotado")
	}

	// Reintento con refresh token anterior ya rotado -> 401
	if _, err := svc.Refresh(ctx, RefreshRequest{RefreshToken: loginRes.RefreshToken}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("reuso de refresh token anterior debía devolver ErrInvalidToken, obtenido %v", err)
	}

	// Logout y verificación de revocación
	if err := svc.Logout(ctx, LogoutRequest{RefreshToken: refreshRes.RefreshToken}); err != nil {
		t.Fatalf("Logout falló: %v", err)
	}
	if _, err := svc.Refresh(ctx, RefreshRequest{RefreshToken: refreshRes.RefreshToken}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("refresh revocado debía devolver ErrInvalidToken, obtenido %v", err)
	}
}

func TestRegisterRejectsRoleEscalationField(t *testing.T) {
	svc := NewService(newMemoryRepo(), newMemorySessionStore(), "secreto-de-pruebas-unitario-con-mas-de-32-chars", 15*time.Minute, 7*24*time.Hour)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandler(svc, nil, logger)

	body := bytes.NewBufferString(`{"email":"hacker@ejemplo.pe","password":"Password123!","nombre_completo":"Test","telefono":"987654321","rol":"SUPER_ADMIN"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", body)
	rec := httptest.NewRecorder()

	handler.Register(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, esperado 400 al enviar campo rol no permitido", rec.Code)
	}
}

func TestUpdateProfileAndChangePassword(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepo()
	sessions := newMemorySessionStore()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(repo, sessions, "secreto-de-pruebas-unitario-con-mas-de-32-chars", 15*time.Minute, 7*24*time.Hour).WithLogger(logger)
	handler := NewHandler(svc, nil, logger)

	router := apphttp.NewRouter(apphttp.RouterConfig{
		Logger:      logger,
		TokenParser: svc.ParseAccessToken,
		MountV1:     handler.RegisterRoutes,
	})

	created, err := svc.Register(ctx, RegisterRequest{
		Email:          "perfil@digibyte.pe",
		Password:       "ClaveInicial2026!",
		NombreCompleto: "Pedro Paredes",
		Telefono:       "987654321",
	})
	if err != nil {
		t.Fatalf("Register falló: %v", err)
	}

	login1, err := svc.Login(ctx, LoginRequest{Email: "perfil@digibyte.pe", Password: "ClaveInicial2026!"})
	if err != nil {
		t.Fatalf("Login #1 falló: %v", err)
	}
	login2, err := svc.Login(ctx, LoginRequest{Email: "perfil@digibyte.pe", Password: "ClaveInicial2026!"})
	if err != nil {
		t.Fatalf("Login #2 falló: %v", err)
	}

	// 1. PATCH /api/v1/auth/me sin token -> 401
	reqUnauth := httptest.NewRequest(http.MethodPatch, "/api/v1/auth/me", bytes.NewBufferString(`{"nombre_completo":"Nuevo"}`))
	recUnauth := httptest.NewRecorder()
	router.ServeHTTP(recUnauth, reqUnauth)
	if recUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("PATCH /me sin token esperaba 401, obtuvo %d", recUnauth.Code)
	}

	// 2. PATCH /api/v1/auth/me con datos válidos -> 200 y perfil actualizado sin password_hash
	reqPatch := httptest.NewRequest(http.MethodPatch, "/api/v1/auth/me", bytes.NewBufferString(`{"nombre_completo":"Pedro Paredes Soto","telefono":"+51 999 888 777"}`))
	reqPatch.Header.Set("Authorization", "Bearer "+login1.AccessToken)
	recPatch := httptest.NewRecorder()
	router.ServeHTTP(recPatch, reqPatch)
	if recPatch.Code != http.StatusOK {
		t.Fatalf("PATCH /me esperaba 200, obtuvo %d: %s", recPatch.Code, recPatch.Body.String())
	}
	if !strings.Contains(recPatch.Body.String(), "Pedro Paredes Soto") || !strings.Contains(recPatch.Body.String(), "+51 999 888 777") {
		t.Fatalf("respuesta PATCH /me inesperada: %s", recPatch.Body.String())
	}
	if strings.Contains(recPatch.Body.String(), "argon2id") {
		t.Fatalf("respuesta PATCH /me no debe exponer password_hash: %s", recPatch.Body.String())
	}

	// 3. PATCH /api/v1/auth/me vacío para JUGADOR con teléfono en blanco -> 400
	reqBadPhone := httptest.NewRequest(http.MethodPatch, "/api/v1/auth/me", bytes.NewBufferString(`{"telefono":"   "}`))
	reqBadPhone.Header.Set("Authorization", "Bearer "+login1.AccessToken)
	recBadPhone := httptest.NewRecorder()
	router.ServeHTTP(recBadPhone, reqBadPhone)
	if recBadPhone.Code != http.StatusBadRequest {
		t.Fatalf("PATCH /me con teléfono vacío para JUGADOR esperaba 400, obtuvo %d", recBadPhone.Code)
	}

	// 4. POST /api/v1/auth/change-password con password_actual incorrecta -> 400 INVALID_CURRENT_PASSWORD
	reqWrongPwd := httptest.NewRequest(http.MethodPost, "/api/v1/auth/change-password", bytes.NewBufferString(`{"password_actual":"Incorrecta2026!","password_nueva":"NuevaClave2026!"}`))
	reqWrongPwd.Header.Set("Authorization", "Bearer "+login1.AccessToken)
	recWrongPwd := httptest.NewRecorder()
	router.ServeHTTP(recWrongPwd, reqWrongPwd)
	if recWrongPwd.Code != http.StatusBadRequest || !strings.Contains(recWrongPwd.Body.String(), "INVALID_CURRENT_PASSWORD") {
		t.Fatalf("change-password con clave actual errónea esperaba 400 INVALID_CURRENT_PASSWORD, obtuvo %d: %s", recWrongPwd.Code, recWrongPwd.Body.String())
	}

	// 5. POST /api/v1/auth/change-password con misma clave -> 400 VALIDATION_ERROR
	reqSamePwd := httptest.NewRequest(http.MethodPost, "/api/v1/auth/change-password", bytes.NewBufferString(`{"password_actual":"ClaveInicial2026!","password_nueva":"ClaveInicial2026!"}`))
	reqSamePwd.Header.Set("Authorization", "Bearer "+login1.AccessToken)
	recSamePwd := httptest.NewRecorder()
	router.ServeHTTP(recSamePwd, reqSamePwd)
	if recSamePwd.Code != http.StatusBadRequest {
		t.Fatalf("change-password con misma contraseña esperaba 400, obtuvo %d", recSamePwd.Code)
	}

	// 6. POST /api/v1/auth/change-password válido -> 200, revoca todos los refresh tokens activos y acepta login con nueva clave
	reqChange := httptest.NewRequest(http.MethodPost, "/api/v1/auth/change-password", bytes.NewBufferString(`{"password_actual":"ClaveInicial2026!","password_nueva":"NuevaClaveSegura2026!"}`))
	reqChange.Header.Set("Authorization", "Bearer "+login1.AccessToken)
	recChange := httptest.NewRecorder()
	router.ServeHTTP(recChange, reqChange)
	if recChange.Code != http.StatusOK || !strings.Contains(recChange.Body.String(), `"sesiones_revocadas":true`) {
		t.Fatalf("change-password válido esperaba 200 con sesiones_revocadas=true, obtuvo %d: %s", recChange.Code, recChange.Body.String())
	}

	// Todos los refresh tokens emitidos antes del cambio fueron revocados
	if _, err := svc.Refresh(ctx, RefreshRequest{RefreshToken: login1.RefreshToken}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("refresh token #1 debía estar revocado, obtuvo %v", err)
	}
	if _, err := svc.Refresh(ctx, RefreshRequest{RefreshToken: login2.RefreshToken}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("refresh token #2 debía estar revocado, obtuvo %v", err)
	}

	// Login con clave vieja falla, con clave nueva funciona
	if _, err := svc.Login(ctx, LoginRequest{Email: created.Email, Password: "ClaveInicial2026!"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("login con contraseña anterior debía fallar, obtuvo %v", err)
	}
	if _, err := svc.Login(ctx, LoginRequest{Email: created.Email, Password: "NuevaClaveSegura2026!"}); err != nil {
		t.Fatalf("login con nueva contraseña falló: %v", err)
	}
}
