package reportes

import (
	"time"

	"github.com/google/uuid"
)

const (
	ZonaHorariaLima    = "America/Lima"
	DefaultRangeDays   = 30
	MaxRangeDays       = 366
	OrigenApp          = "APP"
	OrigenExterna      = "EXTERNA"
	OrigenBloqueo      = "BLOQUEO_MANTENIMIENTO"
	EstadoPendiente    = "PENDIENTE_PAGO"
	EstadoPorValidar   = "POR_VALIDAR"
	EstadoConfirmada   = "CONFIRMADA"
	EstadoCompletada   = "COMPLETADA"
	EstadoExpirada     = "EXPIRADA"
	EstadoRechazada    = "RECHAZADA"
	EstadoCancelada    = "CANCELADA"
	EstadoNoShow       = "NO_SHOW"
)

var (
	allEstados = []string{
		EstadoPendiente,
		EstadoPorValidar,
		EstadoConfirmada,
		EstadoCompletada,
		EstadoExpirada,
		EstadoRechazada,
		EstadoCancelada,
		EstadoNoShow,
	}
	allOrigenes = []string{
		OrigenApp,
		OrigenExterna,
		OrigenBloqueo,
	}
)

// ValidationError representa errores de validación de entrada con detalle por campo.
type ValidationError struct {
	Message string
	Fields  map[string]string
}

func (e *ValidationError) Error() string {
	return e.Message
}

// FinanzasResumen consolida los ingresos, adelantos, saldos y reembolsos pendientes del periodo.
type FinanzasResumen struct {
	IngresosConfirmadosTotal  float64 `json:"ingresos_confirmados_total"`
	AdelantosCobrados         float64 `json:"adelantos_cobrados"`
	SaldosCobrados            float64 `json:"saldos_cobrados"`
	SaldosPendientesPorCobrar float64 `json:"saldos_pendientes_por_cobrar"`
	ReembolsosPendientesCount int     `json:"reembolsos_pendientes_count"`
}

// ConteosResumen desglosa el volumen de reservas del periodo por estado y origen.
type ConteosResumen struct {
	Total     int            `json:"total"`
	PorEstado map[string]int `json:"por_estado"`
	PorOrigen map[string]int `json:"por_origen"`
}

// OcupacionCancha reporta las horas disponibles, ocupadas (excluyendo bloqueos), bloqueadas y tasa de ocupación.
type OcupacionCancha struct {
	CanchaID                uuid.UUID `json:"cancha_id"`
	CanchaNombre            string    `json:"cancha_nombre"`
	Modalidad               string    `json:"modalidad"`
	Activa                  bool      `json:"activa"`
	ReservasEfectivas       int       `json:"reservas_efectivas"`
	HorasDisponibles        float64   `json:"horas_disponibles"`
	HorasOcupadas           float64   `json:"horas_ocupadas"`
	HorasBloqueadas         float64   `json:"horas_bloqueadas"`
	TasaOcupacionPorcentaje float64   `json:"tasa_ocupacion_porcentaje"`
}

// ResumenResponse es el contrato JSON de GET /api/v1/dueno/reportes/resumen (RF-16, CU-09).
type ResumenResponse struct {
	ComplejoID         uuid.UUID         `json:"complejo_id"`
	Desde              string            `json:"desde"`
	Hasta              string            `json:"hasta"`
	ZonaHoraria        string            `json:"zona_horaria"`
	Finanzas           FinanzasResumen   `json:"finanzas"`
	Conteos            ConteosResumen    `json:"conteos"`
	OcupacionPorCancha []OcupacionCancha `json:"ocupacion_por_cancha"`
}

// ---------- Modelos internos de lectura desde BD ----------

// HorarioDia representa la configuración semanal del complejo para un día (0=Domingo..6=Sábado).
type HorarioDia struct {
	DiaSemana      int16
	MinutoApertura int16
	MinutoCierre   int16
	Cerrado        bool
}

// CanchaOcupacionRaw agrupa los minutos ocupados/bloqueados agregados desde PostgreSQL para una cancha.
type CanchaOcupacionRaw struct {
	CanchaID          uuid.UUID
	CanchaNombre      string
	Modalidad         string
	Activa            bool
	ReservasEfectivas int
	MinutosOcupados   float64
	MinutosBloqueados float64
}

// PeriodoAggregates encapsula todas las agregaciones SQL crudas del rango solicitado.
type PeriodoAggregates struct {
	Finanzas  FinanzasResumen
	PorEstado map[string]int
	PorOrigen map[string]int
	Horarios  []HorarioDia
	Canchas   []CanchaOcupacionRaw
}

// DateRange representa el intervalo semiabierto [Start, End) en America/Lima.
type DateRange struct {
	Desde    time.Time
	Hasta    time.Time
	RangeEnd time.Time
}
