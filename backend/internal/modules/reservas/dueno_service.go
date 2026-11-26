package reservas

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

var (
	validOrigenes = map[string]struct{}{
		OrigenApp:                  {},
		OrigenExterna:              {},
		OrigenBloqueoMantenimiento: {},
	}
	validMetodosPagoSaldo = map[string]struct{}{
		MetodoSaldoEfectivo:      {},
		MetodoSaldoYape:          {},
		MetodoSaldoPlin:          {},
		MetodoSaldoTransferencia: {},
		MetodoSaldoTarjeta:       {},
		MetodoSaldoOtro:          {},
	}
)

// ListDuenoReservas lista agenda y bandeja del complejo con filtros y alerta_demora > 30 min (Decisión 3).
func (s *Service) ListDuenoReservas(ctx context.Context, complejoID uuid.UUID, filter ListDuenoReservasFilter) (DuenoReservasListResponse, error) {
	fields := make(map[string]string)

	var desdePtr, hastaEndPtr *time.Time
	var parsedDesde, parsedHasta time.Time

	if raw := strings.TrimSpace(filter.FechaDesde); raw != "" {
		d, err := ParseFechaLima(raw)
		if err != nil {
			fields["fecha_desde"] = "fecha_desde debe tener formato válido YYYY-MM-DD"
		} else {
			parsedDesde = d
			desdePtr = &d
		}
	}
	if raw := strings.TrimSpace(filter.FechaHasta); raw != "" {
		h, err := ParseFechaLima(raw)
		if err != nil {
			fields["fecha_hasta"] = "fecha_hasta debe tener formato válido YYYY-MM-DD"
		} else {
			parsedHasta = h
			end := h.AddDate(0, 0, 1)
			hastaEndPtr = &end
		}
	}
	if desdePtr != nil && hastaEndPtr != nil && parsedDesde.After(parsedHasta) {
		fields["fecha_hasta"] = "fecha_hasta debe ser mayor o igual a fecha_desde"
	}

	estado := strings.ToUpper(strings.TrimSpace(filter.Estado))
	if estado != "" {
		if _, ok := validEstados[estado]; !ok {
			fields["estado"] = "Estado de reserva inválido"
		}
	}

	origen := strings.ToUpper(strings.TrimSpace(filter.Origen))
	if origen != "" {
		if _, ok := validOrigenes[origen]; !ok {
			fields["origen"] = "Origen de reserva inválido (permitidos: APP, EXTERNA, BLOQUEO_MANTENIMIENTO)"
		}
	}

	limit := filter.Limit
	if limit == 0 {
		limit = DefaultDuenoListLimit
	} else if limit < 1 || limit > MaxDuenoListLimit {
		fields["limit"] = "limit debe estar entre 1 y 100"
	}

	if filter.Offset < 0 {
		fields["offset"] = "offset debe ser mayor o igual a 0"
	}

	if len(fields) > 0 {
		return DuenoReservasListResponse{}, &ValidationError{
			Message: "Parámetros de agenda inválidos",
			Fields:  fields,
		}
	}

	items, total, err := s.repo.ListDuenoReservas(ctx, complejoID, DuenoReservasQueryParams{
		Desde:    desdePtr,
		HastaEnd: hastaEndPtr,
		CanchaID: filter.CanchaID,
		Estado:   estado,
		Origen:   origen,
		Limit:    limit,
		Offset:   filter.Offset,
	})
	if err != nil {
		return DuenoReservasListResponse{}, err
	}
	if items == nil {
		items = []DuenoReservaItem{}
	}

	return DuenoReservasListResponse{
		Items:  items,
		Total:  total,
		Limit:  limit,
		Offset: filter.Offset,
	}, nil
}

// AprobarComprobante valida la transición POR_VALIDAR -> CONFIRMADA generando un código QR único seguro.
func (s *Service) AprobarComprobante(ctx context.Context, complejoID, reservaID, duenoUserID uuid.UUID) (DuenoReservaDetail, error) {
	codigoQR, err := generateCodigoQR()
	if err != nil {
		return DuenoReservaDetail{}, fmt.Errorf("error generando código QR: %w", err)
	}
	return s.repo.AprobarComprobanteTx(ctx, complejoID, reservaID, duenoUserID, codigoQR)
}

// RechazarComprobante exige un motivo obligatorio, marca RECHAZADA, revierte cupón y notifica al jugador.
func (s *Service) RechazarComprobante(ctx context.Context, complejoID, reservaID, duenoUserID uuid.UUID, req RechazarComprobanteRequest) (DuenoReservaDetail, error) {
	motivo := strings.TrimSpace(req.Motivo)
	if n := utf8.RuneCountInString(motivo); n == 0 || n > 255 {
		return DuenoReservaDetail{}, &ValidationError{
			Message: "Datos de rechazo inválidos",
			Fields: map[string]string{
				"motivo": "motivo es obligatorio y debe tener entre 1 y 255 caracteres",
			},
		}
	}
	return s.repo.RechazarComprobanteTx(ctx, complejoID, reservaID, duenoUserID, motivo)
}

// CreateExterna valida y registra una reserva externa confirmada con bloqueo GIST y código QR (RF-14).
func (s *Service) CreateExterna(ctx context.Context, complejoID, duenoUserID uuid.UUID, req CreateExternaRequest) (DuenoReservaDetail, error) {
	fields := make(map[string]string)

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

	clienteNombre := strings.TrimSpace(req.ClienteExternoNombre)
	if n := utf8.RuneCountInString(clienteNombre); n == 0 || n > 150 {
		fields["cliente_externo_nombre"] = "cliente_externo_nombre es obligatorio y debe tener entre 1 y 150 caracteres"
	}

	var telefonoPtr *string
	if tel := strings.TrimSpace(req.ClienteExternoTelefono); tel != "" {
		if utf8.RuneCountInString(tel) > 20 {
			fields["cliente_externo_telefono"] = "cliente_externo_telefono no puede exceder 20 caracteres"
		} else {
			telefonoPtr = &tel
		}
	}

	var montoTotalPtr *float64
	if req.MontoTotal != nil {
		if !isValidMoney(*req.MontoTotal, true) {
			fields["monto_total"] = "monto_total debe ser un monto válido mayor o igual a 0 con hasta 2 decimales"
		} else {
			v := fromCents(toCents(*req.MontoTotal))
			montoTotalPtr = &v
		}
	}

	var montoAdelantoPtr *float64
	if req.MontoAdelanto != nil {
		if !isValidMoney(*req.MontoAdelanto, true) {
			fields["monto_adelanto"] = "monto_adelanto debe ser un monto válido mayor o igual a 0 con hasta 2 decimales"
		} else {
			v := fromCents(toCents(*req.MontoAdelanto))
			montoAdelantoPtr = &v
		}
	}

	var notasPtr *string
	if notas := strings.TrimSpace(req.Notas); notas != "" {
		if utf8.RuneCountInString(notas) > 1000 {
			fields["notas"] = "notas no puede exceder 1000 caracteres"
		} else {
			notasPtr = &notas
		}
	}

	if len(fields) > 0 {
		return DuenoReservaDetail{}, &ValidationError{
			Message: "Datos de reserva externa inválidos",
			Fields:  fields,
		}
	}

	if _, _, _, _, err := ExtractRangeMinutesLima(inicio, fin); err != nil {
		return DuenoReservaDetail{}, err
	}

	codigoQR, err := generateCodigoQR()
	if err != nil {
		return DuenoReservaDetail{}, fmt.Errorf("error generando código QR: %w", err)
	}

	return s.repo.CreateExternaTx(ctx, CreateExternaTxInput{
		ComplejoID:             complejoID,
		DuenoUserID:            duenoUserID,
		CanchaID:               canchaID,
		Inicio:                 inicio,
		Fin:                    fin,
		ClienteExternoNombre:   clienteNombre,
		ClienteExternoTelefono: telefonoPtr,
		MontoTotalOverride:     montoTotalPtr,
		MontoAdelanto:          montoAdelantoPtr,
		NotasInternas:          notasPtr,
		CodigoQR:               codigoQR,
	})
}

// CreateBloqueo registra un bloqueo de mantenimiento en tiempo real con montos en 0 y GIST (RN-06).
func (s *Service) CreateBloqueo(ctx context.Context, complejoID, duenoUserID uuid.UUID, req CreateBloqueoRequest) (DuenoReservaDetail, error) {
	fields := make(map[string]string)

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

	motivo := strings.TrimSpace(req.MotivoBloqueo)
	if n := utf8.RuneCountInString(motivo); n == 0 || n > 255 {
		fields["motivo_bloqueo"] = "motivo_bloqueo es obligatorio y debe tener entre 1 y 255 caracteres"
	}

	if len(fields) > 0 {
		return DuenoReservaDetail{}, &ValidationError{
			Message: "Datos de bloqueo de mantenimiento inválidos",
			Fields:  fields,
		}
	}

	if _, _, _, _, err := ExtractRangeMinutesLima(inicio, fin); err != nil {
		return DuenoReservaDetail{}, err
	}

	return s.repo.CreateBloqueoTx(ctx, CreateBloqueoTxInput{
		ComplejoID:    complejoID,
		DuenoUserID:   duenoUserID,
		CanchaID:      canchaID,
		Inicio:        inicio,
		Fin:           fin,
		MotivoBloqueo: motivo,
	})
}

// GetDuenoReservaByQR localiza una reserva CONFIRMADA o COMPLETADA del complejo por su código QR.
func (s *Service) GetDuenoReservaByQR(ctx context.Context, complejoID uuid.UUID, rawQR string) (DuenoReservaDetail, error) {
	codigoQR := strings.TrimSpace(rawQR)
	if codigoQR == "" || utf8.RuneCountInString(codigoQR) > 120 {
		return DuenoReservaDetail{}, ErrReservaNotFound
	}
	return s.repo.GetDuenoReservaByQR(ctx, complejoID, codigoQR)
}

// CheckinReserva valida el método de pago de saldo (si se envió) y completa el check-in en BD.
func (s *Service) CheckinReserva(ctx context.Context, complejoID, reservaID, duenoUserID uuid.UUID, req CheckinRequest) (DuenoReservaDetail, error) {
	metodo := strings.ToUpper(strings.TrimSpace(req.MetodoPagoSaldo))
	if metodo != "" {
		if _, ok := validMetodosPagoSaldo[metodo]; !ok {
			return DuenoReservaDetail{}, &ValidationError{
				Message: "Datos de check-in inválidos",
				Fields: map[string]string{
					"metodo_pago_saldo": "metodo_pago_saldo inválido (permitidos: EFECTIVO, YAPE, PLIN, TRANSFERENCIA, TARJETA, OTRO)",
				},
			}
		}
	}
	return s.repo.CheckinReservaTx(ctx, complejoID, reservaID, duenoUserID, metodo)
}

// MarkNoShow registra inasistencia de una reserva confirmada reteniendo el adelanto.
func (s *Service) MarkNoShow(ctx context.Context, complejoID, reservaID, duenoUserID uuid.UUID, req NoShowRequest) (DuenoReservaDetail, error) {
	motivo := strings.TrimSpace(req.Motivo)
	if utf8.RuneCountInString(motivo) > 500 {
		return DuenoReservaDetail{}, &ValidationError{
			Message: "Datos de No-Show inválidos",
			Fields:  map[string]string{"motivo": "motivo no puede exceder 500 caracteres"},
		}
	}
	return s.repo.MarkNoShowTx(ctx, complejoID, reservaID, duenoUserID, motivo)
}

// CancelReservaJugador cancela una reserva del jugador respetando ventana RN-13 y reembolso RN-14.
func (s *Service) CancelReservaJugador(ctx context.Context, jugadorID, reservaID uuid.UUID, req CancelarReservaRequest) (JugadorReservaDetail, error) {
	motivo := strings.TrimSpace(req.Motivo)
	if utf8.RuneCountInString(motivo) > 500 {
		return JugadorReservaDetail{}, &ValidationError{
			Message: "Datos de cancelación inválidos",
			Fields:  map[string]string{"motivo": "motivo no puede exceder 500 caracteres"},
		}
	}
	return s.repo.CancelReservaJugadorTx(ctx, jugadorID, reservaID, motivo)
}

// CancelReservaDueno exige motivo obligatorio para cancelar cualquier reserva activa del complejo.
func (s *Service) CancelReservaDueno(ctx context.Context, complejoID, reservaID, duenoUserID uuid.UUID, req CancelarReservaRequest) (DuenoReservaDetail, error) {
	motivo := strings.TrimSpace(req.Motivo)
	if n := utf8.RuneCountInString(motivo); n == 0 || n > 500 {
		return DuenoReservaDetail{}, &ValidationError{
			Message: "Datos de cancelación inválidos",
			Fields: map[string]string{
				"motivo": "motivo es obligatorio y debe tener entre 1 y 500 caracteres",
			},
		}
	}
	return s.repo.CancelReservaDuenoTx(ctx, complejoID, reservaID, duenoUserID, motivo)
}

// MarkReembolsoResuelto registra que el complejo resolvió directamente un reembolso pendiente (RN-14).
func (s *Service) MarkReembolsoResuelto(ctx context.Context, complejoID, reservaID, duenoUserID uuid.UUID, req ReembolsoResueltoRequest) (DuenoReservaDetail, error) {
	motivo := strings.TrimSpace(req.Motivo)
	if utf8.RuneCountInString(motivo) > 500 {
		return DuenoReservaDetail{}, &ValidationError{
			Message: "Datos inválidos",
			Fields:  map[string]string{"motivo": "motivo no puede exceder 500 caracteres"},
		}
	}
	return s.repo.MarkReembolsoResueltoTx(ctx, complejoID, reservaID, duenoUserID, motivo)
}

// ReprogramarJugador reprograma in-place una reserva confirmada del jugador dentro de la ventana RN-13.
func (s *Service) ReprogramarJugador(ctx context.Context, jugadorID, reservaID uuid.UUID, req ReprogramarReservaRequest) (JugadorReservaDetail, error) {
	in, err := parseReprogramRequest(req)
	if err != nil {
		return JugadorReservaDetail{}, err
	}
	in.ReservaID = reservaID
	in.JugadorID = &jugadorID
	in.ActorID = jugadorID
	in.IsDueno = false
	return s.repo.ReprogramarJugadorTx(ctx, in)
}

// ReprogramarDueno reprograma in-place una reserva confirmada del complejo (Decisión 4).
func (s *Service) ReprogramarDueno(ctx context.Context, complejoID, reservaID, duenoUserID uuid.UUID, req ReprogramarReservaRequest) (DuenoReservaDetail, error) {
	in, err := parseReprogramRequest(req)
	if err != nil {
		return DuenoReservaDetail{}, err
	}
	in.ReservaID = reservaID
	in.ComplejoID = &complejoID
	in.ActorID = duenoUserID
	in.IsDueno = true
	return s.repo.ReprogramarDuenoTx(ctx, in)
}

func parseReprogramRequest(req ReprogramarReservaRequest) (ReprogramarTxInput, error) {
	fields := make(map[string]string)

	var canchaPtr *uuid.UUID
	if rawCancha := strings.TrimSpace(req.CanchaID); rawCancha != "" {
		parsed, err := uuid.Parse(rawCancha)
		if err != nil || parsed == uuid.Nil {
			fields["cancha_id"] = "cancha_id debe ser un UUID válido"
		} else {
			canchaPtr = &parsed
		}
	}

	nuevoInicio, err := parseISO8601(req.NuevoInicio)
	if err != nil {
		fields["nuevo_inicio"] = "nuevo_inicio debe ser una fecha y hora ISO8601 válida con zona horaria"
	}

	nuevoFin, err := parseISO8601(req.NuevoFin)
	if err != nil {
		fields["nuevo_fin"] = "nuevo_fin debe ser una fecha y hora ISO8601 válida con zona horaria"
	}

	motivo := strings.TrimSpace(req.Motivo)
	if utf8.RuneCountInString(motivo) > 500 {
		fields["motivo"] = "motivo no puede exceder 500 caracteres"
	}

	if len(fields) > 0 {
		return ReprogramarTxInput{}, &ValidationError{
			Message: "Datos de reprogramación inválidos",
			Fields:  fields,
		}
	}

	if _, _, _, _, err := ExtractRangeMinutesLima(nuevoInicio, nuevoFin); err != nil {
		return ReprogramarTxInput{}, err
	}

	return ReprogramarTxInput{
		CanchaID:    canchaPtr,
		NuevoInicio: nuevoInicio,
		NuevoFin:    nuevoFin,
		Motivo:      motivo,
	}, nil
}

func isValidMoney(v float64, allowZero bool) bool {
	if math.IsNaN(v) || math.IsInf(v, 0) || v > MaxPrecioMonto {
		return false
	}
	if allowZero {
		if v < 0 {
			return false
		}
	} else if v <= 0 {
		return false
	}
	return math.Abs(v*100.0-math.Round(v*100.0)) <= 1e-6
}

func generateCodigoQR() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "FDP-" + strings.ToUpper(hex.EncodeToString(buf)), nil
}
