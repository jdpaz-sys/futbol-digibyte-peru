package reportes

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	apphttp "github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/http"
)

// Handler expone los endpoints HTTP de reportes del dueño.
type Handler struct {
	service *Service
	logger  *slog.Logger
}

// NewHandler crea el manejador HTTP del módulo reportes.
func NewHandler(service *Service, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		service: service,
		logger:  logger,
	}
}

// RegisterDuenoRoutes monta las rutas protegidas por RequireAuth + RequireRole("DUENO") + RequireTenant.
func (h *Handler) RegisterDuenoRoutes(dueno chi.Router) {
	dueno.Get("/reportes/resumen", h.handleGetResumen)
}

func (h *Handler) handleGetResumen(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := apphttp.TenantIDFromContext(r.Context())
	if !ok {
		apphttp.WriteError(w, http.StatusForbidden, "TENANT_REQUIRED", "Contexto de complejo requerido")
		return
	}

	q := r.URL.Query()
	res, err := h.service.GetResumen(r.Context(), complejoID, q.Get("desde"), q.Get("hasta"))
	if err != nil {
		h.writeServiceError(w, r, "dueno_reportes_resumen_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) writeServiceError(w http.ResponseWriter, r *http.Request, event string, err error) {
	var valErr *ValidationError
	if errors.As(err, &valErr) {
		apphttp.WriteValidationError(w, valErr.Message, valErr.Fields)
		return
	}
	if errors.Is(err, context.Canceled) {
		return
	}

	h.logger.Error(event,
		slog.String("request_id", chimw.GetReqID(r.Context())),
		slog.String("path", r.URL.Path),
		slog.String("error", err.Error()),
	)
	apphttp.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Error interno del servidor")
}
