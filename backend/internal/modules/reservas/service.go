package reservas

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/db"
	apphttp "github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/http"
)

var (
	slugFormatRegex = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	validEstados    = map[string]struct{}{
		EstadoPendientePago: {},
		EstadoPorValidar:    {},
		EstadoConfirmada:    {},
		EstadoCompletada:    {},
		EstadoExpirada:      {},
		EstadoRechazada:     {},
		EstadoCancelada:     {},
		EstadoNoShow:        {},
	}
)

// Service concentra la lógica de negocio de disponibilidad, cotización, Holds y comprobantes.
type Service struct {
	repo           Store
	storage        db.FileStorage
	pushDispatcher PushDispatcher
	logger         *slog.Logger
	now            func() time.Time
}

// NewService crea una nueva instancia del servicio de reservas.
func NewService(repo Store, storage db.FileStorage, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		repo:    repo,
		storage: storage,
		logger:  logger,
		now:     time.Now,
	}
}

// WithPushDispatcher inyecta el despachador opcional de notificaciones Web Push (VAPID).
func (s *Service) WithPushDispatcher(dispatcher PushDispatcher) *Service {
	s.pushDispatcher = dispatcher
	return s
}

// GetPushConfig expone la configuración pública de Web Push PWA (enabled y vapid_public_key).
func (s *Service) GetPushConfig() PushConfigResponse {
	if s.pushDispatcher == nil || !s.pushDispatcher.Enabled() {
		return PushConfigResponse{
			Enabled:        false,
			VAPIDPublicKey: "",
		}
	}
	return PushConfigResponse{
		Enabled:        true,
		VAPIDPublicKey: s.pushDispatcher.VAPIDPublicKey(),
	}
}

func (s *Service) dispatchPushAsync(ctx context.Context, msg PushMessage) {
	if s.pushDispatcher != nil && s.pushDispatcher.Enabled() {
		s.pushDispatcher.DispatchAsync(ctx, msg)
	}
}

// GetDisponibilidad genera la grilla en vivo de una cancha para una fecha en America/Lima (RF-10).
func (s *Service) GetDisponibilidad(ctx context.Context, rawSlug string, canchaID uuid.UUID, rawFecha string) (DisponibilidadResponse, error) {
	slug := strings.ToLower(strings.TrimSpace(rawSlug))
	if slug == "" || len(slug) > 120 || !slugFormatRegex.MatchString(slug) {
		return DisponibilidadResponse{}, ErrComplejoNotFound
	}

	fields := make(map[string]string)
	if canchaID == uuid.Nil {
		fields["cancha_id"] = "El parámetro cancha_id es obligatorio y debe ser un UUID válido"
	}

	fecha := strings.TrimSpace(rawFecha)
	var dayStart time.Time
	if fecha == "" {
		fields["fecha"] = "El parámetro fecha es obligatorio en formato YYYY-MM-DD"
	} else {
		parsedDay, err := ParseFechaLima(fecha)
		if err != nil {
			fields["fecha"] = "El parámetro fecha debe tener formato válido YYYY-MM-DD"
		} else {
			dayStart = parsedDay
		}
	}

	if len(fields) > 0 {
		return DisponibilidadResponse{}, &ValidationError{
			Message: "Parámetros de disponibilidad inválidos",
			Fields:  fields,
		}
	}

	complejo, err := s.repo.GetComplejoBySlug(ctx, slug)
	if err != nil {
		return DisponibilidadResponse{}, err
	}

	now := s.now()
	if !isSuscripcionVigente(complejo, now) {
		return DisponibilidadResponse{}, ErrComplejoNoDisponible
	}

	cancha, err := s.repo.GetActiveCancha(ctx, complejo.ID, canchaID)
	if err != nil {
		return DisponibilidadResponse{}, err
	}

	diaSemana := int16(dayStart.Weekday())
	horario, err := s.repo.GetHorarioByDia(ctx, complejo.ID, diaSemana)
	if err != nil {
		return DisponibilidadResponse{}, err
	}

	resp := DisponibilidadResponse{
		ComplejoID:       complejo.ID,
		ComplejoSlug:     complejo.Slug,
		CanchaID:         cancha.ID,
		CanchaNombre:     cancha.Nombre,
		Fecha:            fecha,
		ZonaHoraria:      ZonaHorariaLima,
		UnidadReservaMin: complejo.UnidadReservaMin,
		Cerrado:          horario == nil || horario.Cerrado,
		Slots:            []DisponibilidadSlot{},
	}

	if resp.Cerrado {
		return resp, nil
	}

	apertura := FormatMinutes(int(horario.MinutoApertura))
	cierre := FormatMinutes(int(horario.MinutoCierre))
	resp.HoraApertura = &apertura
	resp.HoraCierre = &cierre

	tarifas, err := s.repo.ListActiveTarifasForDay(ctx, complejo.ID, cancha.ID, diaSemana)
	if err != nil {
		return DisponibilidadResponse{}, err
	}

	dayEnd := dayStart.Add(24 * time.Hour)
	ocupaciones, err := s.repo.ListOcupacionesRange(ctx, cancha.ID, dayStart, dayEnd, now)
	if err != nil {
		return DisponibilidadResponse{}, err
	}

	resp.Slots = BuildDaySlots(dayStart, horario, complejo.UnidadReservaMin, cancha.ID, tarifas, ocupaciones, now)
	return resp, nil
}

// Cotizar calcula el importe exacto de un bloque (incluyendo franjas mixtas RN-08 y cupones RN-09).
func (s *Service) Cotizar(ctx context.Context, rawSlug string, req CotizarRequest) (CotizacionResponse, error) {
	slug := strings.ToLower(strings.TrimSpace(rawSlug))
	if slug == "" || len(slug) > 120 || !slugFormatRegex.MatchString(slug) {
		return CotizacionResponse{}, ErrComplejoNotFound
	}

	fields := make(map[string]string)

	canchaID, err := uuid.Parse(strings.TrimSpace(req.CanchaID))
	if err != nil || canchaID == uuid.Nil {
		fields["cancha_id"] = "cancha_id debe ser un UUID válido"
	}

	inicio, err := parseISO8601(req.Inicio)
	if err != nil {
		fields["inicio"] = "inicio debe ser una fecha y hora ISO8601 válida con zona horaria (ej. 2026-11-26T17:00:00-05:00)"
	}

	fin, err := parseISO8601(req.Fin)
	if err != nil {
		fields["fin"] = "fin debe ser una fecha y hora ISO8601 válida con zona horaria (ej. 2026-11-26T19:00:00-05:00)"
	}

	codigoCupon := strings.ToUpper(strings.TrimSpace(req.CodigoCupon))
	if utf8.RuneCountInString(codigoCupon) > 40 {
		fields["codigo_cupon"] = "codigo_cupon no puede exceder 40 caracteres"
	}

	if len(fields) > 0 {
		return CotizacionResponse{}, &ValidationError{
			Message: "Datos de cotización inválidos",
			Fields:  fields,
		}
	}

	_, diaSemana, _, _, err := ExtractRangeMinutesLima(inicio, fin)
	if err != nil {
		return CotizacionResponse{}, err
	}

	complejo, err := s.repo.GetComplejoBySlug(ctx, slug)
	if err != nil {
		return CotizacionResponse{}, err
	}

	now := s.now()
	if !isSuscripcionVigente(complejo, now) {
		return CotizacionResponse{}, ErrComplejoNoDisponible
	}

	cancha, err := s.repo.GetActiveCancha(ctx, complejo.ID, canchaID)
	if err != nil {
		return CotizacionResponse{}, err
	}

	horario, err := s.repo.GetHorarioByDia(ctx, complejo.ID, diaSemana)
	if err != nil {
		return CotizacionResponse{}, err
	}

	tarifas, err := s.repo.ListActiveTarifasForDay(ctx, complejo.ID, cancha.ID, diaSemana)
	if err != nil {
		return CotizacionResponse{}, err
	}

	var cuponPtr *CuponRecord
	if codigoCupon != "" {
		cuponRec, err := s.repo.GetCuponByCodigo(ctx, complejo.ID, codigoCupon)
		if err != nil {
			return CotizacionResponse{}, err
		}
		cuponPtr = &cuponRec
	}

	pricing, err := CalculatePricing(complejo, cancha.ID, horario, tarifas, inicio, fin, cuponPtr, now)
	if err != nil {
		return CotizacionResponse{}, err
	}

	ocupaciones, err := s.repo.ListOcupacionesRange(ctx, cancha.ID, pricing.Inicio, pricing.Fin, now)
	if err != nil {
		return CotizacionResponse{}, err
	}

	terminos := ""
	if complejo.TerminosReserva != nil {
		terminos = *complejo.TerminosReserva
	}

	return CotizacionResponse{
		ComplejoID:          complejo.ID,
		CanchaID:            cancha.ID,
		Inicio:              pricing.Inicio,
		Fin:                 pricing.Fin,
		DuracionMin:         pricing.DuracionMin,
		Disponible:          len(ocupaciones) == 0,
		PrecioBase:          pricing.PrecioBase,
		MontoDescuento:      pricing.MontoDescuento,
		MontoTotal:          pricing.MontoTotal,
		TipoAdelanto:        complejo.TipoAdelanto,
		ValorAdelanto:       fromCents(toCents(complejo.ValorAdelanto)),
		MontoAdelanto:       pricing.MontoAdelanto,
		SaldoPendiente:      pricing.SaldoPendiente,
		HoldDuracionMin:     complejo.HoldDuracionMin,
		HorasMinCancelacion: complejo.HorasMinCancelacion,
		IncluyeHoraMuerta:   pricing.IncluyeHoraMuerta,
		CuponAplicado:       pricing.CuponAplicado,
		Desglose:            pricing.Desglose,
		TerminosReserva:     terminos,
	}, nil
}

// CreateReserva valida la aceptación de términos (Decisión 2) y crea un Hold atómico
// con recálculo completo en servidor y control anticolisión GIST (RN-04, RN-05).
func (s *Service) CreateReserva(ctx context.Context, jugadorID uuid.UUID, req CreateReservaRequest) (CreateReservaResponse, error) {
	fields := make(map[string]string)

	if !req.AceptaTerminos {
		fields["acepta_terminos"] = "Debes aceptar los términos y condiciones de reserva para continuar"
	}

	complejoID, err := uuid.Parse(strings.TrimSpace(req.ComplejoID))
	if err != nil || complejoID == uuid.Nil {
		fields["complejo_id"] = "complejo_id debe ser un UUID válido"
	}

	canchaID, err := uuid.Parse(strings.TrimSpace(req.CanchaID))
	if err != nil || canchaID == uuid.Nil {
		fields["cancha_id"] = "cancha_id debe ser un UUID válido"
	}

	inicio, err := parseISO8601(req.Inicio)
	if err != nil {
		fields["inicio"] = "inicio debe ser una fecha y hora ISO8601 válida con zona horaria"
	}

	fin, err := parseISO8601(req.Fin)
	if err != nil {
		fields["fin"] = "fin debe ser una fecha y hora ISO8601 válida con zona horaria"
	}

	codigoCupon := strings.ToUpper(strings.TrimSpace(req.CodigoCupon))
	if utf8.RuneCountInString(codigoCupon) > 40 {
		fields["codigo_cupon"] = "codigo_cupon no puede exceder 40 caracteres"
	}

	if len(fields) > 0 {
		return CreateReservaResponse{}, &ValidationError{
			Message: "Datos de reserva inválidos",
			Fields:  fields,
		}
	}

	if _, _, _, _, err := ExtractRangeMinutesLima(inicio, fin); err != nil {
		return CreateReservaResponse{}, err
	}

	txOut, err := s.repo.CreateHoldTx(ctx, CreateHoldTxInput{
		ComplejoID:  complejoID,
		CanchaID:    canchaID,
		JugadorID:   jugadorID,
		Inicio:      inicio,
		Fin:         fin,
		CodigoCupon: codigoCupon,
	})
	if err != nil {
		return CreateReservaResponse{}, err
	}

	metodos := txOut.MetodosPago
	if metodos == nil {
		metodos = []MetodoPagoPublico{}
	}

	return CreateReservaResponse{
		Reserva:               txOut.Reserva,
		HoldSegundosRestantes: remainingSeconds(txOut.Reserva.HoldExpiraAt, txOut.ServerNow),
		CuponAplicado:         txOut.Pricing.CuponAplicado,
		Desglose:              txOut.Pricing.Desglose,
		MetodosPago:           metodos,
	}, nil
}

// UploadComprobante valida imagen real (JPG/PNG/WEBP <= 5 MB), guarda el archivo privado vía Storage
// y registra transaccionalmente el comprobante pasando la reserva a POR_VALIDAR (RF-11, RN-11).
func (s *Service) UploadComprobante(ctx context.Context, in UploadComprobanteInput) (UploadComprobanteResponse, error) {
	if s.storage == nil {
		return UploadComprobanteResponse{}, errors.New("almacenamiento de archivos no configurado")
	}

	fields := make(map[string]string)

	if in.ReservaID == uuid.Nil {
		fields["reserva_id"] = "Identificador de reserva inválido"
	}
	if in.MetodoPagoID == uuid.Nil {
		fields["metodo_pago_id"] = "metodo_pago_id es obligatorio y debe ser un UUID válido"
	}

	codigoOp := strings.TrimSpace(in.CodigoOperacion)
	if n := utf8.RuneCountInString(codigoOp); n == 0 || n > 80 {
		fields["codigo_operacion"] = "codigo_operacion es obligatorio y debe tener entre 1 y 80 caracteres"
	}

	if math.IsNaN(in.MontoDeclarado) || math.IsInf(in.MontoDeclarado, 0) || in.MontoDeclarado <= 0 || in.MontoDeclarado > MaxPrecioMonto {
		fields["monto_declarado"] = "monto_declarado debe ser un monto positivo válido"
	} else if math.Abs(in.MontoDeclarado*100.0-math.Round(in.MontoDeclarado*100.0)) > 1e-6 {
		fields["monto_declarado"] = "monto_declarado admite como máximo 2 decimales"
	}

	if len(in.FileData) == 0 {
		fields["evidencia"] = "La imagen de evidencia es obligatoria"
	}

	if len(fields) > 0 {
		return UploadComprobanteResponse{}, &ValidationError{
			Message: "Datos de comprobante inválidos",
			Fields:  fields,
		}
	}

	if len(in.FileData) > MaxEvidenciaBytes {
		return UploadComprobanteResponse{}, ErrFileTooLarge
	}

	mimeType, ext, ok := detectAllowedImageMIME(in.FileData)
	if !ok {
		return UploadComprobanteResponse{}, ErrInvalidFileType
	}

	complejoID, err := s.repo.PrecheckUploadComprobante(ctx, in.ReservaID, in.JugadorID, in.MetodoPagoID)
	if err != nil {
		return UploadComprobanteResponse{}, err
	}

	comprobanteID := uuid.New()
	storageKey := fmt.Sprintf("comprobantes/%s/%s/%s%s", complejoID.String(), in.ReservaID.String(), comprobanteID.String(), ext)

	if err := s.storage.Save(ctx, storageKey, mimeType, bytes.NewReader(in.FileData)); err != nil {
		return UploadComprobanteResponse{}, fmt.Errorf("error guardando evidencia de comprobante: %w", err)
	}

	resp, err := s.repo.RegisterComprobanteTx(ctx, RegisterComprobanteParams{
		ComprobanteID:       comprobanteID,
		ReservaID:           in.ReservaID,
		JugadorID:           in.JugadorID,
		MetodoPagoID:        in.MetodoPagoID,
		CodigoOperacion:     codigoOp,
		MontoDeclarado:      fromCents(toCents(in.MontoDeclarado)),
		EvidenciaStorageKey: storageKey,
	})
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if delErr := s.storage.Delete(cleanupCtx, storageKey); delErr != nil {
			s.logger.Warn("comprobante_orphan_cleanup_error",
				slog.String("storage_key", storageKey),
				slog.String("error", delErr.Error()),
			)
		}
		return UploadComprobanteResponse{}, err
	}

	return resp, nil
}

// ListJugadorReservas devuelve el historial y reservas vigentes del jugador autenticado (CU-13).
func (s *Service) ListJugadorReservas(ctx context.Context, jugadorID uuid.UUID, filter ListJugadorReservasFilter) (JugadorReservasListResponse, error) {
	fields := make(map[string]string)

	estado := strings.ToUpper(strings.TrimSpace(filter.Estado))
	if estado != "" {
		if _, ok := validEstados[estado]; !ok {
			fields["estado"] = "Estado de reserva inválido"
		}
	}

	limit := filter.Limit
	if limit == 0 {
		limit = DefaultListLimit
	} else if limit < 1 || limit > MaxListLimit {
		fields["limit"] = "limit debe estar entre 1 y 50"
	}

	if filter.Offset < 0 {
		fields["offset"] = "offset debe ser mayor o igual a 0"
	}

	if len(fields) > 0 {
		return JugadorReservasListResponse{}, &ValidationError{
			Message: "Parámetros de listado inválidos",
			Fields:  fields,
		}
	}

	normalized := ListJugadorReservasFilter{
		Estado: estado,
		Limit:  limit,
		Offset: filter.Offset,
	}

	items, total, serverNow, err := s.repo.ListJugadorReservas(ctx, jugadorID, normalized)
	if err != nil {
		return JugadorReservasListResponse{}, err
	}
	if items == nil {
		items = []JugadorReservaItem{}
	}

	for i := range items {
		if !allowsQRDisplay(items[i].Estado) {
			items[i].CodigoQR = nil
		}
		if items[i].Estado == EstadoPendientePago && items[i].HoldExpiraAt != nil {
			rem := remainingSeconds(*items[i].HoldExpiraAt, serverNow)
			items[i].HoldSegundosRestantes = &rem
		}
	}

	return JugadorReservasListResponse{
		Items:  items,
		Total:  total,
		Limit:  limit,
		Offset: filter.Offset,
	}, nil
}

// GetJugadorReservaDetail devuelve el detalle completo con coordenadas/referencias para Modo En Ruta.
// Expone codigo_qr únicamente si el estado es CONFIRMADA o COMPLETADA.
func (s *Service) GetJugadorReservaDetail(ctx context.Context, jugadorID, reservaID uuid.UUID) (JugadorReservaDetail, error) {
	detail, serverNow, err := s.repo.GetJugadorReservaDetail(ctx, reservaID)
	if err != nil {
		return JugadorReservaDetail{}, err
	}

	if detail.JugadorID != jugadorID {
		return JugadorReservaDetail{}, ErrReservaForbidden
	}

	if !allowsQRDisplay(detail.Estado) {
		detail.CodigoQR = nil
	}
	if detail.Estado == EstadoPendientePago && detail.HoldExpiraAt != nil {
		rem := remainingSeconds(*detail.HoldExpiraAt, serverNow)
		detail.HoldSegundosRestantes = &rem
	}
	if detail.Comprobantes == nil {
		detail.Comprobantes = []ComprobanteInfo{}
	}

	return detail, nil
}

// OpenComprobanteEvidencia valida que el solicitante sea el jugador titular de la reserva
// o el dueño del complejo propietario y abre el flujo binario desde Storage.
func (s *Service) OpenComprobanteEvidencia(ctx context.Context, actor apphttp.AuthUser, comprobanteID uuid.UUID) (EvidenciaStream, error) {
	if s.storage == nil {
		return EvidenciaStream{}, errors.New("almacenamiento de archivos no configurado")
	}

	rec, err := s.repo.GetComprobanteAccess(ctx, comprobanteID)
	if err != nil {
		return EvidenciaStream{}, err
	}

	isOwnerJugador := actor.Rol == apphttp.RoleJugador && rec.JugadorID != nil && *rec.JugadorID == actor.UserID
	isOwnerDueno := actor.Rol == apphttp.RoleDueno && rec.DuenoID == actor.UserID
	if !isOwnerJugador && !isOwnerDueno {
		return EvidenciaStream{}, ErrEvidenciaForbidden
	}

	if rec.EvidenciaStorageKey == nil || strings.TrimSpace(*rec.EvidenciaStorageKey) == "" {
		return EvidenciaStream{}, ErrEvidenciaNotFound
	}

	rc, err := s.storage.Open(ctx, *rec.EvidenciaStorageKey)
	if err != nil {
		if errors.Is(err, db.ErrObjectNotFound) {
			return EvidenciaStream{}, ErrEvidenciaNotFound
		}
		return EvidenciaStream{}, fmt.Errorf("error abriendo archivo de evidencia: %w", err)
	}

	head := make([]byte, 512)
	n, readErr := io.ReadFull(rc, head)
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		_ = rc.Close()
		return EvidenciaStream{}, fmt.Errorf("error leyendo cabecera de evidencia: %w", readErr)
	}
	prefix := head[:n]

	mimeType, ext, ok := detectAllowedImageMIME(prefix)
	if !ok {
		mimeType = "application/octet-stream"
		ext = ".bin"
	}

	return EvidenciaStream{
		Reader:      rc,
		Prefix:      prefix,
		ContentType: mimeType,
		FileName:    fmt.Sprintf("comprobante-%s%s", comprobanteID.String(), ext),
	}, nil
}

func allowsQRDisplay(estado string) bool {
	return estado == EstadoConfirmada || estado == EstadoCompletada
}

func remainingSeconds(expiraAt, now time.Time) int {
	secs := int(math.Ceil(expiraAt.Sub(now).Seconds()))
	if secs < 0 {
		return 0
	}
	return secs
}

func parseISO8601(raw string) (time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, errors.New("fecha/hora vacía")
	}
	return time.Parse(time.RFC3339Nano, trimmed)
}

func detectAllowedImageMIME(data []byte) (mimeType string, ext string, ok bool) {
	return db.DetectAllowedImageMIME(data)
}
