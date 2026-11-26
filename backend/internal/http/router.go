package apphttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

const (
	RoleSuperAdmin = "SUPER_ADMIN"
	RoleDueno      = "DUENO"
	RoleJugador    = "JUGADOR"

	maxJSONBodyBytes int64 = 1 << 20 // 1 MiB
)

// SuccessResponse define el envoltorio JSON estándar para respuestas exitosas.
type SuccessResponse struct {
	Data any `json:"data"`
}

// ErrorDetail describe un error estructurado y seguro para clientes de la API.
type ErrorDetail struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

// ErrorResponse define el envoltorio JSON estándar para respuestas de error.
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// RouterConfig agrupa las dependencias necesarias para construir el router principal.
type RouterConfig struct {
	CORSAllowedOrigins []string
	Logger             *slog.Logger
	HealthHandler      http.HandlerFunc
	TokenParser        TokenParser
	TenantChecker      TenantOwnershipChecker
	MountV1            func(r chi.Router)
	MountPublic        func(r chi.Router)
	MountAdmin         func(r chi.Router)
	MountDueno         func(r chi.Router)
	MountJugador       func(r chi.Router)
	MountAuthenticated func(r chi.Router)
}

// NewRouter construye el router chi/v5 con middlewares globales y prefijo /api/v1.
func NewRouter(cfg RouterConfig) *chi.Mux {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	r := chi.NewRouter()

	allowedOrigins := cfg.CORSAllowedOrigins
	if len(allowedOrigins) == 0 {
		allowedOrigins = []string{"http://localhost:3000"}
	}

	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(requestLogger(logger))
	r.Use(jsonRecoverer(logger))
	r.Use(securityHeaders)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   allowedOrigins,
		AllowedMethods:   []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Complejo-ID", "X-Request-ID"},
		ExposedHeaders:   []string{"X-Request-ID", "X-RateLimit-Limit", "X-RateLimit-Remaining", "Retry-After"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		WriteError(w, http.StatusNotFound, "NOT_FOUND", "Recurso no encontrado")
	})

	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		WriteError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Método HTTP no permitido para esta ruta")
	})

	r.Route("/api/v1", func(v1 chi.Router) {
		if cfg.HealthHandler != nil {
			v1.Get("/health", cfg.HealthHandler)
		}
		if cfg.MountV1 != nil {
			cfg.MountV1(v1)
		}
		if cfg.MountPublic != nil {
			v1.Route("/public", func(public chi.Router) {
				cfg.MountPublic(public)
			})
		}
		if cfg.MountAdmin != nil {
			v1.Route("/admin", func(admin chi.Router) {
				admin.Use(RequireAuth(cfg.TokenParser))
				admin.Use(RequireRole(RoleSuperAdmin))
				cfg.MountAdmin(admin)
			})
		}
		if cfg.MountDueno != nil {
			v1.Route("/dueno", func(dueno chi.Router) {
				dueno.Use(RequireAuth(cfg.TokenParser))
				dueno.Use(RequireRole(RoleDueno))
				dueno.Use(RequireTenant(cfg.TenantChecker))
				cfg.MountDueno(dueno)
			})
		}
		if cfg.MountJugador != nil {
			v1.Route("/jugador", func(jugador chi.Router) {
				jugador.Use(RequireAuth(cfg.TokenParser))
				jugador.Use(RequireRole(RoleJugador))
				cfg.MountJugador(jugador)
			})
		}
		if cfg.MountAuthenticated != nil {
			v1.Group(func(authed chi.Router) {
				authed.Use(RequireAuth(cfg.TokenParser))
				cfg.MountAuthenticated(authed)
			})
		}
	})

	return r
}

// WriteRawJSON serializa cualquier estructura como respuesta JSON con Content-Type uniforme.
func WriteRawJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// WriteJSON escribe una respuesta exitosa bajo el contrato uniforme {"data": ...}.
func WriteJSON(w http.ResponseWriter, status int, data any) {
	WriteRawJSON(w, status, SuccessResponse{Data: data})
}

// WriteError escribe una respuesta de error bajo el contrato uniforme {"error": {...}}.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteRawJSON(w, status, ErrorResponse{
		Error: ErrorDetail{
			Code:    code,
			Message: message,
		},
	})
}

// WriteValidationError escribe un error 400 con detalle por campo.
func WriteValidationError(w http.ResponseWriter, message string, details map[string]string) {
	WriteRawJSON(w, http.StatusBadRequest, ErrorResponse{
		Error: ErrorDetail{
			Code:    "VALIDATION_ERROR",
			Message: message,
			Details: details,
		},
	})
}

// DecodeJSON decodifica un cuerpo JSON acotado en tamaño y rechaza campos desconocidos.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return fmt.Errorf("el cuerpo de la solicitud excede el tamaño máximo permitido")
		}
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("el cuerpo JSON es obligatorio")
		}
		return fmt.Errorf("JSON inválido: %w", err)
	}

	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("el cuerpo debe contener un único objeto JSON")
	}

	return nil
}

// DecodeOptionalJSON decodifica un cuerpo JSON opcional: permite cuerpo vacío sin error,
// pero valida tamaño, formato y campos desconocidos cuando existe contenido.
func DecodeOptionalJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if r.Body == nil || r.ContentLength == 0 {
		return nil
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return fmt.Errorf("el cuerpo de la solicitud excede el tamaño máximo permitido")
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("JSON inválido: %w", err)
	}

	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("el cuerpo debe contener un único objeto JSON")
	}

	return nil
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(ww, r)

			status := ww.Status()
			if status == 0 {
				status = http.StatusOK
			}

			logger.Info("http_request",
				slog.String("request_id", chimw.GetReqID(r.Context())),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", status),
				slog.Int("bytes", ww.BytesWritten()),
				slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000.0),
			)
		})
	}
}

func jsonRecoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					if rec == http.ErrAbortHandler {
						panic(rec)
					}
					logger.Error("panic_recovered",
						slog.String("request_id", chimw.GetReqID(r.Context())),
						slog.Any("panic", rec),
						slog.String("stack", string(debug.Stack())),
					)
					WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Error interno del servidor")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
