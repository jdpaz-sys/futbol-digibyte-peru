package geo

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	DefaultGeocoderBaseURL   = "https://nominatim.openstreetmap.org"
	DefaultGeocoderUserAgent = "FutbolDigibytePeru/1.0"
	GeocoderHTTPTimeout      = 4 * time.Second

	ReverseCacheTTL    = 24 * time.Hour
	PublicSlugCacheTTL = 60 * time.Second

	DefaultRadioKm     = 10.0
	MaxRadioKm         = 50.0
	DefaultPublicLimit = 20
	MaxPublicLimit     = 50

	ModoBusquedaGPS  = "GPS"
	ModoBusquedaZona = "ZONA"

	EstadoSuscripcionPrueba     = "PRUEBA"
	EstadoSuscripcionActiva     = "ACTIVA"
	EstadoSuscripcionVencida    = "VENCIDA"
	EstadoSuscripcionSuspendida = "SUSPENDIDA"

	MotivoComplejoNoDisponible = "Este complejo deportivo no recibe nuevas reservas temporalmente. Las reservas ya confirmadas mantienen su vigencia."
)

var (
	ErrComplejoNotFound    = errors.New("complejo deportivo no encontrado")
	ErrGeocoderTimeout     = errors.New("tiempo de espera agotado al consultar el proveedor de geocodificación")
	ErrGeocoderUnavailable = errors.New("proveedor de geocodificación temporalmente no disponible")
)

// ValidationError representa errores de validación de parámetros con detalle por campo.
type ValidationError struct {
	Message string
	Fields  map[string]string
}

func (e *ValidationError) Error() string {
	return e.Message
}

// ReverseGeocodeResult contiene la dirección sugerida y demarcación política para el dueño (RF-04, RN-16).
type ReverseGeocodeResult struct {
	Lat              float64 `json:"lat"`
	Lng              float64 `json:"lng"`
	DireccionEscrita string  `json:"direccion_escrita"`
	Departamento     string  `json:"departamento"`
	Provincia        string  `json:"provincia"`
	Distrito         string  `json:"distrito"`
	OrigenCache      bool    `json:"origen_cache"`
}

// ExploreComplejosFilter agrupa los parámetros de entrada públicos para GET /api/v1/public/complejos.
// Por privacidad estricta (RN-19 / RNF-04), Lat y Lng existen únicamente en memoria durante la petición.
type ExploreComplejosFilter struct {
	Lat          *float64
	Lng          *float64
	RadioKm      *float64
	Departamento string
	Provincia    string
	Distrito     string
	Query        string
	Modalidad    string
	Limit        int
	Offset       int
}

// ExploreComplejosQuery contiene los parámetros validados y normalizados hacia PostgreSQL/PostGIS.
type ExploreComplejosQuery struct {
	UseGPS       bool
	Lat          float64
	Lng          float64
	RadioMetros  float64
	Departamento string
	Provincia    string
	Distrito     string
	QueryLike    string
	Modalidad    string
	Limit        int
	Offset       int
}

// PublicComplejoItem resume un complejo afiliado vigente en el catálogo público (RF-09, CU-10).
type PublicComplejoItem struct {
	ID               uuid.UUID `json:"id"`
	Slug             string    `json:"slug"`
	Nombre           string    `json:"nombre"`
	DireccionEscrita string    `json:"direccion_escrita"`
	Referencias      *string   `json:"referencias"`
	Distrito         string    `json:"distrito"`
	Provincia        string    `json:"provincia"`
	Departamento     string    `json:"departamento"`
	Lat              float64   `json:"lat"`
	Lng              float64   `json:"lng"`
	DistanciaMetros  *float64  `json:"distancia_metros"`
	FotoPortadaURL   *string   `json:"foto_portada_url"`
	TarifaDesde      *float64  `json:"tarifa_desde"`
	Modalidades      []string  `json:"modalidades"`
	Servicios        []string  `json:"servicios"`
}

// PublicComplejosListResponse envuelve el listado público sin exponer ni persistir ubicación del jugador.
type PublicComplejosListResponse struct {
	Items        []PublicComplejoItem `json:"items"`
	Total        int                  `json:"total"`
	Limit        int                  `json:"limit"`
	Offset       int                  `json:"offset"`
	ModoBusqueda string               `json:"modo_busqueda"`
	RadioKm      *float64             `json:"radio_km,omitempty"`
}

// ComplejoBaseRecord representa la fila base leída internamente para evaluar vigencia RN-02
// sin exponer campos administrativos internos (dueno_id, estado_suscripcion, suscripcion_vence_at) en JSON.
type ComplejoBaseRecord struct {
	ID                  uuid.UUID
	Slug                string
	Nombre              string
	Descripcion         *string
	TelefonoContacto    *string
	Servicios           []string
	Departamento        string
	Provincia           string
	Distrito            string
	Lat                 *float64
	Lng                 *float64
	DireccionEscrita    *string
	Referencias         *string
	Publicado           bool
	UnidadReservaMin    int16
	HoldDuracionMin     int16
	TipoAdelanto        string
	ValorAdelanto       float64
	HorasMinCancelacion int16
	TerminosReserva     *string
	EstadoSuscripcion   string
	SuscripcionVenceAt  *time.Time
}

// PublicFoto expone una imagen de la galería pública del complejo (sin storage_key interno).
type PublicFoto struct {
	ID          uuid.UUID `json:"id"`
	URL         string    `json:"url"`
	Descripcion *string   `json:"descripcion"`
	Orden       int16     `json:"orden"`
	EsPortada   bool      `json:"es_portada"`
}

// PublicCancha expone una cancha activa del complejo (sin foto_storage_key interno).
type PublicCancha struct {
	ID          uuid.UUID `json:"id"`
	Nombre      string    `json:"nombre"`
	Modalidad   string    `json:"modalidad"`
	Superficie  string    `json:"superficie"`
	Techada     bool      `json:"techada"`
	Iluminacion bool      `json:"iluminacion"`
	FotoURL     *string   `json:"foto_url"`
	Orden       int16     `json:"orden"`
}

// PublicHorarioAtencion expone el horario de atención semanal por día (0=Domingo..6=Sábado).
type PublicHorarioAtencion struct {
	DiaSemana      int16 `json:"dia_semana"`
	MinutoApertura int16 `json:"minuto_apertura"`
	MinutoCierre   int16 `json:"minuto_cierre"`
	Cerrado        bool  `json:"cerrado"`
}

// PublicTarifaFranja expone una tarifa activa general o específica de cancha activa.
type PublicTarifaFranja struct {
	ID           uuid.UUID  `json:"id"`
	CanchaID     *uuid.UUID `json:"cancha_id"`
	DiaSemana    int16      `json:"dia_semana"`
	MinutoInicio int16      `json:"minuto_inicio"`
	MinutoFin    int16      `json:"minuto_fin"`
	PrecioHora   float64    `json:"precio_hora"`
	Etiqueta     *string    `json:"etiqueta"`
	EsHoraMuerta bool       `json:"es_hora_muerta"`
}

// PublicReglasReserva agrupa las condiciones operativas y legales configuradas por el dueño (RF-06).
type PublicReglasReserva struct {
	UnidadReservaMin    int16   `json:"unidad_reserva_min"`
	HoldDuracionMin     int16   `json:"hold_duracion_min"`
	TipoAdelanto        string  `json:"tipo_adelanto"`
	ValorAdelanto       float64 `json:"valor_adelanto"`
	HorasMinCancelacion int16   `json:"horas_min_cancelacion"`
	TerminosReserva     string  `json:"terminos_reserva"`
}

// PublicComplejoFicha representa la ficha pública de un complejo por slug (RN-02, RN-10).
// Antes del checkout expone únicamente tipos_metodos_pago (jamás credenciales ni cuentas).
type PublicComplejoFicha struct {
	ID               uuid.UUID               `json:"id"`
	Slug             string                  `json:"slug"`
	Nombre           string                  `json:"nombre"`
	AceptaReservas   bool                    `json:"acepta_reservas"`
	Motivo           *string                 `json:"motivo,omitempty"`
	Descripcion      *string                 `json:"descripcion,omitempty"`
	Departamento     string                  `json:"departamento"`
	Provincia        string                  `json:"provincia"`
	Distrito         string                  `json:"distrito"`
	DireccionEscrita *string                 `json:"direccion_escrita,omitempty"`
	Referencias      *string                 `json:"referencias,omitempty"`
	TelefonoContacto *string                 `json:"telefono_contacto,omitempty"`
	Lat              *float64                `json:"lat,omitempty"`
	Lng              *float64                `json:"lng,omitempty"`
	Servicios        []string                `json:"servicios"`
	Fotos            []PublicFoto            `json:"fotos"`
	Canchas          []PublicCancha          `json:"canchas"`
	Horarios         []PublicHorarioAtencion `json:"horarios"`
	Tarifas          []PublicTarifaFranja    `json:"tarifas"`
	ReglasReserva    *PublicReglasReserva    `json:"reglas_reserva,omitempty"`
	TiposMetodosPago []string                `json:"tipos_metodos_pago"`
}
