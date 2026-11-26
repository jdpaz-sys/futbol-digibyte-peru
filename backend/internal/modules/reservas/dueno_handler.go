package reservas

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apphttp "github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/http"
)

// RegisterDuenoRoutes monta las rutas de operación diaria del dueño bajo /api/v1/dueno
// (ya protegidas por RequireAuth + RequireRole("DUENO") + RequireTenant).
func (h *Handler) RegisterDuenoRoutes(dueno chi.Router) {
	dueno.Get("/reservas", h.handleListDuenoReservas)
	dueno.Post("/reservas/externas", h.handleCreateExterna)
	dueno.Get("/reservas/qr/{codigoQR}", h.handleGetDuenoReservaByQR)
	dueno.Post("/reservas/{reservaID}/aprobar", h.handleAprobarComprobante)
	dueno.Post("/reservas/{reservaID}/rechazar", h.handleRechazarComprobante)
	dueno.Post("/reservas/{reservaID}/checkin", h.handleCheckinReserva)
	dueno.Post("/reservas/{reservaID}/no-show", h.handleMarkNoShow)
	dueno.Post("/reservas/{reservaID}/cancelar", h.handleCancelReservaDueno)
	dueno.Post("/reservas/{reservaID}/reembolso-resuelto", h.handleMarkReembolsoResuelto)
	dueno.Post("/reservas/{reservaID}/reprogramar", h.handleReprogramarDueno)
	dueno.Post("/bloqueos", h.handleCreateBloqueo)
}

func (h *Handler) handleListDuenoReservas(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := apphttp.TenantIDFromContext(r.Context())
	if !ok {
		apphttp.WriteError(w, http.StatusForbidden, "TENANT_REQUIRED", "Contexto de complejo requerido")
		return
	}

	q := r.URL.Query()
	fields := make(map[string]string)

	filter := ListDuenoReservasFilter{
		FechaDesde: q.Get("fecha_desde"),
		FechaHasta: q.Get("fecha_hasta"),
		Estado:     q.Get("estado"),
		Origen:     q.Get("origen"),
	}

	if rawCancha := strings.TrimSpace(q.Get("cancha_id")); rawCancha != "" {
		parsed, err := uuid.Parse(rawCancha)
		if err != nil || parsed == uuid.Nil {
			fields["cancha_id"] = "cancha_id debe ser un UUID válido"
		} else {
			filter.CanchaID = &parsed
		}
	}

	if rawLimit := strings.TrimSpace(q.Get("limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed < 1 || parsed > MaxDuenoListLimit {
			fields["limit"] = "limit debe ser un entero entre 1 y 100"
		} else {
			filter.Limit = parsed
		}
	}

	if rawOffset := strings.TrimSpace(q.Get("offset")); rawOffset != "" {
		parsed, err := strconv.Atoi(rawOffset)
		if err != nil || parsed < 0 {
			fields["offset"] = "offset debe ser un entero mayor o igual a 0"
		} else {
			filter.Offset = parsed
		}
	}

	if len(fields) > 0 {
		apphttp.WriteValidationError(w, "Parámetros de agenda inválidos", fields)
		return
	}

	res, err := h.service.ListDuenoReservas(r.Context(), complejoID, filter)
	if err != nil {
		h.writeServiceError(w, r, "dueno_list_reservas_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) handleAprobarComprobante(w http.ResponseWriter, r *http.Request) {
	user, complejoID, ok := duenoActorFromRequest(w, r)
	if !ok {
		return
	}

	reservaID, ok := parseReservaIDParam(w, r)
	if !ok {
		return
	}

	res, err := h.service.AprobarComprobante(r.Context(), complejoID, reservaID, user.UserID)
	if err != nil {
		h.writeServiceError(w, r, "dueno_aprobar_comprobante_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) handleRechazarComprobante(w http.ResponseWriter, r *http.Request) {
	user, complejoID, ok := duenoActorFromRequest(w, r)
	if !ok {
		return
	}

	reservaID, ok := parseReservaIDParam(w, r)
	if !ok {
		return
	}

	var req RechazarComprobanteRequest
	if err := apphttp.DecodeJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	res, err := h.service.RechazarComprobante(r.Context(), complejoID, reservaID, user.UserID, req)
	if err != nil {
		h.writeServiceError(w, r, "dueno_rechazar_comprobante_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) handleCreateExterna(w http.ResponseWriter, r *http.Request) {
	user, complejoID, ok := duenoActorFromRequest(w, r)
	if !ok {
		return
	}

	var req CreateExternaRequest
	if err := apphttp.DecodeJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	res, err := h.service.CreateExterna(r.Context(), complejoID, user.UserID, req)
	if err != nil {
		h.writeServiceError(w, r, "dueno_create_externa_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) handleCreateBloqueo(w http.ResponseWriter, r *http.Request) {
	user, complejoID, ok := duenoActorFromRequest(w, r)
	if !ok {
		return
	}

	var req CreateBloqueoRequest
	if err := apphttp.DecodeJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	res, err := h.service.CreateBloqueo(r.Context(), complejoID, user.UserID, req)
	if err != nil {
		h.writeServiceError(w, r, "dueno_create_bloqueo_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) handleGetDuenoReservaByQR(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := apphttp.TenantIDFromContext(r.Context())
	if !ok {
		apphttp.WriteError(w, http.StatusForbidden, "TENANT_REQUIRED", "Contexto de complejo requerido")
		return
	}

	codigoQR := chi.URLParam(r, "codigoQR")
	res, err := h.service.GetDuenoReservaByQR(r.Context(), complejoID, codigoQR)
	if err != nil {
		h.writeServiceError(w, r, "dueno_get_reserva_qr_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) handleCheckinReserva(w http.ResponseWriter, r *http.Request) {
	user, complejoID, ok := duenoActorFromRequest(w, r)
	if !ok {
		return
	}

	reservaID, ok := parseReservaIDParam(w, r)
	if !ok {
		return
	}

	var req CheckinRequest
	if err := apphttp.DecodeOptionalJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	res, err := h.service.CheckinReserva(r.Context(), complejoID, reservaID, user.UserID, req)
	if err != nil {
		h.writeServiceError(w, r, "dueno_checkin_reserva_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) handleMarkNoShow(w http.ResponseWriter, r *http.Request) {
	user, complejoID, ok := duenoActorFromRequest(w, r)
	if !ok {
		return
	}

	reservaID, ok := parseReservaIDParam(w, r)
	if !ok {
		return
	}

	var req NoShowRequest
	if err := apphttp.DecodeOptionalJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	res, err := h.service.MarkNoShow(r.Context(), complejoID, reservaID, user.UserID, req)
	if err != nil {
		h.writeServiceError(w, r, "dueno_no_show_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) handleCancelReservaDueno(w http.ResponseWriter, r *http.Request) {
	user, complejoID, ok := duenoActorFromRequest(w, r)
	if !ok {
		return
	}

	reservaID, ok := parseReservaIDParam(w, r)
	if !ok {
		return
	}

	var req CancelarReservaRequest
	if err := apphttp.DecodeJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	res, err := h.service.CancelReservaDueno(r.Context(), complejoID, reservaID, user.UserID, req)
	if err != nil {
		h.writeServiceError(w, r, "dueno_cancel_reserva_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) handleMarkReembolsoResuelto(w http.ResponseWriter, r *http.Request) {
	user, complejoID, ok := duenoActorFromRequest(w, r)
	if !ok {
		return
	}

	reservaID, ok := parseReservaIDParam(w, r)
	if !ok {
		return
	}

	var req ReembolsoResueltoRequest
	if err := apphttp.DecodeOptionalJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	res, err := h.service.MarkReembolsoResuelto(r.Context(), complejoID, reservaID, user.UserID, req)
	if err != nil {
		h.writeServiceError(w, r, "dueno_reembolso_resuelto_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) handleReprogramarDueno(w http.ResponseWriter, r *http.Request) {
	user, complejoID, ok := duenoActorFromRequest(w, r)
	if !ok {
		return
	}

	reservaID, ok := parseReservaIDParam(w, r)
	if !ok {
		return
	}

	var req ReprogramarReservaRequest
	if err := apphttp.DecodeJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	res, err := h.service.ReprogramarDueno(r.Context(), complejoID, reservaID, user.UserID, req)
	if err != nil {
		h.writeServiceError(w, r, "dueno_reprogramar_reserva_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) handleCancelReservaJugador(w http.ResponseWriter, r *http.Request) {
	user, ok := apphttp.AuthUserFromContext(r.Context())
	if !ok {
		apphttp.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Autenticación requerida")
		return
	}

	reservaID, ok := parseReservaIDParam(w, r)
	if !ok {
		return
	}

	var req CancelarReservaRequest
	if err := apphttp.DecodeOptionalJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	res, err := h.service.CancelReservaJugador(r.Context(), user.UserID, reservaID, req)
	if err != nil {
		h.writeServiceError(w, r, "jugador_cancel_reserva_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) handleReprogramarJugador(w http.ResponseWriter, r *http.Request) {
	user, ok := apphttp.AuthUserFromContext(r.Context())
	if !ok {
		apphttp.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Autenticación requerida")
		return
	}

	reservaID, ok := parseReservaIDParam(w, r)
	if !ok {
		return
	}

	var req ReprogramarReservaRequest
	if err := apphttp.DecodeJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	res, err := h.service.ReprogramarJugador(r.Context(), user.UserID, reservaID, req)
	if err != nil {
		h.writeServiceError(w, r, "jugador_reprogramar_reserva_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, res)
}

func duenoActorFromRequest(w http.ResponseWriter, r *http.Request) (apphttp.AuthUser, uuid.UUID, bool) {
	user, ok := apphttp.AuthUserFromContext(r.Context())
	if !ok {
		apphttp.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Autenticación requerida")
		return apphttp.AuthUser{}, uuid.Nil, false
	}
	complejoID, ok := apphttp.TenantIDFromContext(r.Context())
	if !ok {
		apphttp.WriteError(w, http.StatusForbidden, "TENANT_REQUIRED", "Contexto de complejo requerido")
		return apphttp.AuthUser{}, uuid.Nil, false
	}
	return user, complejoID, true
}

func parseReservaIDParam(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	reservaID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "reservaID")))
	if err != nil || reservaID == uuid.Nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Identificador de reserva inválido")
		return uuid.Nil, false
	}
	return reservaID, true
}
