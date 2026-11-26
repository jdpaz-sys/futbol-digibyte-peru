package suscripciones

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/modules/auth"
)

const (
	metricasCacheKey = "admin:metricas:v1"
	metricasCacheTTL = 30 * time.Second
	maxPrecioMonto   = 99999999.99
)

var (
	limaLocation  *time.Location
	slugRegex     = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	planCodeRegex = regexp.MustCompile(`^[A-Z0-9]+([_-][A-Z0-9]+)*$`)
)

func init() {
	loc, err := time.LoadLocation("America/Lima")
	if err != nil {
		loc = time.FixedZone("America/Lima", -5*60*60)
	}
	limaLocation = loc
}

// MetricsCache abstrae el almacenamiento temporal de métricas globales en Valkey.
type MetricsCache interface {
	Get(ctx context.Context) (*MetricasGlobalesResponse, error)
	Set(ctx context.Context, data MetricasGlobalesResponse, ttl time.Duration) error
	Invalidate(ctx context.Context) error
}

// ValkeyMetricsCache implementa MetricsCache sobre Valkey 8.
type ValkeyMetricsCache struct {
	client *redis.Client
}

// NewValkeyMetricsCache crea un caché corto de métricas respaldado por Valkey.
func NewValkeyMetricsCache(client *redis.Client) *ValkeyMetricsCache {
	return &ValkeyMetricsCache{client: client}
}

func (c *ValkeyMetricsCache) Get(ctx context.Context) (*MetricasGlobalesResponse, error) {
	raw, err := c.client.Get(ctx, metricasCacheKey).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	var out MetricasGlobalesResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *ValkeyMetricsCache) Set(ctx context.Context, data MetricasGlobalesResponse, ttl time.Duration) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return c.client.Set(ctx, metricasCacheKey, payload, ttl).Err()
}

func (c *ValkeyMetricsCache) Invalidate(ctx context.Context) error {
	return c.client.Del(ctx, metricasCacheKey).Err()
}

// PublicCacheInvalidator invalida de forma desacoplada la caché pública por slug en Valkey.
type PublicCacheInvalidator interface {
	InvalidatePublicComplejo(ctx context.Context, slug string) error
}

// Service encapsula la lógica de negocio del módulo B2B suscripciones (Super Admin).
type Service struct {
	repo        Repository
	cache       MetricsCache
	publicCache PublicCacheInvalidator
	logger      *slog.Logger
}

// NewService crea una instancia del servicio de suscripciones.
func NewService(repo Repository, cache MetricsCache) *Service {
	return &Service{
		repo:   repo,
		cache:  cache,
		logger: slog.Default(),
	}
}

// WithPublicCacheInvalidator inyecta el invalidador de caché pública por slug (Bloque B).
func (s *Service) WithPublicCacheInvalidator(inv PublicCacheInvalidator, logger *slog.Logger) *Service {
	s.publicCache = inv
	if logger != nil {
		s.logger = logger
	}
	return s
}

// ListPlanes devuelve el catálogo de planes SaaS filtrable por activo.
func (s *Service) ListPlanes(ctx context.Context, activo *bool) ([]Plan, error) {
	return s.repo.ListPlanes(ctx, activo)
}

// CreatePlan valida y registra un nuevo plan en el catálogo SaaS (Decisión 7).
func (s *Service) CreatePlan(ctx context.Context, req CreatePlanRequest) (Plan, error) {
	nombre := strings.TrimSpace(req.Nombre)
	periodicidad := strings.ToUpper(strings.TrimSpace(req.Periodicidad))
	unidad := strings.ToUpper(strings.TrimSpace(req.UnidadDuracion))

	var codigo string
	if strings.TrimSpace(req.Codigo) != "" {
		codigo = strings.ToUpper(strings.TrimSpace(req.Codigo))
	} else {
		codigo = NormalizePlanCode(nombre)
	}

	fields := make(map[string]string)

	if n := utf8.RuneCountInString(nombre); n < 2 || n > 100 {
		fields["nombre"] = "El nombre del plan debe tener entre 2 y 100 caracteres"
	}
	if codigo == "" || len(codigo) > 40 || !planCodeRegex.MatchString(codigo) {
		fields["codigo"] = "El código debe contener entre 1 y 40 caracteres alfanuméricos en mayúsculas"
	}
	if !isValidPeriodicidad(periodicidad) {
		fields["periodicidad"] = "La periodicidad debe ser PRUEBA, MENSUAL o ANUAL"
	}
	if !isValidUnidadDuracion(unidad) {
		fields["unidad_duracion"] = "La unidad de duración debe ser DIAS o MESES"
	}
	if req.CantidadDuracion <= 0 || req.CantidadDuracion > 1200 {
		fields["cantidad_duracion"] = "La cantidad de duración debe estar entre 1 y 1200"
	}

	precio, precioErr := sanitizeAmount(req.Precio)
	if precioErr != "" {
		fields["precio"] = precioErr
	} else if periodicidad == PeriodicidadPrueba && precio != 0 {
		fields["precio"] = "Los planes de PRUEBA deben tener precio 0.00"
	}

	descripcion, descErr := sanitizeOptionalText(req.Descripcion, 1000)
	if descErr != "" {
		fields["descripcion"] = descErr
	}

	if len(fields) > 0 {
		return Plan{}, &ValidationError{
			Message: "Datos de plan inválidos",
			Fields:  fields,
		}
	}

	activo := true
	if req.Activo != nil {
		activo = *req.Activo
	}

	return s.repo.CreatePlan(ctx, Plan{
		Codigo:           codigo,
		Nombre:           nombre,
		Periodicidad:     periodicidad,
		UnidadDuracion:   unidad,
		CantidadDuracion: int16(req.CantidadDuracion),
		Precio:           precio,
		Descripcion:      descripcion,
		Activo:           activo,
	})
}

// UpdatePlan actualiza nombre, precio, descripción o estado activo de un plan existente.
func (s *Service) UpdatePlan(ctx context.Context, planID uuid.UUID, req UpdatePlanRequest) (Plan, error) {
	if req.Nombre == nil && req.Precio == nil && req.Descripcion == nil && req.Activo == nil {
		return Plan{}, &ValidationError{
			Message: "Debes enviar al menos un campo para actualizar el plan",
			Fields:  map[string]string{"body": "Envía nombre, precio, descripcion o activo"},
		}
	}

	current, err := s.repo.GetPlanByID(ctx, planID)
	if err != nil {
		return Plan{}, err
	}

	fields := make(map[string]string)

	if req.Nombre != nil {
		nombre := strings.TrimSpace(*req.Nombre)
		if n := utf8.RuneCountInString(nombre); n < 2 || n > 100 {
			fields["nombre"] = "El nombre del plan debe tener entre 2 y 100 caracteres"
		} else {
			current.Nombre = nombre
		}
	}

	if req.Precio != nil {
		precio, precioErr := sanitizeAmount(*req.Precio)
		if precioErr != "" {
			fields["precio"] = precioErr
		} else if current.Periodicidad == PeriodicidadPrueba && precio != 0 {
			fields["precio"] = "Los planes de PRUEBA deben mantener precio 0.00"
		} else {
			current.Precio = precio
		}
	}

	if req.Descripcion != nil {
		descripcion, descErr := sanitizeOptionalText(req.Descripcion, 1000)
		if descErr != "" {
			fields["descripcion"] = descErr
		} else {
			current.Descripcion = descripcion
		}
	}

	if req.Activo != nil {
		current.Activo = *req.Activo
	}

	if len(fields) > 0 {
		return Plan{}, &ValidationError{
			Message: "Datos de actualización de plan inválidos",
			Fields:  fields,
		}
	}

	return s.repo.UpdatePlan(ctx, current)
}

// CreateComplejo ejecuta el alta atómica de usuario DUENO + complejo + suscripción inicial (RF-01).
func (s *Service) CreateComplejo(ctx context.Context, adminID uuid.UUID, req CreateComplejoRequest) (ComplejoDetalleResponse, error) {
	fields := make(map[string]string)

	nombre := strings.TrimSpace(req.Nombre)
	if n := utf8.RuneCountInString(nombre); n < 2 || n > 150 {
		fields["nombre"] = "El nombre del complejo debe tener entre 2 y 150 caracteres"
	}

	rawSlug := strings.TrimSpace(req.Slug)
	var slug string
	if rawSlug != "" {
		slug = NormalizeSlug(rawSlug)
	} else {
		slug = NormalizeSlug(nombre)
	}
	if len(slug) < 2 || len(slug) > 120 || !slugRegex.MatchString(slug) {
		fields["slug"] = "El slug debe tener entre 2 y 120 caracteres alfanuméricos separados por guiones"
	}

	planID, err := uuid.Parse(strings.TrimSpace(req.PlanID))
	if err != nil {
		fields["plan_id"] = "Ingresa un identificador de plan válido (UUID)"
	}

	departamento := defaultIfBlank(req.Departamento, "Lima")
	if utf8.RuneCountInString(departamento) > 60 {
		fields["departamento"] = "El departamento no debe exceder 60 caracteres"
	}
	provincia := defaultIfBlank(req.Provincia, "Huaura")
	if utf8.RuneCountInString(provincia) > 60 {
		fields["provincia"] = "La provincia no debe exceder 60 caracteres"
	}
	distrito := defaultIfBlank(req.Distrito, "Huacho")
	if utf8.RuneCountInString(distrito) > 80 {
		fields["distrito"] = "El distrito no debe exceder 80 caracteres"
	}

	descripcion, descErr := sanitizeOptionalText(req.Descripcion, 1000)
	if descErr != "" {
		fields["descripcion"] = descErr
	}

	duenoEmail := auth.NormalizeEmail(req.Dueno.Email)
	if !auth.ValidateEmail(duenoEmail) {
		fields["dueno.email"] = "Ingresa un correo electrónico válido para el dueño"
	}
	if passErr := auth.ValidatePassword(req.Dueno.Password); passErr != "" {
		fields["dueno.password"] = passErr
	}

	duenoNombre := strings.TrimSpace(req.Dueno.NombreCompleto)
	if duenoNombre == "" {
		duenoNombre = strings.TrimSpace(req.Dueno.Nombre)
	}
	if n := utf8.RuneCountInString(duenoNombre); n < 2 || n > 150 {
		fields["dueno.nombre_completo"] = "El nombre del dueño debe tener entre 2 y 150 caracteres"
	}

	duenoTelefono := strings.TrimSpace(req.Dueno.Telefono)
	if !auth.ValidatePhone(duenoTelefono) {
		fields["dueno.telefono"] = "Ingresa un teléfono válido para el dueño (7 a 20 caracteres)"
	}

	var telContacto *string
	if req.TelefonoContacto != nil && strings.TrimSpace(*req.TelefonoContacto) != "" {
		tc := strings.TrimSpace(*req.TelefonoContacto)
		if !auth.ValidatePhone(tc) {
			fields["telefono_contacto"] = "Ingresa un teléfono de contacto válido (7 a 20 caracteres)"
		} else {
			telContacto = &tc
		}
	} else if duenoTelefono != "" {
		tc := duenoTelefono
		telContacto = &tc
	}

	var montoCobrado *float64
	if req.MontoCobrado != nil {
		m, mErr := sanitizeAmount(*req.MontoCobrado)
		if mErr != "" {
			fields["monto_cobrado"] = mErr
		} else {
			montoCobrado = &m
		}
	}

	metodoPago, mpErr := sanitizeOptionalText(req.MetodoPagoSaaS, 30)
	if mpErr != "" {
		fields["metodo_pago_saas"] = mpErr
	}
	referenciaPago, rpErr := sanitizeOptionalText(req.ReferenciaPago, 100)
	if rpErr != "" {
		fields["referencia_pago"] = rpErr
	}
	observaciones, obsErr := sanitizeOptionalText(req.Observaciones, 2000)
	if obsErr != "" {
		fields["observaciones"] = obsErr
	}

	if len(fields) > 0 {
		return ComplejoDetalleResponse{}, &ValidationError{
			Message: "Revisa los datos de alta del complejo y dueño",
			Fields:  fields,
		}
	}

	passHash, err := auth.HashPassword(req.Dueno.Password)
	if err != nil {
		return ComplejoDetalleResponse{}, fmt.Errorf("error generando hash Argon2id del dueño: %w", err)
	}

	res, err := s.repo.CreateComplejoWithOwner(ctx, CreateComplejoParams{
		AdminID:          adminID,
		PlanID:           planID,
		Slug:             slug,
		Nombre:           nombre,
		Descripcion:      descripcion,
		TelefonoContacto: telContacto,
		Departamento:     departamento,
		Provincia:        provincia,
		Distrito:         distrito,
		MontoCobrado:     montoCobrado,
		MetodoPagoSaaS:   metodoPago,
		ReferenciaPago:   referenciaPago,
		Observaciones:    observaciones,
		DuenoEmail:       duenoEmail,
		DuenoPassHash:    passHash,
		DuenoNombre:      duenoNombre,
		DuenoTelefono:    duenoTelefono,
	})
	if err != nil {
		return ComplejoDetalleResponse{}, err
	}

	s.invalidateCache(ctx)
	return res, nil
}

// ListComplejos lista complejos de forma paginada con filtros por estado_suscripcion y por_vencer_dias.
func (s *Service) ListComplejos(ctx context.Context, filter ListComplejosFilter) (ComplejosListResponse, error) {
	fields := make(map[string]string)

	if filter.EstadoSuscripcion != nil {
		est := strings.ToUpper(strings.TrimSpace(*filter.EstadoSuscripcion))
		if !isValidEstadoSuscripcion(est) {
			fields["estado_suscripcion"] = "El estado debe ser PRUEBA, ACTIVA, VENCIDA o SUSPENDIDA"
		} else {
			filter.EstadoSuscripcion = &est
		}
	}

	if filter.PorVencerDias != nil {
		if *filter.PorVencerDias < 1 || *filter.PorVencerDias > 365 {
			fields["por_vencer_dias"] = "El filtro por_vencer_dias debe estar entre 1 y 365"
		}
	}

	if filter.Page <= 0 {
		filter.Page = 1
	}
	if filter.Limit <= 0 {
		filter.Limit = 20
	} else if filter.Limit > 100 {
		filter.Limit = 100
	}

	if len(fields) > 0 {
		return ComplejosListResponse{}, &ValidationError{
			Message: "Parámetros de consulta inválidos",
			Fields:  fields,
		}
	}

	items, total, err := s.repo.ListComplejos(ctx, filter)
	if err != nil {
		return ComplejosListResponse{}, err
	}

	totalPages := 0
	if total > 0 {
		totalPages = (total + filter.Limit - 1) / filter.Limit
	}

	return ComplejosListResponse{
		Items:      items,
		Total:      total,
		Page:       filter.Page,
		Limit:      filter.Limit,
		TotalPages: totalPages,
	}, nil
}

// GetComplejoDetail obtiene el detalle de un complejo junto con su historial comercial completo.
func (s *Service) GetComplejoDetail(ctx context.Context, complejoID uuid.UUID) (ComplejoDetalleResponse, error) {
	return s.repo.GetComplejoDetail(ctx, complejoID)
}

// RenovarSuscripcion asigna o extiende un plan SaaS registrando el snapshot comercial (RF-01).
func (s *Service) RenovarSuscripcion(ctx context.Context, adminID, complejoID uuid.UUID, req RenovarSuscripcionRequest) (OperacionSuscripcionResponse, error) {
	fields := make(map[string]string)

	planID, err := uuid.Parse(strings.TrimSpace(req.PlanID))
	if err != nil {
		fields["plan_id"] = "Ingresa un identificador de plan válido (UUID)"
	}

	var montoCobrado *float64
	if req.MontoCobrado != nil {
		m, mErr := sanitizeAmount(*req.MontoCobrado)
		if mErr != "" {
			fields["monto_cobrado"] = mErr
		} else {
			montoCobrado = &m
		}
	}

	metodoPago, mpErr := sanitizeOptionalText(req.MetodoPagoSaaS, 30)
	if mpErr != "" {
		fields["metodo_pago_saas"] = mpErr
	}
	referenciaPago, rpErr := sanitizeOptionalText(req.ReferenciaPago, 100)
	if rpErr != "" {
		fields["referencia_pago"] = rpErr
	}
	observaciones, obsErr := sanitizeOptionalText(req.Observaciones, 2000)
	if obsErr != "" {
		fields["observaciones"] = obsErr
	}

	if len(fields) > 0 {
		return OperacionSuscripcionResponse{}, &ValidationError{
			Message: "Datos de renovación inválidos",
			Fields:  fields,
		}
	}

	res, err := s.repo.RenovarSuscripcion(ctx, RenovarSuscripcionParams{
		AdminID:        adminID,
		ComplejoID:     complejoID,
		PlanID:         planID,
		MontoCobrado:   montoCobrado,
		MetodoPagoSaaS: metodoPago,
		ReferenciaPago: referenciaPago,
		Observaciones:  observaciones,
	})
	if err != nil {
		return OperacionSuscripcionResponse{}, err
	}

	s.invalidateCache(ctx)
	s.invalidatePublicSlug(ctx, res.Slug)
	return res, nil
}

// CambiarEstadoSuscripcion cambia explícitamente el estado de suscripción del complejo (RN-02).
func (s *Service) CambiarEstadoSuscripcion(ctx context.Context, adminID, complejoID uuid.UUID, req CambiarEstadoSuscripcionRequest) (OperacionSuscripcionResponse, error) {
	fields := make(map[string]string)

	nuevoEstado := strings.ToUpper(strings.TrimSpace(req.Estado))
	if !isValidEstadoSuscripcion(nuevoEstado) {
		fields["estado"] = "El estado debe ser PRUEBA, ACTIVA, VENCIDA o SUSPENDIDA"
	}

	motivo := resolveObservacion(req.Motivo, req.Observaciones)
	if n := utf8.RuneCountInString(motivo); n < 3 || n > 2000 {
		fields["motivo"] = "Ingresa el motivo u observación del cambio de estado (3 a 2000 caracteres)"
	}

	if len(fields) > 0 {
		return OperacionSuscripcionResponse{}, &ValidationError{
			Message: "Datos de cambio de estado inválidos",
			Fields:  fields,
		}
	}

	res, err := s.repo.CambiarEstadoSuscripcion(ctx, CambiarEstadoParams{
		AdminID:       adminID,
		ComplejoID:    complejoID,
		NuevoEstado:   nuevoEstado,
		Observaciones: motivo,
	})
	if err != nil {
		return OperacionSuscripcionResponse{}, err
	}

	s.invalidateCache(ctx)
	s.invalidatePublicSlug(ctx, res.Slug)
	return res, nil
}

// GetMetricasGlobales consulta las métricas ejecutivas del SaaS usando caché corto en Valkey (RF-02).
func (s *Service) GetMetricasGlobales(ctx context.Context) (MetricasGlobalesResponse, error) {
	if s.cache != nil {
		if cached, err := s.cache.Get(ctx); err == nil && cached != nil {
			return *cached, nil
		}
	}

	metricas, err := s.repo.GetMetricasGlobales(ctx)
	if err != nil {
		return MetricasGlobalesResponse{}, err
	}

	if s.cache != nil {
		_ = s.cache.Set(ctx, metricas, metricasCacheTTL)
	}

	return metricas, nil
}

func (s *Service) invalidateCache(ctx context.Context) {
	if s.cache != nil {
		if err := s.cache.Invalidate(ctx); err != nil && s.logger != nil {
			s.logger.Warn("suscripciones_metrics_cache_invalidate_error", slog.String("error", err.Error()))
		}
	}
}

func (s *Service) invalidatePublicSlug(ctx context.Context, slug string) {
	if s.publicCache == nil || strings.TrimSpace(slug) == "" {
		return
	}
	invCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := s.publicCache.InvalidatePublicComplejo(invCtx, slug); err != nil && s.logger != nil {
		s.logger.Warn("suscripciones_public_cache_invalidate_error",
			slog.String("slug", slug),
			slog.String("error", err.Error()),
		)
	}
}

// CalcularVigencia determina [inicio_at, fin_at] en zona America/Lima.
// Si venceActual sigue vigente (> now), extiende desde venceActual; si venció o es nil, inicia en now.
// Para MESES aplica clamping de fin de mes idéntico a make_interval(months => N) en PostgreSQL.
func CalcularVigencia(now time.Time, venceActual *time.Time, unidadDuracion string, cantidadDuracion int) (time.Time, time.Time, error) {
	if cantidadDuracion <= 0 {
		return time.Time{}, time.Time{}, &ValidationError{
			Message: "La duración del plan debe ser mayor a cero",
			Fields:  map[string]string{"cantidad_duracion": "Debe ser mayor a 0"},
		}
	}

	nowLima := now.In(limaLocation)
	inicioAt := nowLima
	if venceActual != nil && venceActual.After(nowLima) {
		inicioAt = venceActual.In(limaLocation)
	}

	switch strings.ToUpper(strings.TrimSpace(unidadDuracion)) {
	case UnidadDias:
		return inicioAt, inicioAt.AddDate(0, 0, cantidadDuracion), nil
	case UnidadMeses:
		return inicioAt, addMonthsClamped(inicioAt, cantidadDuracion), nil
	default:
		return time.Time{}, time.Time{}, &ValidationError{
			Message: "Unidad de duración no soportada",
			Fields:  map[string]string{"unidad_duracion": "Debe ser DIAS o MESES"},
		}
	}
}

func addMonthsClamped(t time.Time, months int) time.Time {
	y, m, d := t.Date()
	firstOfTarget := time.Date(y, m, 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location()).AddDate(0, months, 0)
	lastDayOfTarget := time.Date(firstOfTarget.Year(), firstOfTarget.Month()+1, 0, 0, 0, 0, 0, t.Location()).Day()
	if d > lastDayOfTarget {
		d = lastDayOfTarget
	}
	return time.Date(firstOfTarget.Year(), firstOfTarget.Month(), d, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

// NormalizeSlug convierte cualquier texto en un slug URL-safe compatible con chk_complejos_slug_formato.
func NormalizeSlug(raw string) string {
	clean := stripSpanishAccents(strings.ToLower(strings.TrimSpace(raw)))
	var b strings.Builder
	prevHyphen := false
	for _, r := range clean {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevHyphen = false
		} else if !prevHyphen && b.Len() > 0 {
			b.WriteByte('-')
			prevHyphen = true
		}
	}
	res := strings.Trim(b.String(), "-")
	if len(res) > 120 {
		res = strings.Trim(res[:120], "-")
	}
	return res
}

// NormalizePlanCode deriva un código en mayúsculas de hasta 40 caracteres compatible con chk_planes_codigo_upper.
func NormalizePlanCode(raw string) string {
	clean := strings.ToUpper(stripSpanishAccents(strings.TrimSpace(raw)))
	var b strings.Builder
	prevSep := false
	for _, r := range clean {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevSep = false
		} else if !prevSep && b.Len() > 0 {
			b.WriteByte('_')
			prevSep = true
		}
	}
	res := strings.Trim(b.String(), "_")
	if len(res) > 40 {
		res = strings.Trim(res[:40], "_")
	}
	return res
}

func stripSpanishAccents(s string) string {
	replacer := strings.NewReplacer(
		"á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u", "ñ", "n",
		"Á", "A", "É", "E", "Í", "I", "Ó", "O", "Ú", "U", "Ü", "U", "Ñ", "N",
	)
	return replacer.Replace(s)
}

func isValidPeriodicidad(v string) bool {
	return v == PeriodicidadPrueba || v == PeriodicidadMensual || v == PeriodicidadAnual
}

func isValidUnidadDuracion(v string) bool {
	return v == UnidadDias || v == UnidadMeses
}

func isValidEstadoSuscripcion(v string) bool {
	return v == EstadoPrueba || v == EstadoActiva || v == EstadoVencida || v == EstadoSuspendida
}

func sanitizeAmount(v float64) (float64, string) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, "Ingresa un monto numérico válido"
	}
	if v < 0 || v > maxPrecioMonto {
		return 0, "El monto debe estar entre 0.00 y 99999999.99"
	}
	rounded := math.Round(v*100) / 100
	if math.Abs(v-rounded) > 1e-6 {
		return 0, "El monto admite como máximo 2 decimales"
	}
	return rounded, ""
}

func sanitizeOptionalText(raw *string, maxRunes int) (*string, string) {
	if raw == nil {
		return nil, ""
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil, ""
	}
	if utf8.RuneCountInString(trimmed) > maxRunes {
		return nil, fmt.Sprintf("No debe exceder %d caracteres", maxRunes)
	}
	return &trimmed, ""
}

func defaultIfBlank(raw, fallback string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

func resolveObservacion(motivo, observaciones *string) string {
	m := ""
	if motivo != nil {
		m = strings.TrimSpace(*motivo)
	}
	o := ""
	if observaciones != nil {
		o = strings.TrimSpace(*observaciones)
	}
	if m != "" && o != "" && !strings.EqualFold(m, o) {
		return m + " — " + o
	}
	if m != "" {
		return m
	}
	return o
}
