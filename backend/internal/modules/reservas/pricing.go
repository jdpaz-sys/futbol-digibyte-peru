package reservas

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
)

var limaLocation *time.Location

func init() {
	loc, err := time.LoadLocation(ZonaHorariaLima)
	if err != nil {
		loc = time.FixedZone(ZonaHorariaLima, -5*60*60)
	}
	limaLocation = loc
}

// LimaLocation retorna la zona horaria oficial America/Lima (UTC-5).
func LimaLocation() *time.Location {
	return limaLocation
}

// PricingResult agrupa el cálculo puro y determinístico de precios, cupones y adelanto.
type PricingResult struct {
	Inicio            time.Time
	Fin               time.Time
	DuracionMin       int
	PrecioBase        float64
	MontoDescuento    float64
	MontoTotal        float64
	MontoAdelanto     float64
	SaldoPendiente    float64
	IncluyeHoraMuerta bool
	CuponAplicado     *CuponAplicado
	Desglose          []CotizacionTramo
}

// ParseFechaLima valida una fecha YYYY-MM-DD y devuelve el inicio del día (00:00) en America/Lima.
func ParseFechaLima(fecha string) (time.Time, error) {
	t, err := time.ParseInLocation("2006-01-02", fecha, limaLocation)
	if err != nil {
		return time.Time{}, err
	}
	if t.Format("2006-01-02") != fecha {
		return time.Time{}, fmt.Errorf("formato de fecha inválido")
	}
	return t, nil
}

// ExtractRangeMinutesLima valida que [inicio, fin) corresponda a un único día calendario
// en America/Lima (permitiendo cierre exacto a las 24:00 del mismo día) y sin segundos.
func ExtractRangeMinutesLima(inicio, fin time.Time) (dayStart time.Time, diaSemana int16, startMin int, endMin int, err error) {
	iniLima := inicio.In(limaLocation)
	finLima := fin.In(limaLocation)

	if !finLima.After(iniLima) {
		return time.Time{}, 0, 0, 0, &ValidationError{
			Message: "El rango horario es inválido",
			Fields:  map[string]string{"fin": "La hora de fin debe ser posterior a la hora de inicio"},
		}
	}

	if iniLima.Second() != 0 || iniLima.Nanosecond() != 0 || finLima.Second() != 0 || finLima.Nanosecond() != 0 {
		return time.Time{}, 0, 0, 0, &ValidationError{
			Message: "El rango horario debe tener minutos exactos sin segundos",
			Fields:  map[string]string{"inicio": "Las horas de inicio y fin no deben incluir segundos"},
		}
	}

	dayStart = time.Date(iniLima.Year(), iniLima.Month(), iniLima.Day(), 0, 0, 0, 0, limaLocation)
	startDiff := iniLima.Sub(dayStart)
	endDiff := finLima.Sub(dayStart)

	if startDiff < 0 || endDiff > 24*time.Hour || startDiff%time.Minute != 0 || endDiff%time.Minute != 0 {
		return time.Time{}, 0, 0, 0, &ValidationError{
			Message: "La reserva debe iniciar y finalizar dentro del mismo día calendario (America/Lima)",
			Fields:  map[string]string{"fin": "No se permiten reservas que crucen la medianoche hacia otro día"},
		}
	}

	startMin = int(startDiff / time.Minute)
	endMin = int(endDiff / time.Minute)
	if startMin >= endMin || endMin > 1440 {
		return time.Time{}, 0, 0, 0, &ValidationError{
			Message: "El rango horario es inválido",
			Fields:  map[string]string{"fin": "La hora de fin debe ser mayor a la hora de inicio"},
		}
	}

	return dayStart, int16(dayStart.Weekday()), startMin, endMin, nil
}

// ValidateSlotWindow verifica horario de atención del día y alineación con unidad_reserva_min (RN-07).
func ValidateSlotWindow(horario *HorarioAtencion, unidadReservaMin int16, startMin, endMin int) error {
	if horario == nil || horario.Cerrado {
		return ErrComplejoCerrado
	}
	if unidadReservaMin != 30 && unidadReservaMin != 60 {
		return ErrUnidadReservaInvalida
	}

	apertura := int(horario.MinutoApertura)
	cierre := int(horario.MinutoCierre)
	unidad := int(unidadReservaMin)

	if startMin < apertura || endMin > cierre {
		return ErrHorarioFueraDeRango
	}

	duracion := endMin - startMin
	if duracion < unidad || duracion%unidad != 0 {
		return ErrUnidadReservaInvalida
	}

	if (startMin-apertura)%unidad != 0 {
		return ErrUnidadReservaInvalida
	}

	return nil
}

// ResolveTarifasInterval particiona [startMin, endMin) en tramos tarifarios exactos (RN-08),
// priorizando tarifas específicas de cancha sobre tarifas generales (cancha_id IS NULL).
func ResolveTarifasInterval(
	dayStart time.Time,
	startMin, endMin int,
	canchaID uuid.UUID,
	tarifas []TarifaFranja,
) ([]CotizacionTramo, int64, bool, error) {
	if startMin >= endMin {
		return nil, 0, false, ErrTarifaNoConfigurada
	}

	specific := make([]TarifaFranja, 0, len(tarifas))
	general := make([]TarifaFranja, 0, len(tarifas))
	pointsSet := map[int]struct{}{
		startMin: {},
		endMin:   {},
	}

	for _, t := range tarifas {
		if int(t.MinutoFin) <= startMin || int(t.MinutoInicio) >= endMin {
			continue
		}
		if t.CanchaID != nil {
			if *t.CanchaID != canchaID {
				continue
			}
			specific = append(specific, t)
		} else {
			general = append(general, t)
		}

		if int(t.MinutoInicio) > startMin && int(t.MinutoInicio) < endMin {
			pointsSet[int(t.MinutoInicio)] = struct{}{}
		}
		if int(t.MinutoFin) > startMin && int(t.MinutoFin) < endMin {
			pointsSet[int(t.MinutoFin)] = struct{}{}
		}
	}

	points := make([]int, 0, len(pointsSet))
	for p := range pointsSet {
		points = append(points, p)
	}
	sort.Ints(points)

	type rawSegment struct {
		start  int
		end    int
		tarifa TarifaFranja
	}
	merged := make([]rawSegment, 0, len(points)-1)

	for i := 0; i < len(points)-1; i++ {
		segStart := points[i]
		segEnd := points[i+1]

		matched, ok := findMatchingTarifa(specific, segStart, segEnd)
		if !ok {
			matched, ok = findMatchingTarifa(general, segStart, segEnd)
		}
		if !ok {
			return nil, 0, false, ErrTarifaNoConfigurada
		}

		if len(merged) > 0 && merged[len(merged)-1].tarifa.ID == matched.ID && merged[len(merged)-1].end == segStart {
			merged[len(merged)-1].end = segEnd
		} else {
			merged = append(merged, rawSegment{
				start:  segStart,
				end:    segEnd,
				tarifa: matched,
			})
		}
	}

	desglose := make([]CotizacionTramo, 0, len(merged))
	var precioBaseCents int64
	incluyeHoraMuerta := false

	for _, seg := range merged {
		durMin := seg.end - seg.start
		precioHoraCents := toCents(seg.tarifa.PrecioHora)
		subtotalCents := int64(math.Round(float64(precioHoraCents) * float64(durMin) / 60.0))
		precioBaseCents += subtotalCents

		if seg.tarifa.EsHoraMuerta {
			incluyeHoraMuerta = true
		}

		desglose = append(desglose, CotizacionTramo{
			TarifaID:       seg.tarifa.ID,
			Inicio:         dayStart.Add(time.Duration(seg.start) * time.Minute),
			Fin:            dayStart.Add(time.Duration(seg.end) * time.Minute),
			HoraInicio:     FormatMinutes(seg.start),
			HoraFin:        FormatMinutes(seg.end),
			DuracionMin:    durMin,
			PrecioHora:     fromCents(precioHoraCents),
			Subtotal:       fromCents(subtotalCents),
			EsHoraMuerta:   seg.tarifa.EsHoraMuerta,
			EsTarifaCancha: seg.tarifa.CanchaID != nil,
			Etiqueta:       seg.tarifa.Etiqueta,
		})
	}

	return desglose, precioBaseCents, incluyeHoraMuerta, nil
}

// CalculatePricing ejecuta el motor tarifario puro: tramos mixtos (RN-08), validación de cupón (RN-09)
// y cálculo de adelanto PORCENTAJE o MONTO_FIJO con saldo pendiente (Decisión 1, RN-12).
func CalculatePricing(
	complejo ComplejoContext,
	canchaID uuid.UUID,
	horario *HorarioAtencion,
	tarifas []TarifaFranja,
	inicio, fin time.Time,
	cupon *CuponRecord,
	now time.Time,
) (PricingResult, error) {
	dayStart, _, startMin, endMin, err := ExtractRangeMinutesLima(inicio, fin)
	if err != nil {
		return PricingResult{}, err
	}

	if !inicio.After(now) {
		return PricingResult{}, ErrSlotEnPasado
	}

	if err := ValidateSlotWindow(horario, complejo.UnidadReservaMin, startMin, endMin); err != nil {
		return PricingResult{}, err
	}

	desglose, precioBaseCents, incluyeHoraMuerta, err := ResolveTarifasInterval(
		dayStart,
		startMin,
		endMin,
		canchaID,
		tarifas,
	)
	if err != nil {
		return PricingResult{}, err
	}

	var descuentoCents int64
	var cuponAplicado *CuponAplicado

	if cupon != nil {
		if err := ValidateCuponForPricing(*cupon, incluyeHoraMuerta, now); err != nil {
			return PricingResult{}, err
		}

		switch cupon.TipoDescuento {
		case TipoPorcentaje:
			descuentoCents = int64(math.Round(float64(precioBaseCents) * cupon.ValorDescuento / 100.0))
		case TipoMontoFijo:
			descuentoCents = toCents(cupon.ValorDescuento)
		default:
			return PricingResult{}, ErrCuponInactive
		}

		if descuentoCents > precioBaseCents {
			descuentoCents = precioBaseCents
		}
		if descuentoCents < 0 {
			descuentoCents = 0
		}

		cuponAplicado = &CuponAplicado{
			ID:                   cupon.ID,
			Codigo:               cupon.Codigo,
			Descripcion:          cupon.Descripcion,
			TipoDescuento:        cupon.TipoDescuento,
			ValorDescuento:       fromCents(toCents(cupon.ValorDescuento)),
			AcumulableHoraMuerta: cupon.AcumulableHoraMuerta,
		}
	}

	montoTotalCents := precioBaseCents - descuentoCents
	adelantoCents := calculateAdelantoCents(montoTotalCents, complejo.TipoAdelanto, complejo.ValorAdelanto)
	saldoCents := montoTotalCents - adelantoCents

	return PricingResult{
		Inicio:            dayStart.Add(time.Duration(startMin) * time.Minute),
		Fin:               dayStart.Add(time.Duration(endMin) * time.Minute),
		DuracionMin:       endMin - startMin,
		PrecioBase:        fromCents(precioBaseCents),
		MontoDescuento:    fromCents(descuentoCents),
		MontoTotal:        fromCents(montoTotalCents),
		MontoAdelanto:     fromCents(adelantoCents),
		SaldoPendiente:    fromCents(saldoCents),
		IncluyeHoraMuerta: incluyeHoraMuerta,
		CuponAplicado:     cuponAplicado,
		Desglose:          desglose,
	}, nil
}

// ValidateCuponForPricing aplica vigencia, cupo y regla RN-09 (no acumulación en hora muerta).
func ValidateCuponForPricing(cupon CuponRecord, incluyeHoraMuerta bool, now time.Time) error {
	if !cupon.Activo {
		return ErrCuponInactive
	}
	if now.Before(cupon.ValidoDesde) {
		return ErrCuponNotValidYet
	}
	if cupon.ValidoHasta != nil && now.After(*cupon.ValidoHasta) {
		return ErrCuponExpired
	}
	if cupon.MaxUsos != nil && cupon.UsosActuales >= *cupon.MaxUsos {
		return ErrCuponExhausted
	}
	if incluyeHoraMuerta && !cupon.AcumulableHoraMuerta {
		return ErrCuponNotStackableDeadHour
	}
	return nil
}

// BuildDaySlots construye los bloques de disponibilidad de un día cruzando reservas activas,
// tarifas dinámicas (priorizando cancha sobre general) y bloques pasados en America/Lima.
func BuildDaySlots(
	dayStart time.Time,
	horario *HorarioAtencion,
	unidadReservaMin int16,
	canchaID uuid.UUID,
	tarifas []TarifaFranja,
	ocupaciones []OcupacionIntervalo,
	now time.Time,
) []DisponibilidadSlot {
	if horario == nil || horario.Cerrado || (unidadReservaMin != 30 && unidadReservaMin != 60) {
		return []DisponibilidadSlot{}
	}

	apertura := int(horario.MinutoApertura)
	cierre := int(horario.MinutoCierre)
	unidad := int(unidadReservaMin)
	if apertura >= cierre || cierre-apertura < unidad {
		return []DisponibilidadSlot{}
	}

	slots := make([]DisponibilidadSlot, 0, (cierre-apertura)/unidad)
	for m := apertura; m+unidad <= cierre; m += unidad {
		slotInicio := dayStart.Add(time.Duration(m) * time.Minute)
		slotFin := dayStart.Add(time.Duration(m+unidad) * time.Minute)

		tramos, totalCents, incluyeHoraMuerta, tarifaErr := ResolveTarifasInterval(
			dayStart,
			m,
			m+unidad,
			canchaID,
			tarifas,
		)

		var precioPtr *float64
		var etiquetaPtr *string
		hasTarifa := tarifaErr == nil
		if hasTarifa {
			val := fromCents(totalCents)
			precioPtr = &val
			if len(tramos) == 1 {
				etiquetaPtr = tramos[0].Etiqueta
			}
		}

		disponible := true
		var motivo *string

		switch {
		case !slotInicio.After(now):
			disponible = false
			reason := MotivoSlotPasado
			motivo = &reason
		case intervalOverlapsAny(slotInicio, slotFin, ocupaciones):
			disponible = false
			reason := MotivoSlotOcupado
			motivo = &reason
		case !hasTarifa:
			disponible = false
			reason := MotivoSlotSinTarifa
			motivo = &reason
		}

		slots = append(slots, DisponibilidadSlot{
			Inicio:             slotInicio,
			Fin:                slotFin,
			HoraInicio:         FormatMinutes(m),
			HoraFin:            FormatMinutes(m + unidad),
			Disponible:         disponible,
			MotivoNoDisponible: motivo,
			Precio:             precioPtr,
			EsHoraMuerta:       incluyeHoraMuerta,
			EtiquetaTarifa:     etiquetaPtr,
		})
	}

	return slots
}

// FormatMinutes formatea minutos desde medianoche (0..1440) como HH:MM.
func FormatMinutes(min int) string {
	return fmt.Sprintf("%02d:%02d", min/60, min%60)
}

// ExternaPricingResult contiene los importes calculados (o sobreescritos explícitamente)
// para una reserva externa registrada por el dueño (RF-14).
type ExternaPricingResult struct {
	Inicio         time.Time
	Fin            time.Time
	DuracionMin    int
	PrecioBase     float64
	MontoDescuento float64
	MontoTotal     float64
	MontoAdelanto  float64
	SaldoPendiente float64
}

// CalculateExternaPricing valida horario/unidad y determina montos para una reserva externa.
// Si montoTotalOverride es nil, calcula la tarifa exacta con ResolveTarifasInterval;
// si montoAdelanto es nil, registra adelanto = 0.00 (todo queda como saldo pendiente).
func CalculateExternaPricing(
	complejo ComplejoContext,
	canchaID uuid.UUID,
	horario *HorarioAtencion,
	tarifas []TarifaFranja,
	inicio, fin time.Time,
	montoTotalOverride *float64,
	montoAdelanto *float64,
	now time.Time,
) (ExternaPricingResult, error) {
	dayStart, _, startMin, endMin, err := ExtractRangeMinutesLima(inicio, fin)
	if err != nil {
		return ExternaPricingResult{}, err
	}
	if !inicio.After(now) {
		return ExternaPricingResult{}, ErrSlotEnPasado
	}
	if err := ValidateSlotWindow(horario, complejo.UnidadReservaMin, startMin, endMin); err != nil {
		return ExternaPricingResult{}, err
	}

	var montoTotalCents int64
	if montoTotalOverride != nil {
		montoTotalCents = toCents(*montoTotalOverride)
	} else {
		_, precioBaseCents, _, err := ResolveTarifasInterval(dayStart, startMin, endMin, canchaID, tarifas)
		if err != nil {
			return ExternaPricingResult{}, err
		}
		montoTotalCents = precioBaseCents
	}

	var adelantoCents int64
	if montoAdelanto != nil {
		adelantoCents = toCents(*montoAdelanto)
		if adelantoCents < 0 || adelantoCents > montoTotalCents {
			return ExternaPricingResult{}, &ValidationError{
				Message: "Monto de adelanto inválido",
				Fields: map[string]string{
					"monto_adelanto": "monto_adelanto debe estar entre 0 y el monto total de la reserva",
				},
			}
		}
	}

	saldoCents := montoTotalCents - adelantoCents
	return ExternaPricingResult{
		Inicio:         dayStart.Add(time.Duration(startMin) * time.Minute),
		Fin:            dayStart.Add(time.Duration(endMin) * time.Minute),
		DuracionMin:    endMin - startMin,
		PrecioBase:     fromCents(montoTotalCents),
		MontoDescuento: 0,
		MontoTotal:     fromCents(montoTotalCents),
		MontoAdelanto:  fromCents(adelantoCents),
		SaldoPendiente: fromCents(saldoCents),
	}, nil
}

// ReprogramPricingResult agrupa los montos recalculados tras reprogramar una reserva (Decisión 4).
type ReprogramPricingResult struct {
	Inicio             time.Time
	Fin                time.Time
	DuracionMin        int
	PrecioBase         float64
	MontoDescuento     float64
	MontoTotal         float64
	MontoAdelanto      float64
	SaldoPendiente     float64
	ReembolsoPendiente bool
	ExcedenteReembolso float64
}

// CalculateReprogramPricing recalcula la tarifa para el nuevo rango/cancha conservando el adelanto
// pagado y recalculando saldo_pendiente. Si el nuevo monto_total es menor al adelanto pagado,
// fija saldo_pendiente = 0, ajusta monto_adelanto al nuevo total (respetando el check SQL
// monto_adelanto <= monto_total), activa reembolso_pendiente = true y expone ExcedenteReembolso.
func CalculateReprogramPricing(
	complejo ComplejoContext,
	targetCanchaID uuid.UUID,
	horario *HorarioAtencion,
	tarifas []TarifaFranja,
	nuevoInicio, nuevoFin time.Time,
	cupon *CuponRecord,
	adelantoPagado float64,
	reembolsoPrevio bool,
	now time.Time,
) (ReprogramPricingResult, error) {
	dayStart, _, startMin, endMin, err := ExtractRangeMinutesLima(nuevoInicio, nuevoFin)
	if err != nil {
		return ReprogramPricingResult{}, err
	}
	if !nuevoInicio.After(now) {
		return ReprogramPricingResult{}, ErrSlotEnPasado
	}
	if err := ValidateSlotWindow(horario, complejo.UnidadReservaMin, startMin, endMin); err != nil {
		return ReprogramPricingResult{}, err
	}

	_, precioBaseCents, incluyeHoraMuerta, err := ResolveTarifasInterval(
		dayStart,
		startMin,
		endMin,
		targetCanchaID,
		tarifas,
	)
	if err != nil {
		return ReprogramPricingResult{}, err
	}

	var descuentoCents int64
	if cupon != nil {
		if incluyeHoraMuerta && !cupon.AcumulableHoraMuerta {
			return ReprogramPricingResult{}, ErrCuponNotStackableDeadHour
		}
		switch cupon.TipoDescuento {
		case TipoPorcentaje:
			descuentoCents = int64(math.Round(float64(precioBaseCents) * cupon.ValorDescuento / 100.0))
		case TipoMontoFijo:
			descuentoCents = toCents(cupon.ValorDescuento)
		}
		if descuentoCents > precioBaseCents {
			descuentoCents = precioBaseCents
		}
		if descuentoCents < 0 {
			descuentoCents = 0
		}
	}

	newTotalCents := precioBaseCents - descuentoCents
	oldAdelantoCents := toCents(adelantoPagado)
	if oldAdelantoCents < 0 {
		oldAdelantoCents = 0
	}

	var (
		newAdelantoCents   int64
		newSaldoCents      int64
		excedenteCents     int64
		reembolsoPendiente = reembolsoPrevio
	)

	if newTotalCents >= oldAdelantoCents {
		newAdelantoCents = oldAdelantoCents
		newSaldoCents = newTotalCents - oldAdelantoCents
	} else {
		newAdelantoCents = newTotalCents
		newSaldoCents = 0
		reembolsoPendiente = true
		excedenteCents = oldAdelantoCents - newTotalCents
	}

	return ReprogramPricingResult{
		Inicio:             dayStart.Add(time.Duration(startMin) * time.Minute),
		Fin:                dayStart.Add(time.Duration(endMin) * time.Minute),
		DuracionMin:        endMin - startMin,
		PrecioBase:         fromCents(precioBaseCents),
		MontoDescuento:     fromCents(descuentoCents),
		MontoTotal:         fromCents(newTotalCents),
		MontoAdelanto:      fromCents(newAdelantoCents),
		SaldoPendiente:     fromCents(newSaldoCents),
		ReembolsoPendiente: reembolsoPendiente,
		ExcedenteReembolso: fromCents(excedenteCents),
	}, nil
}

// ValidateCancellationWindow valida RN-13: now <= inicio - horasMinCancelacion.
func ValidateCancellationWindow(inicio time.Time, horasMinCancelacion int16, now time.Time) error {
	deadline := inicio.Add(-time.Duration(horasMinCancelacion) * time.Hour)
	if now.After(deadline) {
		return ErrCancellationWindowExpired
	}
	return nil
}

func calculateAdelantoCents(montoTotalCents int64, tipoAdelanto string, valorAdelanto float64) int64 {
	if montoTotalCents <= 0 {
		return 0
	}

	var adelantoCents int64
	switch tipoAdelanto {
	case TipoMontoFijo:
		adelantoCents = toCents(valorAdelanto)
	default: // PORCENTAJE
		adelantoCents = int64(math.Round(float64(montoTotalCents) * valorAdelanto / 100.0))
	}

	if adelantoCents < 0 {
		return 0
	}
	if adelantoCents > montoTotalCents {
		return montoTotalCents
	}
	return adelantoCents
}

func findMatchingTarifa(list []TarifaFranja, segStart, segEnd int) (TarifaFranja, bool) {
	for _, t := range list {
		if int(t.MinutoInicio) <= segStart && int(t.MinutoFin) >= segEnd {
			return t, true
		}
	}
	return TarifaFranja{}, false
}

func intervalOverlapsAny(inicio, fin time.Time, ocupaciones []OcupacionIntervalo) bool {
	for _, oc := range ocupaciones {
		if oc.Inicio.Before(fin) && oc.Fin.After(inicio) {
			return true
		}
	}
	return false
}

func toCents(amount float64) int64 {
	return int64(math.Round(amount * 100.0))
}

func fromCents(cents int64) float64 {
	return float64(cents) / 100.0
}
