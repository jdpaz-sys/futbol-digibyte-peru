package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	apphttp "github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/http"
)

// Handler expone los endpoints HTTP del módulo de autenticación bajo /api/v1/auth.
type Handler struct {
	service     *Service
	rateLimiter apphttp.RateLimiter
	logger      *slog.Logger
}

// NewHandler crea el handler HTTP del módulo auth.
func NewHandler(service *Service, rateLimiter apphttp.RateLimiter, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		service:     service,
		rateLimiter: rateLimiter,
		logger:      logger,
	}
}

// RegisterRoutes monta las rutas de /api/v1/auth con rate limiting en endpoints sensibles.
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Route("/auth", func(ar chi.Router) {
		ar.With(apphttp.RateLimit(h.rateLimiter, "auth_register", 5, time.Minute, h.logger)).
			Post("/register", h.Register)

		ar.With(apphttp.RateLimit(h.rateLimiter, "auth_login", 10, time.Minute, h.logger)).
			Post("/login", h.Login)

		ar.With(apphttp.RateLimit(h.rateLimiter, "auth_refresh", 20, time.Minute, h.logger)).
			Post("/refresh", h.Refresh)

		ar.With(apphttp.RateLimit(h.rateLimiter, "auth_logout", 20, time.Minute, h.logger)).
			Post("/logout", h.Logout)

		ar.With(apphttp.RequireAuth(h.service.ParseAccessToken)).
			Get("/me", h.Me)
	})
}

// Register maneja POST /api/v1/auth/register (registro rápido de JUGADOR).
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := apphttp.DecodeJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	profile, err := h.service.Register(r.Context(), req)
	if err != nil {
		h.handleError(w, err)
		return
	}

	apphttp.WriteJSON(w, http.StatusCreated, RegisterResponse{User: profile})
}

// Login maneja POST /api/v1/auth/login.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := apphttp.DecodeJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	tokens, err := h.service.Login(r.Context(), req)
	if err != nil {
		h.handleError(w, err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, tokens)
}

// Refresh maneja POST /api/v1/auth/refresh rotando el Refresh Token en Valkey.
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req RefreshRequest
	if err := apphttp.DecodeJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	tokens, err := h.service.Refresh(r.Context(), req)
	if err != nil {
		h.handleError(w, err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, tokens)
}

// Logout maneja POST /api/v1/auth/logout revocando el Refresh Token en Valkey.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	var req LogoutRequest
	if err := apphttp.DecodeJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	if err := h.service.Logout(r.Context(), req); err != nil {
		h.handleError(w, err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, MessageResponse{Message: "Sesión cerrada correctamente"})
}

// Me maneja GET /api/v1/auth/me devolviendo el perfil limpio del usuario autenticado.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	authUser, ok := apphttp.AuthUserFromContext(r.Context())
	if !ok {
		apphttp.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Autenticación requerida")
		return
	}

	profile, err := h.service.Me(r.Context(), authUser.UserID)
	if err != nil {
		h.handleError(w, err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, MeResponse{User: profile})
}

func (h *Handler) handleError(w http.ResponseWriter, err error) {
	var validationErr *ValidationError
	if errors.As(err, &validationErr) {
		apphttp.WriteValidationError(w, validationErr.Message, validationErr.Fields)
		return
	}

	switch {
	case errors.Is(err, ErrEmailAlreadyExists):
		apphttp.WriteError(w, http.StatusConflict, "EMAIL_ALREADY_EXISTS", "Ya existe una cuenta registrada con este correo")
	case errors.Is(err, ErrInvalidCredentials):
		apphttp.WriteError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "Correo o contraseña incorrectos")
	case errors.Is(err, ErrInvalidToken):
		apphttp.WriteError(w, http.StatusUnauthorized, "INVALID_TOKEN", "Token inválido o expirado")
	case errors.Is(err, ErrUserNotFound):
		apphttp.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Sesión inválida")
	case errors.Is(err, ErrAccountInactive):
		apphttp.WriteError(w, http.StatusForbidden, "ACCOUNT_INACTIVE", "La cuenta se encuentra desactivada")
	default:
		h.logger.Error("auth_internal_error", slog.String("error", err.Error()))
		apphttp.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Error interno del servidor")
	}
}
