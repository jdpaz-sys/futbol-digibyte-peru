package complejos

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

// Handler expone los endpoints del panel de autogestión del dueño bajo /api/v1/dueno/*.
type Handler struct {
	service *Service
	logger  *slog.Logger
}

// NewHandler construye un Handler para el módulo complejos.
func NewHandler(service *Service, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		service: service,
		logger:  logger,
	}
}

// RegisterDuenoRoutes monta todas las rutas dentro de /api/v1/dueno ya protegido con
// RequireAuth + RequireRole(RoleDueno) + RequireTenant.
func (h *Handler) RegisterDuenoRoutes(dueno chi.Router) {
	dueno.Get("/complejo", h.handleGetComplejo)
	dueno.Patch("/complejo", h.handleUpdateComplejo)
	dueno.Post("/complejo/publicar", h.handlePublishComplejo)
	dueno.Post("/complejo/despublicar", h.handleUnpublishComplejo)

	dueno.Get("/canchas", h.handleListCanchas)
	dueno.Post("/canchas", h.handleCreateCancha)
	dueno.Patch("/canchas/{canchaID}", h.handleUpdateCancha)
	dueno.Delete("/canchas/{canchaID}", h.handleDeactivateCancha)

	dueno.Get("/horarios", h.handleListHorarios)
	dueno.Put("/horarios", h.handleUpsertHorarios)

	dueno.Get("/tarifas", h.handleListTarifas)
	dueno.Post("/tarifas", h.handleCreateTarifa)
	dueno.Patch("/tarifas/{tarifaID}", h.handleUpdateTarifa)
	dueno.Delete("/tarifas/{tarifaID}", h.handleDeleteTarifa)

	dueno.Get("/cupones", h.handleListCupones)
	dueno.Post("/cupones", h.handleCreateCupon)
	dueno.Patch("/cupones/{cuponID}", h.handleUpdateCupon)

	dueno.Get("/metodos-pago", h.handleListMetodosPago)
	dueno.Post("/metodos-pago", h.handleCreateMetodoPago)
	dueno.Patch("/metodos-pago/{metodoID}", h.handleUpdateMetodoPago)
	dueno.Delete("/metodos-pago/{metodoID}", h.handleDeactivateMetodoPago)
}

// ---------- Complejo ----------

func (h *Handler) handleGetComplejo(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := extractTenantID(w, r)
	if !ok {
		return
	}

	perfil, err := h.service.GetComplejo(r.Context(), complejoID)
	if err != nil {
		h.writeServiceError(w, r, "get_complejo_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, perfil)
}

func (h *Handler) handleUpdateComplejo(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := extractTenantID(w, r)
	if !ok {
		return
	}

	var req UpdateComplejoRequest
	if err := apphttp.DecodeJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	perfil, err := h.service.UpdateComplejo(r.Context(), complejoID, req)
	if err != nil {
		h.writeServiceError(w, r, "update_complejo_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, perfil)
}

func (h *Handler) handlePublishComplejo(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := extractTenantID(w, r)
	if !ok {
		return
	}

	perfil, err := h.service.PublishComplejo(r.Context(), complejoID)
	if err != nil {
		h.writeServiceError(w, r, "publish_complejo_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, perfil)
}

func (h *Handler) handleUnpublishComplejo(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := extractTenantID(w, r)
	if !ok {
		return
	}

	perfil, err := h.service.UnpublishComplejo(r.Context(), complejoID)
	if err != nil {
		h.writeServiceError(w, r, "unpublish_complejo_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, perfil)
}

// ---------- Canchas ----------

func (h *Handler) handleListCanchas(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := extractTenantID(w, r)
	if !ok {
		return
	}

	activaFilter, ok := parseOptionalBoolQuery(w, r, "activa")
	if !ok {
		return
	}

	canchas, err := h.service.ListCanchas(r.Context(), complejoID, activaFilter)
	if err != nil {
		h.writeServiceError(w, r, "list_canchas_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, canchas)
}

func (h *Handler) handleCreateCancha(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := extractTenantID(w, r)
	if !ok {
		return
	}

	var req CreateCanchaRequest
	if err := apphttp.DecodeJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	cancha, err := h.service.CreateCancha(r.Context(), complejoID, req)
	if err != nil {
		h.writeServiceError(w, r, "create_cancha_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusCreated, cancha)
}

func (h *Handler) handleUpdateCancha(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := extractTenantID(w, r)
	if !ok {
		return
	}

	canchaID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "canchaID")))
	if err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Identificador de cancha inválido")
		return
	}

	var req UpdateCanchaRequest
	if err := apphttp.DecodeJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	cancha, err := h.service.UpdateCancha(r.Context(), complejoID, canchaID, req)
	if err != nil {
		h.writeServiceError(w, r, "update_cancha_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, cancha)
}

func (h *Handler) handleDeactivateCancha(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := extractTenantID(w, r)
	if !ok {
		return
	}

	canchaID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "canchaID")))
	if err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Identificador de cancha inválido")
		return
	}

	cancha, err := h.service.DeactivateCancha(r.Context(), complejoID, canchaID)
	if err != nil {
		h.writeServiceError(w, r, "deactivate_cancha_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, cancha)
}

// ---------- Horarios ----------

func (h *Handler) handleListHorarios(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := extractTenantID(w, r)
	if !ok {
		return
	}

	horarios, err := h.service.ListHorarios(r.Context(), complejoID)
	if err != nil {
		h.writeServiceError(w, r, "list_horarios_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, horarios)
}

func (h *Handler) handleUpsertHorarios(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := extractTenantID(w, r)
	if !ok {
		return
	}

	var req UpsertHorariosRequest
	if err := apphttp.DecodeJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	horarios, err := h.service.UpsertHorarios(r.Context(), complejoID, req)
	if err != nil {
		h.writeServiceError(w, r, "upsert_horarios_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, horarios)
}

// ---------- Tarifas ----------

func (h *Handler) handleListTarifas(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := extractTenantID(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()
	fields := make(map[string]string)
	var filter TarifasFilter

	if rawCancha := strings.TrimSpace(q.Get("cancha_id")); rawCancha != "" {
		parsed, err := uuid.Parse(rawCancha)
		if err != nil {
			fields["cancha_id"] = "Debe ser un UUID válido"
		} else {
			filter.CanchaID = &parsed
		}
	}

	if rawDia := strings.TrimSpace(q.Get("dia_semana")); rawDia != "" {
		dia, err := strconv.Atoi(rawDia)
		if err != nil || dia < 0 || dia > 6 {
			fields["dia_semana"] = "Debe ser un entero entre 0 y 6"
		} else {
			d16 := int16(dia)
			filter.DiaSemana = &d16
		}
	}

	if rawActiva := strings.TrimSpace(q.Get("activa")); rawActiva != "" {
		parsed, err := strconv.ParseBool(rawActiva)
		if err != nil {
			fields["activa"] = "El parámetro activa debe ser true o false"
		} else {
			filter.Activa = &parsed
		}
	}

	if len(fields) > 0 {
		apphttp.WriteValidationError(w, "Parámetros de filtro inválidos", fields)
		return
	}

	tarifas, err := h.service.ListTarifas(r.Context(), complejoID, filter)
	if err != nil {
		h.writeServiceError(w, r, "list_tarifas_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, tarifas)
}

func (h *Handler) handleCreateTarifa(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := extractTenantID(w, r)
	if !ok {
		return
	}

	var req CreateTarifaRequest
	if err := apphttp.DecodeJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	tarifa, err := h.service.CreateTarifa(r.Context(), complejoID, req)
	if err != nil {
		h.writeServiceError(w, r, "create_tarifa_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusCreated, tarifa)
}

func (h *Handler) handleUpdateTarifa(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := extractTenantID(w, r)
	if !ok {
		return
	}

	tarifaID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "tarifaID")))
	if err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Identificador de tarifa inválido")
		return
	}

	var req UpdateTarifaRequest
	if err := apphttp.DecodeJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	tarifa, err := h.service.UpdateTarifa(r.Context(), complejoID, tarifaID, req)
	if err != nil {
		h.writeServiceError(w, r, "update_tarifa_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, tarifa)
}

func (h *Handler) handleDeleteTarifa(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := extractTenantID(w, r)
	if !ok {
		return
	}

	tarifaID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "tarifaID")))
	if err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Identificador de tarifa inválido")
		return
	}

	if err := h.service.DeleteTarifa(r.Context(), complejoID, tarifaID); err != nil {
		h.writeServiceError(w, r, "delete_tarifa_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, DeleteResult{
		ID:      tarifaID,
		Deleted: true,
	})
}

// ---------- Cupones ----------

func (h *Handler) handleListCupones(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := extractTenantID(w, r)
	if !ok {
		return
	}

	activoFilter, ok := parseOptionalBoolQuery(w, r, "activo")
	if !ok {
		return
	}

	cupones, err := h.service.ListCupones(r.Context(), complejoID, activoFilter)
	if err != nil {
		h.writeServiceError(w, r, "list_cupones_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, cupones)
}

func (h *Handler) handleCreateCupon(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := extractTenantID(w, r)
	if !ok {
		return
	}

	var req CreateCuponRequest
	if err := apphttp.DecodeJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	cupon, err := h.service.CreateCupon(r.Context(), complejoID, req)
	if err != nil {
		h.writeServiceError(w, r, "create_cupon_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusCreated, cupon)
}

func (h *Handler) handleUpdateCupon(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := extractTenantID(w, r)
	if !ok {
		return
	}

	cuponID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "cuponID")))
	if err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Identificador de cupón inválido")
		return
	}

	var req UpdateCuponRequest
	if err := apphttp.DecodeJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	cupon, err := h.service.UpdateCupon(r.Context(), complejoID, cuponID, req)
	if err != nil {
		h.writeServiceError(w, r, "update_cupon_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, cupon)
}

// ---------- Métodos de Pago ----------

func (h *Handler) handleListMetodosPago(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := extractTenantID(w, r)
	if !ok {
		return
	}

	activoFilter, ok := parseOptionalBoolQuery(w, r, "activo")
	if !ok {
		return
	}

	metodos, err := h.service.ListMetodosPago(r.Context(), complejoID, activoFilter)
	if err != nil {
		h.writeServiceError(w, r, "list_metodos_pago_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, metodos)
}

func (h *Handler) handleCreateMetodoPago(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := extractTenantID(w, r)
	if !ok {
		return
	}

	var req CreateMetodoPagoRequest
	if err := apphttp.DecodeJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	metodo, err := h.service.CreateMetodoPago(r.Context(), complejoID, req)
	if err != nil {
		h.writeServiceError(w, r, "create_metodo_pago_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusCreated, metodo)
}

func (h *Handler) handleUpdateMetodoPago(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := extractTenantID(w, r)
	if !ok {
		return
	}

	metodoID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "metodoID")))
	if err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Identificador de método de pago inválido")
		return
	}

	var req UpdateMetodoPagoRequest
	if err := apphttp.DecodeJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	metodo, err := h.service.UpdateMetodoPago(r.Context(), complejoID, metodoID, req)
	if err != nil {
		h.writeServiceError(w, r, "update_metodo_pago_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, metodo)
}

func (h *Handler) handleDeactivateMetodoPago(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := extractTenantID(w, r)
	if !ok {
		return
	}

	metodoID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "metodoID")))
	if err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Identificador de método de pago inválido")
		return
	}

	metodo, err := h.service.DeactivateMetodoPago(r.Context(), complejoID, metodoID)
	if err != nil {
		h.writeServiceError(w, r, "deactivate_metodo_pago_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, metodo)
}

// ---------- Helpers HTTP ----------

func extractTenantID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	complejoID, ok := apphttp.TenantIDFromContext(r.Context())
	if !ok || complejoID == uuid.Nil {
		apphttp.WriteError(w, http.StatusForbidden, "TENANT_REQUIRED", "No se pudo verificar el complejo deportivo autenticado")
		return uuid.Nil, false
	}
	return complejoID, true
}

func parseOptionalBoolQuery(w http.ResponseWriter, r *http.Request, param string) (*bool, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(param))
	if raw == "" {
		return nil, true
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		apphttp.WriteValidationError(w, "Parámetro de filtro inválido", map[string]string{
			param: "El parámetro " + param + " debe ser true o false",
		})
		return nil, false
	}
	return &parsed, true
}

type publishErrorResponse struct {
	Error publishErrorDetail `json:"error"`
}

type publishErrorDetail struct {
	Code                string                 `json:"code"`
	Message             string                 `json:"message"`
	RequisitosFaltantes []RequisitoPublicacion `json:"requisitos_faltantes"`
}

func (h *Handler) writeServiceError(w http.ResponseWriter, r *http.Request, event string, err error) {
	var valErr *ValidationError
	if errors.As(err, &valErr) {
		apphttp.WriteValidationError(w, valErr.Message, valErr.Fields)
		return
	}

	var pubErr *PublishRequirementsError
	if errors.As(err, &pubErr) {
		apphttp.WriteRawJSON(w, http.StatusUnprocessableEntity, publishErrorResponse{
			Error: publishErrorDetail{
				Code:                "PUBLISH_REQUIREMENTS_NOT_MET",
				Message:             pubErr.Message,
				RequisitosFaltantes: pubErr.RequisitosFaltantes,
			},
		})
		return
	}

	switch {
	case errors.Is(err, ErrComplejoNotFound):
		apphttp.WriteError(w, http.StatusNotFound, "COMPLEJO_NOT_FOUND", "Complejo deportivo no encontrado")
	case errors.Is(err, ErrCanchaNotFound):
		apphttp.WriteError(w, http.StatusNotFound, "CANCHA_NOT_FOUND", "Cancha no encontrada en este complejo")
	case errors.Is(err, ErrTarifaNotFound):
		apphttp.WriteError(w, http.StatusNotFound, "TARIFA_NOT_FOUND", "Tarifa por franja no encontrada")
	case errors.Is(err, ErrCuponNotFound):
		apphttp.WriteError(w, http.StatusNotFound, "CUPON_NOT_FOUND", "Cupón no encontrado")
	case errors.Is(err, ErrMetodoPagoNotFound):
		apphttp.WriteError(w, http.StatusNotFound, "METODO_PAGO_NOT_FOUND", "Método de pago no encontrado")
	case errors.Is(err, ErrTarifaOverlap):
		apphttp.WriteError(w, http.StatusConflict, "TARIFA_OVERLAP", "La franja horaria se solapa con otra tarifa activa para el mismo día y alcance")
	case errors.Is(err, ErrCuponCodeAlreadyExists):
		apphttp.WriteError(w, http.StatusConflict, "CUPON_CODE_ALREADY_EXISTS", "Ya existe un cupón registrado con ese código en este complejo")
	case errors.Is(err, ErrConflict):
		apphttp.WriteError(w, http.StatusConflict, "CONFLICT", "Conflicto de unicidad en el recurso")
	case errors.Is(err, ErrCheckViolation):
		apphttp.WriteError(w, http.StatusBadRequest, "CHECK_VIOLATION", err.Error())
	default:
		h.logger.Error(event,
			slog.String("request_id", chimw.GetReqID(r.Context())),
			slog.String("path", r.URL.Path),
			slog.String("error", err.Error()),
		)
		apphttp.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Error interno del servidor")
	}
}
