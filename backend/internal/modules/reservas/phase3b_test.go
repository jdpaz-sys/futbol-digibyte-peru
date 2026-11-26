package reservas

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apphttp "github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/http"
)

// ---------- Implementación de métodos Fase 3B en fakeStore ----------

func (f *fakeStore) ListDuenoReservas(_ context.Context, complejoID uuid.UUID, params DuenoReservasQueryParams) ([]DuenoReservaItem, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]DuenoReservaItem, 0)
	for _, r := range f.reservas {
		if r.ComplejoID != complejoID {
			continue
		}
		if params.Desde != nil && r.Inicio.Before(*params.Desde) {
			continue
		}
		if params.HastaEnd != nil && !r.Inicio.Before(*params.HastaEnd) {
			continue
		}
		if params.CanchaID != nil && r.CanchaID != *params.CanchaID {
			continue
		}
		if params.Estado != "" && r.Estado != params.Estado {
			continue
		}
		orig := r.Origen
		if orig == "" {
			orig = OrigenApp
		}
		if params.Origen != "" && orig != params.Origen {
			continue
		}
		cancha := f.canchas[r.CanchaID]
		item := DuenoReservaItem{
			ID:                     r.ID,
			ComplejoID:             r.ComplejoID,
			CanchaID:               r.CanchaID,
			CanchaNombre:           cancha.Nombre,
			CanchaModalidad:        cancha.Modalidad,
			Origen:                 orig,
			Estado:                 r.Estado,
			Inicio:                 r.Inicio,
			Fin:                    r.Fin,
			DuracionMin:            int(r.Fin.Sub(r.Inicio) / time.Minute),
			ClienteExternoNombre:   r.ClienteExternoNombre,
			ClienteExternoTelefono: r.ClienteExternoTelefono,
			MotivoBloqueo:          r.MotivoBloqueo,
			NotasInternas:          r.NotasInternas,
			PrecioBase:             r.PrecioBase,
			MontoDescuento:         r.MontoDescuento,
			MontoTotal:             r.MontoTotal,
			MontoAdelanto:          r.MontoAdelanto,
			SaldoPendiente:         r.SaldoPendiente,
			MetodoPagoSaldo:        r.MetodoPagoSaldo,
			SaldoPagadoAt:          r.SaldoPagadoAt,
			HoldExpiraAt:           r.HoldExpiraAt,
			PorValidarDesde:        r.PorValidarDesde,
			CheckinAt:              r.CheckinAt,
			CanceladoAt:            r.CanceladoAt,
			MotivoCancelacion:      r.MotivoCancelacion,
			ReembolsoPendiente:     r.ReembolsoPendiente,
			ReembolsoResueltoAt:    r.ReembolsoResueltoAt,
			ReprogramacionesCount:  r.ReprogramacionesCount,
			CreatedAt:              r.CreatedAt,
			UpdatedAt:              f.now,
		}
		if r.JugadorID != uuid.Nil {
			jid := r.JugadorID
			item.JugadorID = &jid
		}
		if allowsQRDisplay(r.Estado) {
			item.CodigoQR = r.CodigoQR
		}
		if r.Estado == EstadoPorValidar && r.PorValidarDesde != nil {
			wait := f.now.Sub(*r.PorValidarDesde)
			if wait < 0 {
				wait = 0
			}
			mins := int(wait / time.Minute)
			item.MinutosEsperaValidacion = &mins
			item.AlertaDemora = wait > AlertaDemoraThreshold
		}
		out = append(out, item)
	}
	return out, len(out), nil
}

func (f *fakeStore) AprobarComprobanteTx(_ context.Context, complejoID, reservaID, _ uuid.UUID, codigoQR string) (DuenoReservaDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	r, ok := f.reservas[reservaID]
	if !ok || r.ComplejoID != complejoID {
		return DuenoReservaDetail{}, ErrReservaNotFound
	}
	if r.Estado != EstadoPorValidar {
		return DuenoReservaDetail{}, ErrInvalidReservaState
	}

	r.Estado = EstadoConfirmada
	qr := codigoQR
	r.CodigoQR = &qr
	f.audits = append(f.audits, fakeAuditRecord{
		ReservaID:      r.ID,
		Accion:         AccionComprobanteAprobado,
		EstadoAnterior: EstadoPorValidar,
		EstadoNuevo:    EstadoConfirmada,
	})
	if r.JugadorID != uuid.Nil {
		f.pushFakeNotif(r.JugadorID, &r.ComplejoID, &r.ID, NotifReservaConfirmada, "Reserva confirmada", "Aprobada")
	}
	return f.buildFakeDuenoDetail(r), nil
}

func (f *fakeStore) RechazarComprobanteTx(_ context.Context, complejoID, reservaID, _ uuid.UUID, motivo string) (DuenoReservaDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	r, ok := f.reservas[reservaID]
	if !ok || r.ComplejoID != complejoID {
		return DuenoReservaDetail{}, ErrReservaNotFound
	}
	if r.Estado != EstadoPorValidar {
		return DuenoReservaDetail{}, ErrInvalidReservaState
	}

	r.Estado = EstadoRechazada
	if r.CuponID != nil {
		if c, exists := f.cupones[*r.CuponID]; exists && c.UsosActuales > 0 {
			c.UsosActuales--
		}
	}
	f.audits = append(f.audits, fakeAuditRecord{
		ReservaID:      r.ID,
		Accion:         AccionComprobanteRechazado,
		EstadoAnterior: EstadoPorValidar,
		EstadoNuevo:    EstadoRechazada,
		Motivo:         motivo,
	})
	if r.JugadorID != uuid.Nil {
		f.pushFakeNotif(r.JugadorID, &r.ComplejoID, &r.ID, NotifComprobanteRechazado, "Comprobante rechazado", motivo)
	}
	return f.buildFakeDuenoDetail(r), nil
}

func (f *fakeStore) CreateExternaTx(_ context.Context, in CreateExternaTxInput) (DuenoReservaDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	complejo, ok := f.complejos[in.ComplejoID]
	if !ok {
		return DuenoReservaDetail{}, ErrComplejoNotFound
	}
	cancha, ok := f.canchas[in.CanchaID]
	if !ok || cancha.ComplejoID != complejo.ID || !cancha.Activa {
		return DuenoReservaDetail{}, ErrCanchaNotFound
	}
	_, diaSemana, _, _, err := ExtractRangeMinutesLima(in.Inicio, in.Fin)
	if err != nil {
		return DuenoReservaDetail{}, err
	}
	pricing, err := CalculateExternaPricing(complejo, cancha.ID, f.horarios[diaSemana], f.tarifasForDay(diaSemana, cancha.ID), in.Inicio, in.Fin, in.MontoTotalOverride, in.MontoAdelanto, f.now)
	if err != nil {
		return DuenoReservaDetail{}, err
	}
	if f.hasOverlap(cancha.ID, pricing.Inicio, pricing.Fin, uuid.Nil) {
		return DuenoReservaDetail{}, ErrSlotNotAvailable
	}

	id := uuid.New()
	nombre := in.ClienteExternoNombre
	qr := in.CodigoQR
	rec := &fakeReservaRecord{
		ID:                     id,
		ComplejoID:             complejo.ID,
		CanchaID:               cancha.ID,
		Origen:                 OrigenExterna,
		ClienteExternoNombre:   &nombre,
		ClienteExternoTelefono: in.ClienteExternoTelefono,
		NotasInternas:          in.NotasInternas,
		Estado:                 EstadoConfirmada,
		Inicio:                 pricing.Inicio,
		Fin:                    pricing.Fin,
		PrecioBase:             pricing.PrecioBase,
		MontoDescuento:         pricing.MontoDescuento,
		MontoTotal:             pricing.MontoTotal,
		MontoAdelanto:          pricing.MontoAdelanto,
		SaldoPendiente:         pricing.SaldoPendiente,
		CodigoQR:               &qr,
		CreatedAt:              f.now,
	}
	f.reservas[id] = rec
	f.audits = append(f.audits, fakeAuditRecord{
		ReservaID:   id,
		Accion:      AccionReservaExternaCreada,
		EstadoNuevo: EstadoConfirmada,
	})
	return f.buildFakeDuenoDetail(rec), nil
}

func (f *fakeStore) CreateBloqueoTx(_ context.Context, in CreateBloqueoTxInput) (DuenoReservaDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	cancha, ok := f.canchas[in.CanchaID]
	if !ok || cancha.ComplejoID != in.ComplejoID || !cancha.Activa {
		return DuenoReservaDetail{}, ErrCanchaNotFound
	}
	dayStart, _, startMin, endMin, err := ExtractRangeMinutesLima(in.Inicio, in.Fin)
	if err != nil {
		return DuenoReservaDetail{}, err
	}
	normIni := dayStart.Add(time.Duration(startMin) * time.Minute)
	normFin := dayStart.Add(time.Duration(endMin) * time.Minute)
	if !normFin.After(f.now) {
		return DuenoReservaDetail{}, ErrSlotEnPasado
	}
	if f.hasOverlap(cancha.ID, normIni, normFin, uuid.Nil) {
		return DuenoReservaDetail{}, ErrSlotNotAvailable
	}

	id := uuid.New()
	motivo := in.MotivoBloqueo
	rec := &fakeReservaRecord{
		ID:            id,
		ComplejoID:    in.ComplejoID,
		CanchaID:      cancha.ID,
		Origen:        OrigenBloqueoMantenimiento,
		MotivoBloqueo: &motivo,
		Estado:        EstadoConfirmada,
		Inicio:        normIni,
		Fin:           normFin,
		CreatedAt:     f.now,
	}
	f.reservas[id] = rec
	return f.buildFakeDuenoDetail(rec), nil
}

func (f *fakeStore) GetDuenoReservaByQR(_ context.Context, complejoID uuid.UUID, codigoQR string) (DuenoReservaDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, r := range f.reservas {
		if r.ComplejoID == complejoID && r.CodigoQR != nil && *r.CodigoQR == codigoQR && allowsQRDisplay(r.Estado) {
			return f.buildFakeDuenoDetail(r), nil
		}
	}
	return DuenoReservaDetail{}, ErrReservaNotFound
}

func (f *fakeStore) CheckinReservaTx(_ context.Context, complejoID, reservaID, _ uuid.UUID, metodoPagoSaldo string) (DuenoReservaDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	r, ok := f.reservas[reservaID]
	if !ok || r.ComplejoID != complejoID {
		return DuenoReservaDetail{}, ErrReservaNotFound
	}
	orig := r.Origen
	if orig == "" {
		orig = OrigenApp
	}
	if orig != OrigenApp && orig != OrigenExterna {
		return DuenoReservaDetail{}, ErrInvalidReservaOrigen
	}
	if r.Estado != EstadoConfirmada {
		return DuenoReservaDetail{}, ErrInvalidReservaState
	}
	if toCents(r.SaldoPendiente) > 0 {
		if metodoPagoSaldo == "" {
			return DuenoReservaDetail{}, &ValidationError{
				Message: "Datos de check-in inválidos",
				Fields:  map[string]string{"metodo_pago_saldo": "obligatorio"},
			}
		}
		m := metodoPagoSaldo
		now := f.now
		r.MetodoPagoSaldo = &m
		r.SaldoPagadoAt = &now
	}
	now := f.now
	r.Estado = EstadoCompletada
	r.CheckinAt = &now
	return f.buildFakeDuenoDetail(r), nil
}

func (f *fakeStore) MarkNoShowTx(_ context.Context, complejoID, reservaID, _ uuid.UUID, motivo string) (DuenoReservaDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	r, ok := f.reservas[reservaID]
	if !ok || r.ComplejoID != complejoID {
		return DuenoReservaDetail{}, ErrReservaNotFound
	}
	orig := r.Origen
	if orig == "" {
		orig = OrigenApp
	}
	if orig != OrigenApp && orig != OrigenExterna {
		return DuenoReservaDetail{}, ErrInvalidReservaOrigen
	}
	if r.Estado != EstadoConfirmada {
		return DuenoReservaDetail{}, ErrInvalidReservaState
	}
	r.Estado = EstadoNoShow
	r.ReembolsoPendiente = false
	if r.JugadorID != uuid.Nil {
		f.pushFakeNotif(r.JugadorID, &r.ComplejoID, &r.ID, NotifReservaNoShow, "No-Show", motivo)
	}
	return f.buildFakeDuenoDetail(r), nil
}

func (f *fakeStore) CancelReservaJugadorTx(_ context.Context, jugadorID, reservaID uuid.UUID, motivo string) (JugadorReservaDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	r, ok := f.reservas[reservaID]
	if !ok {
		return JugadorReservaDetail{}, ErrReservaNotFound
	}
	if r.JugadorID != jugadorID {
		return JugadorReservaDetail{}, ErrReservaForbidden
	}
	complejo := f.complejos[r.ComplejoID]
	switch r.Estado {
	case EstadoPendientePago:
		r.ReembolsoPendiente = false
	case EstadoPorValidar, EstadoConfirmada:
		if err := ValidateCancellationWindow(r.Inicio, complejo.HorasMinCancelacion, f.now); err != nil {
			return JugadorReservaDetail{}, err
		}
		r.ReembolsoPendiente = true
	default:
		return JugadorReservaDetail{}, ErrInvalidReservaState
	}

	now := f.now
	r.Estado = EstadoCancelada
	r.CanceladoAt = &now
	if motivo != "" {
		m := motivo
		r.MotivoCancelacion = &m
	}
	if r.CuponID != nil {
		if c, exists := f.cupones[*r.CuponID]; exists && c.UsosActuales > 0 {
			c.UsosActuales--
		}
	}
	f.pushFakeNotif(complejo.DuenoID, &complejo.ID, &r.ID, NotifReservaCancelada, "Cancelada", motivo)
	return f.buildFakeJugadorDetail(r), nil
}

func (f *fakeStore) CancelReservaDuenoTx(_ context.Context, complejoID, reservaID, _ uuid.UUID, motivo string) (DuenoReservaDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	r, ok := f.reservas[reservaID]
	if !ok || r.ComplejoID != complejoID {
		return DuenoReservaDetail{}, ErrReservaNotFound
	}
	if r.Estado != EstadoPendientePago && r.Estado != EstadoPorValidar && r.Estado != EstadoConfirmada {
		return DuenoReservaDetail{}, ErrInvalidReservaState
	}
	orig := r.Origen
	if orig == "" {
		orig = OrigenApp
	}
	reembolso := (orig == OrigenApp && (r.Estado == EstadoPorValidar || r.Estado == EstadoConfirmada)) ||
		(orig == OrigenExterna && toCents(r.MontoAdelanto) > 0)

	now := f.now
	m := motivo
	r.Estado = EstadoCancelada
	r.CanceladoAt = &now
	r.MotivoCancelacion = &m
	r.ReembolsoPendiente = reembolso
	if r.CuponID != nil {
		if c, exists := f.cupones[*r.CuponID]; exists && c.UsosActuales > 0 {
			c.UsosActuales--
		}
	}
	if r.JugadorID != uuid.Nil {
		f.pushFakeNotif(r.JugadorID, &r.ComplejoID, &r.ID, NotifReservaCancelada, "Cancelada por dueño", motivo)
	}
	return f.buildFakeDuenoDetail(r), nil
}

func (f *fakeStore) MarkReembolsoResueltoTx(_ context.Context, complejoID, reservaID, _ uuid.UUID, _ string) (DuenoReservaDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	r, ok := f.reservas[reservaID]
	if !ok || r.ComplejoID != complejoID {
		return DuenoReservaDetail{}, ErrReservaNotFound
	}
	if !r.ReembolsoPendiente {
		return DuenoReservaDetail{}, ErrReembolsoNoPendiente
	}
	now := f.now
	r.ReembolsoPendiente = false
	r.ReembolsoResueltoAt = &now
	return f.buildFakeDuenoDetail(r), nil
}

func (f *fakeStore) ReprogramarJugadorTx(_ context.Context, in ReprogramarTxInput) (JugadorReservaDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	excedente, r, err := f.doFakeReprogram(in)
	if err != nil {
		return JugadorReservaDetail{}, err
	}
	d := f.buildFakeJugadorDetail(r)
	d.ExcedenteReembolso = excedente
	return d, nil
}

func (f *fakeStore) ReprogramarDuenoTx(_ context.Context, in ReprogramarTxInput) (DuenoReservaDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	excedente, r, err := f.doFakeReprogram(in)
	if err != nil {
		return DuenoReservaDetail{}, err
	}
	d := f.buildFakeDuenoDetail(r)
	d.ExcedenteReembolso = excedente
	return d, nil
}

func (f *fakeStore) doFakeReprogram(in ReprogramarTxInput) (float64, *fakeReservaRecord, error) {
	r, ok := f.reservas[in.ReservaID]
	if !ok {
		return 0, nil, ErrReservaNotFound
	}
	if in.IsDueno {
		if in.ComplejoID == nil || r.ComplejoID != *in.ComplejoID {
			return 0, nil, ErrReservaNotFound
		}
	} else {
		if in.JugadorID == nil || r.JugadorID != *in.JugadorID {
			return 0, nil, ErrReservaForbidden
		}
	}
	if r.Estado != EstadoConfirmada {
		return 0, nil, ErrInvalidReservaState
	}

	complejo := f.complejos[r.ComplejoID]
	if !in.IsDueno {
		if err := ValidateCancellationWindow(r.Inicio, complejo.HorasMinCancelacion, f.now); err != nil {
			return 0, nil, err
		}
	}

	targetCanchaID := r.CanchaID
	if in.CanchaID != nil {
		targetCanchaID = *in.CanchaID
	}
	cancha, ok := f.canchas[targetCanchaID]
	if !ok || cancha.ComplejoID != complejo.ID || !cancha.Activa {
		return 0, nil, ErrCanchaNotFound
	}

	_, diaSemana, _, _, err := ExtractRangeMinutesLima(in.NuevoInicio, in.NuevoFin)
	if err != nil {
		return 0, nil, err
	}

	var cuponPtr *CuponRecord
	if r.CuponID != nil {
		if c, exists := f.cupones[*r.CuponID]; exists {
			cp := *c
			cuponPtr = &cp
		}
	}

	recalc, err := CalculateReprogramPricing(
		complejo,
		targetCanchaID,
		f.horarios[diaSemana],
		f.tarifasForDay(diaSemana, targetCanchaID),
		in.NuevoInicio,
		in.NuevoFin,
		cuponPtr,
		r.MontoAdelanto,
		r.ReembolsoPendiente,
		f.now,
	)
	if err != nil {
		return 0, nil, err
	}
	if f.hasOverlap(targetCanchaID, recalc.Inicio, recalc.Fin, r.ID) {
		return 0, nil, ErrSlotNotAvailable
	}

	oldCancha := r.CanchaID
	oldIni := r.Inicio
	oldFin := r.Fin
	r.CanchaID = targetCanchaID
	r.Inicio = recalc.Inicio
	r.Fin = recalc.Fin
	r.PrecioBase = recalc.PrecioBase
	r.MontoDescuento = recalc.MontoDescuento
	r.MontoTotal = recalc.MontoTotal
	r.MontoAdelanto = recalc.MontoAdelanto
	r.SaldoPendiente = recalc.SaldoPendiente
	r.ReembolsoPendiente = recalc.ReembolsoPendiente
	r.ReprogramacionesCount++

	f.audits = append(f.audits, fakeAuditRecord{
		ReservaID:        r.ID,
		Accion:           AccionReprogramadaJugador,
		EstadoAnterior:   EstadoConfirmada,
		EstadoNuevo:      EstadoConfirmada,
		CanchaAnteriorID: &oldCancha,
		CanchaNuevaID:    &targetCanchaID,
		RangoAnteriorIni: &oldIni,
		RangoAnteriorFin: &oldFin,
		RangoNuevoIni:    &recalc.Inicio,
		RangoNuevoFin:    &recalc.Fin,
	})
	return recalc.ExcedenteReembolso, r, nil
}

func (f *fakeStore) ListNotificaciones(_ context.Context, usuarioID uuid.UUID, filter ListNotificacionesFilter) (NotificacionesListResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	items := make([]NotificacionItem, 0)
	unread := 0
	for _, n := range f.notifs {
		if n.UsuarioID != usuarioID {
			continue
		}
		if n.LeidaAt == nil {
			unread++
		}
		if filter.SoloNoLeidas && n.LeidaAt != nil {
			continue
		}
		items = append(items, NotificacionItem{
			ID:         n.ID,
			ComplejoID: n.ComplejoID,
			ReservaID:  n.ReservaID,
			Tipo:       n.Tipo,
			Titulo:     n.Titulo,
			Mensaje:    n.Mensaje,
			Leida:      n.LeidaAt != nil,
			LeidaAt:    n.LeidaAt,
			CreatedAt:  n.CreatedAt,
		})
	}
	return NotificacionesListResponse{
		Items:         items,
		Total:         len(items),
		TotalNoLeidas: unread,
		Limit:         filter.Limit,
		Offset:        filter.Offset,
	}, nil
}

func (f *fakeStore) MarkNotificacionLeida(_ context.Context, usuarioID, notificacionID uuid.UUID) (NotificacionItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	n, ok := f.notifs[notificacionID]
	if !ok || n.UsuarioID != usuarioID {
		return NotificacionItem{}, ErrNotificacionNotFound
	}
	if n.LeidaAt == nil {
		now := f.now
		n.LeidaAt = &now
	}
	return NotificacionItem{
		ID:         n.ID,
		ComplejoID: n.ComplejoID,
		ReservaID:  n.ReservaID,
		Tipo:       n.Tipo,
		Titulo:     n.Titulo,
		Mensaje:    n.Mensaje,
		Leida:      true,
		LeidaAt:    n.LeidaAt,
		CreatedAt:  n.CreatedAt,
	}, nil
}

func (f *fakeStore) MarkAllNotificacionesLeidas(_ context.Context, usuarioID uuid.UUID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var count int64
	now := f.now
	for _, n := range f.notifs {
		if n.UsuarioID == usuarioID && n.LeidaAt == nil {
			t := now
			n.LeidaAt = &t
			count++
		}
	}
	return count, nil
}

func (f *fakeStore) UpsertPushSubscription(_ context.Context, usuarioID uuid.UUID, in UpsertPushSubscriptionInput) (PushSubscriptionResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	rec, exists := f.pushSubs[in.Endpoint]
	if !exists {
		rec = &fakePushSubRecord{
			ID:        uuid.New(),
			Endpoint:  in.Endpoint,
			CreatedAt: f.now,
		}
		f.pushSubs[in.Endpoint] = rec
	}
	rec.UsuarioID = usuarioID
	rec.P256dh = in.P256dh
	rec.Auth = in.Auth
	rec.UserAgent = in.UserAgent
	rec.Activa = true
	rec.UpdatedAt = f.now
	return PushSubscriptionResponse{
		ID:        rec.ID,
		Endpoint:  rec.Endpoint,
		UserAgent: rec.UserAgent,
		Activa:    rec.Activa,
		CreatedAt: rec.CreatedAt,
		UpdatedAt: rec.UpdatedAt,
	}, nil
}

func (f *fakeStore) DeactivatePushSubscription(_ context.Context, usuarioID uuid.UUID, endpoint string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	rec, ok := f.pushSubs[endpoint]
	if !ok || rec.UsuarioID != usuarioID {
		return ErrPushSubscriptionNotFound
	}
	rec.Activa = false
	rec.UpdatedAt = f.now
	return nil
}

func (f *fakeStore) pushFakeNotif(usuarioID uuid.UUID, complejoID, reservaID *uuid.UUID, tipo, titulo, mensaje string) {
	id := uuid.New()
	f.notifs[id] = &fakeNotifRecord{
		ID:         id,
		UsuarioID:  usuarioID,
		ComplejoID: complejoID,
		ReservaID:  reservaID,
		Tipo:       tipo,
		Titulo:     titulo,
		Mensaje:    mensaje,
		CreatedAt:  f.now,
	}
	f.notifsCount++
}

func (f *fakeStore) tarifasForDay(diaSemana int16, canchaID uuid.UUID) []TarifaFranja {
	out := make([]TarifaFranja, 0)
	for _, t := range f.tarifas {
		if t.DiaSemana == diaSemana && (t.CanchaID == nil || *t.CanchaID == canchaID) {
			out = append(out, t)
		}
	}
	return out
}

func (f *fakeStore) hasOverlap(canchaID uuid.UUID, ini, fin time.Time, ignoreID uuid.UUID) bool {
	for _, existing := range f.reservas {
		if existing.ID == ignoreID || existing.CanchaID != canchaID {
			continue
		}
		if existing.Estado == EstadoPendientePago || existing.Estado == EstadoPorValidar || existing.Estado == EstadoConfirmada {
			if existing.Inicio.Before(fin) && existing.Fin.After(ini) {
				return true
			}
		}
	}
	return false
}

func (f *fakeStore) buildFakeDuenoDetail(r *fakeReservaRecord) DuenoReservaDetail {
	orig := r.Origen
	if orig == "" {
		orig = OrigenApp
	}
	d := DuenoReservaDetail{
		ID:                     r.ID,
		ComplejoID:             r.ComplejoID,
		Cancha:                 f.canchas[r.CanchaID],
		Origen:                 orig,
		Estado:                 r.Estado,
		Inicio:                 r.Inicio,
		Fin:                    r.Fin,
		DuracionMin:            int(r.Fin.Sub(r.Inicio) / time.Minute),
		ClienteExternoNombre:   r.ClienteExternoNombre,
		ClienteExternoTelefono: r.ClienteExternoTelefono,
		MotivoBloqueo:          r.MotivoBloqueo,
		NotasInternas:          r.NotasInternas,
		CuponID:                r.CuponID,
		PrecioBase:             r.PrecioBase,
		MontoDescuento:         r.MontoDescuento,
		MontoTotal:             r.MontoTotal,
		MontoAdelanto:          r.MontoAdelanto,
		SaldoPendiente:         r.SaldoPendiente,
		MetodoPagoSaldo:        r.MetodoPagoSaldo,
		SaldoPagadoAt:          r.SaldoPagadoAt,
		HoldExpiraAt:           r.HoldExpiraAt,
		PorValidarDesde:        r.PorValidarDesde,
		CheckinAt:              r.CheckinAt,
		CanceladoAt:            r.CanceladoAt,
		MotivoCancelacion:      r.MotivoCancelacion,
		ReembolsoPendiente:     r.ReembolsoPendiente,
		ReembolsoResueltoAt:    r.ReembolsoResueltoAt,
		ReprogramacionesCount:  r.ReprogramacionesCount,
		Comprobantes:           []ComprobanteInfo{},
		CreatedAt:              r.CreatedAt,
		UpdatedAt:              f.now,
	}
	if r.JugadorID != uuid.Nil {
		jid := r.JugadorID
		d.JugadorID = &jid
	}
	if allowsQRDisplay(r.Estado) {
		d.CodigoQR = r.CodigoQR
	}
	return d
}

func (f *fakeStore) buildFakeJugadorDetail(r *fakeReservaRecord) JugadorReservaDetail {
	complejo := f.complejos[r.ComplejoID]
	d := JugadorReservaDetail{
		ID:                    r.ID,
		JugadorID:             r.JugadorID,
		Estado:                r.Estado,
		Origen:                OrigenApp,
		Inicio:                r.Inicio,
		Fin:                   r.Fin,
		DuracionMin:           int(r.Fin.Sub(r.Inicio) / time.Minute),
		PrecioBase:            r.PrecioBase,
		MontoDescuento:        r.MontoDescuento,
		MontoTotal:            r.MontoTotal,
		MontoAdelanto:         r.MontoAdelanto,
		SaldoPendiente:        r.SaldoPendiente,
		MetodoPagoSaldo:       r.MetodoPagoSaldo,
		SaldoPagadoAt:         r.SaldoPagadoAt,
		CheckinAt:             r.CheckinAt,
		CanceladoAt:           r.CanceladoAt,
		MotivoCancelacion:     r.MotivoCancelacion,
		ReembolsoPendiente:    r.ReembolsoPendiente,
		ReembolsoResueltoAt:   r.ReembolsoResueltoAt,
		ReprogramacionesCount: r.ReprogramacionesCount,
		Complejo: ComplejoRutaInfo{
			ID:                  complejo.ID,
			Slug:                complejo.Slug,
			Nombre:              complejo.Nombre,
			HorasMinCancelacion: complejo.HorasMinCancelacion,
		},
		Cancha:       f.canchas[r.CanchaID],
		Comprobantes: []ComprobanteInfo{},
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    f.now,
	}
	if allowsQRDisplay(r.Estado) {
		d.CodigoQR = r.CodigoQR
	}
	return d
}

// ---------- Pruebas unitarias puras de Pricing Fase 3B ----------

func TestCalculateReprogramPricingDayToNightAndOverpaymentRefund(t *testing.T) {
	loc := LimaLocation()
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, loc)
	canchaID := uuid.New()
	complejo := ComplejoContext{
		ID:               uuid.New(),
		UnidadReservaMin: 60,
		TipoAdelanto:     TipoPorcentaje,
		ValorAdelanto:    50,
	}
	horario := &HorarioAtencion{DiaSemana: 4, MinutoApertura: 480, MinutoCierre: 1380} // Jueves 2026-11-26
	tarifas := []TarifaFranja{
		{ID: uuid.New(), DiaSemana: 4, MinutoInicio: 480, MinutoFin: 1080, PrecioHora: 60.00, EsHoraMuerta: true},  // Día S/ 60
		{ID: uuid.New(), DiaSemana: 4, MinutoInicio: 1080, MinutoFin: 1380, PrecioHora: 90.00, EsHoraMuerta: false}, // Noche S/ 90
	}

	// Caso 1: Franja día (S/ 60, adelanto S/ 30) -> Franja noche (S/ 90) => adelanto=30, saldo=60, reembolso=false
	resUp, err := CalculateReprogramPricing(
		complejo,
		canchaID,
		horario,
		tarifas,
		time.Date(2026, 11, 26, 19, 0, 0, 0, loc),
		time.Date(2026, 11, 26, 20, 0, 0, 0, loc),
		nil,
		30.00,
		false,
		now,
	)
	if err != nil {
		t.Fatalf("error inesperado en reprogramación a franja noche: %v", err)
	}
	if resUp.MontoTotal != 90.00 || resUp.MontoAdelanto != 30.00 || resUp.SaldoPendiente != 60.00 || resUp.ReembolsoPendiente || resUp.ExcedenteReembolso != 0 {
		t.Fatalf("recálculo día->noche incorrecto: %+v", resUp)
	}

	// Caso 2: Nuevo total (S/ 60) < adelanto pagado (S/ 80) => saldo=0, adelanto=60, reembolso=true, excedente=20
	resDown, err := CalculateReprogramPricing(
		complejo,
		canchaID,
		horario,
		tarifas,
		time.Date(2026, 11, 26, 10, 0, 0, 0, loc),
		time.Date(2026, 11, 26, 11, 0, 0, 0, loc),
		nil,
		80.00,
		false,
		now,
	)
	if err != nil {
		t.Fatalf("error inesperado en reprogramación con excedente: %v", err)
	}
	if resDown.MontoTotal != 60.00 || resDown.MontoAdelanto != 60.00 || resDown.SaldoPendiente != 0.00 || !resDown.ReembolsoPendiente || resDown.ExcedenteReembolso != 20.00 {
		t.Fatalf("recálculo con excedente incorrecto: %+v", resDown)
	}

	// Caso 3: Cupón no acumulable en hora muerta -> ErrCuponNotStackableDeadHour
	cupon := &CuponRecord{
		ID:                   uuid.New(),
		TipoDescuento:        TipoPorcentaje,
		ValorDescuento:       10,
		AcumulableHoraMuerta: false,
	}
	if _, err := CalculateReprogramPricing(
		complejo,
		canchaID,
		horario,
		tarifas,
		time.Date(2026, 11, 26, 10, 0, 0, 0, loc),
		time.Date(2026, 11, 26, 11, 0, 0, 0, loc),
		cupon,
		30.00,
		false,
		now,
	); !errors.Is(err, ErrCuponNotStackableDeadHour) {
		t.Fatalf("esperaba ErrCuponNotStackableDeadHour, got %v", err)
	}
}

func TestValidateCancellationWindow(t *testing.T) {
	loc := LimaLocation()
	matchStart := time.Date(2026, 11, 27, 18, 0, 0, 0, loc)

	// Exactamente 24h antes -> permitido
	if err := ValidateCancellationWindow(matchStart, 24, time.Date(2026, 11, 26, 18, 0, 0, 0, loc)); err != nil {
		t.Fatalf("exactamente en el borde de 24h debe permitirse: %v", err)
	}
	// 23h59m antes -> vencida (422)
	if err := ValidateCancellationWindow(matchStart, 24, time.Date(2026, 11, 26, 18, 1, 0, 0, loc)); !errors.Is(err, ErrCancellationWindowExpired) {
		t.Fatalf("esperaba ErrCancellationWindowExpired, got %v", err)
	}
}

// ---------- Pruebas integrales de Handler y Servicio (Fase 3B) ----------

func TestPhase3BFullFlowAndTenantIsolation(t *testing.T) {
	loc := LimaLocation()
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, loc)
	store := newFakeStore(now)

	dueno1ID := uuid.New()
	dueno2ID := uuid.New()
	jugadorID := uuid.New()
	complejo1ID := uuid.New()
	complejo2ID := uuid.New()
	cancha1ID := uuid.New()

	vence := now.Add(30 * 24 * time.Hour)
	store.complejos[complejo1ID] = ComplejoContext{
		ID:                  complejo1ID,
		DuenoID:             dueno1ID,
		Slug:                "cancha-huacho",
		Nombre:              "Cancha Huacho",
		Publicado:           true,
		UnidadReservaMin:    60,
		HoldDuracionMin:     10,
		TipoAdelanto:        TipoPorcentaje,
		ValorAdelanto:       50,
		HorasMinCancelacion: 24,
		EstadoSuscripcion:   EstadoSuscripcionActiva,
		SuscripcionVenceAt:  &vence,
	}
	store.complejos[complejo2ID] = ComplejoContext{
		ID:                  complejo2ID,
		DuenoID:             dueno2ID,
		Slug:                "otro-complejo",
		Nombre:              "Otro Complejo",
		Publicado:           true,
		UnidadReservaMin:    60,
		HoldDuracionMin:     10,
		TipoAdelanto:        TipoPorcentaje,
		ValorAdelanto:       50,
		HorasMinCancelacion: 24,
		EstadoSuscripcion:   EstadoSuscripcionActiva,
		SuscripcionVenceAt:  &vence,
	}
	store.canchas[cancha1ID] = CanchaInfo{
		ID:         cancha1ID,
		ComplejoID: complejo1ID,
		Nombre:     "Cancha Principal",
		Modalidad:  "7v7",
		Superficie: "SINTETICO",
		Activa:     true,
	}
	// Viernes 2026-11-27 (Weekday == 5) y Jueves 2026-11-26 (Weekday == 4)
	store.horarios[4] = &HorarioAtencion{DiaSemana: 4, MinutoApertura: 480, MinutoCierre: 1380}
	store.horarios[5] = &HorarioAtencion{DiaSemana: 5, MinutoApertura: 480, MinutoCierre: 1380}
	store.tarifas = []TarifaFranja{
		{ID: uuid.New(), DiaSemana: 4, MinutoInicio: 480, MinutoFin: 1080, PrecioHora: 60.00},
		{ID: uuid.New(), DiaSemana: 4, MinutoInicio: 1080, MinutoFin: 1380, PrecioHora: 90.00},
		{ID: uuid.New(), DiaSemana: 5, MinutoInicio: 480, MinutoFin: 1080, PrecioHora: 60.00},
		{ID: uuid.New(), DiaSemana: 5, MinutoInicio: 1080, MinutoFin: 1380, PrecioHora: 90.00},
	}

	cuponID := uuid.New()
	store.cupones[cuponID] = &CuponRecord{
		ID:             cuponID,
		ComplejoID:     complejo1ID,
		Codigo:         "GOL10",
		TipoDescuento:  TipoMontoFijo,
		ValorDescuento: 10,
		UsosActuales:   1,
		Activo:         true,
	}

	// Reserva 1 en POR_VALIDAR con > 30 min de espera (alerta_demora = true)
	res1ID := uuid.New()
	espera35Min := now.Add(-35 * time.Minute)
	store.reservas[res1ID] = &fakeReservaRecord{
		ID:              res1ID,
		ComplejoID:      complejo1ID,
		CanchaID:        cancha1ID,
		JugadorID:       jugadorID,
		Origen:          OrigenApp,
		Estado:          EstadoPorValidar,
		Inicio:          time.Date(2026, 11, 27, 10, 0, 0, 0, loc),
		Fin:             time.Date(2026, 11, 27, 11, 0, 0, 0, loc),
		PrecioBase:      60,
		MontoTotal:      60,
		MontoAdelanto:   30,
		SaldoPendiente:  30,
		PorValidarDesde: &espera35Min,
		CreatedAt:       espera35Min,
	}

	// Reserva 2 en POR_VALIDAR con cupón para probar rechazo y reversión de cupón
	res2ID := uuid.New()
	espera10Min := now.Add(-10 * time.Minute)
	store.reservas[res2ID] = &fakeReservaRecord{
		ID:              res2ID,
		ComplejoID:      complejo1ID,
		CanchaID:        cancha1ID,
		JugadorID:       jugadorID,
		Origen:          OrigenApp,
		Estado:          EstadoPorValidar,
		Inicio:          time.Date(2026, 11, 27, 12, 0, 0, 0, loc),
		Fin:             time.Date(2026, 11, 27, 13, 0, 0, 0, loc),
		CuponID:         &cuponID,
		PrecioBase:      60,
		MontoDescuento:  10,
		MontoTotal:      50,
		MontoAdelanto:   25,
		SaldoPendiente:  25,
		PorValidarDesde: &espera10Min,
		CreatedAt:       espera10Min,
	}

	svc := NewService(store, nil, nil)
	svc.now = func() time.Time { return now }
	h := NewHandler(svc, nil, nil)

	router := apphttp.NewRouter(apphttp.RouterConfig{
		TokenParser: func(token string) (apphttp.AuthUser, error) {
			switch token {
			case "dueno-1":
				return apphttp.AuthUser{UserID: dueno1ID, Rol: apphttp.RoleDueno, ComplejoID: &complejo1ID}, nil
			case "dueno-2":
				return apphttp.AuthUser{UserID: dueno2ID, Rol: apphttp.RoleDueno, ComplejoID: &complejo2ID}, nil
			case "jugador-1":
				return apphttp.AuthUser{UserID: jugadorID, Rol: apphttp.RoleJugador}, nil
			default:
				return apphttp.AuthUser{}, errors.New("token inválido")
			}
		},
		TenantChecker: func(_ context.Context, userID, complejoID uuid.UUID) (bool, error) {
			return (userID == dueno1ID && complejoID == complejo1ID) || (userID == dueno2ID && complejoID == complejo2ID), nil
		},
		MountDueno: func(dueno chi.Router) {
			h.RegisterDuenoRoutes(dueno)
		},
		MountJugador: func(jugador chi.Router) {
			h.RegisterJugadorRoutes(jugador)
		},
		MountAuthenticated: func(authed chi.Router) {
			h.RegisterAuthenticatedRoutes(authed)
		},
	})

	doJSON := func(method, path, token, body string) *httptest.ResponseRecorder {
		var rdr *bytes.Reader
		if body != "" {
			rdr = bytes.NewReader([]byte(body))
		} else {
			rdr = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, path, rdr)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		return rr
	}

	// 1. GET /api/v1/dueno/reservas?estado=POR_VALIDAR -> verifica alerta_demora
	rr := doJSON(http.MethodGet, "/api/v1/dueno/reservas?estado=POR_VALIDAR", "dueno-1", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET dueno/reservas falló (%d): %s", rr.Code, rr.Body.String())
	}
	var listResp struct {
		Data DuenoReservasListResponse `json:"data"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &listResp)
	alertaByID := map[uuid.UUID]bool{}
	for _, it := range listResp.Data.Items {
		alertaByID[it.ID] = it.AlertaDemora
	}
	if !alertaByID[res1ID] || alertaByID[res2ID] {
		t.Fatalf("alerta_demora incorrecta: res1=%v (want true), res2=%v (want false)", alertaByID[res1ID], alertaByID[res2ID])
	}

	// 2. Aislamiento: dueno-2 intenta aprobar reserva de dueno-1 -> 404
	rrCross := doJSON(http.MethodPost, "/api/v1/dueno/reservas/"+res1ID.String()+"/aprobar", "dueno-2", "")
	if rrCross.Code != http.StatusNotFound {
		t.Fatalf("esperaba 404 en aprobación cross-tenant, got %d", rrCross.Code)
	}

	// 3. dueno-1 aprueba res1 -> CONFIRMADA con codigo_qr
	rrApprove := doJSON(http.MethodPost, "/api/v1/dueno/reservas/"+res1ID.String()+"/aprobar", "dueno-1", "")
	if rrApprove.Code != http.StatusOK {
		t.Fatalf("aprobar falló (%d): %s", rrApprove.Code, rrApprove.Body.String())
	}
	var detailEnv struct {
		Data DuenoReservaDetail `json:"data"`
	}
	_ = json.Unmarshal(rrApprove.Body.Bytes(), &detailEnv)
	if detailEnv.Data.Estado != EstadoConfirmada || detailEnv.Data.CodigoQR == nil || !strings.HasPrefix(*detailEnv.Data.CodigoQR, "FDP-") {
		t.Fatalf("aprobación inválida: %+v", detailEnv.Data)
	}
	qr := *detailEnv.Data.CodigoQR

	// 4. Búsqueda por QR: dueno-2 recibe 404; dueno-1 recibe 200
	if rrQRCross := doJSON(http.MethodGet, "/api/v1/dueno/reservas/qr/"+qr, "dueno-2", ""); rrQRCross.Code != http.StatusNotFound {
		t.Fatalf("esperaba 404 al consultar QR ajeno, got %d", rrQRCross.Code)
	}
	if rrQR := doJSON(http.MethodGet, "/api/v1/dueno/reservas/qr/"+qr, "dueno-1", ""); rrQR.Code != http.StatusOK {
		t.Fatalf("consulta QR falló (%d): %s", rrQR.Code, rrQR.Body.String())
	}

	// 5. Reprogramación por jugador de día (S/ 60) a noche (S/ 90)
	rrReprog := doJSON(http.MethodPost, "/api/v1/jugador/reservas/"+res1ID.String()+"/reprogramar", "jugador-1",
		`{"nuevo_inicio":"2026-11-27T19:00:00-05:00","nuevo_fin":"2026-11-27T20:00:00-05:00","motivo":"Preferimos noche"}`)
	if rrReprog.Code != http.StatusOK {
		t.Fatalf("reprogramar falló (%d): %s", rrReprog.Code, rrReprog.Body.String())
	}
	var jugEnv struct {
		Data JugadorReservaDetail `json:"data"`
	}
	_ = json.Unmarshal(rrReprog.Body.Bytes(), &jugEnv)
	if jugEnv.Data.MontoTotal != 90 || jugEnv.Data.MontoAdelanto != 30 || jugEnv.Data.SaldoPendiente != 60 || jugEnv.Data.ReprogramacionesCount != 1 {
		t.Fatalf("reprogramación jugador incorrecta: %+v", jugEnv.Data)
	}

	// 6. Check-in sin metodo_pago_saldo cuando saldo > 0 -> 400; con YAPE -> 200 COMPLETADA
	if rrChkFail := doJSON(http.MethodPost, "/api/v1/dueno/reservas/"+res1ID.String()+"/checkin", "dueno-1", `{}`); rrChkFail.Code != http.StatusBadRequest {
		t.Fatalf("esperaba 400 sin metodo_pago_saldo, got %d", rrChkFail.Code)
	}
	rrChk := doJSON(http.MethodPost, "/api/v1/dueno/reservas/"+res1ID.String()+"/checkin", "dueno-1", `{"metodo_pago_saldo":"YAPE"}`)
	if rrChk.Code != http.StatusOK {
		t.Fatalf("checkin falló (%d): %s", rrChk.Code, rrChk.Body.String())
	}
	_ = json.Unmarshal(rrChk.Body.Bytes(), &detailEnv)
	if detailEnv.Data.Estado != EstadoCompletada || detailEnv.Data.MontoAdelanto != 30 || detailEnv.Data.SaldoPendiente != 60 {
		t.Fatalf("trazabilidad financiera en checkin alterada: %+v", detailEnv.Data)
	}

	// 7. Rechazo con motivo revierte cupón
	rrRej := doJSON(http.MethodPost, "/api/v1/dueno/reservas/"+res2ID.String()+"/rechazar", "dueno-1", `{"motivo":"Monto incompleto"}`)
	if rrRej.Code != http.StatusOK {
		t.Fatalf("rechazar falló (%d): %s", rrRej.Code, rrRej.Body.String())
	}
	if store.cupones[cuponID].UsosActuales != 0 {
		t.Fatalf("cupón no revertido tras rechazo: usos=%d", store.cupones[cuponID].UsosActuales)
	}

	// 8. Externa + colisión con bloqueo -> 409 SLOT_NOT_AVAILABLE
	rrExt := doJSON(http.MethodPost, "/api/v1/dueno/reservas/externas", "dueno-1",
		`{"cancha_id":"`+cancha1ID.String()+`","inicio":"2026-11-27T14:00:00-05:00","fin":"2026-11-27T15:00:00-05:00","cliente_externo_nombre":"Carlos Ruiz","monto_adelanto":20}`)
	if rrExt.Code != http.StatusCreated {
		t.Fatalf("externa falló (%d): %s", rrExt.Code, rrExt.Body.String())
	}
	rrBlockConflict := doJSON(http.MethodPost, "/api/v1/dueno/bloqueos", "dueno-1",
		`{"cancha_id":"`+cancha1ID.String()+`","inicio":"2026-11-27T14:00:00-05:00","fin":"2026-11-27T15:00:00-05:00","motivo_bloqueo":"Mantenimiento red"}`)
	if rrBlockConflict.Code != http.StatusConflict {
		t.Fatalf("esperaba 409 en bloqueo solapado, got %d", rrBlockConflict.Code)
	}

	// 9. Notificaciones + Push Subscriptions (ciclo completo y aislamiento)
	rrNotif := doJSON(http.MethodGet, "/api/v1/notificaciones?solo_no_leidas=true", "jugador-1", "")
	if rrNotif.Code != http.StatusOK {
		t.Fatalf("GET notificaciones falló (%d): %s", rrNotif.Code, rrNotif.Body.String())
	}
	var notifEnv struct {
		Data NotificacionesListResponse `json:"data"`
	}
	_ = json.Unmarshal(rrNotif.Body.Bytes(), &notifEnv)
	if notifEnv.Data.TotalNoLeidas < 2 {
		t.Fatalf("esperaba >=2 notificaciones no leídas para jugador, got %d", notifEnv.Data.TotalNoLeidas)
	}
	firstNotifID := notifEnv.Data.Items[0].ID
	if rrCrossNotif := doJSON(http.MethodPatch, "/api/v1/notificaciones/"+firstNotifID.String()+"/leer", "dueno-2", ""); rrCrossNotif.Code != http.StatusNotFound {
		t.Fatalf("esperaba 404 al leer notificación ajena, got %d", rrCrossNotif.Code)
	}
	if rrRead := doJSON(http.MethodPatch, "/api/v1/notificaciones/"+firstNotifID.String()+"/leer", "jugador-1", ""); rrRead.Code != http.StatusOK {
		t.Fatalf("PATCH leer notificación falló (%d)", rrRead.Code)
	}
	if rrReadAll := doJSON(http.MethodPost, "/api/v1/notificaciones/leer-todas", "jugador-1", ""); rrReadAll.Code != http.StatusOK {
		t.Fatalf("POST leer-todas falló (%d)", rrReadAll.Code)
	}

	rrPush := doJSON(http.MethodPost, "/api/v1/push-subscriptions", "jugador-1",
		`{"endpoint":"https://fcm.googleapis.com/fcm/send/demo-1","p256dh":"key123","auth":"auth123"}`)
	if rrPush.Code != http.StatusOK {
		t.Fatalf("POST push-subscriptions falló (%d): %s", rrPush.Code, rrPush.Body.String())
	}
	if rrDelCross := doJSON(http.MethodDelete, "/api/v1/push-subscriptions", "dueno-2", `{"endpoint":"https://fcm.googleapis.com/fcm/send/demo-1"}`); rrDelCross.Code != http.StatusNotFound {
		t.Fatalf("esperaba 404 al desactivar push ajeno, got %d", rrDelCross.Code)
	}
	if rrDel := doJSON(http.MethodDelete, "/api/v1/push-subscriptions", "jugador-1", `{"endpoint":"https://fcm.googleapis.com/fcm/send/demo-1"}`); rrDel.Code != http.StatusOK {
		t.Fatalf("DELETE push-subscriptions falló (%d): %s", rrDel.Code, rrDel.Body.String())
	}

	// 10. Cancelación jugador dentro vs fuera de ventana (422) + reembolso-resuelto + No-Show
	resInWinID := uuid.New()
	store.reservas[resInWinID] = &fakeReservaRecord{
		ID:             resInWinID,
		ComplejoID:     complejo1ID,
		CanchaID:       cancha1ID,
		JugadorID:      jugadorID,
		Origen:         OrigenApp,
		Estado:         EstadoConfirmada,
		Inicio:         time.Date(2026, 11, 27, 16, 0, 0, 0, loc), // 54h en el futuro (> 24h)
		Fin:            time.Date(2026, 11, 27, 17, 0, 0, 0, loc),
		PrecioBase:     60,
		MontoTotal:     60,
		MontoAdelanto:  30,
		SaldoPendiente: 30,
		CreatedAt:      now,
	}
	rrCancelOK := doJSON(http.MethodPost, "/api/v1/jugador/reservas/"+resInWinID.String()+"/cancelar", "jugador-1", `{"motivo":"Cambio de planes"}`)
	if rrCancelOK.Code != http.StatusOK {
		t.Fatalf("cancelación en ventana falló (%d): %s", rrCancelOK.Code, rrCancelOK.Body.String())
	}
	_ = json.Unmarshal(rrCancelOK.Body.Bytes(), &jugEnv)
	if jugEnv.Data.Estado != EstadoCancelada || !jugEnv.Data.ReembolsoPendiente {
		t.Fatalf("cancelación en ventana debe marcar reembolso_pendiente=true: %+v", jugEnv.Data)
	}

	// Dueño resuelve reembolso
	rrReemb := doJSON(http.MethodPost, "/api/v1/dueno/reservas/"+resInWinID.String()+"/reembolso-resuelto", "dueno-1", `{}`)
	if rrReemb.Code != http.StatusOK {
		t.Fatalf("reembolso-resuelto falló (%d): %s", rrReemb.Code, rrReemb.Body.String())
	}
	_ = json.Unmarshal(rrReemb.Body.Bytes(), &detailEnv)
	if detailEnv.Data.ReembolsoPendiente || detailEnv.Data.ReembolsoResueltoAt == nil {
		t.Fatalf("reembolso-resuelto no limpió bandera: %+v", detailEnv.Data)
	}

	// Reserva dentro de las próximas 24h -> 422 CANCELLATION_WINDOW_EXPIRED
	resOutWinID := uuid.New()
	store.reservas[resOutWinID] = &fakeReservaRecord{
		ID:             resOutWinID,
		ComplejoID:     complejo1ID,
		CanchaID:       cancha1ID,
		JugadorID:      jugadorID,
		Origen:         OrigenApp,
		Estado:         EstadoConfirmada,
		Inicio:         now.Add(6 * time.Hour),
		Fin:            now.Add(7 * time.Hour),
		PrecioBase:     60,
		MontoTotal:     60,
		MontoAdelanto:  30,
		SaldoPendiente: 30,
		CreatedAt:      now,
	}
	rrCancelLate := doJSON(http.MethodPost, "/api/v1/jugador/reservas/"+resOutWinID.String()+"/cancelar", "jugador-1", `{}`)
	if rrCancelLate.Code != http.StatusUnprocessableEntity {
		t.Fatalf("esperaba 422 fuera de ventana, got %d: %s", rrCancelLate.Code, rrCancelLate.Body.String())
	}
	if !strings.Contains(rrCancelLate.Body.String(), "CANCELLATION_WINDOW_EXPIRED") {
		t.Fatalf("esperaba código CANCELLATION_WINDOW_EXPIRED, got: %s", rrCancelLate.Body.String())
	}

	// Dueño marca No-Show
	rrNoShow := doJSON(http.MethodPost, "/api/v1/dueno/reservas/"+resOutWinID.String()+"/no-show", "dueno-1", `{}`)
	if rrNoShow.Code != http.StatusOK {
		t.Fatalf("no-show falló (%d): %s", rrNoShow.Code, rrNoShow.Body.String())
	}
	_ = json.Unmarshal(rrNoShow.Body.Bytes(), &detailEnv)
	if detailEnv.Data.Estado != EstadoNoShow || detailEnv.Data.ReembolsoPendiente {
		t.Fatalf("estado NO_SHOW inválido: %+v", detailEnv.Data)
	}
}
