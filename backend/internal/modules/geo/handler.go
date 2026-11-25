package geo

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	apphttp "github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/http"
)

const (
	duenoReverseRateScope  = "dueno_geo_reverse"
	duenoReverseRateLimit  = 30
	duenoReverseRateWindow = time.Minute
)

// Handler expone los endpoints geoespaciales del dueño y el catálogo público de complejos.
type Handler struct {
	service     *Service
	rateLimiter apphttp.RateLimiter
	logger      *slog.Logger
}

// NewHandler crea el manejador HTTP para el módulo geo.
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

// RegisterDuenoRoutes monta GET /api/v1/dueno/geo/reverse dentro del subrouter /api/v1/dueno
// (ya protegido con RequireAuth + RequireRole("DUENO") + RequireTenant) añadiendo RateLimit.
func (h *Handler) RegisterDuenoRoutes(dueno chi.Router) {
	dueno.With(apphttp.RateLimit(h.rateLimiter, duenoReverseRateScope, duenoReverseRateLimit, duenoReverseRateWindow, h.logger)).
		Get("/geo/reverse", h.handleReverseGeocode)
}

// RegisterPublicRoutes monta los endpoints 100% públicos bajo /api/v1/public.
func (h *Handler) RegisterPublicRoutes(public chi.Router) {
	public.Get("/complejos", h.handleExploreComplejos)
	public.Get("/complejos/{slug}", h.handleGetPublicComplejoBySlug)
}

func (h *Handler) handleReverseGeocode(w http.ResponseWriter, r *http.Request) {
	if _, ok := apphttp.TenantIDFromContext(r.Context()); !ok {
		apphttp.WriteError(w, http.StatusForbidden, "TENANT_REQUIRED", "No tienes un complejo deportivo asociado")
		return
	}

	q := r.URL.Query()
	rawLat := strings.TrimSpace(q.Get("lat"))
	rawLng := strings.TrimSpace(q.Get("lng"))

	fields := make(map[string]string)
	var lat, lng float64

	if rawLat == "" {
		fields["lat"] = "El parámetro lat es obligatorio"
	} else {
		parsedLat, err := strconv.ParseFloat(rawLat, 64)
		if err != nil || math.IsNaN(parsedLat) || math.IsInf(parsedLat, 0) {
			fields["lat"] = "El parámetro lat debe ser un número decimal válido"
		} else {
			lat = parsedLat
		}
	}

	if rawLng == "" {
		fields["lng"] = "El parámetro lng es obligatorio"
	} else {
		parsedLng, err := strconv.ParseFloat(rawLng, 64)
		if err != nil || math.IsNaN(parsedLng) || math.IsInf(parsedLng, 0) {
			fields["lng"] = "El parámetro lng debe ser un número decimal válido"
		} else {
			lng = parsedLng
		}
	}

	if len(fields) > 0 {
		apphttp.WriteValidationError(w, "Parámetros de coordenadas inválidos", fields)
		return
	}

	res, err := h.service.ReverseGeocode(r.Context(), lat, lng)
	if err != nil {
		h.writeServiceError(w, r, "dueno_reverse_geocode_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) handleExploreComplejos(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	fields := make(map[string]string)

	var filter ExploreComplejosFilter

	rawLat := strings.TrimSpace(q.Get("lat"))
	rawLng := strings.TrimSpace(q.Get("lng"))

	if rawLat != "" {
		parsedLat, err := strconv.ParseFloat(rawLat, 64)
		if err != nil || math.IsNaN(parsedLat) || math.IsInf(parsedLat, 0) {
			fields["lat"] = "El parámetro lat debe ser un número decimal válido"
		} else {
			filter.Lat = &parsedLat
		}
	}

	if rawLng != "" {
		parsedLng, err := strconv.ParseFloat(rawLng, 64)
		if err != nil || math.IsNaN(parsedLng) || math.IsInf(parsedLng, 0) {
			fields["lng"] = "El parámetro lng debe ser un número decimal válido"
		} else {
			filter.Lng = &parsedLng
		}
	}

	if (rawLat == "") != (rawLng == "") && fields["lat"] == "" && fields["lng"] == "" {
		fields["coordenadas"] = "Debe enviar lat y lng juntos, o ninguno para búsqueda por zona sin GPS"
	}

	if rawRadio := strings.TrimSpace(q.Get("radio_km")); rawRadio != "" {
		parsedRadio, err := strconv.ParseFloat(rawRadio, 64)
		if err != nil || math.IsNaN(parsedRadio) || math.IsInf(parsedRadio, 0) {
			fields["radio_km"] = "El parámetro radio_km debe ser un número decimal válido"
		} else {
			filter.RadioKm = &parsedRadio
		}
	}

	if rawLimit := strings.TrimSpace(q.Get("limit")); rawLimit != "" {
		parsedLimit, err := strconv.Atoi(rawLimit)
		if err != nil || parsedLimit < 1 || parsedLimit > MaxPublicLimit {
			fields["limit"] = "El parámetro limit debe ser un entero entre 1 y 50"
		} else {
			filter.Limit = parsedLimit
		}
	}

	if rawOffset := strings.TrimSpace(q.Get("offset")); rawOffset != "" {
		parsedOffset, err := strconv.Atoi(rawOffset)
		if err != nil || parsedOffset < 0 {
			fields["offset"] = "El parámetro offset debe ser un entero mayor o igual a 0"
		} else {
			filter.Offset = parsedOffset
		}
	}

	if len(fields) > 0 {
		apphttp.WriteValidationError(w, "Parámetros de búsqueda inválidos", fields)
		return
	}

	filter.Departamento = q.Get("departamento")
	filter.Provincia = q.Get("provincia")
	filter.Distrito = q.Get("distrito")
	filter.Query = q.Get("q")
	filter.Modalidad = q.Get("modalidad")

	res, err := h.service.ExploreComplejos(r.Context(), filter)
	if err != nil {
		h.writeServiceError(w, r, "public_explore_complejos_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) handleGetPublicComplejoBySlug(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")

	ficha, err := h.service.GetPublicComplejoBySlug(r.Context(), slug)
	if err != nil {
		h.writeServiceError(w, r, "public_get_complejo_slug_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, ficha)
}

// writeServiceError traduce errores de dominio a respuestas HTTP uniformes.
// Por privacidad estricta (RN-19 / RNF-04), nunca registra query params ni coordenadas en logs.
func (h *Handler) writeServiceError(w http.ResponseWriter, r *http.Request, logEvent string, err error) {
	var valErr *ValidationError
	if errors.As(err, &valErr) {
		apphttp.WriteValidationError(w, valErr.Message, valErr.Fields)
		return
	}

	switch {
	case errors.Is(err, ErrComplejoNotFound):
		apphttp.WriteError(w, http.StatusNotFound, "NOT_FOUND", "Complejo deportivo no encontrado")
	case errors.Is(err, ErrGeocoderTimeout):
		apphttp.WriteError(w, http.StatusGatewayTimeout, "GEOCODER_TIMEOUT", "El proveedor de geocodificación excedió el tiempo de espera. Puedes ingresar la dirección manualmente.")
	case errors.Is(err, ErrGeocoderUnavailable):
		apphttp.WriteError(w, http.StatusBadGateway, "GEOCODER_ERROR", "El proveedor de geocodificación no está disponible temporalmente. Puedes ingresar la dirección manualmente.")
	case errors.Is(err, context.Canceled):
		return
	default:
		h.logger.Error(logEvent,
			slog.String("request_id", chimw.GetReqID(r.Context())),
			slog.String("error", err.Error()),
		)
		apphttp.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Error interno del servidor")
	}
}
