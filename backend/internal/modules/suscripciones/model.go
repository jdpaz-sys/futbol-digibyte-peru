package suscripciones

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	PeriodicidadPrueba  = "PRUEBA"
	PeriodicidadMensual = "MENSUAL"
	PeriodicidadAnual   = "ANUAL"

	UnidadDias  = "DIAS"
	UnidadMeses = "MESES"

	EstadoPrueba     = "PRUEBA"
	EstadoActiva     = "ACTIVA"
	EstadoVencida    = "VENCIDA"
	EstadoSuspendida = "SUSPENDIDA"
)

var (
	ErrPlanNotFound          = errors.New("plan de suscripción no encontrado")
	ErrComplejoNotFound      = errors.New("complejo deportivo no encontrado")
	ErrPlanCodeAlreadyExists = errors.New("el código de plan ya está registrado")
	ErrEmailAlreadyExists    = errors.New("el correo electrónico ya está registrado")
	ErrSlugAlreadyExists     = errors.New("el slug del complejo ya está en uso")
	ErrConflict              = errors.New("conflicto de unicidad en el recurso")
)

// ValidationError representa errores de validación de entrada con detalle por campo.
type ValidationError struct {
	Message string
	Fields  map[string]string
}

func (e *ValidationError) Error() string {
	return e.Message
}

// Plan representa un plan del catálogo SaaS en planes_suscripcion.
type Plan struct {
	ID               uuid.UUID `json:"id"`
	Codigo           string    `json:"codigo"`
	Nombre           string    `json:"nombre"`
	Periodicidad     string    `json:"periodicidad"`
	UnidadDuracion   string    `json:"unidad_duracion"`
	CantidadDuracion int16     `json:"cantidad_duracion"`
	Precio           float64   `json:"precio"`
	Descripcion      *string   `json:"descripcion"`
	Activo           bool      `json:"activo"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// CreatePlanRequest define la carga útil para crear un plan SaaS.
type CreatePlanRequest struct {
	Codigo           string  `json:"codigo"`
	Nombre           string  `json:"nombre"`
	Periodicidad     string  `json:"periodicidad"`
	UnidadDuracion   string  `json:"unidad_duracion"`
	CantidadDuracion int     `json:"cantidad_duracion"`
	Precio           float64 `json:"precio"`
	Descripcion      *string `json:"descripcion"`
	Activo           *bool   `json:"activo"`
}

// UpdatePlanRequest define los campos editables de un plan sin alterar snapshots históricos.
type UpdatePlanRequest struct {
	Nombre      *string  `json:"nombre"`
	Precio      *float64 `json:"precio"`
	Descripcion *string  `json:"descripcion"`
	Activo      *bool    `json:"activo"`
}

// CreateDuenoInput define las credenciales y datos del dueño para el alta atómica.
type CreateDuenoInput struct {
	Email          string `json:"email"`
	Password       string `json:"password"`
	NombreCompleto string `json:"nombre_completo"`
	Nombre         string `json:"nombre"`
	Telefono       string `json:"telefono"`
}

// CreateComplejoRequest define la carga útil para dar de alta un complejo + DUENO + suscripción inicial.
type CreateComplejoRequest struct {
	Nombre           string           `json:"nombre"`
	Slug             string           `json:"slug"`
	Descripcion      *string          `json:"descripcion"`
	TelefonoContacto *string          `json:"telefono_contacto"`
	Departamento     string           `json:"departamento"`
	Provincia        string           `json:"provincia"`
	Distrito         string           `json:"distrito"`
	PlanID           string           `json:"plan_id"`
	MontoCobrado     *float64         `json:"monto_cobrado"`
	MetodoPagoSaaS   *string          `json:"metodo_pago_saas"`
	ReferenciaPago   *string          `json:"referencia_pago"`
	Observaciones    *string          `json:"observaciones"`
	Dueno            CreateDuenoInput `json:"dueno"`
}

// DuenoResumen expone la información pública/administrativa del usuario DUENO (sin password_hash).
type DuenoResumen struct {
	ID             uuid.UUID `json:"id"`
	Email          string    `json:"email"`
	NombreCompleto string    `json:"nombre_completo"`
	Telefono       *string   `json:"telefono"`
	Activo         bool      `json:"activo"`
}

// PlanVigenteResumen expone los datos esenciales del plan actual asignado al complejo.
type PlanVigenteResumen struct {
	ID           uuid.UUID `json:"id"`
	Codigo       string    `json:"codigo"`
	Nombre       string    `json:"nombre"`
	Periodicidad string    `json:"periodicidad"`
	Precio       float64   `json:"precio"`
}

// ComplejoItem representa un complejo deportivo con su dueño y plan vigente.
type ComplejoItem struct {
	ID                 uuid.UUID           `json:"id"`
	Slug               string              `json:"slug"`
	Nombre             string              `json:"nombre"`
	Descripcion        *string             `json:"descripcion"`
	TelefonoContacto   *string             `json:"telefono_contacto"`
	Departamento       string              `json:"departamento"`
	Provincia          string              `json:"provincia"`
	Distrito           string              `json:"distrito"`
	Publicado          bool                `json:"publicado"`
	EstadoSuscripcion  string              `json:"estado_suscripcion"`
	SuscripcionVenceAt *time.Time          `json:"suscripcion_vence_at"`
	Dueno              DuenoResumen        `json:"dueno"`
	PlanActual         *PlanVigenteResumen `json:"plan_actual"`
	CreatedAt          time.Time           `json:"created_at"`
	UpdatedAt          time.Time           `json:"updated_at"`
}

// SuscripcionHistorialItem representa un registro inmutable en suscripciones_historial.
type SuscripcionHistorialItem struct {
	ID                  uuid.UUID `json:"id"`
	ComplejoID          uuid.UUID `json:"complejo_id"`
	PlanID              uuid.UUID `json:"plan_id"`
	PlanNombre          string    `json:"plan_nombre"`
	Periodicidad        string    `json:"periodicidad"`
	MontoCobrado        float64   `json:"monto_cobrado"`
	MetodoPagoSaaS      *string   `json:"metodo_pago_saas"`
	ReferenciaPago      *string   `json:"referencia_pago"`
	Estado              string    `json:"estado"`
	InicioAt            time.Time `json:"inicio_at"`
	FinAt               time.Time `json:"fin_at"`
	RegistradoPor       uuid.UUID `json:"registrado_por"`
	RegistradoPorNombre *string   `json:"registrado_por_nombre,omitempty"`
	Observaciones       *string   `json:"observaciones"`
	CreatedAt           time.Time `json:"created_at"`
}

// ComplejoDetalleResponse devuelve el detalle del complejo junto a su historial completo.
type ComplejoDetalleResponse struct {
	ComplejoItem
	Historial []SuscripcionHistorialItem `json:"historial"`
}

// OperacionSuscripcionResponse devuelve el complejo actualizado y el evento registrado en historial.
type OperacionSuscripcionResponse struct {
	ComplejoItem
	EventoHistorial SuscripcionHistorialItem `json:"evento_historial"`
}

// ListComplejosFilter encapsula filtros y paginación para el listado administrativo.
type ListComplejosFilter struct {
	EstadoSuscripcion *string
	PorVencerDias     *int
	Page              int
	Limit             int
}

// ComplejosListResponse estructura la respuesta paginada de complejos.
type ComplejosListResponse struct {
	Items      []ComplejoItem `json:"items"`
	Total      int            `json:"total"`
	Page       int            `json:"page"`
	Limit      int            `json:"limit"`
	TotalPages int            `json:"total_pages"`
}

// RenovarSuscripcionRequest define la carga útil para renovar o asignar un plan SaaS.
type RenovarSuscripcionRequest struct {
	PlanID         string   `json:"plan_id"`
	MontoCobrado   *float64 `json:"monto_cobrado"`
	MetodoPagoSaaS *string  `json:"metodo_pago_saas"`
	ReferenciaPago *string  `json:"referencia_pago"`
	Observaciones  *string  `json:"observaciones"`
}

// CambiarEstadoSuscripcionRequest permite suspender, vencer o reactivar la suscripción de un complejo.
type CambiarEstadoSuscripcionRequest struct {
	Estado        string  `json:"estado"`
	Motivo        *string `json:"motivo"`
	Observaciones *string `json:"observaciones"`
}

// ComplejosMetricas agrupa conteos de complejos por estado de suscripción y alerta de vencimiento.
type ComplejosMetricas struct {
	Total          int `json:"total"`
	Prueba         int `json:"prueba"`
	Activa         int `json:"activa"`
	Vencida        int `json:"vencida"`
	Suspendida     int `json:"suspendida"`
	PorVencer7Dias int `json:"por_vencer_7_dias"`
}

// IngresosMetricas resume la facturación B2B acumulada desde suscripciones_historial.
type IngresosMetricas struct {
	Total     float64 `json:"total"`
	MesActual float64 `json:"mes_actual"`
	Moneda    string  `json:"moneda"`
}

// ReservasMetricas resume el volumen global de reservas del SaaS.
type ReservasMetricas struct {
	Total                   int `json:"total"`
	Confirmadas             int `json:"confirmadas"`
	Completadas             int `json:"completadas"`
	ConfirmadasYCompletadas int `json:"confirmadas_completadas"`
}

// MetricasGlobalesResponse consolida los indicadores ejecutivos de RF-02 / CU-02.
type MetricasGlobalesResponse struct {
	Complejos             ComplejosMetricas `json:"complejos"`
	IngresosSuscripciones IngresosMetricas  `json:"ingresos_suscripciones"`
	Reservas              ReservasMetricas  `json:"reservas"`
	GeneradoAt            time.Time         `json:"generado_at"`
}

// CreateComplejoParams contiene los datos ya validados y normalizados para la transacción de alta.
type CreateComplejoParams struct {
	AdminID          uuid.UUID
	PlanID           uuid.UUID
	Slug             string
	Nombre           string
	Descripcion      *string
	TelefonoContacto *string
	Departamento     string
	Provincia        string
	Distrito         string
	MontoCobrado     *float64
	MetodoPagoSaaS   *string
	ReferenciaPago   *string
	Observaciones    *string
	DuenoEmail       string
	DuenoPassHash    string
	DuenoNombre      string
	DuenoTelefono    string
}

// RenovarSuscripcionParams contiene los datos validados para la transacción de renovación.
type RenovarSuscripcionParams struct {
	AdminID        uuid.UUID
	ComplejoID     uuid.UUID
	PlanID         uuid.UUID
	MontoCobrado   *float64
	MetodoPagoSaaS *string
	ReferenciaPago *string
	Observaciones  *string
}

// CambiarEstadoParams contiene los datos validados para el cambio explícito de estado.
type CambiarEstadoParams struct {
	AdminID       uuid.UUID
	ComplejoID    uuid.UUID
	NuevoEstado   string
	Observaciones string
}
