package suscripciones

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	apphttp "github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/http"
)

// Handler expone los endpoints administrativos B2B de planes, complejos, suscripciones y métricas.
type Handler struct {
	service *Service
	logger  *slog.Logger
}

// NewHandler construye un Handler para el módulo suscripciones.
func NewHandler(service *Service, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		service: service,
		logger:  logger,
	}
}

// RegisterAdminRoutes monta las rutas dentro de un subrouter /api/v1/admin ya protegido
// con RequireAuth + RequireRole(RoleSuperAdmin).
func (h *Handler) RegisterAdminRoutes(admin chi.Router) {
	admin.Get("/planes", h.handleListPlanes)
	admin.Post("/planes", h.handleCreatePlan)
	admin.Patch("/planes/{planID}", h.handleUpdatePlan)

	admin.Post("/complejos", h.handleCreateComplejo)
	admin.Get("/complejos", h.handleListComplejos)
	admin.Get("/complejos/{complejoID}", h.handleGetComplejoDetail)
	admin.Post("/complejos/{complejoID}/suscripciones/renovar", h.handleRenovarSuscripcion)
	admin.Patch("/complejos/{complejoID}/suscripcion/estado", h.handleCambiarEstadoSuscripcion)

	admin.Get("/metricas", h.handleGetMetricas)
}

func (h *Handler) handleListPlanes(w http.ResponseWriter, r *http.Request) {
	var activoFilter *bool
	rawActivo := strings.TrimSpace(r.URL.Query().Get("activo"))
	if rawActivo != "" {
		parsed, err := strconv.ParseBool(rawActivo)
		if err != nil {
			apphttp.WriteValidationError(w, "Parámetro de filtro inválido", map[string]string{
				"activo": "El parámetro activo debe ser true o false",
			})
			return
		}
		activoFilter = &parsed
	}

	planes, err := h.service.ListPlanes(r.Context(), activoFilter)
	if err != nil {
		h.writeServiceError(w, r, "list_planes_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, planes)
}

func (h *Handler) handleCreatePlan(w http.ResponseWriter, r *http.Request) {
	var req CreatePlanRequest
	if err := apphttp.DecodeJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	plan, err := h.service.CreatePlan(r.Context(), req)
	if err != nil {
		h.writeServiceError(w, r, "create_plan_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusCreated, plan)
}

func (h *Handler) handleUpdatePlan(w http.ResponseWriter, r *http.Request) {
	planID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "planID")))
	if err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Identificador de plan inválido")
		return
	}

	var req UpdatePlanRequest
	if err := apphttp.DecodeJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	plan, err := h.service.UpdatePlan(r.Context(), planID, req)
	if err != nil {
		h.writeServiceError(w, r, "update_plan_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, plan)
}

func (h *Handler) handleCreateComplejo(w http.ResponseWriter, r *http.Request) {
	authUser, ok := apphttp.AuthUserFromContext(r.Context())
	if !ok {
		apphttp.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Autenticación requerida")
		return
	}

	var req CreateComplejoRequest
	if err := apphttp.DecodeJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	res, err := h.service.CreateComplejo(r.Context(), authUser.UserID, req)
	if err != nil {
		h.writeServiceError(w, r, "create_complejo_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) handleListComplejos(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	fields := make(map[string]string)

	var filter ListComplejosFilter

	if rawEstado := strings.TrimSpace(q.Get("estado_suscripcion")); rawEstado != "" {
		filter.EstadoSuscripcion = &rawEstado
	}

	if rawDias := strings.TrimSpace(q.Get("por_vencer_dias")); rawDias != "" {
		dias, err := strconv.Atoi(rawDias)
		if err != nil {
			fields["por_vencer_dias"] = "Debe ser un número entero válido"
		} else {
			filter.PorVencerDias = &dias
		}
	}

	if rawPage := strings.TrimSpace(q.Get("page")); rawPage != "" {
		page, err := strconv.Atoi(rawPage)
		if err != nil || page < 1 {
			fields["page"] = "El número de página debe ser un entero mayor o igual a 1"
		} else {
			filter.Page = page
		}
	}

	if rawLimit := strings.TrimSpace(q.Get("limit")); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit < 1 {
			fields["limit"] = "El límite por página debe ser un entero mayor o igual a 1"
		} else {
			filter.Limit = limit
		}
	}

	if len(fields) > 0 {
		apphttp.WriteValidationError(w, "Parámetros de paginación o filtro inválidos", fields)
		return
	}

	res, err := h.service.ListComplejos(r.Context(), filter)
	if err != nil {
		h.writeServiceError(w, r, "list_complejos_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) handleGetComplejoDetail(w http.ResponseWriter, r *http.Request) {
	complejoID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "complejoID")))
	if err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Identificador de complejo inválido")
		return
	}

	res, err := h.service.GetComplejoDetail(r.Context(), complejoID)
	if err != nil {
		h.writeServiceError(w, r, "get_complejo_detail_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) handleRenovarSuscripcion(w http.ResponseWriter, r *http.Request) {
	authUser, ok := apphttp.AuthUserFromContext(r.Context())
	if !ok {
		apphttp.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Autenticación requerida")
		return
	}

	complejoID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "complejoID")))
	if err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Identificador de complejo inválido")
		return
	}

	var req RenovarSuscripcionRequest
	if err := apphttp.DecodeJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	res, err := h.service.RenovarSuscripcion(r.Context(), authUser.UserID, complejoID, req)
	if err != nil {
		h.writeServiceError(w, r, "renovar_suscripcion_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) handleCambiarEstadoSuscripcion(w http.ResponseWriter, r *http.Request) {
	authUser, ok := apphttp.AuthUserFromContext(r.Context())
	if !ok {
		apphttp.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Autenticación requerida")
		return
	}

	complejoID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "complejoID")))
	if err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Identificador de complejo inválido")
		return
	}

	var req CambiarEstadoSuscripcionRequest
	if err := apphttp.DecodeJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	res, err := h.service.CambiarEstadoSuscripcion(r.Context(), authUser.UserID, complejoID, req)
	if err != nil {
		h.writeServiceError(w, r, "cambiar_estado_suscripcion_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) handleGetMetricas(w http.ResponseWriter, r *http.Request) {
	res, err := h.service.GetMetricasGlobales(r.Context())
	if err != nil {
		h.writeServiceError(w, r, "get_metricas_globales_error", err)
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

	switch {
	case errors.Is(err, ErrPlanNotFound):
		apphttp.WriteError(w, http.StatusNotFound, "PLAN_NOT_FOUND", "Plan de suscripción no encontrado")
	case errors.Is(err, ErrComplejoNotFound):
		apphttp.WriteError(w, http.StatusNotFound, "COMPLEJO_NOT_FOUND", "Complejo deportivo no encontrado")
	case errors.Is(err, ErrPlanCodeAlreadyExists):
		apphttp.WriteError(w, http.StatusConflict, "PLAN_CODE_ALREADY_EXISTS", "Ya existe un plan registrado con ese código")
	case errors.Is(err, ErrEmailAlreadyExists):
		apphttp.WriteError(w, http.StatusConflict, "EMAIL_ALREADY_EXISTS", "El correo electrónico ya está registrado")
	case errors.Is(err, ErrSlugAlreadyExists):
		apphttp.WriteError(w, http.StatusConflict, "SLUG_ALREADY_EXISTS", "El slug del complejo ya está en uso")
	case errors.Is(err, ErrConflict):
		apphttp.WriteError(w, http.StatusConflict, "CONFLICT", "Conflicto de unicidad en el recurso")
	default:
		h.logger.Error(event,
			slog.String("request_id", chimw.GetReqID(r.Context())),
			slog.String("path", r.URL.Path),
			slog.String("error", err.Error()),
		)
		apphttp.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Error interno del servidor")
	}
}
