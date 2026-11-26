package reservas

import (
	"errors"
	"io"
	"time"

	"github.com/google/uuid"
)

const (
	EstadoPendientePago = "PENDIENTE_PAGO"
	EstadoPorValidar    = "POR_VALIDAR"
	EstadoConfirmada    = "CONFIRMADA"
	EstadoCompletada    = "COMPLETADA"
	EstadoExpirada      = "EXPIRADA"
	EstadoRechazada     = "RECHAZADA"
	EstadoCancelada     = "CANCELADA"
	EstadoNoShow        = "NO_SHOW"

	OrigenApp                  = "APP"
	OrigenExterna              = "EXTERNA"
	OrigenBloqueoMantenimiento = "BLOQUEO_MANTENIMIENTO"

	TipoPorcentaje = "PORCENTAJE"
	TipoMontoFijo  = "MONTO_FIJO"

	EstadoSuscripcionPrueba = "PRUEBA"
	EstadoSuscripcionActiva = "ACTIVA"

	RevisionPendiente = "PENDIENTE"
	RevisionAprobado  = "APROBADO"
	RevisionRechazado = "RECHAZADO"

	MetodoPagoYape          = "YAPE"
	MetodoPagoPlin          = "PLIN"
	MetodoPagoTransferencia = "TRANSFERENCIA"
	MetodoPagoCulqi         = "CULQI"
	MetodoPagoMercadoPago   = "MERCADOPAGO"

	MetodoSaldoEfectivo      = "EFECTIVO"
	MetodoSaldoYape          = "YAPE"
	MetodoSaldoPlin          = "PLIN"
	MetodoSaldoTransferencia = "TRANSFERENCIA"
	MetodoSaldoTarjeta       = "TARJETA"
	MetodoSaldoOtro          = "OTRO"

	MotivoSlotPasado    = "PASADO"
	MotivoSlotOcupado   = "OCUPADO"
	MotivoSlotSinTarifa = "SIN_TARIFA"

	AccionHoldCreado              = "HOLD_CREADO"
	AccionComprobanteSubido       = "COMPROBANTE_SUBIDO"
	AccionHoldExpirado            = "HOLD_EXPIRADO"
	AccionComprobanteAprobado     = "COMPROBANTE_APROBADO"
	AccionComprobanteRechazado    = "COMPROBANTE_RECHAZADO"
	AccionReservaExternaCreada    = "RESERVA_EXTERNA_CREADA"
	AccionBloqueoCreado           = "BLOQUEO_CREADO"
	AccionCheckinCompletado       = "CHECKIN_COMPLETADO"
	AccionNoShowRegistrado        = "NO_SHOW_REGISTRADO"
	AccionReservaCanceladaJugador = "RESERVA_CANCELADA_JUGADOR"
	AccionReservaCanceladaDueno   = "RESERVA_CANCELADA_DUENO"
	AccionReembolsoResuelto       = "REEMBOLSO_RESUELTO"
	AccionReprogramadaJugador     = "RESERVA_REPROGRAMADA_JUGADOR"
	AccionReprogramadaDueno       = "RESERVA_REPROGRAMADA_DUENO"

	NotifComprobantePorValidar = "COMPROBANTE_POR_VALIDAR"
	NotifReservaConfirmada     = "RESERVA_CONFIRMADA"
	NotifComprobanteRechazado  = "COMPROBANTE_RECHAZADO"
	NotifReservaNoShow         = "RESERVA_NO_SHOW"
	NotifReservaCancelada      = "RESERVA_CANCELADA"
	NotifReservaReprogramada   = "RESERVA_REPROGRAMADA"
	NotifReembolsoResuelto     = "REEMBOLSO_RESUELTO"
	NotifSuscripcionVencida    = "SUSCRIPCION_VENCIDA"
	NotifSuscripcionPorVencer  = "SUSCRIPCION_POR_VENCER"
	NotifRecordatorioPartido   = "RECORDATORIO_PARTIDO"

	ZonaHorariaLima = "America/Lima"

	DefaultListLimit                 = 20
	MaxListLimit                     = 50
	DefaultDuenoListLimit            = 50
	MaxDuenoListLimit                = 100
	MaxEvidenciaBytes                = 5 << 20 // 5 MiB
	MaxPrecioMonto                   = 99999999.99
	DefaultWorkerInterval            = 30 * time.Second
	DefaultMaintenanceInterval       = 5 * time.Minute
	DefaultWorkerBatchSize           = 100
	AlertaDemoraThreshold            = 30 * time.Minute
	ExpiringSubscriptionNoticeWindow = 3 * 24 * time.Hour
	UpcomingMatchReminderWindow      = 2 * time.Hour
)

var (
	ErrComplejoNotFound          = errors.New("complejo deportivo no encontrado")
	ErrComplejoNoDisponible      = errors.New("este complejo deportivo no recibe nuevas reservas temporalmente")
	ErrCanchaNotFound            = errors.New("cancha no encontrada o inactiva")
	ErrComplejoCerrado           = errors.New("el complejo no atiende en el día seleccionado")
	ErrHorarioFueraDeRango       = errors.New("el horario solicitado está fuera del horario de atención del complejo")
	ErrUnidadReservaInvalida     = errors.New("el horario solicitado no respeta la unidad mínima de reserva del complejo")
	ErrSlotEnPasado              = errors.New("no es posible cotizar o reservar un horario que ya inició o pasó")
	ErrTarifaNoConfigurada       = errors.New("no existe tarifa activa configurada para todo el rango horario solicitado")
	ErrCuponNotFound             = errors.New("el código de cupón no existe para este complejo")
	ErrCuponInactive             = errors.New("el cupón no se encuentra activo")
	ErrCuponNotValidYet          = errors.New("el cupón aún no inicia su vigencia")
	ErrCuponExpired              = errors.New("el cupón ha expirado")
	ErrCuponExhausted            = errors.New("el cupón alcanzó su límite máximo de usos")
	ErrCuponNotStackableDeadHour = errors.New("el cupón no es acumulable con tarifas de hora muerta")
	ErrSlotNotAvailable          = errors.New("el horario seleccionado ya no está disponible")
	ErrReservaNotFound           = errors.New("reserva no encontrada")
	ErrReservaForbidden          = errors.New("no tienes permisos para acceder a esta reserva")
	ErrHoldExpired               = errors.New("el tiempo límite del bloqueo temporal expiró")
	ErrInvalidReservaState       = errors.New("la reserva no se encuentra en un estado válido para esta operación")
	ErrInvalidReservaOrigen      = errors.New("esta operación no aplica para el origen de esta reserva")
	ErrCancellationWindowExpired = errors.New("ya venció la ventana mínima de cancelación o reprogramación configurada por el complejo")
	ErrReembolsoNoPendiente      = errors.New("la reserva no tiene un reembolso directo pendiente")
	ErrMetodoPagoNotFound        = errors.New("método de pago no encontrado o inactivo en este complejo")
	ErrMetodoPagoNoManual        = errors.New("el método de pago seleccionado no admite comprobantes manuales")
	ErrComprobanteNotFound       = errors.New("comprobante de pago no encontrado")
	ErrEvidenciaNotFound         = errors.New("archivo de evidencia no encontrado")
	ErrEvidenciaForbidden        = errors.New("no tienes permisos para descargar esta evidencia")
	ErrFileTooLarge              = errors.New("la imagen del comprobante excede el límite máximo de 5 MB")
	ErrInvalidFileType           = errors.New("el comprobante debe ser una imagen válida en formato JPG, PNG o WEBP")
	ErrNotificacionNotFound      = errors.New("notificación no encontrada")
	ErrPushSubscriptionNotFound  = errors.New("suscripción Web Push no encontrada")
)

// ValidationError representa errores de validación de entrada con detalle por campo.
type ValidationError struct {
	Message string
	Fields  map[string]string
}

func (e *ValidationError) Error() string {
	return e.Message
}

// ---------- Modelos internos de lectura desde BD ----------

// ComplejoContext agrupa las reglas operativas y vigencia de suscripción necesarias para reservar.
type ComplejoContext struct {
	ID                  uuid.UUID
	DuenoID             uuid.UUID
	Slug                string
	Nombre              string
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

// CanchaInfo representa una cancha activa del complejo.
type CanchaInfo struct {
	ID          uuid.UUID `json:"id"`
	ComplejoID  uuid.UUID `json:"-"`
	Nombre      string    `json:"nombre"`
	Modalidad   string    `json:"modalidad"`
	Superficie  string    `json:"superficie"`
	Techada     bool      `json:"techada"`
	Iluminacion bool      `json:"iluminacion"`
	Activa      bool      `json:"-"`
}

// HorarioAtencion representa el horario de atención semanal para un día (0=Domingo..6=Sábado).
type HorarioAtencion struct {
	DiaSemana      int16
	MinutoApertura int16
	MinutoCierre   int16
	Cerrado        bool
}

// TarifaFranja representa una tarifa activa (específica de cancha o general) para un día.
type TarifaFranja struct {
	ID           uuid.UUID
	CanchaID     *uuid.UUID
	DiaSemana    int16
	MinutoInicio int16
	MinutoFin    int16
	PrecioHora   float64
	Etiqueta     *string
	EsHoraMuerta bool
}

// CuponRecord representa un cupón promocional de un complejo.
type CuponRecord struct {
	ID                   uuid.UUID
	ComplejoID           uuid.UUID
	Codigo               string
	Descripcion          *string
	TipoDescuento        string
	ValorDescuento       float64
	ValidoDesde          time.Time
	ValidoHasta          *time.Time
	MaxUsos              *int
	UsosActuales         int
	AcumulableHoraMuerta bool
	Activo               bool
}

// OcupacionIntervalo representa un rango horario ocupado [Inicio, Fin) de una cancha.
type OcupacionIntervalo struct {
	Inicio time.Time
	Fin    time.Time
	Estado string
}

// MetodoPagoPublico expone únicamente datos seguros de cobro directo del complejo (RN-10).
// Nunca incluye credenciales_cifradas ni rutas internas de almacenamiento.
type MetodoPagoPublico struct {
	ID                uuid.UUID `json:"id"`
	Tipo              string    `json:"tipo"`
	Etiqueta          *string   `json:"etiqueta,omitempty"`
	Titular           *string   `json:"titular,omitempty"`
	EntidadFinanciera *string   `json:"entidad_financiera,omitempty"`
	NumeroDestino     *string   `json:"numero_destino,omitempty"`
	CCI               *string   `json:"cci,omitempty"`
	QRURL             *string   `json:"qr_url,omitempty"`
	LlavePublica      *string   `json:"llave_publica,omitempty"`
	Instrucciones     *string   `json:"instrucciones,omitempty"`
}

// ComprobanteAccessRecord contiene los metadatos de autorización para servir evidencia privada.
type ComprobanteAccessRecord struct {
	ID                  uuid.UUID
	ReservaID           uuid.UUID
	ComplejoID          uuid.UUID
	JugadorID           *uuid.UUID
	DuenoID             uuid.UUID
	EvidenciaStorageKey *string
}

// ---------- Contratos API: Disponibilidad ----------

// DisponibilidadSlot describe un bloque horario individual del día consultado.
type DisponibilidadSlot struct {
	Inicio             time.Time `json:"inicio"`
	Fin                time.Time `json:"fin"`
	HoraInicio         string    `json:"hora_inicio"`
	HoraFin            string    `json:"hora_fin"`
	Disponible         bool      `json:"disponible"`
	MotivoNoDisponible *string   `json:"motivo_no_disponible,omitempty"`
	Precio             *float64  `json:"precio"`
	EsHoraMuerta       bool      `json:"es_hora_muerta"`
	EtiquetaTarifa     *string   `json:"etiqueta_tarifa,omitempty"`
}

// DisponibilidadResponse devuelve la grilla en vivo de una cancha para una fecha en America/Lima.
type DisponibilidadResponse struct {
	ComplejoID       uuid.UUID            `json:"complejo_id"`
	ComplejoSlug     string               `json:"complejo_slug"`
	CanchaID         uuid.UUID            `json:"cancha_id"`
	CanchaNombre     string               `json:"cancha_nombre"`
	Fecha            string               `json:"fecha"`
	ZonaHoraria      string               `json:"zona_horaria"`
	UnidadReservaMin int16                `json:"unidad_reserva_min"`
	Cerrado          bool                 `json:"cerrado"`
	HoraApertura     *string              `json:"hora_apertura,omitempty"`
	HoraCierre       *string              `json:"hora_cierre,omitempty"`
	Slots            []DisponibilidadSlot `json:"slots"`
}

// ---------- Contratos API: Cotizador ----------

// CotizarRequest representa el cuerpo de POST /api/v1/public/complejos/{slug}/cotizar.
type CotizarRequest struct {
	CanchaID    string `json:"cancha_id"`
	Inicio      string `json:"inicio"`
	Fin         string `json:"fin"`
	CodigoCupon string `json:"codigo_cupon,omitempty"`
}

// CotizacionTramo detalla un sub-bloque tarifario (soporta franjas mixtas RN-08).
type CotizacionTramo struct {
	TarifaID       uuid.UUID `json:"tarifa_id"`
	Inicio         time.Time `json:"inicio"`
	Fin            time.Time `json:"fin"`
	HoraInicio     string    `json:"hora_inicio"`
	HoraFin        string    `json:"hora_fin"`
	DuracionMin    int       `json:"duracion_min"`
	PrecioHora     float64   `json:"precio_hora"`
	Subtotal       float64   `json:"subtotal"`
	EsHoraMuerta   bool      `json:"es_hora_muerta"`
	EsTarifaCancha bool      `json:"es_tarifa_cancha"`
	Etiqueta       *string   `json:"etiqueta,omitempty"`
}

// CuponAplicado resume el cupón validado y aplicado a la cotización.
type CuponAplicado struct {
	ID                   uuid.UUID `json:"id"`
	Codigo               string    `json:"codigo"`
	Descripcion          *string   `json:"descripcion,omitempty"`
	TipoDescuento        string    `json:"tipo_descuento"`
	ValorDescuento       float64   `json:"valor_descuento"`
	AcumulableHoraMuerta bool      `json:"acumulable_hora_muerta"`
}

// CotizacionResponse devuelve el cálculo exacto previo al Hold.
type CotizacionResponse struct {
	ComplejoID          uuid.UUID         `json:"complejo_id"`
	CanchaID            uuid.UUID         `json:"cancha_id"`
	Inicio              time.Time         `json:"inicio"`
	Fin                 time.Time         `json:"fin"`
	DuracionMin         int               `json:"duracion_min"`
	Disponible          bool              `json:"disponible"`
	PrecioBase          float64           `json:"precio_base"`
	MontoDescuento      float64           `json:"monto_descuento"`
	MontoTotal          float64           `json:"monto_total"`
	TipoAdelanto        string            `json:"tipo_adelanto"`
	ValorAdelanto       float64           `json:"valor_adelanto"`
	MontoAdelanto       float64           `json:"monto_adelanto"`
	SaldoPendiente      float64           `json:"saldo_pendiente"`
	HoldDuracionMin     int16             `json:"hold_duracion_min"`
	HorasMinCancelacion int16             `json:"horas_min_cancelacion"`
	IncluyeHoraMuerta   bool              `json:"incluye_hora_muerta"`
	CuponAplicado       *CuponAplicado    `json:"cupon_aplicado,omitempty"`
	Desglose            []CotizacionTramo `json:"desglose"`
	TerminosReserva     string            `json:"terminos_reserva"`
}

// ---------- Contratos API: Crear Reserva (Hold) ----------

// CreateReservaRequest representa el cuerpo de POST /api/v1/jugador/reservas.
type CreateReservaRequest struct {
	ComplejoID     string `json:"complejo_id"`
	CanchaID       string `json:"cancha_id"`
	Inicio         string `json:"inicio"`
	Fin            string `json:"fin"`
	CodigoCupon    string `json:"codigo_cupon,omitempty"`
	AceptaTerminos bool   `json:"acepta_terminos"`
}

// ReservaResumen contiene los datos principales de la reserva creada con Hold.
type ReservaResumen struct {
	ID                  uuid.UUID  `json:"id"`
	ComplejoID          uuid.UUID  `json:"complejo_id"`
	ComplejoNombre      string     `json:"complejo_nombre"`
	CanchaID            uuid.UUID  `json:"cancha_id"`
	CanchaNombre        string     `json:"cancha_nombre"`
	JugadorID           uuid.UUID  `json:"jugador_id"`
	Origen              string     `json:"origen"`
	Estado              string     `json:"estado"`
	Inicio              time.Time  `json:"inicio"`
	Fin                 time.Time  `json:"fin"`
	DuracionMin         int        `json:"duracion_min"`
	CuponID             *uuid.UUID `json:"cupon_id,omitempty"`
	PrecioBase          float64    `json:"precio_base"`
	MontoDescuento      float64    `json:"monto_descuento"`
	MontoTotal          float64    `json:"monto_total"`
	MontoAdelanto       float64    `json:"monto_adelanto"`
	SaldoPendiente      float64    `json:"saldo_pendiente"`
	TerminosAceptadosAt time.Time  `json:"terminos_aceptados_at"`
	HoldExpiraAt        time.Time  `json:"hold_expira_at"`
	CreatedAt           time.Time  `json:"created_at"`
}

// CreateReservaResponse devuelve el Hold creado, temporizador restante y medios de cobro directos (RN-10).
type CreateReservaResponse struct {
	Reserva               ReservaResumen      `json:"reserva"`
	HoldSegundosRestantes int                 `json:"hold_segundos_restantes"`
	CuponAplicado         *CuponAplicado      `json:"cupon_aplicado,omitempty"`
	Desglose              []CotizacionTramo   `json:"desglose"`
	MetodosPago           []MetodoPagoPublico `json:"metodos_pago"`
}

// ---------- Contratos API: Comprobante Manual ----------

// UploadComprobanteInput encapsula los datos validados del formulario multipart/form-data.
type UploadComprobanteInput struct {
	ReservaID       uuid.UUID
	JugadorID       uuid.UUID
	MetodoPagoID    uuid.UUID
	CodigoOperacion string
	MontoDeclarado  float64
	FileData        []byte
}

// ComprobanteInfo representa un comprobante de pago sin exponer rutas físicas ni storage_key.
type ComprobanteInfo struct {
	ID                uuid.UUID  `json:"id"`
	ReservaID         uuid.UUID  `json:"reserva_id"`
	MetodoPagoID      uuid.UUID  `json:"metodo_pago_id"`
	MetodoPagoTipo    string     `json:"metodo_pago_tipo"`
	EsAutomatico      bool       `json:"es_automatico"`
	EstadoRevision    string     `json:"estado_revision"`
	MontoDeclarado    float64    `json:"monto_declarado"`
	CodigoOperacion   *string    `json:"codigo_operacion,omitempty"`
	EvidenciaEndpoint *string    `json:"evidencia_endpoint,omitempty"`
	ValidadoAt        *time.Time `json:"validado_at,omitempty"`
	MotivoRechazo     *string    `json:"motivo_rechazo,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
}

// UploadComprobanteResponse confirma el paso a POR_VALIDAR tras subir el comprobante.
type UploadComprobanteResponse struct {
	ReservaID       uuid.UUID       `json:"reserva_id"`
	EstadoReserva   string          `json:"estado_reserva"`
	PorValidarDesde time.Time       `json:"por_validar_desde"`
	Comprobante     ComprobanteInfo `json:"comprobante"`
}

// EvidenciaStream contiene el flujo binario seguro y metadatos para servir un comprobante privado.
type EvidenciaStream struct {
	Reader      io.ReadCloser
	Prefix      []byte
	ContentType string
	FileName    string
}

// ---------- Contratos API: Mis Reservas (Jugador) ----------

// ListJugadorReservasFilter filtra el listado paginado de reservas del jugador autenticado.
type ListJugadorReservasFilter struct {
	Estado string
	Limit  int
	Offset int
}

// JugadorReservaItem resume una reserva en GET /api/v1/jugador/reservas.
type JugadorReservaItem struct {
	ID                    uuid.UUID  `json:"id"`
	ComplejoID            uuid.UUID  `json:"complejo_id"`
	ComplejoSlug          string     `json:"complejo_slug"`
	ComplejoNombre        string     `json:"complejo_nombre"`
	ComplejoDireccion     *string    `json:"complejo_direccion,omitempty"`
	CanchaID              uuid.UUID  `json:"cancha_id"`
	CanchaNombre          string     `json:"cancha_nombre"`
	CanchaModalidad       string     `json:"cancha_modalidad"`
	Estado                string     `json:"estado"`
	Origen                string     `json:"origen"`
	Inicio                time.Time  `json:"inicio"`
	Fin                   time.Time  `json:"fin"`
	DuracionMin           int        `json:"duracion_min"`
	MontoTotal            float64    `json:"monto_total"`
	MontoAdelanto         float64    `json:"monto_adelanto"`
	SaldoPendiente        float64    `json:"saldo_pendiente"`
	HoldExpiraAt          *time.Time `json:"hold_expira_at,omitempty"`
	HoldSegundosRestantes *int       `json:"hold_segundos_restantes,omitempty"`
	PorValidarDesde       *time.Time `json:"por_validar_desde,omitempty"`
	CodigoQR              *string    `json:"codigo_qr,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
}

// JugadorReservasListResponse envuelve el listado paginado de reservas del jugador.
type JugadorReservasListResponse struct {
	Items  []JugadorReservaItem `json:"items"`
	Total  int                  `json:"total"`
	Limit  int                  `json:"limit"`
	Offset int                  `json:"offset"`
}

// ComplejoRutaInfo provee ubicación exacta, dirección y referencias del complejo para Modo En Ruta.
type ComplejoRutaInfo struct {
	ID                  uuid.UUID `json:"id"`
	Slug                string    `json:"slug"`
	Nombre              string    `json:"nombre"`
	DireccionEscrita    *string   `json:"direccion_escrita,omitempty"`
	Referencias         *string   `json:"referencias,omitempty"`
	Distrito            string    `json:"distrito"`
	Provincia           string    `json:"provincia"`
	Departamento        string    `json:"departamento"`
	TelefonoContacto    *string   `json:"telefono_contacto,omitempty"`
	Lat                 *float64  `json:"lat"`
	Lng                 *float64  `json:"lng"`
	HorasMinCancelacion int16     `json:"horas_min_cancelacion"`
}

// JugadorReservaDetail entrega la vista completa de una reserva para el jugador (CU-13).
// CodigoQR únicamente se expone cuando Estado es CONFIRMADA o COMPLETADA.
type JugadorReservaDetail struct {
	ID                    uuid.UUID           `json:"id"`
	JugadorID             uuid.UUID           `json:"jugador_id"`
	Estado                string              `json:"estado"`
	Origen                string              `json:"origen"`
	Inicio                time.Time           `json:"inicio"`
	Fin                   time.Time           `json:"fin"`
	DuracionMin           int                 `json:"duracion_min"`
	PrecioBase            float64             `json:"precio_base"`
	MontoDescuento        float64             `json:"monto_descuento"`
	MontoTotal            float64             `json:"monto_total"`
	MontoAdelanto         float64             `json:"monto_adelanto"`
	SaldoPendiente        float64             `json:"saldo_pendiente"`
	MetodoPagoSaldo       *string             `json:"metodo_pago_saldo,omitempty"`
	SaldoPagadoAt         *time.Time          `json:"saldo_pagado_at,omitempty"`
	TerminosAceptadosAt   *time.Time          `json:"terminos_aceptados_at,omitempty"`
	HoldExpiraAt          *time.Time          `json:"hold_expira_at,omitempty"`
	HoldSegundosRestantes *int                `json:"hold_segundos_restantes,omitempty"`
	PorValidarDesde       *time.Time          `json:"por_validar_desde,omitempty"`
	CodigoQR              *string             `json:"codigo_qr,omitempty"`
	CheckinAt             *time.Time          `json:"checkin_at,omitempty"`
	CanceladoAt           *time.Time          `json:"cancelado_at,omitempty"`
	MotivoCancelacion     *string             `json:"motivo_cancelacion,omitempty"`
	ReembolsoPendiente    bool                `json:"reembolso_pendiente"`
	ReembolsoResueltoAt   *time.Time          `json:"reembolso_resuelto_at,omitempty"`
	ReprogramacionesCount int16               `json:"reprogramaciones_count"`
	ExcedenteReembolso    float64             `json:"excedente_reembolso,omitempty"`
	Complejo              ComplejoRutaInfo    `json:"complejo"`
	Cancha                CanchaInfo          `json:"cancha"`
	Comprobantes          []ComprobanteInfo   `json:"comprobantes"`
	MetodosPago           []MetodoPagoPublico `json:"metodos_pago,omitempty"`
	CreatedAt             time.Time           `json:"created_at"`
	UpdatedAt             time.Time           `json:"updated_at"`
}

// ---------- Contratos API: Operación Diaria del Dueño (Fase 3B) ----------

// ListDuenoReservasFilter filtra la agenda y bandeja de validación del complejo.
type ListDuenoReservasFilter struct {
	FechaDesde string
	FechaHasta string
	CanchaID   *uuid.UUID
	Estado     string
	Origen     string
	Limit      int
	Offset     int
}

// DuenoReservasQueryParams contiene el rango ya normalizado en America/Lima para consulta SQL.
type DuenoReservasQueryParams struct {
	Desde    *time.Time
	HastaEnd *time.Time
	CanchaID *uuid.UUID
	Estado   string
	Origen   string
	Limit    int
	Offset   int
}

// DuenoReservaItem resume una reserva en la agenda/bandeja de GET /api/v1/dueno/reservas.
type DuenoReservaItem struct {
	ID                         uuid.UUID        `json:"id"`
	ComplejoID                 uuid.UUID        `json:"complejo_id"`
	CanchaID                   uuid.UUID        `json:"cancha_id"`
	CanchaNombre               string           `json:"cancha_nombre"`
	CanchaModalidad            string           `json:"cancha_modalidad"`
	Origen                     string           `json:"origen"`
	Estado                     string           `json:"estado"`
	Inicio                     time.Time        `json:"inicio"`
	Fin                        time.Time        `json:"fin"`
	DuracionMin                int              `json:"duracion_min"`
	JugadorID                  *uuid.UUID       `json:"jugador_id,omitempty"`
	JugadorNombre              *string          `json:"jugador_nombre,omitempty"`
	JugadorTelefono            *string          `json:"jugador_telefono,omitempty"`
	ClienteExternoNombre       *string          `json:"cliente_externo_nombre,omitempty"`
	ClienteExternoTelefono     *string          `json:"cliente_externo_telefono,omitempty"`
	MotivoBloqueo              *string          `json:"motivo_bloqueo,omitempty"`
	NotasInternas              *string          `json:"notas_internas,omitempty"`
	PrecioBase                 float64          `json:"precio_base"`
	MontoDescuento             float64          `json:"monto_descuento"`
	MontoTotal                 float64          `json:"monto_total"`
	MontoAdelanto              float64          `json:"monto_adelanto"`
	SaldoPendiente             float64          `json:"saldo_pendiente"`
	MetodoPagoSaldo            *string          `json:"metodo_pago_saldo,omitempty"`
	SaldoPagadoAt              *time.Time       `json:"saldo_pagado_at,omitempty"`
	HoldExpiraAt               *time.Time       `json:"hold_expira_at,omitempty"`
	PorValidarDesde            *time.Time       `json:"por_validar_desde,omitempty"`
	AlertaDemora               bool             `json:"alerta_demora"`
	MinutosEsperaValidacion    *int             `json:"minutos_espera_validacion,omitempty"`
	UltimoComprobantePendiente *ComprobanteInfo `json:"ultimo_comprobante_pendiente,omitempty"`
	CodigoQR                   *string          `json:"codigo_qr,omitempty"`
	CheckinAt                  *time.Time       `json:"checkin_at,omitempty"`
	CanceladoAt                *time.Time       `json:"cancelado_at,omitempty"`
	MotivoCancelacion          *string          `json:"motivo_cancelacion,omitempty"`
	ReembolsoPendiente         bool             `json:"reembolso_pendiente"`
	ReembolsoResueltoAt        *time.Time       `json:"reembolso_resuelto_at,omitempty"`
	ReprogramacionesCount      int16            `json:"reprogramaciones_count"`
	CreatedAt                  time.Time        `json:"created_at"`
	UpdatedAt                  time.Time        `json:"updated_at"`
}

// DuenoReservasListResponse envuelve la agenda paginada del complejo.
type DuenoReservasListResponse struct {
	Items  []DuenoReservaItem `json:"items"`
	Total  int                `json:"total"`
	Limit  int                `json:"limit"`
	Offset int                `json:"offset"`
}

// DuenoReservaDetail entrega la vista completa de una reserva para operaciones del dueño.
type DuenoReservaDetail struct {
	ID                     uuid.UUID         `json:"id"`
	ComplejoID             uuid.UUID         `json:"complejo_id"`
	Cancha                 CanchaInfo        `json:"cancha"`
	Origen                 string            `json:"origen"`
	Estado                 string            `json:"estado"`
	Inicio                 time.Time         `json:"inicio"`
	Fin                    time.Time         `json:"fin"`
	DuracionMin            int               `json:"duracion_min"`
	JugadorID              *uuid.UUID        `json:"jugador_id,omitempty"`
	JugadorNombre          *string           `json:"jugador_nombre,omitempty"`
	JugadorTelefono        *string           `json:"jugador_telefono,omitempty"`
	ClienteExternoNombre   *string           `json:"cliente_externo_nombre,omitempty"`
	ClienteExternoTelefono *string           `json:"cliente_externo_telefono,omitempty"`
	MotivoBloqueo          *string           `json:"motivo_bloqueo,omitempty"`
	NotasInternas          *string           `json:"notas_internas,omitempty"`
	CuponID                *uuid.UUID        `json:"cupon_id,omitempty"`
	PrecioBase             float64           `json:"precio_base"`
	MontoDescuento         float64           `json:"monto_descuento"`
	MontoTotal             float64           `json:"monto_total"`
	MontoAdelanto          float64           `json:"monto_adelanto"`
	SaldoPendiente         float64           `json:"saldo_pendiente"`
	MetodoPagoSaldo        *string           `json:"metodo_pago_saldo,omitempty"`
	SaldoPagadoAt          *time.Time        `json:"saldo_pagado_at,omitempty"`
	HoldExpiraAt           *time.Time        `json:"hold_expira_at,omitempty"`
	PorValidarDesde        *time.Time        `json:"por_validar_desde,omitempty"`
	AlertaDemora           bool              `json:"alerta_demora"`
	CodigoQR               *string           `json:"codigo_qr,omitempty"`
	CheckinAt              *time.Time        `json:"checkin_at,omitempty"`
	CanceladoAt            *time.Time        `json:"cancelado_at,omitempty"`
	MotivoCancelacion      *string           `json:"motivo_cancelacion,omitempty"`
	ReembolsoPendiente     bool              `json:"reembolso_pendiente"`
	ReembolsoResueltoAt    *time.Time        `json:"reembolso_resuelto_at,omitempty"`
	ReprogramacionesCount  int16             `json:"reprogramaciones_count"`
	ExcedenteReembolso     float64           `json:"excedente_reembolso,omitempty"`
	Comprobantes           []ComprobanteInfo `json:"comprobantes"`
	CreatedAt              time.Time         `json:"created_at"`
	UpdatedAt              time.Time         `json:"updated_at"`
}

// RechazarComprobanteRequest representa el cuerpo de POST /api/v1/dueno/reservas/{reservaID}/rechazar.
type RechazarComprobanteRequest struct {
	Motivo string `json:"motivo"`
}

// CreateExternaRequest representa el cuerpo de POST /api/v1/dueno/reservas/externas.
type CreateExternaRequest struct {
	CanchaID               string   `json:"cancha_id"`
	Inicio                 string   `json:"inicio"`
	Fin                    string   `json:"fin"`
	ClienteExternoNombre   string   `json:"cliente_externo_nombre"`
	ClienteExternoTelefono string   `json:"cliente_externo_telefono,omitempty"`
	MontoTotal             *float64 `json:"monto_total,omitempty"`
	MontoAdelanto          *float64 `json:"monto_adelanto,omitempty"`
	Notas                  string   `json:"notas,omitempty"`
}

// CreateBloqueoRequest representa el cuerpo de POST /api/v1/dueno/bloqueos.
type CreateBloqueoRequest struct {
	CanchaID      string `json:"cancha_id"`
	Inicio        string `json:"inicio"`
	Fin           string `json:"fin"`
	MotivoBloqueo string `json:"motivo_bloqueo"`
}

// CheckinRequest representa el cuerpo de POST /api/v1/dueno/reservas/{reservaID}/checkin.
type CheckinRequest struct {
	MetodoPagoSaldo string `json:"metodo_pago_saldo,omitempty"`
}

// NoShowRequest representa el cuerpo opcional de POST /api/v1/dueno/reservas/{reservaID}/no-show.
type NoShowRequest struct {
	Motivo string `json:"motivo,omitempty"`
}

// CancelarReservaRequest representa el cuerpo de cancelación para jugador (motivo opcional) o dueño (motivo obligatorio).
type CancelarReservaRequest struct {
	Motivo string `json:"motivo,omitempty"`
}

// ReembolsoResueltoRequest representa el cuerpo opcional de POST /api/v1/dueno/reservas/{reservaID}/reembolso-resuelto.
type ReembolsoResueltoRequest struct {
	Motivo string `json:"motivo,omitempty"`
}

// ReprogramarReservaRequest representa el cuerpo de reprogramación para jugador o dueño (Decisión 4).
type ReprogramarReservaRequest struct {
	CanchaID    string `json:"cancha_id,omitempty"`
	NuevoInicio string `json:"nuevo_inicio"`
	NuevoFin    string `json:"nuevo_fin"`
	Motivo      string `json:"motivo,omitempty"`
}

// ---------- Parámetros transaccionales adicionales (Fase 3B) ----------

// CreateExternaTxInput encapsula los datos validados para registrar una reserva externa atómicamente.
type CreateExternaTxInput struct {
	ComplejoID             uuid.UUID
	DuenoUserID            uuid.UUID
	CanchaID               uuid.UUID
	Inicio                 time.Time
	Fin                    time.Time
	ClienteExternoNombre   string
	ClienteExternoTelefono *string
	MontoTotalOverride     *float64
	MontoAdelanto          *float64
	NotasInternas          *string
	CodigoQR               string
}

// CreateBloqueoTxInput encapsula los datos validados para registrar un bloqueo de mantenimiento.
type CreateBloqueoTxInput struct {
	ComplejoID    uuid.UUID
	DuenoUserID   uuid.UUID
	CanchaID      uuid.UUID
	Inicio        time.Time
	Fin           time.Time
	MotivoBloqueo string
}

// ReprogramarTxInput encapsula los datos validados para reprogramar una reserva en el sitio (Decisión 4).
type ReprogramarTxInput struct {
	ReservaID   uuid.UUID
	ComplejoID  *uuid.UUID // Presente cuando ejecuta el dueño
	JugadorID   *uuid.UUID // Presente cuando ejecuta el jugador
	ActorID     uuid.UUID
	IsDueno     bool
	CanchaID    *uuid.UUID // Nil = conservar cancha actual
	NuevoInicio time.Time
	NuevoFin    time.Time
	Motivo      string
}

// ---------- Parámetros de escritura transaccional ----------

// CreateHoldParams contiene los valores recalculados en servidor para persistir el Hold atómicamente.
type CreateHoldParams struct {
	ComplejoID     uuid.UUID
	CanchaID       uuid.UUID
	JugadorID      uuid.UUID
	Inicio         time.Time
	Fin            time.Time
	CuponID        *uuid.UUID
	PrecioBase     float64
	MontoDescuento float64
	MontoTotal     float64
	MontoAdelanto  float64
	SaldoPendiente float64
	HoldDuracion   int16
}

// CreateHoldResult devuelve la reserva creada, los medios de cobro activos y la hora del servidor DB.
type CreateHoldResult struct {
	Reserva     ReservaResumen
	MetodosPago []MetodoPagoPublico
	ServerNow   time.Time
}

// RegisterComprobanteParams agrupa los datos para insertar comprobante y pasar la reserva a POR_VALIDAR.
type RegisterComprobanteParams struct {
	ComprobanteID       uuid.UUID
	ReservaID           uuid.UUID
	JugadorID           uuid.UUID
	MetodoPagoID        uuid.UUID
	CodigoOperacion     string
	MontoDeclarado      float64
	EvidenciaStorageKey string
}
