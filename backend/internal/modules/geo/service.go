package geo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/redis/go-redis/v9"
)

const (
	reverseCacheKeyPrefix    = "geo:reverse:"
	publicSlugCacheKeyPrefix = "geo:public:complejo:"
	maxGeocoderBodyBytes     = 1 << 20 // 1 MiB
)

var (
	slugFormatRegex   = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	allowedModalities = map[string]struct{}{
		"5v5":   {},
		"6v6":   {},
		"7v7":   {},
		"8v8":   {},
		"9v9":   {},
		"11v11": {},
	}
)

// Cache abstrae la caché en Valkey exclusivamente para:
// 1. Geocodificación inversa de coordenadas del dueño (TTL 24h).
// 2. Ficha pública de complejo por slug (TTL 60s).
// Prohibido usar esta interfaz para coordenadas GPS de jugadores (RN-19 / RNF-04).
type Cache interface {
	GetReverse(ctx context.Context, coordKey string) (*ReverseGeocodeResult, error)
	SetReverse(ctx context.Context, coordKey string, val ReverseGeocodeResult, ttl time.Duration) error
	GetPublicComplejo(ctx context.Context, slug string) (*PublicComplejoFicha, error)
	SetPublicComplejo(ctx context.Context, slug string, val PublicComplejoFicha, ttl time.Duration) error
}

// ValkeyCache implementa Cache respaldado por Valkey 8.
type ValkeyCache struct {
	client *redis.Client
}

// NewValkeyCache construye un adaptador Valkey para el módulo geo.
func NewValkeyCache(client *redis.Client) *ValkeyCache {
	return &ValkeyCache{client: client}
}

func (c *ValkeyCache) GetReverse(ctx context.Context, coordKey string) (*ReverseGeocodeResult, error) {
	if c == nil || c.client == nil {
		return nil, nil
	}
	raw, err := c.client.Get(ctx, reverseCacheKeyPrefix+coordKey).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	var res ReverseGeocodeResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *ValkeyCache) SetReverse(ctx context.Context, coordKey string, val ReverseGeocodeResult, ttl time.Duration) error {
	if c == nil || c.client == nil || ttl <= 0 {
		return nil
	}
	payload, err := json.Marshal(val)
	if err != nil {
		return err
	}
	return c.client.Set(ctx, reverseCacheKeyPrefix+coordKey, payload, ttl).Err()
}

func (c *ValkeyCache) GetPublicComplejo(ctx context.Context, slug string) (*PublicComplejoFicha, error) {
	if c == nil || c.client == nil {
		return nil, nil
	}
	raw, err := c.client.Get(ctx, publicSlugCacheKeyPrefix+slug).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	var res PublicComplejoFicha
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *ValkeyCache) SetPublicComplejo(ctx context.Context, slug string, val PublicComplejoFicha, ttl time.Duration) error {
	if c == nil || c.client == nil || ttl <= 0 {
		return nil
	}
	payload, err := json.Marshal(val)
	if err != nil {
		return err
	}
	return c.client.Set(ctx, publicSlugCacheKeyPrefix+slug, payload, ttl).Err()
}

// InvalidatePublicComplejo elimina inmediatamente de Valkey la ficha pública cacheada de un complejo por slug.
func (c *ValkeyCache) InvalidatePublicComplejo(ctx context.Context, slug string) error {
	if c == nil || c.client == nil {
		return nil
	}
	cleanSlug := strings.ToLower(strings.TrimSpace(slug))
	if cleanSlug == "" {
		return nil
	}
	return c.client.Del(ctx, publicSlugCacheKeyPrefix+cleanSlug).Err()
}

// ServiceConfig parametriza el cliente geocoder externo y utilidades del servicio.
type ServiceConfig struct {
	GeocoderBaseURL   string
	GeocoderUserAgent string
	HTTPClient        *http.Client
	Logger            *slog.Logger
	Now               func() time.Time
}

// Service implementa la lógica de negocio geoespacial y de exploración pública.
type Service struct {
	repo              Store
	cache             Cache
	geocoderBaseURL   string
	geocoderUserAgent string
	httpClient        *http.Client
	logger            *slog.Logger
	now               func() time.Time
}

// NewService crea una instancia de Service con timeout HTTP estricto de 4 segundos por defecto.
func NewService(repo Store, cache Cache, cfg ServiceConfig) *Service {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.GeocoderBaseURL), "/")
	if baseURL == "" {
		baseURL = DefaultGeocoderBaseURL
	}

	userAgent := strings.TrimSpace(cfg.GeocoderUserAgent)
	if userAgent == "" {
		userAgent = DefaultGeocoderUserAgent
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: GeocoderHTTPTimeout}
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	return &Service{
		repo:              repo,
		cache:             cache,
		geocoderBaseURL:   baseURL,
		geocoderUserAgent: userAgent,
		httpClient:        httpClient,
		logger:            logger,
		now:               nowFn,
	}
}

// ReverseGeocode traduce coordenadas WGS84 del complejo del dueño a dirección escrita y demarcación política (RF-04, RN-15, RN-16).
// Redondea lat y lng a 5 decimales para caché en Valkey (TTL 24h).
func (s *Service) ReverseGeocode(ctx context.Context, lat, lng float64) (ReverseGeocodeResult, error) {
	fields := validateLatLng(lat, lng)
	if len(fields) > 0 {
		return ReverseGeocodeResult{}, &ValidationError{
			Message: "Coordenadas geográficas fuera de rango",
			Fields:  fields,
		}
	}

	roundedLat := roundCoord5(lat)
	roundedLng := roundCoord5(lng)
	coordKey := fmt.Sprintf("%.5f:%.5f", roundedLat, roundedLng)

	if s.cache != nil {
		cached, err := s.cache.GetReverse(ctx, coordKey)
		if err != nil {
			s.logger.Warn("geo_reverse_cache_get_error", slog.String("error", err.Error()))
		} else if cached != nil {
			cached.Lat = roundedLat
			cached.Lng = roundedLng
			cached.OrigenCache = true
			return *cached, nil
		}
	}

	res, err := s.fetchExternalReverseGeocode(ctx, roundedLat, roundedLng)
	if err != nil {
		return ReverseGeocodeResult{}, err
	}

	res.Lat = roundedLat
	res.Lng = roundedLng
	res.OrigenCache = false

	if s.cache != nil {
		if cacheErr := s.cache.SetReverse(ctx, coordKey, res, ReverseCacheTTL); cacheErr != nil {
			s.logger.Warn("geo_reverse_cache_set_error", slog.String("error", cacheErr.Error()))
		}
	}

	return res, nil
}

// ExploreComplejos ejecuta la búsqueda pública de complejos afiliados vigentes (RF-09, CU-10, RN-02, RN-19).
// Garantiza privacidad estricta: jamás persiste, cachea ni registra en logs las coordenadas del jugador.
func (s *Service) ExploreComplejos(ctx context.Context, filter ExploreComplejosFilter) (PublicComplejosListResponse, error) {
	fields := make(map[string]string)

	hasLat := filter.Lat != nil
	hasLng := filter.Lng != nil
	if hasLat != hasLng {
		fields["coordenadas"] = "Debe enviar lat y lng juntos, o ninguno para búsqueda por zona sin GPS"
	}

	var (
		useGPS  bool
		latVal  float64
		lngVal  float64
		radioKm = DefaultRadioKm
	)

	if hasLat && hasLng {
		useGPS = true
		latVal = *filter.Lat
		lngVal = *filter.Lng
		for k, v := range validateLatLng(latVal, lngVal) {
			fields[k] = v
		}
	}

	if filter.RadioKm != nil {
		r := *filter.RadioKm
		if math.IsNaN(r) || math.IsInf(r, 0) || r <= 0 || r > MaxRadioKm {
			fields["radio_km"] = "El radio de búsqueda debe ser mayor a 0 y menor o igual a 50 km"
		} else {
			radioKm = r
		}
	}

	limit := filter.Limit
	if limit == 0 {
		limit = DefaultPublicLimit
	} else if limit < 1 || limit > MaxPublicLimit {
		fields["limit"] = "El parámetro limit debe estar entre 1 y 50"
	}

	if filter.Offset < 0 {
		fields["offset"] = "El parámetro offset debe ser mayor o igual a 0"
	}

	modalidad := strings.ToLower(strings.TrimSpace(filter.Modalidad))
	if modalidad != "" {
		if _, ok := allowedModalities[modalidad]; !ok {
			fields["modalidad"] = "Modalidad inválida (permitidas: 5v5, 6v6, 7v7, 8v8, 9v9, 11v11)"
		}
	}

	departamento := strings.TrimSpace(filter.Departamento)
	if utf8.RuneCountInString(departamento) > 60 {
		fields["departamento"] = "El departamento no puede exceder 60 caracteres"
	}

	provincia := strings.TrimSpace(filter.Provincia)
	if utf8.RuneCountInString(provincia) > 60 {
		fields["provincia"] = "La provincia no puede exceder 60 caracteres"
	}

	distrito := strings.TrimSpace(filter.Distrito)
	if utf8.RuneCountInString(distrito) > 80 {
		fields["distrito"] = "El distrito no puede exceder 80 caracteres"
	}

	rawTextQuery := strings.TrimSpace(filter.Query)
	var queryLike string
	if rawTextQuery != "" {
		if utf8.RuneCountInString(rawTextQuery) > 100 {
			fields["q"] = "El texto de búsqueda no puede exceder 100 caracteres"
		} else {
			queryLike = "%" + escapeILIKE(rawTextQuery) + "%"
		}
	}

	if len(fields) > 0 {
		return PublicComplejosListResponse{}, &ValidationError{
			Message: "Parámetros de búsqueda inválidos",
			Fields:  fields,
		}
	}

	repoQuery := ExploreComplejosQuery{
		UseGPS:       useGPS,
		Lat:          latVal,
		Lng:          lngVal,
		RadioMetros:  radioKm * 1000.0,
		Departamento: departamento,
		Provincia:    provincia,
		Distrito:     distrito,
		QueryLike:    queryLike,
		Modalidad:    modalidad,
		Limit:        limit,
		Offset:       filter.Offset,
	}

	items, total, err := s.repo.ExplorePublicComplejos(ctx, repoQuery)
	if err != nil {
		return PublicComplejosListResponse{}, err
	}
	if items == nil {
		items = []PublicComplejoItem{}
	}

	resp := PublicComplejosListResponse{
		Items:        items,
		Total:        total,
		Limit:        limit,
		Offset:       filter.Offset,
		ModoBusqueda: ModoBusquedaZona,
	}
	if useGPS {
		resp.ModoBusqueda = ModoBusquedaGPS
		rCopy := radioKm
		resp.RadioKm = &rCopy
	}

	return resp, nil
}

// GetPublicComplejoBySlug obtiene la ficha pública de un complejo aplicando el filtro de vigencia RN-02
// y caché ligera en Valkey por slug (TTL máximo 60 segundos).
func (s *Service) GetPublicComplejoBySlug(ctx context.Context, rawSlug string) (PublicComplejoFicha, error) {
	slug := strings.ToLower(strings.TrimSpace(rawSlug))
	if slug == "" || len(slug) > 120 || !slugFormatRegex.MatchString(slug) {
		return PublicComplejoFicha{}, ErrComplejoNotFound
	}

	if s.cache != nil {
		cached, err := s.cache.GetPublicComplejo(ctx, slug)
		if err != nil {
			s.logger.Warn("geo_public_slug_cache_get_error",
				slog.String("slug", slug),
				slog.String("error", err.Error()),
			)
		} else if cached != nil {
			return *cached, nil
		}
	}

	base, err := s.repo.GetComplejoBaseBySlug(ctx, slug)
	if err != nil {
		return PublicComplejoFicha{}, err
	}

	now := s.now()
	esVigente := (base.EstadoSuscripcion == EstadoSuscripcionPrueba || base.EstadoSuscripcion == EstadoSuscripcionActiva) &&
		base.SuscripcionVenceAt != nil &&
		base.SuscripcionVenceAt.After(now)

	if !esVigente {
		motivo := MotivoComplejoNoDisponible
		fichaNoDisponible := PublicComplejoFicha{
			ID:               base.ID,
			Slug:             base.Slug,
			Nombre:           base.Nombre,
			AceptaReservas:   false,
			Motivo:           &motivo,
			Departamento:     base.Departamento,
			Provincia:        base.Provincia,
			Distrito:         base.Distrito,
			Servicios:        []string{},
			Fotos:            []PublicFoto{},
			Canchas:          []PublicCancha{},
			Horarios:         []PublicHorarioAtencion{},
			Tarifas:          []PublicTarifaFranja{},
			TiposMetodosPago: []string{},
		}

		if s.cache != nil {
			if cacheErr := s.cache.SetPublicComplejo(ctx, slug, fichaNoDisponible, PublicSlugCacheTTL); cacheErr != nil {
				s.logger.Warn("geo_public_slug_cache_set_error",
					slog.String("slug", slug),
					slog.String("error", cacheErr.Error()),
				)
			}
		}

		return fichaNoDisponible, nil
	}

	fotos, canchas, horarios, tarifas, tiposMetodosPago, err := s.repo.GetComplejoPublicCatalog(ctx, base.ID)
	if err != nil {
		return PublicComplejoFicha{}, err
	}

	if fotos == nil {
		fotos = []PublicFoto{}
	}
	if canchas == nil {
		canchas = []PublicCancha{}
	}
	if horarios == nil {
		horarios = []PublicHorarioAtencion{}
	}
	if tarifas == nil {
		tarifas = []PublicTarifaFranja{}
	}
	if tiposMetodosPago == nil {
		tiposMetodosPago = []string{}
	}
	servicios := base.Servicios
	if servicios == nil {
		servicios = []string{}
	}

	terminos := ""
	if base.TerminosReserva != nil {
		terminos = *base.TerminosReserva
	}

	ficha := PublicComplejoFicha{
		ID:               base.ID,
		Slug:             base.Slug,
		Nombre:           base.Nombre,
		AceptaReservas:   true,
		Descripcion:      base.Descripcion,
		Departamento:     base.Departamento,
		Provincia:        base.Provincia,
		Distrito:         base.Distrito,
		DireccionEscrita: base.DireccionEscrita,
		Referencias:      base.Referencias,
		TelefonoContacto: base.TelefonoContacto,
		Lat:              base.Lat,
		Lng:              base.Lng,
		Servicios:        servicios,
		Fotos:            fotos,
		Canchas:          canchas,
		Horarios:         horarios,
		Tarifas:          tarifas,
		ReglasReserva: &PublicReglasReserva{
			UnidadReservaMin:    base.UnidadReservaMin,
			HoldDuracionMin:     base.HoldDuracionMin,
			TipoAdelanto:        base.TipoAdelanto,
			ValorAdelanto:       base.ValorAdelanto,
			HorasMinCancelacion: base.HorasMinCancelacion,
			TerminosReserva:     terminos,
		},
		TiposMetodosPago: tiposMetodosPago,
	}

	if s.cache != nil {
		ttl := PublicSlugCacheTTL
		if remaining := base.SuscripcionVenceAt.Sub(now); remaining < ttl {
			ttl = remaining
		}
		if ttl >= time.Second {
			if cacheErr := s.cache.SetPublicComplejo(ctx, slug, ficha, ttl); cacheErr != nil {
				s.logger.Warn("geo_public_slug_cache_set_error",
					slog.String("slug", slug),
					slog.String("error", cacheErr.Error()),
				)
			}
		}
	}

	return ficha, nil
}

type nominatimAddress struct {
	Road          string `json:"road"`
	Pedestrian    string `json:"pedestrian"`
	Footway       string `json:"footway"`
	HouseNumber   string `json:"house_number"`
	Neighbourhood string `json:"neighbourhood"`
	Suburb        string `json:"suburb"`
	CityDistrict  string `json:"city_district"`
	District      string `json:"district"`
	Municipality  string `json:"municipality"`
	Town          string `json:"town"`
	City          string `json:"city"`
	Village       string `json:"village"`
	Hamlet        string `json:"hamlet"`
	County        string `json:"county"`
	Province      string `json:"province"`
	StateDistrict string `json:"state_district"`
	State         string `json:"state"`
	Region        string `json:"region"`
}

type nominatimReverseResponse struct {
	DisplayName string           `json:"display_name"`
	Name        string           `json:"name"`
	Error       string           `json:"error"`
	Address     nominatimAddress `json:"address"`
}

func (s *Service) fetchExternalReverseGeocode(parentCtx context.Context, lat, lng float64) (ReverseGeocodeResult, error) {
	reqCtx, cancel := context.WithTimeout(parentCtx, GeocoderHTTPTimeout)
	defer cancel()

	params := url.Values{}
	params.Set("format", "jsonv2")
	params.Set("lat", strconv.FormatFloat(lat, 'f', 5, 64))
	params.Set("lon", strconv.FormatFloat(lng, 'f', 5, 64))
	params.Set("addressdetails", "1")
	params.Set("accept-language", "es")

	endpoint := s.geocoderBaseURL + "/reverse?" + params.Encode()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ReverseGeocodeResult{}, ErrGeocoderUnavailable
	}
	req.Header.Set("User-Agent", s.geocoderUserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		if parentCtx.Err() != nil && errors.Is(parentCtx.Err(), context.Canceled) {
			return ReverseGeocodeResult{}, parentCtx.Err()
		}
		if isTimeoutErr(err) {
			return ReverseGeocodeResult{}, ErrGeocoderTimeout
		}
		return ReverseGeocodeResult{}, ErrGeocoderUnavailable
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusGatewayTimeout || resp.StatusCode == http.StatusRequestTimeout {
		return ReverseGeocodeResult{}, ErrGeocoderTimeout
	}
	if resp.StatusCode != http.StatusOK {
		return ReverseGeocodeResult{}, ErrGeocoderUnavailable
	}

	var parsed nominatimReverseResponse
	dec := json.NewDecoder(io.LimitReader(resp.Body, maxGeocoderBodyBytes))
	if err := dec.Decode(&parsed); err != nil {
		return ReverseGeocodeResult{}, ErrGeocoderUnavailable
	}

	if strings.TrimSpace(parsed.Error) != "" {
		return ReverseGeocodeResult{}, ErrGeocoderUnavailable
	}

	addr := parsed.Address
	departamento := truncateRunes(cleanAdminPrefix(firstNonEmpty(addr.State, addr.Region)), 60)
	provinciaRaw := firstNonEmpty(addr.County, addr.Province, addr.StateDistrict)
	if provinciaRaw == "" && strings.TrimSpace(addr.State) != "" {
		provinciaRaw = addr.Region
	}
	provincia := truncateRunes(cleanAdminPrefix(provinciaRaw), 60)
	distrito := truncateRunes(cleanAdminPrefix(firstNonEmpty(
		addr.CityDistrict,
		addr.District,
		addr.Municipality,
		addr.Town,
		addr.City,
		addr.Village,
		addr.Suburb,
		addr.Hamlet,
	)), 80)

	direccion := strings.TrimSpace(parsed.DisplayName)
	if direccion == "" {
		via := firstNonEmpty(addr.Road, addr.Pedestrian, addr.Footway, parsed.Name)
		if via != "" && strings.TrimSpace(addr.HouseNumber) != "" {
			via = via + " " + strings.TrimSpace(addr.HouseNumber)
		}
		parts := make([]string, 0, 5)
		for _, p := range []string{via, strings.TrimSpace(addr.Neighbourhood), distrito, provincia, departamento} {
			if p != "" {
				parts = append(parts, p)
			}
		}
		direccion = strings.Join(parts, ", ")
	}
	direccion = truncateRunes(direccion, 255)

	if direccion == "" && departamento == "" && provincia == "" && distrito == "" {
		return ReverseGeocodeResult{}, ErrGeocoderUnavailable
	}

	return ReverseGeocodeResult{
		Lat:              lat,
		Lng:              lng,
		DireccionEscrita: direccion,
		Departamento:     departamento,
		Provincia:        provincia,
		Distrito:         distrito,
	}, nil
}

func validateLatLng(lat, lng float64) map[string]string {
	fields := make(map[string]string)
	if math.IsNaN(lat) || math.IsInf(lat, 0) || lat < -90 || lat > 90 {
		fields["lat"] = "La latitud debe estar entre -90 y 90"
	}
	if math.IsNaN(lng) || math.IsInf(lng, 0) || lng < -180 || lng > 180 {
		fields["lng"] = "La longitud debe estar entre -180 y 180"
	}
	return fields
}

func roundCoord5(v float64) float64 {
	rounded := math.Round(v*100000) / 100000
	if rounded == 0 {
		return 0
	}
	return rounded
}

func isTimeoutErr(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func cleanAdminPrefix(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	prefixes := []string{
		"departamento de ",
		"región ",
		"region ",
		"provincia de ",
		"distrito de ",
	}
	lower := strings.ToLower(s)
	for _, p := range prefixes {
		if strings.HasPrefix(lower, p) {
			trimmed := strings.TrimSpace(s[len(p):])
			if trimmed != "" {
				return trimmed
			}
		}
	}
	return s
}

func truncateRunes(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:maxRunes]))
}

func escapeILIKE(s string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`%`, `\%`,
		`_`, `\_`,
	)
	return replacer.Replace(s)
}
