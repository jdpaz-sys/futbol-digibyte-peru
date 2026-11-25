package complejos

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	maxMoneyAmount       = 99999999.99
	maxReferenciasRunes  = 150
	maxServiciosItems    = 30
	maxServicioItemRunes = 60
	maxShortint          = 32767
)

var (
	validModalidades = map[string]struct{}{
		Modalidad5v5:   {},
		Modalidad6v6:   {},
		Modalidad7v7:   {},
		Modalidad8v8:   {},
		Modalidad9v9:   {},
		Modalidad11v11: {},
	}
	validSuperficies = map[string]struct{}{
		SuperficieSintetico:    {},
		SuperficieGrassNatural: {},
		SuperficieLoza:         {},
		SuperficieParquet:      {},
	}
	validTiposMetodoPago = map[string]struct{}{
		MetodoPagoYape:          {},
		MetodoPagoPlin:          {},
		MetodoPagoTransferencia: {},
		MetodoPagoCulqi:         {},
		MetodoPagoMercadoPago:   {},
	}
)

// CredentialsEncryptor cifra credenciales sensibles de pasarela (AES-256-GCM) antes de guardarlas en BYTEA.
type CredentialsEncryptor interface {
	Encrypt(plaintext []byte) ([]byte, error)
}

// Service contiene la lógica de negocio, validaciones y reglas RN-01/RN-03/RN-08/RN-09/RN-10 del módulo complejos.
type Service struct {
	repo   Repository
	cipher CredentialsEncryptor
}

// NewService crea un Service para la autogestión del complejo del dueño.
func NewService(repo Repository, cipher CredentialsEncryptor) *Service {
	return &Service{
		repo:   repo,
		cipher: cipher,
	}
}

// IsValidAdelanto verifica las reglas de RF-06 / chk_complejos_adelanto_valido.
func IsValidAdelanto(tipo string, valor float64) bool {
	if !isFinite(valor) {
		return false
	}
	switch tipo {
	case TipoAdelantoPorcentaje:
		return valor >= 1 && valor <= 100
	case TipoAdelantoMontoFijo:
		return valor > 0 && valor <= maxMoneyAmount
	default:
		return false
	}
}

// ValidateCoordinates valida latitud y longitud WGS84 (SRID 4326) en par obligatorio.
func ValidateCoordinates(lat, lng *float64) map[string]string {
	fields := make(map[string]string)
	if lat == nil && lng == nil {
		return fields
	}
	if lat == nil || lng == nil {
		if lat == nil {
			fields["lat"] = "Debe enviar lat junto con lng"
		}
		if lng == nil {
			fields["lng"] = "Debe enviar lng junto con lat"
		}
		return fields
	}
	if !isFinite(*lat) || *lat < -90 || *lat > 90 {
		fields["lat"] = "La latitud debe estar entre -90 y 90"
	}
	if !isFinite(*lng) || *lng < -180 || *lng > 180 {
		fields["lng"] = "La longitud debe estar entre -180 y 180"
	}
	return fields
}

// EvaluatePublishChecklist evalúa los 8 requisitos obligatorios de RN-03 para publicar un complejo.
func EvaluatePublishChecklist(c ComplejoPerfil, counts ChecklistCounts) ChecklistPublicacion {
	faltantes := make([]RequisitoPublicacion, 0)

	if c.Lat == nil || c.Lng == nil {
		faltantes = append(faltantes, RequisitoPublicacion{
			Codigo:  ReqUbicacionRequerida,
			Mensaje: "Debe registrar la ubicación exacta del complejo (coordenadas latitud y longitud)",
		})
	}
	if c.DireccionEscrita == nil || strings.TrimSpace(*c.DireccionEscrita) == "" {
		faltantes = append(faltantes, RequisitoPublicacion{
			Codigo:  ReqDireccionRequerida,
			Mensaje: "Debe registrar la dirección escrita del complejo",
		})
	}
	if c.TerminosReserva == nil || strings.TrimSpace(*c.TerminosReserva) == "" {
		faltantes = append(faltantes, RequisitoPublicacion{
			Codigo:  ReqTerminosRequeridos,
			Mensaje: "Debe registrar los términos y condiciones de reserva",
		})
	}
	if !IsValidAdelanto(c.TipoAdelanto, c.ValorAdelanto) {
		faltantes = append(faltantes, RequisitoPublicacion{
			Codigo:  ReqAdelantoInvalido,
			Mensaje: "Debe configurar una regla de adelanto válida (porcentaje 1..100 o monto fijo > 0)",
		})
	}
	if counts.CanchasActivas < 1 {
		faltantes = append(faltantes, RequisitoPublicacion{
			Codigo:  ReqCanchaActivaRequerida,
			Mensaje: "Debe registrar al menos 1 cancha activa",
		})
	}
	if counts.HorariosAbiertos < 1 {
		faltantes = append(faltantes, RequisitoPublicacion{
			Codigo:  ReqHorarioAbiertoRequerido,
			Mensaje: "Debe configurar al menos 1 día de atención abierto",
		})
	}
	if counts.TarifasActivas < 1 {
		faltantes = append(faltantes, RequisitoPublicacion{
			Codigo:  ReqTarifaActivaRequerida,
			Mensaje: "Debe registrar al menos 1 tarifa activa por franja",
		})
	}
	if counts.MetodosPagoActivos < 1 {
		faltantes = append(faltantes, RequisitoPublicacion{
			Codigo:  ReqMetodoPagoActivoRequerido,
			Mensaje: "Debe registrar al menos 1 método de pago activo",
		})
	}

	return ChecklistPublicacion{
		ListoParaPublicar:   len(faltantes) == 0,
		CanchasActivas:      counts.CanchasActivas,
		HorariosAbiertos:    counts.HorariosAbiertos,
		TarifasActivas:      counts.TarifasActivas,
		MetodosPagoActivos:  counts.MetodosPagoActivos,
		RequisitosFaltantes: faltantes,
	}
}

// ---------- Complejo ----------

func (s *Service) GetComplejo(ctx context.Context, complejoID uuid.UUID) (ComplejoPerfil, error) {
	return s.repo.GetComplejo(ctx, complejoID)
}

func (s *Service) UpdateComplejo(ctx context.Context, complejoID uuid.UUID, req UpdateComplejoRequest) (ComplejoPerfil, error) {
	if !hasUpdateComplejoFields(req) {
		return ComplejoPerfil{}, &ValidationError{
			Message: "Debe enviar al menos un campo para actualizar el complejo",
			Fields:  map[string]string{"body": "Se requiere al menos un campo editable"},
		}
	}

	current, err := s.repo.GetComplejo(ctx, complejoID)
	if err != nil {
		return ComplejoPerfil{}, err
	}

	fields := make(map[string]string)

	params := UpdateComplejoParams{
		ComplejoID:          complejoID,
		Nombre:              current.Nombre,
		Descripcion:         current.Descripcion,
		TelefonoContacto:    current.TelefonoContacto,
		Servicios:           current.Servicios,
		Departamento:        current.Departamento,
		Provincia:           current.Provincia,
		Distrito:            current.Distrito,
		DireccionEscrita:    current.DireccionEscrita,
		Referencias:         current.Referencias,
		UnidadReservaMin:    current.UnidadReservaMin,
		HoldDuracionMin:     current.HoldDuracionMin,
		TipoAdelanto:        current.TipoAdelanto,
		ValorAdelanto:       current.ValorAdelanto,
		HorasMinCancelacion: current.HorasMinCancelacion,
		TerminosReserva:     current.TerminosReserva,
	}

	if req.Nombre != nil {
		nombre := strings.TrimSpace(*req.Nombre)
		if n := utf8.RuneCountInString(nombre); n < 2 || n > 150 {
			fields["nombre"] = "El nombre del complejo debe tener entre 2 y 150 caracteres"
		} else {
			params.Nombre = nombre
		}
	}

	if req.Descripcion != nil {
		desc := normalizeOptionalString(req.Descripcion)
		if desc != nil && utf8.RuneCountInString(*desc) > 4000 {
			fields["descripcion"] = "La descripción no puede exceder los 4000 caracteres"
		} else {
			params.Descripcion = desc
		}
	}

	telPtr := req.TelefonoContacto
	if req.Telefono != nil {
		if req.TelefonoContacto != nil && strings.TrimSpace(*req.TelefonoContacto) != strings.TrimSpace(*req.Telefono) {
			fields["telefono_contacto"] = "No envíes valores distintos en telefono y telefono_contacto"
		} else if telPtr == nil {
			telPtr = req.Telefono
		}
	}
	if telPtr != nil {
		tel := normalizeOptionalString(telPtr)
		if tel != nil && utf8.RuneCountInString(*tel) > 20 {
			fields["telefono_contacto"] = "El teléfono de contacto no puede exceder los 20 caracteres"
		} else {
			params.TelefonoContacto = tel
		}
	}

	if req.Servicios != nil {
		cleaned, servErr := validateServicios(*req.Servicios)
		if servErr != "" {
			fields["servicios"] = servErr
		} else {
			params.Servicios = cleaned
		}
	}

	if req.Departamento != nil {
		dep := strings.TrimSpace(*req.Departamento)
		if n := utf8.RuneCountInString(dep); n < 2 || n > 60 {
			fields["departamento"] = "El departamento debe tener entre 2 y 60 caracteres"
		} else {
			params.Departamento = dep
		}
	}

	if req.Provincia != nil {
		prov := strings.TrimSpace(*req.Provincia)
		if n := utf8.RuneCountInString(prov); n < 2 || n > 60 {
			fields["provincia"] = "La provincia debe tener entre 2 y 60 caracteres"
		} else {
			params.Provincia = prov
		}
	}

	if req.Distrito != nil {
		dist := strings.TrimSpace(*req.Distrito)
		if n := utf8.RuneCountInString(dist); n < 2 || n > 80 {
			fields["distrito"] = "El distrito debe tener entre 2 y 80 caracteres"
		} else {
			params.Distrito = dist
		}
	}

	for k, v := range ValidateCoordinates(req.Lat, req.Lng) {
		fields[k] = v
	}
	if req.Lat != nil && req.Lng != nil && fields["lat"] == "" && fields["lng"] == "" {
		latVal := *req.Lat
		lngVal := *req.Lng
		params.Lat = &latVal
		params.Lng = &lngVal
	}

	if req.DireccionEscrita != nil {
		dir := normalizeOptionalString(req.DireccionEscrita)
		if dir != nil && utf8.RuneCountInString(*dir) > 255 {
			fields["direccion_escrita"] = "La dirección escrita no puede exceder los 255 caracteres"
		} else if current.Publicado && dir == nil {
			fields["direccion_escrita"] = "No puedes vaciar la dirección mientras el complejo esté publicado"
		} else {
			params.DireccionEscrita = dir
		}
	}

	if req.Referencias != nil {
		ref := normalizeOptionalString(req.Referencias)
		if ref != nil && utf8.RuneCountInString(*ref) > maxReferenciasRunes {
			fields["referencias"] = fmt.Sprintf("Las referencias no pueden exceder los %d caracteres", maxReferenciasRunes)
		} else {
			params.Referencias = ref
		}
	}

	if req.UnidadReservaMin != nil {
		if *req.UnidadReservaMin != 30 && *req.UnidadReservaMin != 60 {
			fields["unidad_reserva_min"] = "La unidad mínima de reserva debe ser 30 o 60 minutos"
		} else {
			params.UnidadReservaMin = int16(*req.UnidadReservaMin)
		}
	}

	if req.HoldDuracionMin != nil {
		if *req.HoldDuracionMin < 5 || *req.HoldDuracionMin > 30 {
			fields["hold_duracion_min"] = "La duración del hold debe estar entre 5 y 30 minutos"
		} else {
			params.HoldDuracionMin = int16(*req.HoldDuracionMin)
		}
	}

	if req.TipoAdelanto != nil {
		tipo := strings.ToUpper(strings.TrimSpace(*req.TipoAdelanto))
		if tipo != TipoAdelantoPorcentaje && tipo != TipoAdelantoMontoFijo {
			fields["tipo_adelanto"] = "El tipo de adelanto debe ser PORCENTAJE o MONTO_FIJO"
		} else {
			params.TipoAdelanto = tipo
		}
	}

	if req.ValorAdelanto != nil {
		if !isFinite(*req.ValorAdelanto) {
			fields["valor_adelanto"] = "El valor de adelanto debe ser un número válido"
		} else {
			params.ValorAdelanto = round2(*req.ValorAdelanto)
		}
	}

	if fields["tipo_adelanto"] == "" && fields["valor_adelanto"] == "" {
		if !IsValidAdelanto(params.TipoAdelanto, params.ValorAdelanto) {
			if params.TipoAdelanto == TipoAdelantoPorcentaje {
				fields["valor_adelanto"] = "Para PORCENTAJE, el valor de adelanto debe estar entre 1 y 100"
			} else {
				fields["valor_adelanto"] = "Para MONTO_FIJO, el valor de adelanto debe ser mayor a 0"
			}
		}
	}

	if req.HorasMinCancelacion != nil {
		if *req.HorasMinCancelacion < 0 || *req.HorasMinCancelacion > 168 {
			fields["horas_min_cancelacion"] = "Las horas mínimas de cancelación deben estar entre 0 y 168"
		} else {
			params.HorasMinCancelacion = int16(*req.HorasMinCancelacion)
		}
	}

	if req.TerminosReserva != nil {
		term := normalizeOptionalString(req.TerminosReserva)
		if term != nil && utf8.RuneCountInString(*term) > 10000 {
			fields["terminos_reserva"] = "Los términos de reserva no pueden exceder los 10000 caracteres"
		} else if current.Publicado && term == nil {
			fields["terminos_reserva"] = "No puedes vaciar los términos de reserva mientras el complejo esté publicado"
		} else {
			params.TerminosReserva = term
		}
	}

	if len(fields) > 0 {
		return ComplejoPerfil{}, &ValidationError{
			Message: "Datos inválidos para actualizar el complejo",
			Fields:  fields,
		}
	}

	return s.repo.UpdateComplejo(ctx, params)
}

func (s *Service) PublishComplejo(ctx context.Context, complejoID uuid.UUID) (ComplejoPerfil, error) {
	return s.repo.PublishComplejo(ctx, complejoID)
}

func (s *Service) UnpublishComplejo(ctx context.Context, complejoID uuid.UUID) (ComplejoPerfil, error) {
	return s.repo.UnpublishComplejo(ctx, complejoID)
}

// ---------- Canchas ----------

func (s *Service) ListCanchas(ctx context.Context, complejoID uuid.UUID, activa *bool) ([]Cancha, error) {
	return s.repo.ListCanchas(ctx, complejoID, activa)
}

func (s *Service) CreateCancha(ctx context.Context, complejoID uuid.UUID, req CreateCanchaRequest) (Cancha, error) {
	fields := make(map[string]string)

	nombre := strings.TrimSpace(req.Nombre)
	if n := utf8.RuneCountInString(nombre); n < 1 || n > 100 {
		fields["nombre"] = "El nombre de la cancha es obligatorio y debe tener máximo 100 caracteres"
	}

	modalidad := strings.ToLower(strings.TrimSpace(req.Modalidad))
	if _, ok := validModalidades[modalidad]; !ok {
		fields["modalidad"] = "La modalidad debe ser 5v5, 6v6, 7v7, 8v8, 9v9 u 11v11"
	}

	superficie := SuperficieSintetico
	if strings.TrimSpace(req.Superficie) != "" {
		superficie = strings.ToUpper(strings.TrimSpace(req.Superficie))
		if _, ok := validSuperficies[superficie]; !ok {
			fields["superficie"] = "La superficie debe ser SINTETICO, GRASS_NATURAL, LOZA o PARQUET"
		}
	}

	techada := false
	if req.Techada != nil {
		techada = *req.Techada
	}

	iluminacion := true
	if req.Iluminacion != nil {
		iluminacion = *req.Iluminacion
	}

	var orden int16
	if req.Orden != nil {
		if *req.Orden < 0 || *req.Orden > maxShortint {
			fields["orden"] = "El orden debe ser un número entero mayor o igual a 0"
		} else {
			orden = int16(*req.Orden)
		}
	}

	activa := true
	if req.Activa != nil {
		activa = *req.Activa
	}

	fotoURL := normalizeOptionalString(req.FotoURL)
	if fotoURL != nil && utf8.RuneCountInString(*fotoURL) > 1000 {
		fields["foto_url"] = "La URL de foto excede el máximo permitido"
	}
	fotoKey := normalizeOptionalString(req.FotoStorageKey)
	if fotoKey != nil && utf8.RuneCountInString(*fotoKey) > 500 {
		fields["foto_storage_key"] = "La clave de almacenamiento excede el máximo permitido"
	}

	if len(fields) > 0 {
		return Cancha{}, &ValidationError{
			Message: "Datos inválidos para crear la cancha",
			Fields:  fields,
		}
	}

	return s.repo.CreateCancha(ctx, Cancha{
		ComplejoID:     complejoID,
		Nombre:         nombre,
		Modalidad:      modalidad,
		Superficie:     superficie,
		Techada:        techada,
		Iluminacion:    iluminacion,
		FotoURL:        fotoURL,
		FotoStorageKey: fotoKey,
		Orden:          orden,
		Activa:         activa,
	})
}

func (s *Service) UpdateCancha(ctx context.Context, complejoID, canchaID uuid.UUID, req UpdateCanchaRequest) (Cancha, error) {
	if req.Nombre == nil && req.Modalidad == nil && req.Superficie == nil &&
		req.Techada == nil && req.Iluminacion == nil && req.FotoURL == nil &&
		req.FotoStorageKey == nil && req.Orden == nil && req.Activa == nil {
		return Cancha{}, &ValidationError{
			Message: "Debe enviar al menos un campo para actualizar la cancha",
			Fields:  map[string]string{"body": "Se requiere al menos un campo editable"},
		}
	}

	current, err := s.repo.GetCancha(ctx, complejoID, canchaID)
	if err != nil {
		return Cancha{}, err
	}

	fields := make(map[string]string)

	if req.Nombre != nil {
		nombre := strings.TrimSpace(*req.Nombre)
		if n := utf8.RuneCountInString(nombre); n < 1 || n > 100 {
			fields["nombre"] = "El nombre de la cancha debe tener entre 1 y 100 caracteres"
		} else {
			current.Nombre = nombre
		}
	}

	if req.Modalidad != nil {
		mod := strings.ToLower(strings.TrimSpace(*req.Modalidad))
		if _, ok := validModalidades[mod]; !ok {
			fields["modalidad"] = "La modalidad debe ser 5v5, 6v6, 7v7, 8v8, 9v9 u 11v11"
		} else {
			current.Modalidad = mod
		}
	}

	if req.Superficie != nil {
		sup := strings.ToUpper(strings.TrimSpace(*req.Superficie))
		if _, ok := validSuperficies[sup]; !ok {
			fields["superficie"] = "La superficie debe ser SINTETICO, GRASS_NATURAL, LOZA o PARQUET"
		} else {
			current.Superficie = sup
		}
	}

	if req.Techada != nil {
		current.Techada = *req.Techada
	}
	if req.Iluminacion != nil {
		current.Iluminacion = *req.Iluminacion
	}

	if req.FotoURL != nil {
		fotoURL := normalizeOptionalString(req.FotoURL)
		if fotoURL != nil && utf8.RuneCountInString(*fotoURL) > 1000 {
			fields["foto_url"] = "La URL de foto excede el máximo permitido"
		} else {
			current.FotoURL = fotoURL
		}
	}

	if req.FotoStorageKey != nil {
		fotoKey := normalizeOptionalString(req.FotoStorageKey)
		if fotoKey != nil && utf8.RuneCountInString(*fotoKey) > 500 {
			fields["foto_storage_key"] = "La clave de almacenamiento excede el máximo permitido"
		} else {
			current.FotoStorageKey = fotoKey
		}
	}

	if req.Orden != nil {
		if *req.Orden < 0 || *req.Orden > maxShortint {
			fields["orden"] = "El orden debe ser un número entero mayor o igual a 0"
		} else {
			current.Orden = int16(*req.Orden)
		}
	}

	if req.Activa != nil {
		current.Activa = *req.Activa
	}

	if len(fields) > 0 {
		return Cancha{}, &ValidationError{
			Message: "Datos inválidos para actualizar la cancha",
			Fields:  fields,
		}
	}

	return s.repo.UpdateCancha(ctx, current)
}

func (s *Service) DeactivateCancha(ctx context.Context, complejoID, canchaID uuid.UUID) (Cancha, error) {
	return s.repo.DeactivateCancha(ctx, complejoID, canchaID)
}

// ---------- Horarios de Atención ----------

func (s *Service) ListHorarios(ctx context.Context, complejoID uuid.UUID) ([]HorarioAtencion, error) {
	return s.repo.ListHorarios(ctx, complejoID)
}

func (s *Service) UpsertHorarios(ctx context.Context, complejoID uuid.UUID, req UpsertHorariosRequest) ([]HorarioAtencion, error) {
	fields := make(map[string]string)

	if len(req.Horarios) != 7 {
		fields["horarios"] = "Debe enviar exactamente los 7 días de la semana (0=Domingo a 6=Sábado)"
		return nil, &ValidationError{
			Message: "Configuración semanal de horarios incompleta",
			Fields:  fields,
		}
	}

	seen := make([]bool, 7)
	prepared := make([]HorarioAtencion, 0, 7)

	for i, item := range req.Horarios {
		prefix := fmt.Sprintf("horarios[%d]", i)
		if item.DiaSemana < 0 || item.DiaSemana > 6 {
			fields[prefix+".dia_semana"] = "El día de la semana debe estar entre 0 (Domingo) y 6 (Sábado)"
			continue
		}
		if seen[item.DiaSemana] {
			fields[prefix+".dia_semana"] = fmt.Sprintf("El día %d está duplicado", item.DiaSemana)
			continue
		}
		seen[item.DiaSemana] = true

		if item.MinutoApertura < 0 || item.MinutoApertura > 1440 {
			fields[prefix+".minuto_apertura"] = "El minuto de apertura debe estar entre 0 y 1440"
		}
		if item.MinutoCierre < 0 || item.MinutoCierre > 1440 {
			fields[prefix+".minuto_cierre"] = "El minuto de cierre debe estar entre 0 y 1440"
		}
		if !item.Cerrado && item.MinutoApertura >= item.MinutoCierre {
			fields[prefix+".minuto_cierre"] = "Cuando el día está abierto, minuto_apertura debe ser menor que minuto_cierre"
		}

		prepared = append(prepared, HorarioAtencion{
			ComplejoID:     complejoID,
			DiaSemana:      int16(item.DiaSemana),
			MinutoApertura: int16(item.MinutoApertura),
			MinutoCierre:   int16(item.MinutoCierre),
			Cerrado:        item.Cerrado,
		})
	}

	for d, ok := range seen {
		if !ok {
			fields["horarios"] = fmt.Sprintf("Falta configurar el día de semana %d", d)
			break
		}
	}

	if len(fields) > 0 {
		return nil, &ValidationError{
			Message: "Horarios de atención inválidos",
			Fields:  fields,
		}
	}

	sort.Slice(prepared, func(i, j int) bool {
		return prepared[i].DiaSemana < prepared[j].DiaSemana
	})

	return s.repo.UpsertHorarios(ctx, complejoID, prepared)
}

// ---------- Tarifas por Franja ----------

func (s *Service) ListTarifas(ctx context.Context, complejoID uuid.UUID, filter TarifasFilter) ([]TarifaFranja, error) {
	if filter.DiaSemana != nil && (*filter.DiaSemana < 0 || *filter.DiaSemana > 6) {
		return nil, &ValidationError{
			Message: "Filtro de tarifa inválido",
			Fields:  map[string]string{"dia_semana": "El día de la semana debe estar entre 0 y 6"},
		}
	}
	return s.repo.ListTarifas(ctx, complejoID, filter)
}

func (s *Service) CreateTarifa(ctx context.Context, complejoID uuid.UUID, req CreateTarifaRequest) (TarifaFranja, error) {
	fields := make(map[string]string)

	var canchaID *uuid.UUID
	if req.CanchaID != nil && strings.TrimSpace(*req.CanchaID) != "" {
		parsed, err := uuid.Parse(strings.TrimSpace(*req.CanchaID))
		if err != nil || parsed == uuid.Nil {
			fields["cancha_id"] = "Identificador de cancha inválido"
		} else {
			canchaID = &parsed
		}
	}

	var diaSemana int16
	if req.DiaSemana == nil {
		fields["dia_semana"] = "El día de la semana (0..6) es obligatorio"
	} else if *req.DiaSemana < 0 || *req.DiaSemana > 6 {
		fields["dia_semana"] = "El día de la semana debe estar entre 0 (Domingo) y 6 (Sábado)"
	} else {
		diaSemana = int16(*req.DiaSemana)
	}

	var minutoInicio, minutoFin int16
	if req.MinutoInicio == nil {
		fields["minuto_inicio"] = "El minuto de inicio es obligatorio"
	} else if *req.MinutoInicio < 0 || *req.MinutoInicio >= 1440 {
		fields["minuto_inicio"] = "El minuto de inicio debe estar entre 0 y 1439"
	} else {
		minutoInicio = int16(*req.MinutoInicio)
	}

	if req.MinutoFin == nil {
		fields["minuto_fin"] = "El minuto de fin es obligatorio"
	} else if *req.MinutoFin <= 0 || *req.MinutoFin > 1440 {
		fields["minuto_fin"] = "El minuto de fin debe estar entre 1 y 1440"
	} else {
		minutoFin = int16(*req.MinutoFin)
	}

	if fields["minuto_inicio"] == "" && fields["minuto_fin"] == "" && minutoInicio >= minutoFin {
		fields["minuto_fin"] = "minuto_inicio debe ser estrictamente menor que minuto_fin"
	}

	var precioHora float64
	if req.PrecioHora == nil {
		fields["precio_hora"] = "El precio por hora es obligatorio"
	} else if !isFinite(*req.PrecioHora) || *req.PrecioHora <= 0 || *req.PrecioHora > maxMoneyAmount {
		fields["precio_hora"] = "El precio por hora debe ser un monto mayor a 0"
	} else {
		precioHora = round2(*req.PrecioHora)
	}

	etiqueta := normalizeOptionalString(req.Etiqueta)
	if etiqueta != nil && utf8.RuneCountInString(*etiqueta) > 80 {
		fields["etiqueta"] = "La etiqueta no puede exceder los 80 caracteres"
	}

	esHoraMuerta := false
	if req.EsHoraMuerta != nil {
		esHoraMuerta = *req.EsHoraMuerta
	}

	activa := true
	if req.Activa != nil {
		activa = *req.Activa
	}

	if len(fields) > 0 {
		return TarifaFranja{}, &ValidationError{
			Message: "Datos inválidos para crear la tarifa por franja",
			Fields:  fields,
		}
	}

	// Defensa en profundidad multi-inquilino (además de la FK compuesta en PostgreSQL)
	if canchaID != nil {
		if _, err := s.repo.GetCancha(ctx, complejoID, *canchaID); err != nil {
			return TarifaFranja{}, err
		}
	}

	return s.repo.CreateTarifa(ctx, TarifaFranja{
		ComplejoID:   complejoID,
		CanchaID:     canchaID,
		DiaSemana:    diaSemana,
		MinutoInicio: minutoInicio,
		MinutoFin:    minutoFin,
		PrecioHora:   precioHora,
		Etiqueta:     etiqueta,
		EsHoraMuerta: esHoraMuerta,
		Activa:       activa,
	})
}

func (s *Service) UpdateTarifa(ctx context.Context, complejoID, tarifaID uuid.UUID, req UpdateTarifaRequest) (TarifaFranja, error) {
	if !req.CanchaID.Set && req.DiaSemana == nil && req.MinutoInicio == nil &&
		req.MinutoFin == nil && req.PrecioHora == nil && req.Etiqueta == nil &&
		req.EsHoraMuerta == nil && req.Activa == nil {
		return TarifaFranja{}, &ValidationError{
			Message: "Debe enviar al menos un campo para actualizar la tarifa",
			Fields:  map[string]string{"body": "Se requiere al menos un campo editable"},
		}
	}

	current, err := s.repo.GetTarifa(ctx, complejoID, tarifaID)
	if err != nil {
		return TarifaFranja{}, err
	}

	fields := make(map[string]string)

	var verifyCanchaID *uuid.UUID
	if req.CanchaID.Set {
		if req.CanchaID.Value != nil {
			if *req.CanchaID.Value == uuid.Nil {
				fields["cancha_id"] = "Identificador de cancha inválido"
			} else {
				current.CanchaID = req.CanchaID.Value
				verifyCanchaID = req.CanchaID.Value
			}
		} else {
			current.CanchaID = nil
		}
	}

	if req.DiaSemana != nil {
		if *req.DiaSemana < 0 || *req.DiaSemana > 6 {
			fields["dia_semana"] = "El día de la semana debe estar entre 0 (Domingo) y 6 (Sábado)"
		} else {
			current.DiaSemana = int16(*req.DiaSemana)
		}
	}

	if req.MinutoInicio != nil {
		if *req.MinutoInicio < 0 || *req.MinutoInicio >= 1440 {
			fields["minuto_inicio"] = "El minuto de inicio debe estar entre 0 y 1439"
		} else {
			current.MinutoInicio = int16(*req.MinutoInicio)
		}
	}

	if req.MinutoFin != nil {
		if *req.MinutoFin <= 0 || *req.MinutoFin > 1440 {
			fields["minuto_fin"] = "El minuto de fin debe estar entre 1 y 1440"
		} else {
			current.MinutoFin = int16(*req.MinutoFin)
		}
	}

	if fields["minuto_inicio"] == "" && fields["minuto_fin"] == "" && current.MinutoInicio >= current.MinutoFin {
		fields["minuto_fin"] = "minuto_inicio debe ser estrictamente menor que minuto_fin"
	}

	if req.PrecioHora != nil {
		if !isFinite(*req.PrecioHora) || *req.PrecioHora <= 0 || *req.PrecioHora > maxMoneyAmount {
			fields["precio_hora"] = "El precio por hora debe ser un monto mayor a 0"
		} else {
			current.PrecioHora = round2(*req.PrecioHora)
		}
	}

	if req.Etiqueta != nil {
		etiqueta := normalizeOptionalString(req.Etiqueta)
		if etiqueta != nil && utf8.RuneCountInString(*etiqueta) > 80 {
			fields["etiqueta"] = "La etiqueta no puede exceder los 80 caracteres"
		} else {
			current.Etiqueta = etiqueta
		}
	}

	if req.EsHoraMuerta != nil {
		current.EsHoraMuerta = *req.EsHoraMuerta
	}
	if req.Activa != nil {
		current.Activa = *req.Activa
	}

	if len(fields) > 0 {
		return TarifaFranja{}, &ValidationError{
			Message: "Datos inválidos para actualizar la tarifa por franja",
			Fields:  fields,
		}
	}

	if verifyCanchaID != nil {
		if _, err := s.repo.GetCancha(ctx, complejoID, *verifyCanchaID); err != nil {
			return TarifaFranja{}, err
		}
	}

	return s.repo.UpdateTarifa(ctx, current)
}

func (s *Service) DeleteTarifa(ctx context.Context, complejoID, tarifaID uuid.UUID) error {
	return s.repo.DeleteTarifa(ctx, complejoID, tarifaID)
}

// ---------- Cupones ----------

func (s *Service) ListCupones(ctx context.Context, complejoID uuid.UUID, activo *bool) ([]Cupon, error) {
	return s.repo.ListCupones(ctx, complejoID, activo)
}

func (s *Service) CreateCupon(ctx context.Context, complejoID uuid.UUID, req CreateCuponRequest) (Cupon, error) {
	fields := make(map[string]string)

	codigo := strings.ToUpper(strings.TrimSpace(req.Codigo))
	if msg := validateCuponCodigo(codigo); msg != "" {
		fields["codigo"] = msg
	}

	descripcion := normalizeOptionalString(req.Descripcion)
	if descripcion != nil && utf8.RuneCountInString(*descripcion) > 150 {
		fields["descripcion"] = "La descripción del cupón no puede exceder los 150 caracteres"
	}

	tipoDescuento := strings.ToUpper(strings.TrimSpace(req.TipoDescuento))
	if tipoDescuento != TipoDescuentoPorcentaje && tipoDescuento != TipoDescuentoMontoFijo {
		fields["tipo_descuento"] = "El tipo de descuento debe ser PORCENTAJE o MONTO_FIJO"
	}

	var valorDescuento float64
	if req.ValorDescuento == nil {
		fields["valor_descuento"] = "El valor de descuento es obligatorio"
	} else if !isFinite(*req.ValorDescuento) {
		fields["valor_descuento"] = "El valor de descuento debe ser un número válido"
	} else {
		valorDescuento = round2(*req.ValorDescuento)
		if fields["tipo_descuento"] == "" {
			if msg := validateDescuentoValor(tipoDescuento, valorDescuento); msg != "" {
				fields["valor_descuento"] = msg
			}
		}
	}

	validoDesde := time.Now().UTC()
	if req.ValidoDesde != nil {
		validoDesde = req.ValidoDesde.UTC()
	}

	var validoHasta *time.Time
	if req.ValidoHasta != nil {
		vh := req.ValidoHasta.UTC()
		if !validoDesde.Before(vh) {
			fields["valido_hasta"] = "La fecha valido_hasta debe ser posterior a valido_desde"
		} else {
			validoHasta = &vh
		}
	}

	if req.MaxUsos != nil && *req.MaxUsos <= 0 {
		fields["max_usos"] = "El máximo de usos debe ser mayor a 0"
	}

	acumulable := false
	if req.AcumulableHoraMuerta != nil {
		acumulable = *req.AcumulableHoraMuerta
	}

	activo := true
	if req.Activo != nil {
		activo = *req.Activo
	}

	if len(fields) > 0 {
		return Cupon{}, &ValidationError{
			Message: "Datos inválidos para crear el cupón",
			Fields:  fields,
		}
	}

	return s.repo.CreateCupon(ctx, Cupon{
		ComplejoID:           complejoID,
		Codigo:               codigo,
		Descripcion:          descripcion,
		TipoDescuento:        tipoDescuento,
		ValorDescuento:       valorDescuento,
		ValidoDesde:          validoDesde,
		ValidoHasta:          validoHasta,
		MaxUsos:              req.MaxUsos,
		AcumulableHoraMuerta: acumulable,
		Activo:               activo,
	})
}

func (s *Service) UpdateCupon(ctx context.Context, complejoID, cuponID uuid.UUID, req UpdateCuponRequest) (Cupon, error) {
	if req.Codigo == nil && req.Descripcion == nil && req.TipoDescuento == nil &&
		req.ValorDescuento == nil && req.ValidoDesde == nil && req.ValidoHasta == nil &&
		req.MaxUsos == nil && req.AcumulableHoraMuerta == nil && req.Activo == nil {
		return Cupon{}, &ValidationError{
			Message: "Debe enviar al menos un campo para actualizar el cupón",
			Fields:  map[string]string{"body": "Se requiere al menos un campo editable"},
		}
	}

	current, err := s.repo.GetCupon(ctx, complejoID, cuponID)
	if err != nil {
		return Cupon{}, err
	}

	fields := make(map[string]string)

	if req.Codigo != nil {
		codigo := strings.ToUpper(strings.TrimSpace(*req.Codigo))
		if msg := validateCuponCodigo(codigo); msg != "" {
			fields["codigo"] = msg
		} else {
			current.Codigo = codigo
		}
	}

	if req.Descripcion != nil {
		desc := normalizeOptionalString(req.Descripcion)
		if desc != nil && utf8.RuneCountInString(*desc) > 150 {
			fields["descripcion"] = "La descripción del cupón no puede exceder los 150 caracteres"
		} else {
			current.Descripcion = desc
		}
	}

	if req.TipoDescuento != nil {
		tipo := strings.ToUpper(strings.TrimSpace(*req.TipoDescuento))
		if tipo != TipoDescuentoPorcentaje && tipo != TipoDescuentoMontoFijo {
			fields["tipo_descuento"] = "El tipo de descuento debe ser PORCENTAJE o MONTO_FIJO"
		} else {
			current.TipoDescuento = tipo
		}
	}

	if req.ValorDescuento != nil {
		if !isFinite(*req.ValorDescuento) {
			fields["valor_descuento"] = "El valor de descuento debe ser un número válido"
		} else {
			current.ValorDescuento = round2(*req.ValorDescuento)
		}
	}

	if fields["tipo_descuento"] == "" && fields["valor_descuento"] == "" {
		if msg := validateDescuentoValor(current.TipoDescuento, current.ValorDescuento); msg != "" {
			fields["valor_descuento"] = msg
		}
	}

	if req.ValidoDesde != nil {
		current.ValidoDesde = req.ValidoDesde.UTC()
	}
	if req.ValidoHasta != nil {
		vh := req.ValidoHasta.UTC()
		current.ValidoHasta = &vh
	}
	if current.ValidoHasta != nil && !current.ValidoDesde.Before(*current.ValidoHasta) {
		fields["valido_hasta"] = "La fecha valido_hasta debe ser posterior a valido_desde"
	}

	if req.MaxUsos != nil {
		if *req.MaxUsos <= 0 {
			fields["max_usos"] = "El máximo de usos debe ser mayor a 0"
		} else if *req.MaxUsos < current.UsosActuales {
			fields["max_usos"] = fmt.Sprintf("El máximo de usos no puede ser menor a los usos actuales (%d)", current.UsosActuales)
		} else {
			v := *req.MaxUsos
			current.MaxUsos = &v
		}
	}

	if req.AcumulableHoraMuerta != nil {
		current.AcumulableHoraMuerta = *req.AcumulableHoraMuerta
	}
	if req.Activo != nil {
		current.Activo = *req.Activo
	}

	if len(fields) > 0 {
		return Cupon{}, &ValidationError{
			Message: "Datos inválidos para actualizar el cupón",
			Fields:  fields,
		}
	}

	return s.repo.UpdateCupon(ctx, current)
}

// ---------- Métodos de Pago ----------

func (s *Service) ListMetodosPago(ctx context.Context, complejoID uuid.UUID, activo *bool) ([]MetodoPago, error) {
	return s.repo.ListMetodosPago(ctx, complejoID, activo)
}

func (s *Service) CreateMetodoPago(ctx context.Context, complejoID uuid.UUID, req CreateMetodoPagoRequest) (MetodoPago, error) {
	fields := make(map[string]string)

	tipo := strings.ToUpper(strings.TrimSpace(req.Tipo))
	if _, ok := validTiposMetodoPago[tipo]; !ok {
		fields["tipo"] = "El tipo debe ser YAPE, PLIN, TRANSFERENCIA, CULQI o MERCADOPAGO"
	}

	params := SaveMetodoPagoParams{
		ComplejoID:        complejoID,
		Tipo:              tipo,
		Etiqueta:          normalizeOptionalString(req.Etiqueta),
		Titular:           normalizeOptionalString(req.Titular),
		EntidadFinanciera: normalizeOptionalString(req.EntidadFinanciera),
		NumeroDestino:     normalizeOptionalString(req.NumeroDestino),
		CCI:               normalizeOptionalString(req.CCI),
		QRURL:             normalizeOptionalString(req.QRURL),
		QRStorageKey:      normalizeOptionalString(req.QRStorageKey),
		LlavePublica:      normalizeOptionalString(req.LlavePublica),
		Instrucciones:     normalizeOptionalString(req.Instrucciones),
		Activo:            true,
	}
	if req.Activo != nil {
		params.Activo = *req.Activo
	}

	validateMetodoPagoFieldLengths(params, fields)

	hasCreds, compactCreds, credErr := parseGatewayCredentials(req.Credenciales)
	if credErr != "" {
		fields["credenciales"] = credErr
	}

	if fields["tipo"] == "" {
		validateMetodoPagoByType(params, true, hasCreds, fields)
	}

	if len(fields) > 0 {
		return MetodoPago{}, &ValidationError{
			Message: "Datos inválidos para crear el método de pago",
			Fields:  fields,
		}
	}

	if hasCreds {
		if s.cipher == nil {
			return MetodoPago{}, errors.New("cifrador de credenciales no configurado")
		}
		encrypted, err := s.cipher.Encrypt(compactCreds)
		if err != nil {
			return MetodoPago{}, fmt.Errorf("error cifrando credenciales de pasarela: %w", err)
		}
		params.CredencialesCifradas = encrypted
		params.UpdateCredenciales = true
	}

	return s.repo.CreateMetodoPago(ctx, params)
}

func (s *Service) UpdateMetodoPago(ctx context.Context, complejoID, metodoID uuid.UUID, req UpdateMetodoPagoRequest) (MetodoPago, error) {
	hasRawCreds := len(bytes.TrimSpace(req.Credenciales)) > 0
	if req.Etiqueta == nil && req.Titular == nil && req.EntidadFinanciera == nil &&
		req.NumeroDestino == nil && req.CCI == nil && req.QRURL == nil &&
		req.QRStorageKey == nil && req.LlavePublica == nil && !hasRawCreds &&
		req.Instrucciones == nil && req.Activo == nil {
		return MetodoPago{}, &ValidationError{
			Message: "Debe enviar al menos un campo para actualizar el método de pago",
			Fields:  map[string]string{"body": "Se requiere al menos un campo editable"},
		}
	}

	current, err := s.repo.GetMetodoPago(ctx, complejoID, metodoID)
	if err != nil {
		return MetodoPago{}, err
	}

	params := SaveMetodoPagoParams{
		ID:                current.ID,
		ComplejoID:        complejoID,
		Tipo:              current.Tipo,
		Etiqueta:          current.Etiqueta,
		Titular:           current.Titular,
		EntidadFinanciera: current.EntidadFinanciera,
		NumeroDestino:     current.NumeroDestino,
		CCI:               current.CCI,
		QRURL:             current.QRURL,
		QRStorageKey:      current.QRStorageKey,
		LlavePublica:      current.LlavePublica,
		Instrucciones:     current.Instrucciones,
		Activo:            current.Activo,
	}

	if req.Etiqueta != nil {
		params.Etiqueta = normalizeOptionalString(req.Etiqueta)
	}
	if req.Titular != nil {
		params.Titular = normalizeOptionalString(req.Titular)
	}
	if req.EntidadFinanciera != nil {
		params.EntidadFinanciera = normalizeOptionalString(req.EntidadFinanciera)
	}
	if req.NumeroDestino != nil {
		params.NumeroDestino = normalizeOptionalString(req.NumeroDestino)
	}
	if req.CCI != nil {
		params.CCI = normalizeOptionalString(req.CCI)
	}
	if req.QRURL != nil {
		params.QRURL = normalizeOptionalString(req.QRURL)
	}
	if req.QRStorageKey != nil {
		params.QRStorageKey = normalizeOptionalString(req.QRStorageKey)
	}
	if req.LlavePublica != nil {
		params.LlavePublica = normalizeOptionalString(req.LlavePublica)
	}
	if req.Instrucciones != nil {
		params.Instrucciones = normalizeOptionalString(req.Instrucciones)
	}
	if req.Activo != nil {
		params.Activo = *req.Activo
	}

	fields := make(map[string]string)
	validateMetodoPagoFieldLengths(params, fields)

	hasCreds, compactCreds, credErr := parseGatewayCredentials(req.Credenciales)
	if credErr != "" {
		fields["credenciales"] = credErr
	}

	validateMetodoPagoByType(params, false, hasCreds, fields)

	if len(fields) > 0 {
		return MetodoPago{}, &ValidationError{
			Message: "Datos inválidos para actualizar el método de pago",
			Fields:  fields,
		}
	}

	if hasCreds {
		if s.cipher == nil {
			return MetodoPago{}, errors.New("cifrador de credenciales no configurado")
		}
		encrypted, err := s.cipher.Encrypt(compactCreds)
		if err != nil {
			return MetodoPago{}, fmt.Errorf("error cifrando credenciales de pasarela: %w", err)
		}
		params.CredencialesCifradas = encrypted
		params.UpdateCredenciales = true
	}

	return s.repo.UpdateMetodoPago(ctx, params)
}

func (s *Service) DeactivateMetodoPago(ctx context.Context, complejoID, metodoID uuid.UUID) (MetodoPago, error) {
	return s.repo.DeactivateMetodoPago(ctx, complejoID, metodoID)
}

// ---------- Helpers de validación ----------

func hasUpdateComplejoFields(req UpdateComplejoRequest) bool {
	return req.Nombre != nil ||
		req.Descripcion != nil ||
		req.TelefonoContacto != nil ||
		req.Telefono != nil ||
		req.Servicios != nil ||
		req.Departamento != nil ||
		req.Provincia != nil ||
		req.Distrito != nil ||
		req.Lat != nil ||
		req.Lng != nil ||
		req.DireccionEscrita != nil ||
		req.Referencias != nil ||
		req.UnidadReservaMin != nil ||
		req.HoldDuracionMin != nil ||
		req.TipoAdelanto != nil ||
		req.ValorAdelanto != nil ||
		req.HorasMinCancelacion != nil ||
		req.TerminosReserva != nil
}

func validateServicios(raw []string) ([]string, string) {
	if len(raw) > maxServiciosItems {
		return nil, fmt.Sprintf("No puedes registrar más de %d servicios", maxServiciosItems)
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		trimmed := strings.TrimSpace(s)
		if trimmed == "" {
			return nil, "Los servicios no pueden contener valores vacíos"
		}
		if utf8.RuneCountInString(trimmed) > maxServicioItemRunes {
			return nil, fmt.Sprintf("Cada servicio debe tener máximo %d caracteres", maxServicioItemRunes)
		}
		key := strings.ToLower(trimmed)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out, ""
}

func validateCuponCodigo(codigo string) string {
	n := utf8.RuneCountInString(codigo)
	if n < 2 || n > 40 {
		return "El código del cupón debe tener entre 2 y 40 caracteres"
	}
	for _, r := range codigo {
		isUpper := r >= 'A' && r <= 'Z'
		isDigit := r >= '0' && r <= '9'
		if !isUpper && !isDigit && r != '-' && r != '_' {
			return "El código del cupón solo puede contener letras, números, guiones y guion bajo sin espacios"
		}
	}
	return ""
}

func validateDescuentoValor(tipo string, valor float64) string {
	switch tipo {
	case TipoDescuentoPorcentaje:
		if valor <= 0 || valor > 100 {
			return "Para PORCENTAJE, el valor de descuento debe ser mayor a 0 y menor o igual a 100"
		}
	case TipoDescuentoMontoFijo:
		if valor <= 0 || valor > maxMoneyAmount {
			return "Para MONTO_FIJO, el valor de descuento debe ser mayor a 0"
		}
	}
	return ""
}

func validateMetodoPagoFieldLengths(p SaveMetodoPagoParams, fields map[string]string) {
	if p.Etiqueta != nil && utf8.RuneCountInString(*p.Etiqueta) > 80 {
		fields["etiqueta"] = "La etiqueta no puede exceder los 80 caracteres"
	}
	if p.Titular != nil && utf8.RuneCountInString(*p.Titular) > 150 {
		fields["titular"] = "El titular no puede exceder los 150 caracteres"
	}
	if p.EntidadFinanciera != nil && utf8.RuneCountInString(*p.EntidadFinanciera) > 60 {
		fields["entidad_financiera"] = "La entidad financiera no puede exceder los 60 caracteres"
	}
	if p.NumeroDestino != nil && utf8.RuneCountInString(*p.NumeroDestino) > 50 {
		fields["numero_destino"] = "El número de destino/cuenta no puede exceder los 50 caracteres"
	}
	if p.CCI != nil && utf8.RuneCountInString(*p.CCI) > 25 {
		fields["cci"] = "El CCI no puede exceder los 25 caracteres"
	}
	if p.QRURL != nil && utf8.RuneCountInString(*p.QRURL) > 1000 {
		fields["qr_url"] = "La URL del QR excede el máximo permitido"
	}
	if p.QRStorageKey != nil && utf8.RuneCountInString(*p.QRStorageKey) > 500 {
		fields["qr_storage_key"] = "La clave de almacenamiento del QR excede el máximo permitido"
	}
	if p.LlavePublica != nil && utf8.RuneCountInString(*p.LlavePublica) > 200 {
		fields["llave_publica"] = "La llave pública no puede exceder los 200 caracteres"
	}
	if p.Instrucciones != nil && utf8.RuneCountInString(*p.Instrucciones) > 255 {
		fields["instrucciones"] = "Las instrucciones no pueden exceder los 255 caracteres"
	}
}

func validateMetodoPagoByType(p SaveMetodoPagoParams, isCreate, hasCreds bool, fields map[string]string) {
	switch p.Tipo {
	case MetodoPagoYape, MetodoPagoPlin:
		if p.Titular == nil {
			fields["titular"] = "El nombre del titular es obligatorio para Yape o Plin"
		}
		if p.NumeroDestino == nil && p.QRURL == nil {
			fields["numero_destino"] = "Debe registrar el número de celular (numero_destino) o qr_url para Yape o Plin"
		}
		if hasCreds {
			fields["credenciales"] = "Las billeteras digitales (YAPE/PLIN) no aceptan credenciales de pasarela"
		}
	case MetodoPagoTransferencia:
		if p.Titular == nil {
			fields["titular"] = "El nombre del titular es obligatorio para transferencia bancaria"
		}
		if p.EntidadFinanciera == nil {
			fields["entidad_financiera"] = "El banco (entidad_financiera) es obligatorio para transferencia bancaria"
		}
		if p.NumeroDestino == nil {
			fields["numero_destino"] = "El número de cuenta (numero_destino) es obligatorio para transferencia bancaria"
		}
		if hasCreds {
			fields["credenciales"] = "Las transferencias bancarias no aceptan credenciales de pasarela"
		}
	case MetodoPagoCulqi, MetodoPagoMercadoPago:
		if p.LlavePublica == nil {
			fields["llave_publica"] = "La llave pública (llave_publica) es obligatoria para pasarelas de pago"
		}
		if isCreate && !hasCreds && fields["credenciales"] == "" {
			fields["credenciales"] = "Las credenciales privadas de la pasarela son obligatorias al registrar CULQI o MERCADOPAGO"
		}
	}
}

// parseGatewayCredentials valida que credenciales sea un objeto JSON no vacío (o ausente/null).
func parseGatewayCredentials(raw json.RawMessage) (bool, []byte, string) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return false, nil, ""
	}
	if len(trimmed) > 16384 {
		return false, nil, "Las credenciales exceden el tamaño máximo permitido"
	}

	var obj map[string]any
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return false, nil, "Las credenciales deben enviarse como un objeto JSON válido (ej. {\"secret_key\":\"...\"})"
	}
	if len(obj) == 0 {
		return false, nil, "El objeto de credenciales no puede estar vacío"
	}

	var compact bytes.Buffer
	if err := json.Compact(&compact, trimmed); err != nil {
		return false, nil, "No se pudo normalizar el JSON de credenciales"
	}
	return true, compact.Bytes(), ""
}

func normalizeOptionalString(v *string) *string {
	if v == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*v)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
