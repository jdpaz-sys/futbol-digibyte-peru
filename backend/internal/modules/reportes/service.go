package reportes

import (
	"context"
	"fmt"
	"math"
	"strings"
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

// Service calcula reportes operativos y financieros del complejo en America/Lima (RF-16).
type Service struct {
	repo Store
	now  func() time.Time
}

// NewService crea el servicio de reportes operativos y financieros.
func NewService(repo Store) *Service {
	return &Service{
		repo: repo,
		now:  time.Now,
	}
}

// GetResumen devuelve finanzas, conteos por estado/origen y ocupación por cancha del rango solicitado.
// Si no se envían fechas, aplica por defecto los últimos 30 días en zona America/Lima.
func (s *Service) GetResumen(ctx context.Context, complejoID uuid.UUID, rawDesde, rawHasta string) (ResumenResponse, error) {
	dr, err := s.resolveDateRange(rawDesde, rawHasta)
	if err != nil {
		return ResumenResponse{}, err
	}

	agg, err := s.repo.GetPeriodoAggregates(ctx, complejoID, dr.Desde, dr.RangeEnd)
	if err != nil {
		return ResumenResponse{}, err
	}

	finanzas := FinanzasResumen{
		IngresosConfirmadosTotal:  round2(agg.Finanzas.IngresosConfirmadosTotal),
		AdelantosCobrados:         round2(agg.Finanzas.AdelantosCobrados),
		SaldosCobrados:            round2(agg.Finanzas.SaldosCobrados),
		SaldosPendientesPorCobrar: round2(agg.Finanzas.SaldosPendientesPorCobrar),
		ReembolsosPendientesCount: agg.Finanzas.ReembolsosPendientesCount,
	}

	porEstado := make(map[string]int, len(allEstados))
	totalEstado := 0
	for _, e := range allEstados {
		cnt := agg.PorEstado[e]
		porEstado[e] = cnt
		totalEstado += cnt
	}

	porOrigen := make(map[string]int, len(allOrigenes))
	for _, o := range allOrigenes {
		porOrigen[o] = agg.PorOrigen[o]
	}

	minutosDisponibles := CalculateAvailableMinutes(dr.Desde, dr.Hasta, agg.Horarios)
	horasDisponibles := round2(float64(minutosDisponibles) / 60.0)

	ocupacion := make([]OcupacionCancha, 0, len(agg.Canchas))
	for _, c := range agg.Canchas {
		tasa := 0.0
		if minutosDisponibles > 0 {
			tasa = round2((c.MinutosOcupados / float64(minutosDisponibles)) * 100.0)
		}
		ocupacion = append(ocupacion, OcupacionCancha{
			CanchaID:                c.CanchaID,
			CanchaNombre:            c.CanchaNombre,
			Modalidad:               c.Modalidad,
			Activa:                  c.Activa,
			ReservasEfectivas:       c.ReservasEfectivas,
			HorasDisponibles:        horasDisponibles,
			HorasOcupadas:           round2(c.MinutosOcupados / 60.0),
			HorasBloqueadas:         round2(c.MinutosBloqueados / 60.0),
			TasaOcupacionPorcentaje: tasa,
		})
	}

	return ResumenResponse{
		ComplejoID:  complejoID,
		Desde:       dr.Desde.Format("2006-01-02"),
		Hasta:       dr.Hasta.Format("2006-01-02"),
		ZonaHoraria: ZonaHorariaLima,
		Finanzas:    finanzas,
		Conteos: ConteosResumen{
			Total:     totalEstado,
			PorEstado: porEstado,
			PorOrigen: porOrigen,
		},
		OcupacionPorCancha: ocupacion,
	}, nil
}

// CalculateAvailableMinutes suma los minutos de atención configurados en horarios_atencion
// para cada día calendario comprendido entre desde y hasta (inclusive) en America/Lima.
func CalculateAvailableMinutes(desde, hasta time.Time, horarios []HorarioDia) int {
	if desde.After(hasta) {
		return 0
	}

	byDay := make(map[int16]HorarioDia, len(horarios))
	for _, h := range horarios {
		byDay[h.DiaSemana] = h
	}

	totalMin := 0
	for d := desde; !d.After(hasta); d = d.AddDate(0, 0, 1) {
		h, ok := byDay[int16(d.Weekday())]
		if !ok || h.Cerrado || h.MinutoCierre <= h.MinutoApertura {
			continue
		}
		totalMin += int(h.MinutoCierre - h.MinutoApertura)
	}
	return totalMin
}

func (s *Service) resolveDateRange(rawDesde, rawHasta string) (DateRange, error) {
	nowLima := s.now().In(limaLocation)
	today := time.Date(nowLima.Year(), nowLima.Month(), nowLima.Day(), 0, 0, 0, 0, limaLocation)

	desdeStr := strings.TrimSpace(rawDesde)
	hastaStr := strings.TrimSpace(rawHasta)
	fields := make(map[string]string)

	var desde, hasta time.Time
	hasDesde := desdeStr != ""
	hasHasta := hastaStr != ""

	if hasDesde {
		d, err := parseFechaLima(desdeStr)
		if err != nil {
			fields["desde"] = "desde debe tener formato válido YYYY-MM-DD"
		} else {
			desde = d
		}
	}
	if hasHasta {
		h, err := parseFechaLima(hastaStr)
		if err != nil {
			fields["hasta"] = "hasta debe tener formato válido YYYY-MM-DD"
		} else {
			hasta = h
		}
	}

	if len(fields) > 0 {
		return DateRange{}, &ValidationError{
			Message: "Parámetros de rango de fechas inválidos",
			Fields:  fields,
		}
	}

	switch {
	case !hasDesde && !hasHasta:
		hasta = today
		desde = today.AddDate(0, 0, -(DefaultRangeDays - 1))
	case hasDesde && !hasHasta:
		if desde.After(today) {
			hasta = desde
		} else {
			hasta = today
		}
	case !hasDesde && hasHasta:
		desde = hasta.AddDate(0, 0, -(DefaultRangeDays - 1))
	}

	if desde.After(hasta) {
		return DateRange{}, &ValidationError{
			Message: "Parámetros de rango de fechas inválidos",
			Fields:  map[string]string{"hasta": "hasta debe ser mayor o igual a desde"},
		}
	}

	days := 0
	for d := desde; !d.After(hasta); d = d.AddDate(0, 0, 1) {
		days++
		if days > MaxRangeDays {
			return DateRange{}, &ValidationError{
				Message: "El rango máximo permitido es de 366 días",
				Fields:  map[string]string{"hasta": "El periodo consultado no puede exceder 366 días"},
			}
		}
	}

	return DateRange{
		Desde:    desde,
		Hasta:    hasta,
		RangeEnd: hasta.AddDate(0, 0, 1),
	}, nil
}

func parseFechaLima(raw string) (time.Time, error) {
	t, err := time.ParseInLocation("2006-01-02", raw, limaLocation)
	if err != nil {
		return time.Time{}, err
	}
	if t.Format("2006-01-02") != raw {
		return time.Time{}, fmt.Errorf("fecha inválida")
	}
	return t, nil
}

func round2(v float64) float64 {
	return math.Round(v*100.0) / 100.0
}
