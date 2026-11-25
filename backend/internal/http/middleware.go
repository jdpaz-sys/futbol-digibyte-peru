package apphttp

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type contextKey string

const (
	authUserContextKey contextKey = "fdp_auth_user"
	tenantIDContextKey contextKey = "fdp_tenant_id"
)

// AuthUser representa la identidad autenticada cargada en context.Context.
type AuthUser struct {
	UserID     uuid.UUID
	Rol        string
	ComplejoID *uuid.UUID
}

// TokenParser valida un Access Token JWT y devuelve el usuario autenticado.
type TokenParser func(token string) (AuthUser, error)

// TenantOwnershipChecker verifica en PostgreSQL si el usuario es dueño del complejo.
type TenantOwnershipChecker func(ctx context.Context, userID, complejoID uuid.UUID) (bool, error)

// RateLimiter abstrae el control de cuota por clave y ventana de tiempo.
type RateLimiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, remaining int, retryAfter time.Duration, err error)
}

// WithAuthUser almacena el usuario autenticado en el contexto de la solicitud.
func WithAuthUser(ctx context.Context, user AuthUser) context.Context {
	return context.WithValue(ctx, authUserContextKey, user)
}

// AuthUserFromContext recupera el usuario autenticado del contexto.
func AuthUserFromContext(ctx context.Context) (AuthUser, bool) {
	user, ok := ctx.Value(authUserContextKey).(AuthUser)
	return user, ok
}

// WithTenantID almacena el complejo_id verificado en el contexto de la solicitud.
func WithTenantID(ctx context.Context, complejoID uuid.UUID) context.Context {
	return context.WithValue(ctx, tenantIDContextKey, complejoID)
}

// TenantIDFromContext recupera el complejo_id verificado del contexto.
func TenantIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(tenantIDContextKey).(uuid.UUID)
	return id, ok
}

// RequireAuth exige un header Authorization: Bearer <jwt> válido e inyecta
// {UserID, Rol, ComplejoID} en context.Context.
func RequireAuth(parseToken TokenParser) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if parseToken == nil {
				WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Validador de autenticación no configurado")
				return
			}

			token, ok := extractBearerToken(r.Header.Get("Authorization"))
			if !ok {
				WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Token de acceso requerido")
				return
			}

			user, err := parseToken(token)
			if err != nil {
				WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Token de acceso inválido o expirado")
				return
			}

			ctx := WithAuthUser(r.Context(), user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireRole restringe el acceso únicamente a los roles permitidos (SUPER_ADMIN, DUENO, JUGADOR).
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		allowed[role] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := AuthUserFromContext(r.Context())
			if !ok {
				WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Autenticación requerida")
				return
			}

			if _, permitted := allowed[user.Rol]; !permitted {
				WriteError(w, http.StatusForbidden, "FORBIDDEN", "No tienes permisos para acceder a este recurso")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// NewPostgresTenantChecker crea un verificador parametrizado de propiedad de complejo en PostgreSQL.
func NewPostgresTenantChecker(pool *pgxpool.Pool) TenantOwnershipChecker {
	return func(ctx context.Context, userID, complejoID uuid.UUID) (bool, error) {
		var owns bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM complejos WHERE id = $1 AND dueno_id = $2)`,
			complejoID, userID,
		).Scan(&owns)
		if err != nil {
			return false, err
		}
		return owns, nil
	}
}

// RequireTenant garantiza aislamiento multi-inquilino estricto (RN-01 / RNF-01).
// Para DUENO verifica que el complejo_id pertenezca realmente al usuario autenticado
// y rechaza cualquier intento cruzado con 403 Forbidden.
func RequireTenant(checkOwnership TenantOwnershipChecker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := AuthUserFromContext(r.Context())
			if !ok {
				WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Autenticación requerida")
				return
			}

			explicitID, conflict, parseErr := extractExplicitComplejoID(r)
			if parseErr != nil {
				WriteError(w, http.StatusBadRequest, "INVALID_TENANT_ID", "Identificador de complejo inválido")
				return
			}
			if conflict {
				WriteError(w, http.StatusForbidden, "TENANT_MISMATCH", "Conflicto entre el complejo de la ruta y el encabezado")
				return
			}

			switch user.Rol {
			case RoleDueno:
				var targetID uuid.UUID
				switch {
				case explicitID != nil:
					targetID = *explicitID
				case user.ComplejoID != nil:
					targetID = *user.ComplejoID
				default:
					WriteError(w, http.StatusForbidden, "TENANT_REQUIRED", "No tienes un complejo deportivo asociado")
					return
				}

				if checkOwnership == nil {
					WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Verificador de inquilino no configurado")
					return
				}

				owns, err := checkOwnership(r.Context(), user.UserID, targetID)
				if err != nil {
					WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "No se pudo verificar el acceso al complejo")
					return
				}
				if !owns {
					WriteError(w, http.StatusForbidden, "TENANT_FORBIDDEN", "No tienes acceso a este complejo deportivo")
					return
				}

				verified := targetID
				user.ComplejoID = &verified
				ctx := WithAuthUser(r.Context(), user)
				ctx = WithTenantID(ctx, verified)
				next.ServeHTTP(w, r.WithContext(ctx))

			case RoleSuperAdmin:
				if explicitID == nil {
					WriteError(w, http.StatusBadRequest, "TENANT_REQUIRED", "Se requiere complejo_id en la ruta o encabezado X-Complejo-ID")
					return
				}
				verified := *explicitID
				user.ComplejoID = &verified
				ctx := WithAuthUser(r.Context(), user)
				ctx = WithTenantID(ctx, verified)
				next.ServeHTTP(w, r.WithContext(ctx))

			default:
				WriteError(w, http.StatusForbidden, "FORBIDDEN", "Acceso restringido a administradores de complejo")
			}
		})
	}
}

var rateLimitLuaScript = redis.NewScript(`
local current = redis.call("INCR", KEYS[1])
if current == 1 then
	redis.call("PEXPIRE", KEYS[1], ARGV[1])
end
local ttl = redis.call("PTTL", KEYS[1])
if ttl < 0 then
	redis.call("PEXPIRE", KEYS[1], ARGV[1])
	ttl = tonumber(ARGV[1])
end
return {current, ttl}
`)

// ValkeyRateLimiter implementa limitación de tasa atómica mediante script Lua en Valkey.
type ValkeyRateLimiter struct {
	client *redis.Client
}

// NewValkeyRateLimiter crea un limitador de tasa respaldado por Valkey.
func NewValkeyRateLimiter(client *redis.Client) *ValkeyRateLimiter {
	return &ValkeyRateLimiter{client: client}
}

// Allow incrementa atómicamente el contador para la clave y evalúa si está dentro del límite.
func (l *ValkeyRateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, int, time.Duration, error) {
	windowMs := window.Milliseconds()
	if windowMs <= 0 {
		windowMs = 1000
	}

	res, err := rateLimitLuaScript.Run(ctx, l.client, []string{key}, windowMs).Int64Slice()
	if err != nil {
		return false, 0, 0, err
	}
	if len(res) != 2 {
		return false, 0, 0, fmt.Errorf("respuesta inesperada del script de rate limit")
	}

	current := int(res[0])
	ttl := time.Duration(res[1]) * time.Millisecond

	remaining := limit - current
	if remaining < 0 {
		remaining = 0
	}

	return current <= limit, remaining, ttl, nil
}

// RateLimit aplica un límite de solicitudes por IP y ámbito usando Valkey.
func RateLimit(limiter RateLimiter, scope string, limit int, window time.Duration, logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if limiter == nil || limit <= 0 || window <= 0 {
				next.ServeHTTP(w, r)
				return
			}

			clientIP := extractClientIP(r)
			key := fmt.Sprintf("ratelimit:%s:%s", scope, clientIP)

			allowed, remaining, retryAfter, err := limiter.Allow(r.Context(), key, limit, window)
			if err != nil {
				logger.Warn("rate_limit_backend_error",
					slog.String("scope", scope),
					slog.String("error", err.Error()),
				)
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))

			if !allowed {
				retrySeconds := int(math.Ceil(retryAfter.Seconds()))
				if retrySeconds < 1 {
					retrySeconds = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(retrySeconds))
				WriteError(w, http.StatusTooManyRequests, "RATE_LIMIT_EXCEEDED", "Demasiadas solicitudes. Intenta nuevamente en unos momentos.")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func extractBearerToken(authHeader string) (string, bool) {
	parts := strings.SplitN(strings.TrimSpace(authHeader), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	token := strings.TrimSpace(parts[1])
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return "", false
	}
	return token, true
}

func extractExplicitComplejoID(r *http.Request) (id *uuid.UUID, conflict bool, err error) {
	paramRaw := strings.TrimSpace(chi.URLParam(r, "complejo_id"))
	headerRaw := strings.TrimSpace(r.Header.Get("X-Complejo-ID"))

	var paramUUID *uuid.UUID
	if paramRaw != "" {
		parsed, parseErr := uuid.Parse(paramRaw)
		if parseErr != nil {
			return nil, false, parseErr
		}
		paramUUID = &parsed
	}

	var headerUUID *uuid.UUID
	if headerRaw != "" {
		parsed, parseErr := uuid.Parse(headerRaw)
		if parseErr != nil {
			return nil, false, parseErr
		}
		headerUUID = &parsed
	}

	if paramUUID != nil && headerUUID != nil {
		if *paramUUID != *headerUUID {
			return nil, true, nil
		}
		return paramUUID, false, nil
	}
	if paramUUID != nil {
		return paramUUID, false, nil
	}
	return headerUUID, false, nil
}

func extractClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	ip := strings.TrimSpace(r.RemoteAddr)
	if ip == "" {
		return "unknown"
	}
	return ip
}
