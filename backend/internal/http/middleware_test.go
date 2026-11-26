package apphttp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestRequireAuth(t *testing.T) {
	expectedUserID := uuid.New()
	expectedComplejoID := uuid.New()

	parser := func(token string) (AuthUser, error) {
		if token == "valid-token" {
			return AuthUser{
				UserID:     expectedUserID,
				Rol:        RoleDueno,
				ComplejoID: &expectedComplejoID,
			}, nil
		}
		return AuthUser{}, errors.New("token inválido")
	}

	var captured AuthUser
	var hasUser bool
	handler := RequireAuth(parser)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, hasUser = AuthUserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
		wantUser   bool
	}{
		{
			name:       "sin header Authorization",
			authHeader: "",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "esquema no Bearer",
			authHeader: "Basic dXNlcjpwYXNz",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "Bearer vacío",
			authHeader: "Bearer   ",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "token inválido o expirado",
			authHeader: "Bearer expired-token",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "token válido inyecta AuthUser en contexto",
			authHeader: "Bearer valid-token",
			wantStatus: http.StatusOK,
			wantUser:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			captured = AuthUser{}
			hasUser = false

			req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, esperado %d", rec.Code, tc.wantStatus)
			}
			if hasUser != tc.wantUser {
				t.Fatalf("hasUser = %v, esperado %v", hasUser, tc.wantUser)
			}
			if tc.wantUser {
				if captured.UserID != expectedUserID {
					t.Errorf("UserID = %s, esperado %s", captured.UserID, expectedUserID)
				}
				if captured.Rol != RoleDueno {
					t.Errorf("Rol = %s, esperado %s", captured.Rol, RoleDueno)
				}
				if captured.ComplejoID == nil || *captured.ComplejoID != expectedComplejoID {
					t.Errorf("ComplejoID = %v, esperado %s", captured.ComplejoID, expectedComplejoID)
				}
			}
		})
	}
}

func TestRequireRole(t *testing.T) {
	handler := RequireRole(RoleSuperAdmin, RoleDueno)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		name       string
		user       *AuthUser
		wantStatus int
	}{
		{
			name:       "sin usuario autenticado devuelve 401",
			user:       nil,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "JUGADOR rechazado con 403",
			user:       &AuthUser{UserID: uuid.New(), Rol: RoleJugador},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "DUENO autorizado",
			user:       &AuthUser{UserID: uuid.New(), Rol: RoleDueno},
			wantStatus: http.StatusOK,
		},
		{
			name:       "SUPER_ADMIN autorizado",
			user:       &AuthUser{UserID: uuid.New(), Rol: RoleSuperAdmin},
			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/metricas", nil)
			if tc.user != nil {
				req = req.WithContext(WithAuthUser(req.Context(), *tc.user))
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, esperado %d", rec.Code, tc.wantStatus)
			}
		})
	}
}

func TestRequireTenant(t *testing.T) {
	ownerID := uuid.New()
	ownComplejoID := uuid.New()
	otherComplejoID := uuid.New()

	ownershipChecker := func(_ context.Context, userID, complejoID uuid.UUID) (bool, error) {
		return userID == ownerID && complejoID == ownComplejoID, nil
	}

	buildRouter := func(checker TenantOwnershipChecker, injectUser *AuthUser) http.Handler {
		r := chi.NewRouter()
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if injectUser != nil {
					r = r.WithContext(WithAuthUser(r.Context(), *injectUser))
				}
				next.ServeHTTP(w, r)
			})
		})
		r.With(RequireTenant(checker)).Get("/complejos/{complejo_id}/canchas", func(w http.ResponseWriter, r *http.Request) {
			tenantID, ok := TenantIDFromContext(r.Context())
			if !ok {
				t.Error("se esperaba tenantID en el contexto")
			}
			w.Header().Set("X-Verified-Tenant", tenantID.String())
			w.WriteHeader(http.StatusOK)
		})
		r.With(RequireTenant(checker)).Get("/mi-complejo", func(w http.ResponseWriter, r *http.Request) {
			tenantID, ok := TenantIDFromContext(r.Context())
			if !ok {
				t.Error("se esperaba tenantID en el contexto")
			}
			w.Header().Set("X-Verified-Tenant", tenantID.String())
			w.WriteHeader(http.StatusOK)
		})
		return r
	}

	t.Run("DUENO accede a su propio complejo por ruta", func(t *testing.T) {
		user := &AuthUser{UserID: ownerID, Rol: RoleDueno, ComplejoID: &ownComplejoID}
		router := buildRouter(ownershipChecker, user)

		req := httptest.NewRequest(http.MethodGet, "/complejos/"+ownComplejoID.String()+"/canchas", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, esperado 200", rec.Code)
		}
		if got := rec.Header().Get("X-Verified-Tenant"); got != ownComplejoID.String() {
			t.Fatalf("X-Verified-Tenant = %q, esperado %q", got, ownComplejoID.String())
		}
	})

	t.Run("DUENO usa complejo del token cuando ruta no especifica complejo_id", func(t *testing.T) {
		user := &AuthUser{UserID: ownerID, Rol: RoleDueno, ComplejoID: &ownComplejoID}
		router := buildRouter(ownershipChecker, user)

		req := httptest.NewRequest(http.MethodGet, "/mi-complejo", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, esperado 200", rec.Code)
		}
		if got := rec.Header().Get("X-Verified-Tenant"); got != ownComplejoID.String() {
			t.Fatalf("X-Verified-Tenant = %q, esperado %q", got, ownComplejoID.String())
		}
	})

	t.Run("DUENO intentando acceso cruzado a otro complejo recibe 403", func(t *testing.T) {
		user := &AuthUser{UserID: ownerID, Rol: RoleDueno, ComplejoID: &ownComplejoID}
		router := buildRouter(ownershipChecker, user)

		req := httptest.NewRequest(http.MethodGet, "/complejos/"+otherComplejoID.String()+"/canchas", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, esperado 403", rec.Code)
		}
	})

	t.Run("DUENO con conflicto entre ruta y encabezado recibe 403", func(t *testing.T) {
		user := &AuthUser{UserID: ownerID, Rol: RoleDueno, ComplejoID: &ownComplejoID}
		router := buildRouter(ownershipChecker, user)

		req := httptest.NewRequest(http.MethodGet, "/complejos/"+ownComplejoID.String()+"/canchas", nil)
		req.Header.Set("X-Complejo-ID", otherComplejoID.String())
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, esperado 403", rec.Code)
		}
	})

	t.Run("DUENO sin complejo asignado recibe 403", func(t *testing.T) {
		user := &AuthUser{UserID: ownerID, Rol: RoleDueno, ComplejoID: nil}
		router := buildRouter(ownershipChecker, user)

		req := httptest.NewRequest(http.MethodGet, "/mi-complejo", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, esperado 403", rec.Code)
		}
	})

	t.Run("error en consulta de propiedad falla cerrado con 500", func(t *testing.T) {
		user := &AuthUser{UserID: ownerID, Rol: RoleDueno, ComplejoID: &ownComplejoID}
		failingChecker := func(_ context.Context, _, _ uuid.UUID) (bool, error) {
			return false, errors.New("db unavailable")
		}
		router := buildRouter(failingChecker, user)

		req := httptest.NewRequest(http.MethodGet, "/mi-complejo", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, esperado 500", rec.Code)
		}
	})

	t.Run("JUGADOR en ruta multi-tenant recibe 403", func(t *testing.T) {
		user := &AuthUser{UserID: uuid.New(), Rol: RoleJugador}
		router := buildRouter(ownershipChecker, user)

		req := httptest.NewRequest(http.MethodGet, "/complejos/"+ownComplejoID.String()+"/canchas", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, esperado 403", rec.Code)
		}
	})

	t.Run("SUPER_ADMIN con complejo_id explícito accede correctamente", func(t *testing.T) {
		user := &AuthUser{UserID: uuid.New(), Rol: RoleSuperAdmin}
		router := buildRouter(ownershipChecker, user)

		req := httptest.NewRequest(http.MethodGet, "/mi-complejo", nil)
		req.Header.Set("X-Complejo-ID", otherComplejoID.String())
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, esperado 200", rec.Code)
		}
		if got := rec.Header().Get("X-Verified-Tenant"); got != otherComplejoID.String() {
			t.Fatalf("X-Verified-Tenant = %q, esperado %q", got, otherComplejoID.String())
		}
	})

	t.Run("UUID inválido en X-Complejo-ID devuelve 400", func(t *testing.T) {
		user := &AuthUser{UserID: ownerID, Rol: RoleDueno, ComplejoID: &ownComplejoID}
		router := buildRouter(ownershipChecker, user)

		req := httptest.NewRequest(http.MethodGet, "/mi-complejo", nil)
		req.Header.Set("X-Complejo-ID", "no-es-uuid")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, esperado 400", rec.Code)
		}
	})
}

type stubRateLimiter struct {
	counts map[string]int
}

func (s *stubRateLimiter) Allow(_ context.Context, key string, limit int, window time.Duration) (bool, int, time.Duration, error) {
	if s.counts == nil {
		s.counts = make(map[string]int)
	}
	s.counts[key]++
	remaining := limit - s.counts[key]
	if remaining < 0 {
		remaining = 0
	}
	return s.counts[key] <= limit, remaining, window, nil
}

func TestRateLimit(t *testing.T) {
	limiter := &stubRateLimiter{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler := RateLimit(limiter, "auth_login", 2, time.Minute, logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 1; i <= 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		req.RemoteAddr = "203.0.113.10:54321"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if i <= 2 && rec.Code != http.StatusOK {
			t.Fatalf("intento %d: status = %d, esperado 200", i, rec.Code)
		}
		if i == 3 {
			if rec.Code != http.StatusTooManyRequests {
				t.Fatalf("intento 3: status = %d, esperado 429", rec.Code)
			}
			if rec.Header().Get("Retry-After") == "" {
				t.Fatal("se esperaba encabezado Retry-After en respuesta 429")
			}
		}
	}
}

func TestRouterCORSPreflightForFrontend(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := NewRouter(RouterConfig{
		CORSAllowedOrigins: []string{"http://localhost:3000", "http://127.0.0.1:3000"},
		Logger:             logger,
		HealthHandler: func(w http.ResponseWriter, _ *http.Request) {
			WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		},
	})

	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
	for _, method := range methods {
		t.Run("preflight_"+method, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodOptions, "/api/v1/dueno/complejo", nil)
			req.Header.Set("Origin", "http://localhost:3000")
			req.Header.Set("Access-Control-Request-Method", method)
			req.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type, X-Complejo-ID")
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
				t.Fatalf("preflight %s status = %d, esperado 200 o 204", method, rec.Code)
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
				t.Fatalf("Access-Control-Allow-Origin = %q, esperado http://localhost:3000", got)
			}
			if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
				t.Fatalf("Access-Control-Allow-Credentials = %q, esperado true", got)
			}
			allowHeaders := strings.ToLower(rec.Header().Get("Access-Control-Allow-Headers"))
			for _, hdr := range []string{"authorization", "content-type", "x-complejo-id"} {
				if !strings.Contains(allowHeaders, hdr) {
					t.Fatalf("Access-Control-Allow-Headers = %q no incluye %q", allowHeaders, hdr)
				}
			}
		})
	}

	t.Run("origen_no_permitido_rechazado", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/api/v1/health", nil)
		req.Header.Set("Origin", "https://malicioso.example.com")
		req.Header.Set("Access-Control-Request-Method", http.MethodGet)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("origen no permitido no debería recibir Access-Control-Allow-Origin, obtenido %q", got)
		}
	})
}
