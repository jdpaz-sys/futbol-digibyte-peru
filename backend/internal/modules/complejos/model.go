package complejos

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	TipoAdelantoPorcentaje = "PORCENTAJE"
	TipoAdelantoMontoFijo  = "MONTO_FIJO"

	TipoDescuentoPorcentaje = "PORCENTAJE"
	TipoDescuentoMontoFijo  = "MONTO_FIJO"

	Modalidad5v5   = "5v5"
	Modalidad6v6   = "6v6"
	Modalidad7v7   = "7v7"
	Modalidad8v8   = "8v8"
	Modalidad9v9   = "9v9"
	Modalidad11v11 = "11v11"

	SuperficieSintetico    = "SINTETICO"
	SuperficieGrassNatural = "GRASS_NATURAL"
	SuperficieLoza         = "LOZA"
	SuperficieParquet      = "PARQUET"

	MetodoPagoYape          = "YAPE"
	MetodoPagoPlin          = "PLIN"
	MetodoPagoTransferencia = "TRANSFERENCIA"
	MetodoPagoCulqi         = "CULQI"
	MetodoPagoMercadoPago   = "MERCADOPAGO"

	ReqUbicacionRequerida        = "UBICACION_REQUERIDA"
	ReqDireccionRequerida        = "DIRECCION_REQUERIDA"
	ReqTerminosRequeridos        = "TERMINOS_RESERVA_REQUERIDOS"
	ReqAdelantoInvalido          = "ADELANTO_INVALIDO"
	ReqCanchaActivaRequerida     = "CANCHA_ACTIVA_REQUERIDA"
	ReqHorarioAbiertoRequerido   = "HORARIO_ABIERTO_REQUERIDO"
	ReqTarifaActivaRequerida     = "TARIFA_ACTIVA_REQUERIDA"
	ReqMetodoPagoActivoRequerido = "METODO_PAGO_ACTIVO_REQUERIDO"
)

var (
	ErrComplejoNotFound       = errors.New("complejo deportivo no encontrado")
	ErrCanchaNotFound         = errors.New("cancha no encontrada")
	ErrFotoNotFound           = errors.New("foto del complejo no encontrada")
	ErrTarifaNotFound         = errors.New("tarifa por franja no encontrada")
	ErrCuponNotFound          = errors.New("cupón no encontrado")
	ErrMetodoPagoNotFound     = errors.New("método de pago no encontrado")
	ErrMetodoPagoNoQR         = errors.New("solo los métodos de pago YAPE o PLIN admiten código QR")
	ErrFileTooLarge           = errors.New("la imagen excede el límite máximo de 5 MB")
	ErrInvalidFileType        = errors.New("el archivo debe ser una imagen válida en formato JPG, PNG o WEBP")
	ErrMediaNotFound          = errors.New("archivo multimedia no encontrado")
	ErrMediaForbidden         = errors.New("acceso denegado a esta ruta multimedia")
	ErrTarifaOverlap          = errors.New("la franja horaria se solapa con otra tarifa activa")
	ErrCuponCodeAlreadyExists = errors.New("ya existe un cupón con ese código en este complejo")
	ErrConflict               = errors.New("conflicto de unicidad en el recurso")
	ErrCheckViolation         = errors.New("los datos enviados incumplen una restricción de integridad")
)

// ValidationError representa errores de validación de entrada con detalle por campo.
type ValidationError struct {
	Message string
	Fields  map[string]string
}

func (e *ValidationError) Error() string {
	return e.Message
}

// RequisitoPublicacion describe un requisito pendiente de RN-03 para publicar el complejo.
type RequisitoPublicacion struct {
	Codigo  string `json:"codigo"`
	Mensaje string `json:"mensaje"`
}

// PublishRequirementsError indica que el complejo no cumple RN-03 para pasar a publicado = true (HTTP 422).
type PublishRequirementsError struct {
	Message             string
	RequisitosFaltantes []RequisitoPublicacion
}

func (e *PublishRequirementsError) Error() string {
	return e.Message
}

// OptionalUUID distingue entre un campo ausente en JSON, un valor explícito null y un UUID válido.
type OptionalUUID struct {
	Set   bool
	Value *uuid.UUID
}

// UnmarshalJSON implementa la deserialización nullable/opcional para UUIDs en operaciones PATCH.
func (o *OptionalUUID) UnmarshalJSON(data []byte) error {
	o.Set = true
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		o.Value = nil
		return nil
	}

	var raw string
	if err := json.Unmarshal(trimmed, &raw); err != nil {
		return fmt.Errorf("debe ser una cadena UUID válida o null")
	}

	raw = strings.TrimSpace(raw)
	if raw == "" {
		o.Value = nil
		return nil
	}

	parsed, err := uuid.Parse(raw)
	if err != nil {
		return fmt.Errorf("UUID inválido")
	}
	o.Value = &parsed
	return nil
}

// ChecklistCounts agrupa los conteos operativos usados para evaluar RN-03.
type ChecklistCounts struct {
	CanchasActivas     int
	HorariosAbiertos   int
	TarifasActivas     int
	MetodosPagoActivos int
}

// ChecklistPublicacion resume el cumplimiento de RN-03 para el panel del dueño.
type ChecklistPublicacion struct {
	ListoParaPublicar   bool                   `json:"listo_para_publicar"`
	CanchasActivas      int                    `json:"canchas_activas"`
	HorariosAbiertos    int                    `json:"horarios_abiertos"`
	TarifasActivas      int                    `json:"tarifas_activas"`
	MetodosPagoActivos  int                    `json:"metodos_pago_activos"`
	RequisitosFaltantes []RequisitoPublicacion `json:"requisitos_faltantes"`
}

// ComplejoPerfil representa el perfil completo del complejo del dueño con coordenadas y checklist RN-03.
type ComplejoPerfil struct {
	ID                   uuid.UUID            `json:"id"`
	DuenoID              uuid.UUID            `json:"dueno_id"`
	PlanActualID         *uuid.UUID           `json:"plan_actual_id"`
	Slug                 string               `json:"slug"`
	Nombre               string               `json:"nombre"`
	Descripcion          *string              `json:"descripcion"`
	TelefonoContacto     *string              `json:"telefono_contacto"`
	Servicios            []string             `json:"servicios"`
	Departamento         string               `json:"departamento"`
	Provincia            string               `json:"provincia"`
	Distrito             string               `json:"distrito"`
	Lat                  *float64             `json:"lat"`
	Lng                  *float64             `json:"lng"`
	DireccionEscrita     *string              `json:"direccion_escrita"`
	Referencias          *string              `json:"referencias"`
	Publicado            bool                 `json:"publicado"`
	UnidadReservaMin     int16                `json:"unidad_reserva_min"`
	HoldDuracionMin      int16                `json:"hold_duracion_min"`
	TipoAdelanto         string               `json:"tipo_adelanto"`
	ValorAdelanto        float64              `json:"valor_adelanto"`
	HorasMinCancelacion  int16                `json:"horas_min_cancelacion"`
	TerminosReserva      *string              `json:"terminos_reserva"`
	EstadoSuscripcion    string               `json:"estado_suscripcion"`
	SuscripcionVenceAt   *time.Time           `json:"suscripcion_vence_at"`
	ChecklistPublicacion ChecklistPublicacion `json:"checklist_publicacion"`
	CreatedAt            time.Time            `json:"created_at"`
	UpdatedAt            time.Time            `json:"updated_at"`
}

// UpdateComplejoRequest define los campos editables por el dueño en PATCH /api/v1/dueno/complejo.
type UpdateComplejoRequest struct {
	Nombre              *string   `json:"nombre"`
	Descripcion         *string   `json:"descripcion"`
	TelefonoContacto    *string   `json:"telefono_contacto"`
	Telefono            *string   `json:"telefono"`
	Servicios           *[]string `json:"servicios"`
	Departamento        *string   `json:"departamento"`
	Provincia           *string   `json:"provincia"`
	Distrito            *string   `json:"distrito"`
	Lat                 *float64  `json:"lat"`
	Lng                 *float64  `json:"lng"`
	DireccionEscrita    *string   `json:"direccion_escrita"`
	Referencias         *string   `json:"referencias"`
	UnidadReservaMin    *int      `json:"unidad_reserva_min"`
	HoldDuracionMin     *int      `json:"hold_duracion_min"`
	TipoAdelanto        *string   `json:"tipo_adelanto"`
	ValorAdelanto       *float64  `json:"valor_adelanto"`
	HorasMinCancelacion *int      `json:"horas_min_cancelacion"`
	TerminosReserva     *string   `json:"terminos_reserva"`
}

// UpdateComplejoParams contiene los valores validados listos para persistir en complejos.
type UpdateComplejoParams struct {
	ComplejoID          uuid.UUID
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
	UnidadReservaMin    int16
	HoldDuracionMin     int16
	TipoAdelanto        string
	ValorAdelanto       float64
	HorasMinCancelacion int16
	TerminosReserva     *string
}

// Cancha representa una unidad reservable en la tabla canchas.
type Cancha struct {
	ID             uuid.UUID `json:"id"`
	ComplejoID     uuid.UUID `json:"complejo_id"`
	Nombre         string    `json:"nombre"`
	Modalidad      string    `json:"modalidad"`
	Superficie     string    `json:"superficie"`
	Techada        bool      `json:"techada"`
	Iluminacion    bool      `json:"iluminacion"`
	FotoURL        *string   `json:"foto_url"`
	FotoStorageKey *string   `json:"foto_storage_key"`
	Orden          int16     `json:"orden"`
	Activa         bool      `json:"activa"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// CreateCanchaRequest define el payload para crear una cancha del complejo.
type CreateCanchaRequest struct {
	Nombre         string  `json:"nombre"`
	Modalidad      string  `json:"modalidad"`
	Superficie     string  `json:"superficie"`
	Techada        *bool   `json:"techada"`
	Iluminacion    *bool   `json:"iluminacion"`
	FotoURL        *string `json:"foto_url"`
	FotoStorageKey *string `json:"foto_storage_key"`
	Orden          *int    `json:"orden"`
	Activa         *bool   `json:"activa"`
}

// UpdateCanchaRequest define los campos modificables de una cancha existente.
type UpdateCanchaRequest struct {
	Nombre         *string `json:"nombre"`
	Modalidad      *string `json:"modalidad"`
	Superficie     *string `json:"superficie"`
	Techada        *bool   `json:"techada"`
	Iluminacion    *bool   `json:"iluminacion"`
	FotoURL        *string `json:"foto_url"`
	FotoStorageKey *string `json:"foto_storage_key"`
	Orden          *int    `json:"orden"`
	Activa         *bool   `json:"activa"`
}

// HorarioAtencion representa la franja de apertura/cierre de un día de la semana (0=Domingo..6=Sábado).
type HorarioAtencion struct {
	ID             uuid.UUID `json:"id"`
	ComplejoID     uuid.UUID `json:"complejo_id"`
	DiaSemana      int16     `json:"dia_semana"`
	MinutoApertura int16     `json:"minuto_apertura"`
	MinutoCierre   int16     `json:"minuto_cierre"`
	Cerrado        bool      `json:"cerrado"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// HorarioDiaInput define la entrada de un día en PUT /api/v1/dueno/horarios.
type HorarioDiaInput struct {
	DiaSemana      int  `json:"dia_semana"`
	MinutoApertura int  `json:"minuto_apertura"`
	MinutoCierre   int  `json:"minuto_cierre"`
	Cerrado        bool `json:"cerrado"`
}

// UpsertHorariosRequest recibe los 7 días de la semana para reemplazo atómico.
type UpsertHorariosRequest struct {
	Horarios []HorarioDiaInput `json:"horarios"`
}

// TarifaFranja representa una tarifa dinámica por día/franja general o específica de cancha.
type TarifaFranja struct {
	ID           uuid.UUID  `json:"id"`
	ComplejoID   uuid.UUID  `json:"complejo_id"`
	CanchaID     *uuid.UUID `json:"cancha_id"`
	DiaSemana    int16      `json:"dia_semana"`
	MinutoInicio int16      `json:"minuto_inicio"`
	MinutoFin    int16      `json:"minuto_fin"`
	PrecioHora   float64    `json:"precio_hora"`
	Etiqueta     *string    `json:"etiqueta"`
	EsHoraMuerta bool       `json:"es_hora_muerta"`
	Activa       bool       `json:"activa"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// TarifasFilter agrupa filtros opcionales para GET /api/v1/dueno/tarifas.
type TarifasFilter struct {
	CanchaID  *uuid.UUID
	DiaSemana *int16
	Activa    *bool
}

// CreateTarifaRequest define el payload para crear una tarifa por franja horaria.
type CreateTarifaRequest struct {
	CanchaID     *string  `json:"cancha_id"`
	DiaSemana    *int     `json:"dia_semana"`
	MinutoInicio *int     `json:"minuto_inicio"`
	MinutoFin    *int     `json:"minuto_fin"`
	PrecioHora   *float64 `json:"precio_hora"`
	Etiqueta     *string  `json:"etiqueta"`
	EsHoraMuerta *bool    `json:"es_hora_muerta"`
	Activa       *bool    `json:"activa"`
}

// UpdateTarifaRequest define los campos editables de una tarifa por franja.
type UpdateTarifaRequest struct {
	CanchaID     OptionalUUID `json:"cancha_id"`
	DiaSemana    *int         `json:"dia_semana"`
	MinutoInicio *int         `json:"minuto_inicio"`
	MinutoFin    *int         `json:"minuto_fin"`
	PrecioHora   *float64     `json:"precio_hora"`
	Etiqueta     *string      `json:"etiqueta"`
	EsHoraMuerta *bool        `json:"es_hora_muerta"`
	Activa       *bool        `json:"activa"`
}

// DeleteResult confirma la eliminación de un recurso por identificador.
type DeleteResult struct {
	ID      uuid.UUID `json:"id"`
	Deleted bool      `json:"deleted"`
}

// Cupon representa un cupón promocional aislado al complejo (RF-08, RN-09).
type Cupon struct {
	ID                   uuid.UUID  `json:"id"`
	ComplejoID           uuid.UUID  `json:"complejo_id"`
	Codigo               string     `json:"codigo"`
	Descripcion          *string    `json:"descripcion"`
	TipoDescuento        string     `json:"tipo_descuento"`
	ValorDescuento       float64    `json:"valor_descuento"`
	ValidoDesde          time.Time  `json:"valido_desde"`
	ValidoHasta          *time.Time `json:"valido_hasta"`
	MaxUsos              *int       `json:"max_usos"`
	UsosActuales         int        `json:"usos_actuales"`
	AcumulableHoraMuerta bool       `json:"acumulable_hora_muerta"`
	Activo               bool       `json:"activo"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

// CreateCuponRequest define el payload para registrar un cupón del complejo.
type CreateCuponRequest struct {
	Codigo               string     `json:"codigo"`
	Descripcion          *string    `json:"descripcion"`
	TipoDescuento        string     `json:"tipo_descuento"`
	ValorDescuento       *float64   `json:"valor_descuento"`
	ValidoDesde          *time.Time `json:"valido_desde"`
	ValidoHasta          *time.Time `json:"valido_hasta"`
	MaxUsos              *int       `json:"max_usos"`
	AcumulableHoraMuerta *bool      `json:"acumulable_hora_muerta"`
	Activo               *bool      `json:"activo"`
}

// UpdateCuponRequest define los campos editables de un cupón existente.
type UpdateCuponRequest struct {
	Codigo               *string    `json:"codigo"`
	Descripcion          *string    `json:"descripcion"`
	TipoDescuento        *string    `json:"tipo_descuento"`
	ValorDescuento       *float64   `json:"valor_descuento"`
	ValidoDesde          *time.Time `json:"valido_desde"`
	ValidoHasta          *time.Time `json:"valido_hasta"`
	MaxUsos              *int       `json:"max_usos"`
	AcumulableHoraMuerta *bool      `json:"acumulable_hora_muerta"`
	Activo               *bool      `json:"activo"`
}

// MetodoPago representa un medio de cobro directo del complejo (RF-07, RN-10).
// Por seguridad jamás expone credenciales_cifradas ni secretos de pasarela.
type MetodoPago struct {
	ID                uuid.UUID `json:"id"`
	ComplejoID        uuid.UUID `json:"complejo_id"`
	Tipo              string    `json:"tipo"`
	Etiqueta          *string   `json:"etiqueta"`
	Titular           *string   `json:"titular"`
	EntidadFinanciera *string   `json:"entidad_financiera"`
	NumeroDestino     *string   `json:"numero_destino"`
	CCI               *string   `json:"cci"`
	QRURL             *string   `json:"qr_url"`
	QRStorageKey      *string   `json:"qr_storage_key"`
	LlavePublica      *string   `json:"llave_publica"`
	TieneCredenciales bool      `json:"tiene_credenciales"`
	Instrucciones     *string   `json:"instrucciones"`
	Activo            bool      `json:"activo"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// CreateMetodoPagoRequest define el payload para dar de alta un método de cobro propio.
type CreateMetodoPagoRequest struct {
	Tipo              string          `json:"tipo"`
	Etiqueta          *string         `json:"etiqueta"`
	Titular           *string         `json:"titular"`
	EntidadFinanciera *string         `json:"entidad_financiera"`
	NumeroDestino     *string         `json:"numero_destino"`
	CCI               *string         `json:"cci"`
	QRURL             *string         `json:"qr_url"`
	QRStorageKey      *string         `json:"qr_storage_key"`
	LlavePublica      *string         `json:"llave_publica"`
	Credenciales      json.RawMessage `json:"credenciales"`
	Instrucciones     *string         `json:"instrucciones"`
	Activo            *bool           `json:"activo"`
}

// UpdateMetodoPagoRequest define los campos editables de un método de cobro propio.
type UpdateMetodoPagoRequest struct {
	Etiqueta          *string         `json:"etiqueta"`
	Titular           *string         `json:"titular"`
	EntidadFinanciera *string         `json:"entidad_financiera"`
	NumeroDestino     *string         `json:"numero_destino"`
	CCI               *string         `json:"cci"`
	QRURL             *string         `json:"qr_url"`
	QRStorageKey      *string         `json:"qr_storage_key"`
	LlavePublica      *string         `json:"llave_publica"`
	Credenciales      json.RawMessage `json:"credenciales"`
	Instrucciones     *string         `json:"instrucciones"`
	Activo            *bool           `json:"activo"`
}

// SaveMetodoPagoParams encapsula internamente los datos listos para persistir, incluyendo el BYTEA cifrado.
type SaveMetodoPagoParams struct {
	ID                   uuid.UUID
	ComplejoID           uuid.UUID
	Tipo                 string
	Etiqueta             *string
	Titular              *string
	EntidadFinanciera    *string
	NumeroDestino        *string
	CCI                  *string
	QRURL                *string
	QRStorageKey         *string
	LlavePublica         *string
	CredencialesCifradas []byte
	UpdateCredenciales   bool
	Instrucciones        *string
	Activo               bool
}

// ComplejoFoto representa una fotografía de la galería del complejo en complejo_fotos.
type ComplejoFoto struct {
	ID          uuid.UUID `json:"id"`
	ComplejoID  uuid.UUID `json:"complejo_id"`
	URL         string    `json:"url"`
	StorageKey  *string   `json:"storage_key,omitempty"`
	Descripcion *string   `json:"descripcion"`
	Orden       int16     `json:"orden"`
	EsPortada   bool      `json:"es_portada"`
	CreatedAt   time.Time `json:"created_at"`
}

// UploadComplejoFotoInput encapsula los datos multipart validados para subir una foto a la galería.
type UploadComplejoFotoInput struct {
	ComplejoID  uuid.UUID
	FileData    []byte
	Descripcion *string
	Orden       int
	EsPortada   bool
}

// UpdateComplejoFotoRequest define los campos editables en PATCH /api/v1/dueno/fotos/{fotoID}.
type UpdateComplejoFotoRequest struct {
	Descripcion *string `json:"descripcion"`
	Orden       *int    `json:"orden"`
	EsPortada   *bool   `json:"es_portada"`
}

// PublicMediaStream contiene el flujo binario seguro y metadatos para servir media bajo public/.
type PublicMediaStream struct {
	Reader      io.ReadCloser
	Prefix      []byte
	ContentType string
	IsInline    bool
}
