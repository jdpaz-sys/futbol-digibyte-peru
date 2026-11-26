package reservas

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	apphttp "github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/http"
)

const (
	createReservaRateScope  = "jugador_create_reserva"
	createReservaRateLimit  = 20
	createReservaRateWindow = time.Minute

	maxMultipartBodyBytes int64 = MaxEvidenciaBytes + (512 << 10) // 5 MiB evidencia + 512 KiB campos/cabeceras
	multipartMemoryLimit  int64 = MaxEvidenciaBytes + (256 << 10)
)

// Handler expone los endpoints HTTP de disponibilidad, cotización, Holds, comprobantes y evidencia privada.
type Handler struct {
	service     *Service
	rateLimiter apphttp.RateLimiter
	logger      *slog.Logger
}

// NewHandler crea el manejador HTTP del módulo reservas.
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

// RegisterPublicRoutes monta disponibilidad y cotizador bajo /api/v1/public.
func (h *Handler) RegisterPublicRoutes(public chi.Router) {
	public.Get("/complejos/{slug}/disponibilidad", h.handleGetDisponibilidad)
	public.Post("/complejos/{slug}/cotizar", h.handleCotizar)
}

// RegisterJugadorRoutes monta las rutas protegidas con RequireAuth + RequireRole("JUGADOR") bajo /api/v1/jugador.
func (h *Handler) RegisterJugadorRoutes(jugador chi.Router) {
	jugador.With(apphttp.RateLimit(h.rateLimiter, createReservaRateScope, createReservaRateLimit, createReservaRateWindow, h.logger)).
		Post("/reservas", h.handleCreateReserva)
	jugador.Post("/reservas/{reservaID}/comprobante", h.handleUploadComprobante)
	jugador.Get("/reservas", h.handleListJugadorReservas)
	jugador.Get("/reservas/{reservaID}", h.handleGetJugadorReservaDetail)
}

// RegisterAuthenticatedRoutes monta rutas protegidas únicamente con RequireAuth (múltiples roles con validación por recurso).
func (h *Handler) RegisterAuthenticatedRoutes(authed chi.Router) {
	authed.Get("/comprobantes/{comprobanteID}/evidencia", h.handleGetComprobanteEvidencia)
}

func (h *Handler) handleGetDisponibilidad(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	q := r.URL.Query()

	fields := make(map[string]string)

	rawCanchaID := strings.TrimSpace(q.Get("cancha_id"))
	var canchaID uuid.UUID
	if rawCanchaID == "" {
		fields["cancha_id"] = "El parámetro cancha_id es obligatorio"
	} else {
		parsed, err := uuid.Parse(rawCanchaID)
		if err != nil || parsed == uuid.Nil {
			fields["cancha_id"] = "El parámetro cancha_id debe ser un UUID válido"
		} else {
			canchaID = parsed
		}
	}

	rawFecha := strings.TrimSpace(q.Get("fecha"))
	if rawFecha == "" {
		fields["fecha"] = "El parámetro fecha es obligatorio en formato YYYY-MM-DD"
	}

	if len(fields) > 0 {
		apphttp.WriteValidationError(w, "Parámetros de disponibilidad inválidos", fields)
		return
	}

	res, err := h.service.GetDisponibilidad(r.Context(), slug, canchaID, rawFecha)
	if err != nil {
		h.writeServiceError(w, r, "public_disponibilidad_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) handleCotizar(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")

	var req CotizarRequest
	if err := apphttp.DecodeJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	res, err := h.service.Cotizar(r.Context(), slug, req)
	if err != nil {
		h.writeServiceError(w, r, "public_cotizar_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) handleCreateReserva(w http.ResponseWriter, r *http.Request) {
	user, ok := apphttp.AuthUserFromContext(r.Context())
	if !ok {
		apphttp.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Autenticación requerida")
		return
	}

	var req CreateReservaRequest
	if err := apphttp.DecodeJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	res, err := h.service.CreateReserva(r.Context(), user.UserID, req)
	if err != nil {
		h.writeServiceError(w, r, "jugador_create_reserva_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) handleUploadComprobante(w http.ResponseWriter, r *http.Request) {
	user, ok := apphttp.AuthUserFromContext(r.Context())
	if !ok {
		apphttp.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Autenticación requerida")
		return
	}

	reservaID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "reservaID")))
	if err != nil || reservaID == uuid.Nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Identificador de reserva inválido")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxMultipartBodyBytes)
	if err := r.ParseMultipartForm(multipartMemoryLimit); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			apphttp.WriteError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", ErrFileTooLarge.Error())
			return
		}
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_MULTIPART", "La solicitud debe enviarse en formato multipart/form-data válido")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	fields := make(map[string]string)

	rawMetodoID := strings.TrimSpace(r.FormValue("metodo_pago_id"))
	var metodoPagoID uuid.UUID
	if rawMetodoID == "" {
		fields["metodo_pago_id"] = "metodo_pago_id es obligatorio"
	} else {
		parsed, parseErr := uuid.Parse(rawMetodoID)
		if parseErr != nil || parsed == uuid.Nil {
			fields["metodo_pago_id"] = "metodo_pago_id debe ser un UUID válido"
		} else {
			metodoPagoID = parsed
		}
	}

	codigoOperacion := strings.TrimSpace(r.FormValue("codigo_operacion"))
	if codigoOperacion == "" {
		fields["codigo_operacion"] = "codigo_operacion es obligatorio"
	}

	rawMonto := strings.TrimSpace(r.FormValue("monto_declarado"))
	var montoDeclarado float64
	if rawMonto == "" {
		fields["monto_declarado"] = "monto_declarado es obligatorio"
	} else {
		parsedMonto, parseErr := strconv.ParseFloat(rawMonto, 64)
		if parseErr != nil {
			fields["monto_declarado"] = "monto_declarado debe ser un número decimal válido"
		} else {
			montoDeclarado = parsedMonto
		}
	}

	file, _, fileErr := r.FormFile("evidencia")
	if fileErr != nil {
		if errors.Is(fileErr, http.ErrMissingFile) {
			fields["evidencia"] = "El archivo evidencia es obligatorio"
		} else {
			fields["evidencia"] = "No se pudo leer el archivo evidencia"
		}
	}

	if len(fields) > 0 {
		if file != nil {
			_ = file.Close()
		}
		apphttp.WriteValidationError(w, "Datos de comprobante inválidos", fields)
		return
	}
	defer file.Close()

	// Leemos hasta MaxEvidenciaBytes + 1 para validar el tamaño real del archivo sin confiar en headers.
	fileBytes, readErr := io.ReadAll(io.LimitReader(file, MaxEvidenciaBytes+1))
	if readErr != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(readErr, &maxBytesErr) {
			apphttp.WriteError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", ErrFileTooLarge.Error())
			return
		}
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_FILE", "No se pudo leer la imagen enviada")
		return
	}
	if len(fileBytes) > MaxEvidenciaBytes {
		apphttp.WriteError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", ErrFileTooLarge.Error())
		return
	}

	res, err := h.service.UploadComprobante(r.Context(), UploadComprobanteInput{
		ReservaID:       reservaID,
		JugadorID:       user.UserID,
		MetodoPagoID:    metodoPagoID,
		CodigoOperacion: codigoOperacion,
		MontoDeclarado:  montoDeclarado,
		FileData:        fileBytes,
	})
	if err != nil {
		h.writeServiceError(w, r, "jugador_upload_comprobante_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) handleListJugadorReservas(w http.ResponseWriter, r *http.Request) {
	user, ok := apphttp.AuthUserFromContext(r.Context())
	if !ok {
		apphttp.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Autenticación requerida")
		return
	}

	q := r.URL.Query()
	fields := make(map[string]string)

	filter := ListJugadorReservasFilter{
		Estado: q.Get("estado"),
	}

	if rawLimit := strings.TrimSpace(q.Get("limit")); rawLimit != "" {
		parsedLimit, err := strconv.Atoi(rawLimit)
		if err != nil || parsedLimit < 1 || parsedLimit > MaxListLimit {
			fields["limit"] = "limit debe ser un entero entre 1 y 50"
		} else {
			filter.Limit = parsedLimit
		}
	}

	if rawOffset := strings.TrimSpace(q.Get("offset")); rawOffset != "" {
		parsedOffset, err := strconv.Atoi(rawOffset)
		if err != nil || parsedOffset < 0 {
			fields["offset"] = "offset debe ser un entero mayor o igual a 0"
		} else {
			filter.Offset = parsedOffset
		}
	}

	if len(fields) > 0 {
		apphttp.WriteValidationError(w, "Parámetros de listado inválidos", fields)
		return
	}

	res, err := h.service.ListJugadorReservas(r.Context(), user.UserID, filter)
	if err != nil {
		h.writeServiceError(w, r, "jugador_list_reservas_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) handleGetJugadorReservaDetail(w http.ResponseWriter, r *http.Request) {
	user, ok := apphttp.AuthUserFromContext(r.Context())
	if !ok {
		apphttp.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Autenticación requerida")
		return
	}

	reservaID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "reservaID")))
	if err != nil || reservaID == uuid.Nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Identificador de reserva inválido")
		return
	}

	detail, err := h.service.GetJugadorReservaDetail(r.Context(), user.UserID, reservaID)
	if err != nil {
		h.writeServiceError(w, r, "jugador_get_reserva_detail_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, detail)
}

func (h *Handler) handleGetComprobanteEvidencia(w http.ResponseWriter, r *http.Request) {
	user, ok := apphttp.AuthUserFromContext(r.Context())
	if !ok {
		apphttp.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Autenticación requerida")
		return
	}

	comprobanteID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "comprobanteID")))
	if err != nil || comprobanteID == uuid.Nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Identificador de comprobante inválido")
		return
	}

	stream, err := h.service.OpenComprobanteEvidencia(r.Context(), user, comprobanteID)
	if err != nil {
		h.writeServiceError(w, r, "get_comprobante_evidencia_error", err)
		return
	}
	defer stream.Reader.Close()

	disposition := "inline"
	if stream.ContentType == "application/octet-stream" {
		disposition = "attachment"
	}

	w.Header().Set("Content-Type", stream.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s"`, disposition, stream.FileName))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	if len(stream.Prefix) > 0 {
		if _, err := w.Write(stream.Prefix); err != nil {
			return
		}
	}
	_, _ = io.Copy(w, stream.Reader)
}

func (h *Handler) writeServiceError(w http.ResponseWriter, r *http.Request, event string, err error) {
	var valErr *ValidationError
	if errors.As(err, &valErr) {
		apphttp.WriteValidationError(w, valErr.Message, valErr.Fields)
		return
	}

	switch {
	case errors.Is(err, ErrComplejoNotFound):
		apphttp.WriteError(w, http.StatusNotFound, "COMPLEJO_NOT_FOUND", err.Error())
	case errors.Is(err, ErrComplejoNoDisponible):
		apphttp.WriteError(w, http.StatusUnprocessableEntity, "COMPLEJO_NO_DISPONIBLE", err.Error())
	case errors.Is(err, ErrCanchaNotFound):
		apphttp.WriteError(w, http.StatusNotFound, "CANCHA_NOT_FOUND", err.Error())
	case errors.Is(err, ErrComplejoCerrado):
		apphttp.WriteError(w, http.StatusUnprocessableEntity, "COMPLEJO_CERRADO", err.Error())
	case errors.Is(err, ErrHorarioFueraDeRango):
		apphttp.WriteError(w, http.StatusBadRequest, "OUT_OF_OPERATING_HOURS", err.Error())
	case errors.Is(err, ErrUnidadReservaInvalida):
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_RESERVATION_UNIT", err.Error())
	case errors.Is(err, ErrSlotEnPasado):
		apphttp.WriteError(w, http.StatusBadRequest, "SLOT_IN_PAST", err.Error())
	case errors.Is(err, ErrTarifaNoConfigurada):
		apphttp.WriteError(w, http.StatusUnprocessableEntity, "TARIFA_NO_CONFIGURADA", err.Error())
	case errors.Is(err, ErrCuponNotFound):
		apphttp.WriteError(w, http.StatusUnprocessableEntity, "COUPON_NOT_FOUND", err.Error())
	case errors.Is(err, ErrCuponInactive):
		apphttp.WriteError(w, http.StatusUnprocessableEntity, "COUPON_INACTIVE", err.Error())
	case errors.Is(err, ErrCuponNotValidYet):
		apphttp.WriteError(w, http.StatusUnprocessableEntity, "COUPON_NOT_VALID_YET", err.Error())
	case errors.Is(err, ErrCuponExpired):
		apphttp.WriteError(w, http.StatusUnprocessableEntity, "COUPON_EXPIRED", err.Error())
	case errors.Is(err, ErrCuponExhausted):
		apphttp.WriteError(w, http.StatusUnprocessableEntity, "COUPON_EXHAUSTED", err.Error())
	case errors.Is(err, ErrCuponNotStackableDeadHour):
		apphttp.WriteError(w, http.StatusUnprocessableEntity, "COUPON_NOT_STACKABLE_DEAD_HOUR", err.Error())
	case errors.Is(err, ErrSlotNotAvailable):
		apphttp.WriteError(w, http.StatusConflict, "SLOT_NOT_AVAILABLE", err.Error())
	case errors.Is(err, ErrReservaNotFound):
		apphttp.WriteError(w, http.StatusNotFound, "RESERVA_NOT_FOUND", err.Error())
	case errors.Is(err, ErrReservaForbidden):
		apphttp.WriteError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
	case errors.Is(err, ErrHoldExpired):
		apphttp.WriteError(w, http.StatusConflict, "HOLD_EXPIRED", err.Error())
	case errors.Is(err, ErrInvalidReservaState):
		apphttp.WriteError(w, http.StatusConflict, "INVALID_RESERVA_STATE", err.Error())
	case errors.Is(err, ErrMetodoPagoNotFound):
		apphttp.WriteError(w, http.StatusNotFound, "METODO_PAGO_NOT_FOUND", err.Error())
	case errors.Is(err, ErrMetodoPagoNoManual):
		apphttp.WriteError(w, http.StatusBadRequest, "METODO_PAGO_NO_MANUAL", err.Error())
	case errors.Is(err, ErrComprobanteNotFound):
		apphttp.WriteError(w, http.StatusNotFound, "COMPROBANTE_NOT_FOUND", err.Error())
	case errors.Is(err, ErrEvidenciaNotFound):
		apphttp.WriteError(w, http.StatusNotFound, "EVIDENCIA_NOT_FOUND", err.Error())
	case errors.Is(err, ErrEvidenciaForbidden):
		apphttp.WriteError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
	case errors.Is(err, ErrFileTooLarge):
		apphttp.WriteError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", err.Error())
	case errors.Is(err, ErrInvalidFileType):
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_FILE_TYPE", err.Error())
	case errors.Is(err, context.Canceled):
		return
	default:
		h.logger.Error(event,
			slog.String("request_id", chimw.GetReqID(r.Context())),
			slog.String("path", r.URL.Path),
			slog.String("error", err.Error()),
		)
		apphttp.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Error interno del servidor")
	}
}
