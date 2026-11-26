package reservas

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ListDuenoReservas consulta la agenda y bandeja del complejo con filtros combinables,
// calculando alerta_demora (> 30 min en POR_VALIDAR, Decisión 3) y último comprobante pendiente.
func (r *PostgresRepository) ListDuenoReservas(ctx context.Context, complejoID uuid.UUID, params DuenoReservasQueryParams) ([]DuenoReservaItem, int, error) {
	var estadoParam *string
	if params.Estado != "" {
		e := params.Estado
		estadoParam = &e
	}
	var origenParam *string
	if params.Origen != "" {
		o := params.Origen
		origenParam = &o
	}

	const countSQL = `
		SELECT COUNT(*), NOW()
		FROM reservas r
		WHERE r.complejo_id = $1
		  AND ($2::timestamptz IS NULL OR lower(r.rango_horario) >= $2::timestamptz)
		  AND ($3::timestamptz IS NULL OR lower(r.rango_horario) < $3::timestamptz)
		  AND ($4::uuid IS NULL OR r.cancha_id = $4)
		  AND ($5::varchar IS NULL OR r.estado = $5)
		  AND ($6::varchar IS NULL OR r.origen = $6)`

	var total int
	var dbNow time.Time
	if err := r.pool.QueryRow(ctx, countSQL,
		complejoID,
		params.Desde,
		params.HastaEnd,
		params.CanchaID,
		estadoParam,
		origenParam,
	).Scan(&total, &dbNow); err != nil {
		return nil, 0, fmt.Errorf("error contando reservas del complejo: %w", err)
	}
	if total == 0 {
		return []DuenoReservaItem{}, 0, nil
	}

	const listSQL = `
		SELECT
			r.id,
			r.complejo_id,
			r.cancha_id,
			ca.nombre,
			ca.modalidad,
			r.origen,
			r.estado,
			lower(r.rango_horario),
			upper(r.rango_horario),
			r.jugador_id,
			u.nombre_completo,
			u.telefono,
			r.cliente_externo_nombre,
			r.cliente_externo_telefono,
			r.motivo_bloqueo,
			r.notas_internas,
			r.precio_base::float8,
			r.monto_descuento::float8,
			r.monto_total::float8,
			r.monto_adelanto::float8,
			r.saldo_pendiente::float8,
			r.metodo_pago_saldo,
			r.saldo_pagado_at,
			r.hold_expira_at,
			r.por_validar_desde,
			CASE WHEN r.estado IN ('CONFIRMADA', 'COMPLETADA') THEN r.codigo_qr ELSE NULL END,
			r.checkin_at,
			r.cancelado_at,
			r.motivo_cancelacion,
			r.reembolso_pendiente,
			r.reembolso_resuelto_at,
			r.reprogramaciones_count,
			r.created_at,
			r.updated_at,
			ucp.id,
			ucp.metodo_pago_id,
			ucp.metodo_pago_tipo,
			ucp.es_automatico,
			ucp.estado_revision,
			ucp.monto_declarado,
			ucp.codigo_operacion,
			ucp.tiene_evidencia,
			ucp.validado_at,
			ucp.motivo_rechazo,
			ucp.created_at
		FROM reservas r
		INNER JOIN canchas ca ON ca.id = r.cancha_id
		LEFT JOIN usuarios u ON u.id = r.jugador_id
		LEFT JOIN LATERAL (
			SELECT
				cp.id,
				cp.metodo_pago_id,
				mp.tipo AS metodo_pago_tipo,
				cp.es_automatico,
				cp.estado_revision,
				cp.monto_declarado::float8 AS monto_declarado,
				cp.codigo_operacion,
				(cp.evidencia_storage_key IS NOT NULL AND btrim(cp.evidencia_storage_key) <> '') AS tiene_evidencia,
				cp.validado_at,
				cp.motivo_rechazo,
				cp.created_at
			FROM comprobantes_pago cp
			INNER JOIN metodos_pago_complejo mp ON mp.id = cp.metodo_pago_id
			WHERE cp.reserva_id = r.id
			  AND cp.complejo_id = r.complejo_id
			  AND cp.estado_revision = 'PENDIENTE'
			ORDER BY cp.created_at DESC
			LIMIT 1
		) ucp ON r.estado = 'POR_VALIDAR'
		WHERE r.complejo_id = $1
		  AND ($2::timestamptz IS NULL OR lower(r.rango_horario) >= $2::timestamptz)
		  AND ($3::timestamptz IS NULL OR lower(r.rango_horario) < $3::timestamptz)
		  AND ($4::uuid IS NULL OR r.cancha_id = $4)
		  AND ($5::varchar IS NULL OR r.estado = $5)
		  AND ($6::varchar IS NULL OR r.origen = $6)
		ORDER BY lower(r.rango_horario) ASC, r.created_at DESC, r.id ASC
		LIMIT $7 OFFSET $8`

	rows, err := r.pool.Query(ctx, listSQL,
		complejoID,
		params.Desde,
		params.HastaEnd,
		params.CanchaID,
		estadoParam,
		origenParam,
		params.Limit,
		params.Offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("error listando reservas del complejo: %w", err)
	}
	defer rows.Close()

	items := make([]DuenoReservaItem, 0, params.Limit)
	for rows.Next() {
		var (
			item                DuenoReservaItem
			inicioDB, finDB     time.Time
			saldoPagadoAt       *time.Time
			holdExpiraAt        *time.Time
			porValidarDesde     *time.Time
			checkinAt           *time.Time
			canceladoAt         *time.Time
			reembolsoResueltoAt *time.Time
			createdAt           time.Time
			updatedAt           time.Time

			ucpID             *uuid.UUID
			ucpMetodoID       *uuid.UUID
			ucpMetodoTipo     *string
			ucpEsAuto         *bool
			ucpEstadoRev      *string
			ucpMonto          *float64
			ucpCodigoOp       *string
			ucpTieneEvidencia *bool
			ucpValidadoAt     *time.Time
			ucpMotivoRechazo  *string
			ucpCreatedAt      *time.Time
		)

		if err := rows.Scan(
			&item.ID,
			&item.ComplejoID,
			&item.CanchaID,
			&item.CanchaNombre,
			&item.CanchaModalidad,
			&item.Origen,
			&item.Estado,
			&inicioDB,
			&finDB,
			&item.JugadorID,
			&item.JugadorNombre,
			&item.JugadorTelefono,
			&item.ClienteExternoNombre,
			&item.ClienteExternoTelefono,
			&item.MotivoBloqueo,
			&item.NotasInternas,
			&item.PrecioBase,
			&item.MontoDescuento,
			&item.MontoTotal,
			&item.MontoAdelanto,
			&item.SaldoPendiente,
			&item.MetodoPagoSaldo,
			&saldoPagadoAt,
			&holdExpiraAt,
			&porValidarDesde,
			&item.CodigoQR,
			&checkinAt,
			&canceladoAt,
			&item.MotivoCancelacion,
			&item.ReembolsoPendiente,
			&reembolsoResueltoAt,
			&item.ReprogramacionesCount,
			&createdAt,
			&updatedAt,
			&ucpID,
			&ucpMetodoID,
			&ucpMetodoTipo,
			&ucpEsAuto,
			&ucpEstadoRev,
			&ucpMonto,
			&ucpCodigoOp,
			&ucpTieneEvidencia,
			&ucpValidadoAt,
			&ucpMotivoRechazo,
			&ucpCreatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("error escaneando reserva del complejo: %w", err)
		}

		item.Inicio = inicioDB.In(limaLocation)
		item.Fin = finDB.In(limaLocation)
		item.DuracionMin = int(finDB.Sub(inicioDB) / time.Minute)
		item.SaldoPagadoAt = toLimaPtr(saldoPagadoAt)
		item.HoldExpiraAt = toLimaPtr(holdExpiraAt)
		item.PorValidarDesde = toLimaPtr(porValidarDesde)
		item.CheckinAt = toLimaPtr(checkinAt)
		item.CanceladoAt = toLimaPtr(canceladoAt)
		item.ReembolsoResueltoAt = toLimaPtr(reembolsoResueltoAt)
		item.CreatedAt = createdAt.In(limaLocation)
		item.UpdatedAt = updatedAt.In(limaLocation)

		if item.Estado == EstadoPorValidar && porValidarDesde != nil {
			waitDur := dbNow.Sub(*porValidarDesde)
			if waitDur < 0 {
				waitDur = 0
			}
			mins := int(waitDur / time.Minute)
			item.MinutosEsperaValidacion = &mins
			item.AlertaDemora = waitDur > AlertaDemoraThreshold
		}

		if ucpID != nil && ucpMetodoID != nil && ucpMetodoTipo != nil && ucpEsAuto != nil && ucpEstadoRev != nil && ucpMonto != nil && ucpCreatedAt != nil {
			comp := &ComprobanteInfo{
				ID:              *ucpID,
				ReservaID:       item.ID,
				MetodoPagoID:    *ucpMetodoID,
				MetodoPagoTipo:  *ucpMetodoTipo,
				EsAutomatico:    *ucpEsAuto,
				EstadoRevision:  *ucpEstadoRev,
				MontoDeclarado:  *ucpMonto,
				CodigoOperacion: ucpCodigoOp,
				ValidadoAt:      toLimaPtr(ucpValidadoAt),
				MotivoRechazo:   ucpMotivoRechazo,
				CreatedAt:       ucpCreatedAt.In(limaLocation),
			}
			if ucpTieneEvidencia != nil && *ucpTieneEvidencia {
				ep := BuildEvidenciaEndpoint(*ucpID)
				comp.EvidenciaEndpoint = &ep
			}
			item.UltimoComprobantePendiente = comp
		}

		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("error iterando reservas del complejo: %w", err)
	}

	return items, total, nil
}

// AprobarComprobanteTx aprueba atómicamente (FOR UPDATE) el comprobante pendiente, confirma la reserva,
// emite codigo_qr único, escribe auditoría y notifica al jugador (RF-12, HU-09).
func (r *PostgresRepository) AprobarComprobanteTx(ctx context.Context, complejoID, reservaID, duenoUserID uuid.UUID, codigoQR string) (DuenoReservaDetail, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return DuenoReservaDetail{}, fmt.Errorf("error iniciando transacción de aprobación: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	locked, err := lockDuenoReservaTx(ctx, tx, complejoID, reservaID)
	if err != nil {
		return DuenoReservaDetail{}, err
	}
	if locked.Estado != EstadoPorValidar {
		return DuenoReservaDetail{}, ErrInvalidReservaState
	}

	const approveCompSQL = `
		UPDATE comprobantes_pago
		SET estado_revision = 'APROBADO',
		    validado_por = $3,
		    validado_at = $4::timestamptz
		WHERE id = (
			SELECT id
			FROM comprobantes_pago
			WHERE reserva_id = $1
			  AND complejo_id = $2
			  AND estado_revision = 'PENDIENTE'
			ORDER BY created_at DESC
			LIMIT 1
			FOR UPDATE
		)
		RETURNING id`

	var approvedCompID uuid.UUID
	if err := tx.QueryRow(ctx, approveCompSQL, reservaID, complejoID, duenoUserID, locked.DBNow).Scan(&approvedCompID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DuenoReservaDetail{}, ErrComprobanteNotFound
		}
		return DuenoReservaDetail{}, fmt.Errorf("error aprobando comprobante de pago: %w", err)
	}

	const confirmReservaSQL = `
		UPDATE reservas
		SET estado = 'CONFIRMADA',
		    codigo_qr = $3
		WHERE id = $1
		  AND complejo_id = $2
		  AND estado = 'POR_VALIDAR'`

	tag, err := tx.Exec(ctx, confirmReservaSQL, reservaID, complejoID, codigoQR)
	if err != nil {
		return DuenoReservaDetail{}, fmt.Errorf("error confirmando reserva: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return DuenoReservaDetail{}, ErrInvalidReservaState
	}

	if err := insertAuditoriaTx(ctx, tx, auditInsertParams{
		ReservaID:        reservaID,
		ComplejoID:       complejoID,
		Accion:           AccionComprobanteAprobado,
		EstadoAnterior:   strPtr(EstadoPorValidar),
		EstadoNuevo:      EstadoConfirmada,
		CanchaAnteriorID: &locked.CanchaID,
		CanchaNuevaID:    &locked.CanchaID,
		RangoAnteriorIni: &locked.Inicio,
		RangoAnteriorFin: &locked.Fin,
		RangoNuevoIni:    &locked.Inicio,
		RangoNuevoFin:    &locked.Fin,
		ActorID:          &duenoUserID,
		Motivo:           fmt.Sprintf("Comprobante %s aprobado por el dueño y código QR emitido", approvedCompID.String()),
	}); err != nil {
		return DuenoReservaDetail{}, err
	}

	if locked.JugadorID != nil {
		iniLima := locked.Inicio.In(limaLocation)
		finLima := locked.Fin.In(limaLocation)
		msg := fmt.Sprintf(
			"Tu comprobante fue aprobado por %s. Tu reserva en %s el %s de %s a %s está confirmada y tu código QR ya está disponible.",
			locked.ComplejoNombre,
			locked.CanchaNombre,
			iniLima.Format("2006-01-02"),
			iniLima.Format("15:04"),
			finLima.Format("15:04"),
		)
		if err := insertNotificacionTx(ctx, tx, *locked.JugadorID, &complejoID, &reservaID, NotifReservaConfirmada, "Reserva confirmada", msg); err != nil {
			return DuenoReservaDetail{}, err
		}
	}

	detail, err := getDuenoReservaDetail(ctx, tx, complejoID, reservaID)
	if err != nil {
		return DuenoReservaDetail{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return DuenoReservaDetail{}, fmt.Errorf("error confirmando transacción de aprobación: %w", err)
	}

	return detail, nil
}

// RechazarComprobanteTx rechaza atómicamente el comprobante pendiente con motivo obligatorio,
// libera el horario (RECHAZADA), revierte usos del cupón si aplica, audita y notifica al jugador.
func (r *PostgresRepository) RechazarComprobanteTx(ctx context.Context, complejoID, reservaID, duenoUserID uuid.UUID, motivo string) (DuenoReservaDetail, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return DuenoReservaDetail{}, fmt.Errorf("error iniciando transacción de rechazo: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	locked, err := lockDuenoReservaTx(ctx, tx, complejoID, reservaID)
	if err != nil {
		return DuenoReservaDetail{}, err
	}
	if locked.Estado != EstadoPorValidar {
		return DuenoReservaDetail{}, ErrInvalidReservaState
	}

	const rejectCompSQL = `
		UPDATE comprobantes_pago
		SET estado_revision = 'RECHAZADO',
		    motivo_rechazo = $3,
		    validado_por = $4,
		    validado_at = $5::timestamptz
		WHERE id = (
			SELECT id
			FROM comprobantes_pago
			WHERE reserva_id = $1
			  AND complejo_id = $2
			  AND estado_revision = 'PENDIENTE'
			ORDER BY created_at DESC
			LIMIT 1
			FOR UPDATE
		)
		RETURNING id`

	var rejectedCompID uuid.UUID
	if err := tx.QueryRow(ctx, rejectCompSQL, reservaID, complejoID, motivo, duenoUserID, locked.DBNow).Scan(&rejectedCompID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DuenoReservaDetail{}, ErrComprobanteNotFound
		}
		return DuenoReservaDetail{}, fmt.Errorf("error rechazando comprobante de pago: %w", err)
	}

	const rejectReservaSQL = `
		UPDATE reservas
		SET estado = 'RECHAZADA'
		WHERE id = $1
		  AND complejo_id = $2
		  AND estado = 'POR_VALIDAR'`

	tag, err := tx.Exec(ctx, rejectReservaSQL, reservaID, complejoID)
	if err != nil {
		return DuenoReservaDetail{}, fmt.Errorf("error marcando reserva como rechazada: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return DuenoReservaDetail{}, ErrInvalidReservaState
	}

	if locked.CuponID != nil {
		if err := revertCuponUsoTx(ctx, tx, complejoID, *locked.CuponID); err != nil {
			return DuenoReservaDetail{}, err
		}
	}

	if err := insertAuditoriaTx(ctx, tx, auditInsertParams{
		ReservaID:        reservaID,
		ComplejoID:       complejoID,
		Accion:           AccionComprobanteRechazado,
		EstadoAnterior:   strPtr(EstadoPorValidar),
		EstadoNuevo:      EstadoRechazada,
		CanchaAnteriorID: &locked.CanchaID,
		CanchaNuevaID:    &locked.CanchaID,
		RangoAnteriorIni: &locked.Inicio,
		RangoAnteriorFin: &locked.Fin,
		RangoNuevoIni:    &locked.Inicio,
		RangoNuevoFin:    &locked.Fin,
		ActorID:          &duenoUserID,
		Motivo:           motivo,
	}); err != nil {
		return DuenoReservaDetail{}, err
	}

	if locked.JugadorID != nil {
		iniLima := locked.Inicio.In(limaLocation)
		finLima := locked.Fin.In(limaLocation)
		msg := fmt.Sprintf(
			"Tu comprobante para %s (%s de %s a %s) en %s fue rechazado. Motivo: %s",
			locked.CanchaNombre,
			iniLima.Format("2006-01-02"),
			iniLima.Format("15:04"),
			finLima.Format("15:04"),
			locked.ComplejoNombre,
			motivo,
		)
		if err := insertNotificacionTx(ctx, tx, *locked.JugadorID, &complejoID, &reservaID, NotifComprobanteRechazado, "Comprobante rechazado", msg); err != nil {
			return DuenoReservaDetail{}, err
		}
	}

	detail, err := getDuenoReservaDetail(ctx, tx, complejoID, reservaID)
	if err != nil {
		return DuenoReservaDetail{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return DuenoReservaDetail{}, fmt.Errorf("error confirmando transacción de rechazo: %w", err)
	}

	return detail, nil
}

// CreateExternaTx registra una reserva externa (WhatsApp/puerta) confirmada con bloqueo GIST (RF-14, RN-06).
func (r *PostgresRepository) CreateExternaTx(ctx context.Context, in CreateExternaTxInput) (DuenoReservaDetail, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return DuenoReservaDetail{}, fmt.Errorf("error iniciando transacción de reserva externa: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var dbNow time.Time
	if err := tx.QueryRow(ctx, `SELECT NOW()`).Scan(&dbNow); err != nil {
		return DuenoReservaDetail{}, fmt.Errorf("error obteniendo hora del servidor: %w", err)
	}

	complejo, err := getComplejoByIDForDueno(ctx, tx, in.ComplejoID)
	if err != nil {
		return DuenoReservaDetail{}, err
	}

	cancha, err := getActiveCancha(ctx, tx, complejo.ID, in.CanchaID)
	if err != nil {
		return DuenoReservaDetail{}, err
	}

	_, diaSemana, _, _, err := ExtractRangeMinutesLima(in.Inicio, in.Fin)
	if err != nil {
		return DuenoReservaDetail{}, err
	}

	horario, err := getHorarioByDia(ctx, tx, complejo.ID, diaSemana)
	if err != nil {
		return DuenoReservaDetail{}, err
	}

	var tarifas []TarifaFranja
	if in.MontoTotalOverride == nil {
		tarifas, err = listActiveTarifasForDay(ctx, tx, complejo.ID, cancha.ID, diaSemana)
		if err != nil {
			return DuenoReservaDetail{}, err
		}
	}

	pricing, err := CalculateExternaPricing(
		complejo,
		cancha.ID,
		horario,
		tarifas,
		in.Inicio,
		in.Fin,
		in.MontoTotalOverride,
		in.MontoAdelanto,
		dbNow,
	)
	if err != nil {
		return DuenoReservaDetail{}, err
	}

	if err := expireOverlappingStaleHoldsTx(ctx, tx, cancha.ID, pricing.Inicio, pricing.Fin, dbNow); err != nil {
		return DuenoReservaDetail{}, err
	}

	const insertSQL = `
		INSERT INTO reservas (
			complejo_id,
			cancha_id,
			creado_por,
			origen,
			cliente_externo_nombre,
			cliente_externo_telefono,
			rango_horario,
			estado,
			precio_base,
			monto_descuento,
			monto_total,
			monto_adelanto,
			saldo_pendiente,
			codigo_qr,
			notas_internas
		)
		VALUES (
			$1, $2, $3, 'EXTERNA', $4, $5,
			tstzrange($6::timestamptz, $7::timestamptz, '[)'),
			'CONFIRMADA',
			$8::numeric(10,2),
			$9::numeric(10,2),
			$10::numeric(10,2),
			$11::numeric(10,2),
			$12::numeric(10,2),
			$13,
			$14
		)
		RETURNING id, lower(rango_horario), upper(rango_horario)`

	var (
		reservaID       uuid.UUID
		inicioDB, finDB time.Time
	)
	err = tx.QueryRow(ctx, insertSQL,
		complejo.ID,
		cancha.ID,
		in.DuenoUserID,
		in.ClienteExternoNombre,
		in.ClienteExternoTelefono,
		pricing.Inicio,
		pricing.Fin,
		formatDecimal(pricing.PrecioBase),
		formatDecimal(pricing.MontoDescuento),
		formatDecimal(pricing.MontoTotal),
		formatDecimal(pricing.MontoAdelanto),
		formatDecimal(pricing.SaldoPendiente),
		in.CodigoQR,
		in.NotasInternas,
	).Scan(&reservaID, &inicioDB, &finDB)
	if err != nil {
		return DuenoReservaDetail{}, mapReservaPgError(err)
	}

	if err := insertAuditoriaTx(ctx, tx, auditInsertParams{
		ReservaID:     reservaID,
		ComplejoID:    complejo.ID,
		Accion:        AccionReservaExternaCreada,
		EstadoNuevo:   EstadoConfirmada,
		CanchaNuevaID: &cancha.ID,
		RangoNuevoIni: &inicioDB,
		RangoNuevoFin: &finDB,
		ActorID:       &in.DuenoUserID,
		Motivo:        fmt.Sprintf("Reserva externa registrada para %s (Total S/ %.2f, Adelanto S/ %.2f)", in.ClienteExternoNombre, pricing.MontoTotal, pricing.MontoAdelanto),
	}); err != nil {
		return DuenoReservaDetail{}, err
	}

	detail, err := getDuenoReservaDetail(ctx, tx, complejo.ID, reservaID)
	if err != nil {
		return DuenoReservaDetail{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return DuenoReservaDetail{}, fmt.Errorf("error confirmando reserva externa: %w", err)
	}

	return detail, nil
}

// CreateBloqueoTx crea un bloqueo de mantenimiento (origen BLOQUEO_MANTENIMIENTO, montos en 0) con GIST (RN-06).
func (r *PostgresRepository) CreateBloqueoTx(ctx context.Context, in CreateBloqueoTxInput) (DuenoReservaDetail, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return DuenoReservaDetail{}, fmt.Errorf("error iniciando transacción de bloqueo: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var dbNow time.Time
	if err := tx.QueryRow(ctx, `SELECT NOW()`).Scan(&dbNow); err != nil {
		return DuenoReservaDetail{}, fmt.Errorf("error obteniendo hora del servidor: %w", err)
	}

	dayStart, _, startMin, endMin, err := ExtractRangeMinutesLima(in.Inicio, in.Fin)
	if err != nil {
		return DuenoReservaDetail{}, err
	}
	normInicio := dayStart.Add(time.Duration(startMin) * time.Minute)
	normFin := dayStart.Add(time.Duration(endMin) * time.Minute)
	if !normFin.After(dbNow) {
		return DuenoReservaDetail{}, ErrSlotEnPasado
	}

	cancha, err := getActiveCancha(ctx, tx, in.ComplejoID, in.CanchaID)
	if err != nil {
		return DuenoReservaDetail{}, err
	}

	if err := expireOverlappingStaleHoldsTx(ctx, tx, cancha.ID, normInicio, normFin, dbNow); err != nil {
		return DuenoReservaDetail{}, err
	}

	const insertSQL = `
		INSERT INTO reservas (
			complejo_id,
			cancha_id,
			creado_por,
			origen,
			rango_horario,
			estado,
			precio_base,
			monto_descuento,
			monto_total,
			monto_adelanto,
			saldo_pendiente,
			motivo_bloqueo
		)
		VALUES (
			$1, $2, $3, 'BLOQUEO_MANTENIMIENTO',
			tstzrange($4::timestamptz, $5::timestamptz, '[)'),
			'CONFIRMADA',
			0.00, 0.00, 0.00, 0.00, 0.00,
			$6
		)
		RETURNING id, lower(rango_horario), upper(rango_horario)`

	var (
		reservaID       uuid.UUID
		inicioDB, finDB time.Time
	)
	if err := tx.QueryRow(ctx, insertSQL,
		in.ComplejoID,
		cancha.ID,
		in.DuenoUserID,
		normInicio,
		normFin,
		in.MotivoBloqueo,
	).Scan(&reservaID, &inicioDB, &finDB); err != nil {
		return DuenoReservaDetail{}, mapReservaPgError(err)
	}

	if err := insertAuditoriaTx(ctx, tx, auditInsertParams{
		ReservaID:     reservaID,
		ComplejoID:    in.ComplejoID,
		Accion:        AccionBloqueoCreado,
		EstadoNuevo:   EstadoConfirmada,
		CanchaNuevaID: &cancha.ID,
		RangoNuevoIni: &inicioDB,
		RangoNuevoFin: &finDB,
		ActorID:       &in.DuenoUserID,
		Motivo:        in.MotivoBloqueo,
	}); err != nil {
		return DuenoReservaDetail{}, err
	}

	detail, err := getDuenoReservaDetail(ctx, tx, in.ComplejoID, reservaID)
	if err != nil {
		return DuenoReservaDetail{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return DuenoReservaDetail{}, fmt.Errorf("error confirmando bloqueo de mantenimiento: %w", err)
	}

	return detail, nil
}

// GetDuenoReservaByQR busca una reserva CONFIRMADA o COMPLETADA perteneciente al complejo por codigo_qr.
func (r *PostgresRepository) GetDuenoReservaByQR(ctx context.Context, complejoID uuid.UUID, codigoQR string) (DuenoReservaDetail, error) {
	const findSQL = `
		SELECT id
		FROM reservas
		WHERE complejo_id = $1
		  AND codigo_qr = $2
		  AND estado IN ('CONFIRMADA', 'COMPLETADA')`

	var reservaID uuid.UUID
	if err := r.pool.QueryRow(ctx, findSQL, complejoID, codigoQR).Scan(&reservaID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DuenoReservaDetail{}, ErrReservaNotFound
		}
		return DuenoReservaDetail{}, fmt.Errorf("error buscando reserva por código QR: %w", err)
	}

	return getDuenoReservaDetail(ctx, r.pool, complejoID, reservaID)
}

// CheckinReservaTx registra el check-in (COMPLETADA) conservando monto_adelanto y saldo_pendiente
// intactos para la trazabilidad financiera de reportes y marcando saldo_pagado_at y metodo_pago_saldo.
func (r *PostgresRepository) CheckinReservaTx(ctx context.Context, complejoID, reservaID, duenoUserID uuid.UUID, metodoPagoSaldo string) (DuenoReservaDetail, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return DuenoReservaDetail{}, fmt.Errorf("error iniciando transacción de check-in: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	locked, err := lockDuenoReservaTx(ctx, tx, complejoID, reservaID)
	if err != nil {
		return DuenoReservaDetail{}, err
	}
	if locked.Origen != OrigenApp && locked.Origen != OrigenExterna {
		return DuenoReservaDetail{}, ErrInvalidReservaOrigen
	}
	if locked.Estado != EstadoConfirmada {
		return DuenoReservaDetail{}, ErrInvalidReservaState
	}

	var metodoParam *string
	var saldoPagadoAt *time.Time
	auditMotivo := "Check-in registrado sin saldo pendiente"

	if toCents(locked.SaldoPendiente) > 0 {
		if metodoPagoSaldo == "" {
			return DuenoReservaDetail{}, &ValidationError{
				Message: "Datos de check-in inválidos",
				Fields: map[string]string{
					"metodo_pago_saldo": "metodo_pago_saldo es obligatorio cuando existe saldo pendiente por cobrar",
				},
			}
		}
		m := metodoPagoSaldo
		metodoParam = &m
		nowCopy := locked.DBNow
		saldoPagadoAt = &nowCopy
		auditMotivo = fmt.Sprintf("Check-in registrado y saldo liquidado (%s, S/ %.2f)", m, locked.SaldoPendiente)
	}

	const updateSQL = `
		UPDATE reservas
		SET estado = 'COMPLETADA',
		    checkin_at = $3::timestamptz,
		    checkin_por = $4,
		    metodo_pago_saldo = $5,
		    saldo_pagado_at = $6
		WHERE id = $1
		  AND complejo_id = $2
		  AND estado = 'CONFIRMADA'`

	tag, err := tx.Exec(ctx, updateSQL, reservaID, complejoID, locked.DBNow, duenoUserID, metodoParam, saldoPagadoAt)
	if err != nil {
		return DuenoReservaDetail{}, fmt.Errorf("error registrando check-in: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return DuenoReservaDetail{}, ErrInvalidReservaState
	}

	if err := insertAuditoriaTx(ctx, tx, auditInsertParams{
		ReservaID:        reservaID,
		ComplejoID:       complejoID,
		Accion:           AccionCheckinCompletado,
		EstadoAnterior:   strPtr(EstadoConfirmada),
		EstadoNuevo:      EstadoCompletada,
		CanchaAnteriorID: &locked.CanchaID,
		CanchaNuevaID:    &locked.CanchaID,
		RangoAnteriorIni: &locked.Inicio,
		RangoAnteriorFin: &locked.Fin,
		RangoNuevoIni:    &locked.Inicio,
		RangoNuevoFin:    &locked.Fin,
		ActorID:          &duenoUserID,
		Motivo:           auditMotivo,
	}); err != nil {
		return DuenoReservaDetail{}, err
	}

	detail, err := getDuenoReservaDetail(ctx, tx, complejoID, reservaID)
	if err != nil {
		return DuenoReservaDetail{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return DuenoReservaDetail{}, fmt.Errorf("error confirmando check-in: %w", err)
	}

	return detail, nil
}

// MarkNoShowTx marca una reserva confirmada como NO_SHOW aplicando retención del adelanto (RF-15).
func (r *PostgresRepository) MarkNoShowTx(ctx context.Context, complejoID, reservaID, duenoUserID uuid.UUID, motivo string) (DuenoReservaDetail, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return DuenoReservaDetail{}, fmt.Errorf("error iniciando transacción de no-show: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	locked, err := lockDuenoReservaTx(ctx, tx, complejoID, reservaID)
	if err != nil {
		return DuenoReservaDetail{}, err
	}
	if locked.Origen != OrigenApp && locked.Origen != OrigenExterna {
		return DuenoReservaDetail{}, ErrInvalidReservaOrigen
	}
	if locked.Estado != EstadoConfirmada {
		return DuenoReservaDetail{}, ErrInvalidReservaState
	}

	const updateSQL = `
		UPDATE reservas
		SET estado = 'NO_SHOW',
		    reembolso_pendiente = false
		WHERE id = $1
		  AND complejo_id = $2
		  AND estado = 'CONFIRMADA'`

	tag, err := tx.Exec(ctx, updateSQL, reservaID, complejoID)
	if err != nil {
		return DuenoReservaDetail{}, fmt.Errorf("error marcando reserva como no-show: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return DuenoReservaDetail{}, ErrInvalidReservaState
	}

	auditMotivo := motivo
	if auditMotivo == "" {
		auditMotivo = "Inasistencia registrada por el complejo (adelanto retenido según política)"
	}

	if err := insertAuditoriaTx(ctx, tx, auditInsertParams{
		ReservaID:        reservaID,
		ComplejoID:       complejoID,
		Accion:           AccionNoShowRegistrado,
		EstadoAnterior:   strPtr(EstadoConfirmada),
		EstadoNuevo:      EstadoNoShow,
		CanchaAnteriorID: &locked.CanchaID,
		CanchaNuevaID:    &locked.CanchaID,
		RangoAnteriorIni: &locked.Inicio,
		RangoAnteriorFin: &locked.Fin,
		RangoNuevoIni:    &locked.Inicio,
		RangoNuevoFin:    &locked.Fin,
		ActorID:          &duenoUserID,
		Motivo:           auditMotivo,
	}); err != nil {
		return DuenoReservaDetail{}, err
	}

	if locked.JugadorID != nil {
		iniLima := locked.Inicio.In(limaLocation)
		finLima := locked.Fin.In(limaLocation)
		msg := fmt.Sprintf(
			"Tu reserva en %s (%s) el %s de %s a %s fue marcada como inasistencia (No-Show). El adelanto queda retenido según las condiciones del complejo.",
			locked.ComplejoNombre,
			locked.CanchaNombre,
			iniLima.Format("2006-01-02"),
			iniLima.Format("15:04"),
			finLima.Format("15:04"),
		)
		if err := insertNotificacionTx(ctx, tx, *locked.JugadorID, &complejoID, &reservaID, NotifReservaNoShow, "Reserva marcada como No-Show", msg); err != nil {
			return DuenoReservaDetail{}, err
		}
	}

	detail, err := getDuenoReservaDetail(ctx, tx, complejoID, reservaID)
	if err != nil {
		return DuenoReservaDetail{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return DuenoReservaDetail{}, fmt.Errorf("error confirmando no-show: %w", err)
	}

	return detail, nil
}

// CancelReservaJugadorTx cancela una reserva del jugador validando ventana RN-13 y marcando reembolso_pendiente (RN-14).
func (r *PostgresRepository) CancelReservaJugadorTx(ctx context.Context, jugadorID, reservaID uuid.UUID, motivo string) (JugadorReservaDetail, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return JugadorReservaDetail{}, fmt.Errorf("error iniciando transacción de cancelación: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	locked, err := lockReservaByIDTx(ctx, tx, reservaID)
	if err != nil {
		return JugadorReservaDetail{}, err
	}
	if locked.JugadorID == nil || *locked.JugadorID != jugadorID {
		return JugadorReservaDetail{}, ErrReservaForbidden
	}

	var reembolsoPendiente bool
	switch locked.Estado {
	case EstadoPendientePago:
		reembolsoPendiente = false
	case EstadoPorValidar, EstadoConfirmada:
		if err := ValidateCancellationWindow(locked.Inicio, locked.HorasMinCancelacion, locked.DBNow); err != nil {
			return JugadorReservaDetail{}, err
		}
		reembolsoPendiente = true
	default:
		return JugadorReservaDetail{}, ErrInvalidReservaState
	}

	var motivoParam *string
	if motivo != "" {
		m := motivo
		motivoParam = &m
	}

	const cancelSQL = `
		UPDATE reservas
		SET estado = 'CANCELADA',
		    cancelado_at = $2::timestamptz,
		    motivo_cancelacion = $3,
		    reembolso_pendiente = $4
		WHERE id = $1
		  AND estado IN ('PENDIENTE_PAGO', 'POR_VALIDAR', 'CONFIRMADA')`

	tag, err := tx.Exec(ctx, cancelSQL, reservaID, locked.DBNow, motivoParam, reembolsoPendiente)
	if err != nil {
		return JugadorReservaDetail{}, fmt.Errorf("error cancelando reserva del jugador: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return JugadorReservaDetail{}, ErrInvalidReservaState
	}

	if locked.CuponID != nil {
		if err := revertCuponUsoTx(ctx, tx, locked.ComplejoID, *locked.CuponID); err != nil {
			return JugadorReservaDetail{}, err
		}
	}

	auditMotivo := motivo
	if auditMotivo == "" {
		auditMotivo = "Reserva cancelada por el jugador dentro de la política del complejo"
	}

	if err := insertAuditoriaTx(ctx, tx, auditInsertParams{
		ReservaID:        reservaID,
		ComplejoID:       locked.ComplejoID,
		Accion:           AccionReservaCanceladaJugador,
		EstadoAnterior:   strPtr(locked.Estado),
		EstadoNuevo:      EstadoCancelada,
		CanchaAnteriorID: &locked.CanchaID,
		CanchaNuevaID:    &locked.CanchaID,
		RangoAnteriorIni: &locked.Inicio,
		RangoAnteriorFin: &locked.Fin,
		RangoNuevoIni:    &locked.Inicio,
		RangoNuevoFin:    &locked.Fin,
		ActorID:          &jugadorID,
		Motivo:           auditMotivo,
	}); err != nil {
		return JugadorReservaDetail{}, err
	}

	iniLima := locked.Inicio.In(limaLocation)
	finLima := locked.Fin.In(limaLocation)
	notifMsg := fmt.Sprintf(
		"Un jugador canceló su reserva en %s el %s de %s a %s.",
		locked.CanchaNombre,
		iniLima.Format("2006-01-02"),
		iniLima.Format("15:04"),
		finLima.Format("15:04"),
	)
	if reembolsoPendiente {
		notifMsg += " Requiere coordinar reembolso directo del adelanto."
	}
	if motivo != "" {
		notifMsg += fmt.Sprintf(" Motivo: %s", motivo)
	}
	if err := insertNotificacionTx(ctx, tx, locked.DuenoID, &locked.ComplejoID, &reservaID, NotifReservaCancelada, "Reserva cancelada por jugador", notifMsg); err != nil {
		return JugadorReservaDetail{}, err
	}

	detail, _, err := getJugadorReservaDetailWithQuerier(ctx, tx, reservaID)
	if err != nil {
		return JugadorReservaDetail{}, err
	}
	detail.CodigoQR = nil

	if err := tx.Commit(ctx); err != nil {
		return JugadorReservaDetail{}, fmt.Errorf("error confirmando cancelación del jugador: %w", err)
	}

	return detail, nil
}

// CancelReservaDuenoTx cancela una reserva del complejo (APP, EXTERNA o BLOQUEO_MANTENIMIENTO),
// activa reembolso_pendiente si existió pago previo, revierte cupón, audita y notifica al jugador.
func (r *PostgresRepository) CancelReservaDuenoTx(ctx context.Context, complejoID, reservaID, duenoUserID uuid.UUID, motivo string) (DuenoReservaDetail, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return DuenoReservaDetail{}, fmt.Errorf("error iniciando transacción de cancelación por dueño: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	locked, err := lockDuenoReservaTx(ctx, tx, complejoID, reservaID)
	if err != nil {
		return DuenoReservaDetail{}, err
	}
	if locked.Estado != EstadoPendientePago && locked.Estado != EstadoPorValidar && locked.Estado != EstadoConfirmada {
		return DuenoReservaDetail{}, ErrInvalidReservaState
	}

	reembolsoPendiente := false
	switch locked.Origen {
	case OrigenApp:
		if locked.Estado == EstadoPorValidar || locked.Estado == EstadoConfirmada {
			reembolsoPendiente = true
		}
	case OrigenExterna:
		if toCents(locked.MontoAdelanto) > 0 {
			reembolsoPendiente = true
		}
	}

	const cancelSQL = `
		UPDATE reservas
		SET estado = 'CANCELADA',
		    cancelado_at = $3::timestamptz,
		    motivo_cancelacion = $4,
		    reembolso_pendiente = $5
		WHERE id = $1
		  AND complejo_id = $2
		  AND estado IN ('PENDIENTE_PAGO', 'POR_VALIDAR', 'CONFIRMADA')`

	tag, err := tx.Exec(ctx, cancelSQL, reservaID, complejoID, locked.DBNow, motivo, reembolsoPendiente)
	if err != nil {
		return DuenoReservaDetail{}, fmt.Errorf("error cancelando reserva por dueño: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return DuenoReservaDetail{}, ErrInvalidReservaState
	}

	if locked.CuponID != nil {
		if err := revertCuponUsoTx(ctx, tx, complejoID, *locked.CuponID); err != nil {
			return DuenoReservaDetail{}, err
		}
	}

	if err := insertAuditoriaTx(ctx, tx, auditInsertParams{
		ReservaID:        reservaID,
		ComplejoID:       complejoID,
		Accion:           AccionReservaCanceladaDueno,
		EstadoAnterior:   strPtr(locked.Estado),
		EstadoNuevo:      EstadoCancelada,
		CanchaAnteriorID: &locked.CanchaID,
		CanchaNuevaID:    &locked.CanchaID,
		RangoAnteriorIni: &locked.Inicio,
		RangoAnteriorFin: &locked.Fin,
		RangoNuevoIni:    &locked.Inicio,
		RangoNuevoFin:    &locked.Fin,
		ActorID:          &duenoUserID,
		Motivo:           motivo,
	}); err != nil {
		return DuenoReservaDetail{}, err
	}

	if locked.JugadorID != nil {
		iniLima := locked.Inicio.In(limaLocation)
		finLima := locked.Fin.In(limaLocation)
		msg := fmt.Sprintf(
			"Tu reserva en %s (%s) el %s de %s a %s fue cancelada por el complejo. Motivo: %s",
			locked.ComplejoNombre,
			locked.CanchaNombre,
			iniLima.Format("2006-01-02"),
			iniLima.Format("15:04"),
			finLima.Format("15:04"),
			motivo,
		)
		if reembolsoPendiente {
			msg += " El complejo gestionará el reembolso directo de tu adelanto."
		}
		if err := insertNotificacionTx(ctx, tx, *locked.JugadorID, &complejoID, &reservaID, NotifReservaCancelada, "Reserva cancelada por el complejo", msg); err != nil {
			return DuenoReservaDetail{}, err
		}
	}

	detail, err := getDuenoReservaDetail(ctx, tx, complejoID, reservaID)
	if err != nil {
		return DuenoReservaDetail{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return DuenoReservaDetail{}, fmt.Errorf("error confirmando cancelación por dueño: %w", err)
	}

	return detail, nil
}

// MarkReembolsoResueltoTx marca reembolso_pendiente = false y reembolso_resuelto_at = NOW() (RN-14).
func (r *PostgresRepository) MarkReembolsoResueltoTx(ctx context.Context, complejoID, reservaID, duenoUserID uuid.UUID, motivo string) (DuenoReservaDetail, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return DuenoReservaDetail{}, fmt.Errorf("error iniciando transacción de reembolso resuelto: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	locked, err := lockDuenoReservaTx(ctx, tx, complejoID, reservaID)
	if err != nil {
		return DuenoReservaDetail{}, err
	}
	if !locked.ReembolsoPendiente {
		return DuenoReservaDetail{}, ErrReembolsoNoPendiente
	}

	const updateSQL = `
		UPDATE reservas
		SET reembolso_pendiente = false,
		    reembolso_resuelto_at = $3::timestamptz
		WHERE id = $1
		  AND complejo_id = $2
		  AND reembolso_pendiente = true`

	tag, err := tx.Exec(ctx, updateSQL, reservaID, complejoID, locked.DBNow)
	if err != nil {
		return DuenoReservaDetail{}, fmt.Errorf("error marcando reembolso como resuelto: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return DuenoReservaDetail{}, ErrReembolsoNoPendiente
	}

	auditMotivo := motivo
	if auditMotivo == "" {
		auditMotivo = "Reembolso directo marcado como resuelto por el complejo"
	}

	if err := insertAuditoriaTx(ctx, tx, auditInsertParams{
		ReservaID:        reservaID,
		ComplejoID:       complejoID,
		Accion:           AccionReembolsoResuelto,
		EstadoAnterior:   strPtr(locked.Estado),
		EstadoNuevo:      locked.Estado,
		CanchaAnteriorID: &locked.CanchaID,
		CanchaNuevaID:    &locked.CanchaID,
		RangoAnteriorIni: &locked.Inicio,
		RangoAnteriorFin: &locked.Fin,
		RangoNuevoIni:    &locked.Inicio,
		RangoNuevoFin:    &locked.Fin,
		ActorID:          &duenoUserID,
		Motivo:           auditMotivo,
	}); err != nil {
		return DuenoReservaDetail{}, err
	}

	if locked.JugadorID != nil {
		msg := fmt.Sprintf(
			"%s registró como resuelto el reembolso directo asociado a tu reserva en %s.",
			locked.ComplejoNombre,
			locked.CanchaNombre,
		)
		if err := insertNotificacionTx(ctx, tx, *locked.JugadorID, &complejoID, &reservaID, NotifReembolsoResuelto, "Reembolso directo resuelto", msg); err != nil {
			return DuenoReservaDetail{}, err
		}
	}

	detail, err := getDuenoReservaDetail(ctx, tx, complejoID, reservaID)
	if err != nil {
		return DuenoReservaDetail{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return DuenoReservaDetail{}, fmt.Errorf("error confirmando reembolso resuelto: %w", err)
	}

	return detail, nil
}

// ReprogramarJugadorTx reprograma in-place una reserva CONFIRMADA del jugador respetando RN-13 y Decisión 4.
func (r *PostgresRepository) ReprogramarJugadorTx(ctx context.Context, in ReprogramarTxInput) (JugadorReservaDetail, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return JugadorReservaDetail{}, fmt.Errorf("error iniciando transacción de reprogramación: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	excedente, err := executeReprogramTx(ctx, tx, in)
	if err != nil {
		return JugadorReservaDetail{}, err
	}

	detail, _, err := getJugadorReservaDetailWithQuerier(ctx, tx, in.ReservaID)
	if err != nil {
		return JugadorReservaDetail{}, err
	}
	detail.ExcedenteReembolso = excedente

	if err := tx.Commit(ctx); err != nil {
		return JugadorReservaDetail{}, fmt.Errorf("error confirmando reprogramación del jugador: %w", err)
	}

	return detail, nil
}

// ReprogramarDuenoTx reprograma in-place una reserva CONFIRMADA del complejo (Decisión 4).
func (r *PostgresRepository) ReprogramarDuenoTx(ctx context.Context, in ReprogramarTxInput) (DuenoReservaDetail, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return DuenoReservaDetail{}, fmt.Errorf("error iniciando transacción de reprogramación por dueño: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	excedente, err := executeReprogramTx(ctx, tx, in)
	if err != nil {
		return DuenoReservaDetail{}, err
	}

	detail, err := getDuenoReservaDetail(ctx, tx, *in.ComplejoID, in.ReservaID)
	if err != nil {
		return DuenoReservaDetail{}, err
	}
	detail.ExcedenteReembolso = excedente

	if err := tx.Commit(ctx); err != nil {
		return DuenoReservaDetail{}, fmt.Errorf("error confirmando reprogramación por dueño: %w", err)
	}

	return detail, nil
}

func executeReprogramTx(ctx context.Context, tx pgx.Tx, in ReprogramarTxInput) (float64, error) {
	var (
		locked lockedReservaRow
		err    error
	)
	if in.IsDueno {
		if in.ComplejoID == nil {
			return 0, ErrReservaNotFound
		}
		locked, err = lockDuenoReservaTx(ctx, tx, *in.ComplejoID, in.ReservaID)
	} else {
		locked, err = lockReservaByIDTx(ctx, tx, in.ReservaID)
	}
	if err != nil {
		return 0, err
	}

	if !in.IsDueno {
		if in.JugadorID == nil || locked.JugadorID == nil || *locked.JugadorID != *in.JugadorID {
			return 0, ErrReservaForbidden
		}
	}

	if locked.Estado != EstadoConfirmada {
		return 0, ErrInvalidReservaState
	}

	if !in.IsDueno {
		if err := ValidateCancellationWindow(locked.Inicio, locked.HorasMinCancelacion, locked.DBNow); err != nil {
			return 0, err
		}
	}

	targetCanchaID := locked.CanchaID
	if in.CanchaID != nil && *in.CanchaID != uuid.Nil {
		targetCanchaID = *in.CanchaID
	}

	targetCancha, err := getActiveCancha(ctx, tx, locked.ComplejoID, targetCanchaID)
	if err != nil {
		return 0, err
	}

	dayStart, diaSemana, startMin, endMin, err := ExtractRangeMinutesLima(in.NuevoInicio, in.NuevoFin)
	if err != nil {
		return 0, err
	}
	normInicio := dayStart.Add(time.Duration(startMin) * time.Minute)
	normFin := dayStart.Add(time.Duration(endMin) * time.Minute)

	if targetCanchaID == locked.CanchaID && normInicio.Equal(locked.Inicio) && normFin.Equal(locked.Fin) {
		return 0, &ValidationError{
			Message: "La reprogramación requiere modificar la cancha o el rango horario",
			Fields: map[string]string{
				"nuevo_inicio": "La nueva cancha y horario son idénticos a la reserva actual",
			},
		}
	}

	var (
		precioBase         float64
		montoDescuento     float64
		montoTotal         float64
		montoAdelanto      float64
		saldoPendiente     float64
		reembolsoPendiente = locked.ReembolsoPendiente
		excedenteReembolso float64
	)

	if locked.Origen == OrigenBloqueoMantenimiento {
		if !normFin.After(locked.DBNow) {
			return 0, ErrSlotEnPasado
		}
	} else {
		complejo, err := getComplejoByIDForDueno(ctx, tx, locked.ComplejoID)
		if err != nil {
			return 0, err
		}
		horario, err := getHorarioByDia(ctx, tx, locked.ComplejoID, diaSemana)
		if err != nil {
			return 0, err
		}
		tarifas, err := listActiveTarifasForDay(ctx, tx, locked.ComplejoID, targetCancha.ID, diaSemana)
		if err != nil {
			return 0, err
		}
		var cuponPtr *CuponRecord
		if locked.CuponID != nil {
			cuponRec, err := getCuponByID(ctx, tx, locked.ComplejoID, *locked.CuponID)
			if err != nil {
				return 0, err
			}
			cuponPtr = &cuponRec
		}

		recalc, err := CalculateReprogramPricing(
			complejo,
			targetCancha.ID,
			horario,
			tarifas,
			normInicio,
			normFin,
			cuponPtr,
			locked.MontoAdelanto,
			locked.ReembolsoPendiente,
			locked.DBNow,
		)
		if err != nil {
			return 0, err
		}

		normInicio = recalc.Inicio
		normFin = recalc.Fin
		precioBase = recalc.PrecioBase
		montoDescuento = recalc.MontoDescuento
		montoTotal = recalc.MontoTotal
		montoAdelanto = recalc.MontoAdelanto
		saldoPendiente = recalc.SaldoPendiente
		reembolsoPendiente = recalc.ReembolsoPendiente
		excedenteReembolso = recalc.ExcedenteReembolso
	}

	if err := expireOverlappingStaleHoldsTx(ctx, tx, targetCancha.ID, normInicio, normFin, locked.DBNow); err != nil {
		return 0, err
	}

	const updateSQL = `
		UPDATE reservas
		SET cancha_id = $2,
		    rango_horario = tstzrange($3::timestamptz, $4::timestamptz, '[)'),
		    precio_base = $5::numeric(10,2),
		    monto_descuento = $6::numeric(10,2),
		    monto_total = $7::numeric(10,2),
		    monto_adelanto = $8::numeric(10,2),
		    saldo_pendiente = $9::numeric(10,2),
		    reembolso_pendiente = $10,
		    reprogramaciones_count = reprogramaciones_count + 1
		WHERE id = $1
		  AND estado = 'CONFIRMADA'`

	tag, err := tx.Exec(ctx, updateSQL,
		locked.ID,
		targetCancha.ID,
		normInicio,
		normFin,
		formatDecimal(precioBase),
		formatDecimal(montoDescuento),
		formatDecimal(montoTotal),
		formatDecimal(montoAdelanto),
		formatDecimal(saldoPendiente),
		reembolsoPendiente,
	)
	if err != nil {
		return 0, mapReservaPgError(err)
	}
	if tag.RowsAffected() == 0 {
		return 0, ErrInvalidReservaState
	}

	accion := AccionReprogramadaJugador
	if in.IsDueno {
		accion = AccionReprogramadaDueno
	}

	auditMotivo := strings.TrimSpace(in.Motivo)
	if auditMotivo == "" {
		if in.IsDueno {
			auditMotivo = "Reserva reprogramada por el complejo"
		} else {
			auditMotivo = "Reserva reprogramada por el jugador"
		}
	}
	if excedenteReembolso > 0 {
		auditMotivo = fmt.Sprintf(
			"%s (nuevo total S/ %.2f menor al adelanto previo S/ %.2f; excedente pendiente de reembolso: S/ %.2f)",
			auditMotivo, montoTotal, locked.MontoAdelanto, excedenteReembolso,
		)
	}

	if err := insertAuditoriaTx(ctx, tx, auditInsertParams{
		ReservaID:        locked.ID,
		ComplejoID:       locked.ComplejoID,
		Accion:           accion,
		EstadoAnterior:   strPtr(EstadoConfirmada),
		EstadoNuevo:      EstadoConfirmada,
		CanchaAnteriorID: &locked.CanchaID,
		CanchaNuevaID:    &targetCancha.ID,
		RangoAnteriorIni: &locked.Inicio,
		RangoAnteriorFin: &locked.Fin,
		RangoNuevoIni:    &normInicio,
		RangoNuevoFin:    &normFin,
		ActorID:          &in.ActorID,
		Motivo:           auditMotivo,
	}); err != nil {
		return 0, err
	}

	newIniLima := normInicio.In(limaLocation)
	newFinLima := normFin.In(limaLocation)
	if in.IsDueno {
		if locked.JugadorID != nil {
			msg := fmt.Sprintf(
				"%s reprogramó tu reserva para %s el %s de %s a %s. Nuevo saldo pendiente: S/ %.2f.",
				locked.ComplejoNombre,
				targetCancha.Nombre,
				newIniLima.Format("2006-01-02"),
				newIniLima.Format("15:04"),
				newFinLima.Format("15:04"),
				saldoPendiente,
			)
			if excedenteReembolso > 0 {
				msg += fmt.Sprintf(" Existe un excedente de S/ %.2f pendiente de reembolso directo.", excedenteReembolso)
			}
			if err := insertNotificacionTx(ctx, tx, *locked.JugadorID, &locked.ComplejoID, &locked.ID, NotifReservaReprogramada, "Reserva reprogramada por el complejo", msg); err != nil {
				return 0, err
			}
		}
	} else {
		msg := fmt.Sprintf(
			"Un jugador reprogramó su reserva a %s el %s de %s a %s. Nuevo saldo pendiente: S/ %.2f.",
			targetCancha.Nombre,
			newIniLima.Format("2006-01-02"),
			newIniLima.Format("15:04"),
			newFinLima.Format("15:04"),
			saldoPendiente,
		)
		if excedenteReembolso > 0 {
			msg += fmt.Sprintf(" Requiere reembolso directo de excedente por S/ %.2f.", excedenteReembolso)
		}
		if err := insertNotificacionTx(ctx, tx, locked.DuenoID, &locked.ComplejoID, &locked.ID, NotifReservaReprogramada, "Reserva reprogramada por jugador", msg); err != nil {
			return 0, err
		}
	}

	return excedenteReembolso, nil
}

// ---------- Helpers internos transaccionales (Fase 3B) ----------

type lockedReservaRow struct {
	ID                  uuid.UUID
	ComplejoID          uuid.UUID
	ComplejoNombre      string
	DuenoID             uuid.UUID
	HorasMinCancelacion int16
	CanchaID            uuid.UUID
	CanchaNombre        string
	JugadorID           *uuid.UUID
	Origen              string
	Estado              string
	CuponID             *uuid.UUID
	Inicio              time.Time
	Fin                 time.Time
	MontoTotal          float64
	MontoAdelanto       float64
	SaldoPendiente      float64
	ReembolsoPendiente  bool
	DBNow               time.Time
}

func lockDuenoReservaTx(ctx context.Context, tx pgx.Tx, complejoID, reservaID uuid.UUID) (lockedReservaRow, error) {
	const sql = `
		SELECT
			r.id,
			r.complejo_id,
			c.nombre,
			c.dueno_id,
			c.horas_min_cancelacion,
			r.cancha_id,
			ca.nombre,
			r.jugador_id,
			r.origen,
			r.estado,
			r.cupon_id,
			lower(r.rango_horario),
			upper(r.rango_horario),
			r.monto_total::float8,
			r.monto_adelanto::float8,
			r.saldo_pendiente::float8,
			r.reembolso_pendiente,
			NOW()
		FROM reservas r
		INNER JOIN complejos c ON c.id = r.complejo_id
		INNER JOIN canchas ca ON ca.id = r.cancha_id
		WHERE r.id = $1
		  AND r.complejo_id = $2
		FOR UPDATE OF r`

	return scanLockedReserva(ctx, tx.QueryRow(ctx, sql, reservaID, complejoID))
}

func lockReservaByIDTx(ctx context.Context, tx pgx.Tx, reservaID uuid.UUID) (lockedReservaRow, error) {
	const sql = `
		SELECT
			r.id,
			r.complejo_id,
			c.nombre,
			c.dueno_id,
			c.horas_min_cancelacion,
			r.cancha_id,
			ca.nombre,
			r.jugador_id,
			r.origen,
			r.estado,
			r.cupon_id,
			lower(r.rango_horario),
			upper(r.rango_horario),
			r.monto_total::float8,
			r.monto_adelanto::float8,
			r.saldo_pendiente::float8,
			r.reembolso_pendiente,
			NOW()
		FROM reservas r
		INNER JOIN complejos c ON c.id = r.complejo_id
		INNER JOIN canchas ca ON ca.id = r.cancha_id
		WHERE r.id = $1
		FOR UPDATE OF r`

	return scanLockedReserva(ctx, tx.QueryRow(ctx, sql, reservaID))
}

func scanLockedReserva(ctx context.Context, row pgx.Row) (lockedReservaRow, error) {
	_ = ctx
	var out lockedReservaRow
	if err := row.Scan(
		&out.ID,
		&out.ComplejoID,
		&out.ComplejoNombre,
		&out.DuenoID,
		&out.HorasMinCancelacion,
		&out.CanchaID,
		&out.CanchaNombre,
		&out.JugadorID,
		&out.Origen,
		&out.Estado,
		&out.CuponID,
		&out.Inicio,
		&out.Fin,
		&out.MontoTotal,
		&out.MontoAdelanto,
		&out.SaldoPendiente,
		&out.ReembolsoPendiente,
		&out.DBNow,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return lockedReservaRow{}, ErrReservaNotFound
		}
		return lockedReservaRow{}, fmt.Errorf("error bloqueando reserva: %w", err)
	}
	return out, nil
}

func getDuenoReservaDetail(ctx context.Context, q dbQuerier, complejoID, reservaID uuid.UUID) (DuenoReservaDetail, error) {
	const sql = `
		SELECT
			r.id,
			r.complejo_id,
			ca.id,
			ca.nombre,
			ca.modalidad,
			ca.superficie,
			ca.techada,
			ca.iluminacion,
			r.origen,
			r.estado,
			lower(r.rango_horario),
			upper(r.rango_horario),
			r.jugador_id,
			u.nombre_completo,
			u.telefono,
			r.cliente_externo_nombre,
			r.cliente_externo_telefono,
			r.motivo_bloqueo,
			r.notas_internas,
			r.cupon_id,
			r.precio_base::float8,
			r.monto_descuento::float8,
			r.monto_total::float8,
			r.monto_adelanto::float8,
			r.saldo_pendiente::float8,
			r.metodo_pago_saldo,
			r.saldo_pagado_at,
			r.hold_expira_at,
			r.por_validar_desde,
			CASE WHEN r.estado IN ('CONFIRMADA', 'COMPLETADA') THEN r.codigo_qr ELSE NULL END,
			r.checkin_at,
			r.cancelado_at,
			r.motivo_cancelacion,
			r.reembolso_pendiente,
			r.reembolso_resuelto_at,
			r.reprogramaciones_count,
			r.created_at,
			r.updated_at,
			NOW()
		FROM reservas r
		INNER JOIN canchas ca ON ca.id = r.cancha_id
		LEFT JOIN usuarios u ON u.id = r.jugador_id
		WHERE r.id = $1 AND r.complejo_id = $2`

	var (
		out                 DuenoReservaDetail
		inicioDB, finDB     time.Time
		saldoPagadoAt       *time.Time
		holdExpiraAt        *time.Time
		porValidarDesde     *time.Time
		checkinAt           *time.Time
		canceladoAt         *time.Time
		reembolsoResueltoAt *time.Time
		createdAt           time.Time
		updatedAt           time.Time
		dbNow               time.Time
	)

	if err := q.QueryRow(ctx, sql, reservaID, complejoID).Scan(
		&out.ID,
		&out.ComplejoID,
		&out.Cancha.ID,
		&out.Cancha.Nombre,
		&out.Cancha.Modalidad,
		&out.Cancha.Superficie,
		&out.Cancha.Techada,
		&out.Cancha.Iluminacion,
		&out.Origen,
		&out.Estado,
		&inicioDB,
		&finDB,
		&out.JugadorID,
		&out.JugadorNombre,
		&out.JugadorTelefono,
		&out.ClienteExternoNombre,
		&out.ClienteExternoTelefono,
		&out.MotivoBloqueo,
		&out.NotasInternas,
		&out.CuponID,
		&out.PrecioBase,
		&out.MontoDescuento,
		&out.MontoTotal,
		&out.MontoAdelanto,
		&out.SaldoPendiente,
		&out.MetodoPagoSaldo,
		&saldoPagadoAt,
		&holdExpiraAt,
		&porValidarDesde,
		&out.CodigoQR,
		&checkinAt,
		&canceladoAt,
		&out.MotivoCancelacion,
		&out.ReembolsoPendiente,
		&reembolsoResueltoAt,
		&out.ReprogramacionesCount,
		&createdAt,
		&updatedAt,
		&dbNow,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DuenoReservaDetail{}, ErrReservaNotFound
		}
		return DuenoReservaDetail{}, fmt.Errorf("error consultando detalle de reserva para dueño: %w", err)
	}

	out.Cancha.ComplejoID = out.ComplejoID
	out.Inicio = inicioDB.In(limaLocation)
	out.Fin = finDB.In(limaLocation)
	out.DuracionMin = int(finDB.Sub(inicioDB) / time.Minute)
	out.SaldoPagadoAt = toLimaPtr(saldoPagadoAt)
	out.HoldExpiraAt = toLimaPtr(holdExpiraAt)
	out.PorValidarDesde = toLimaPtr(porValidarDesde)
	out.CheckinAt = toLimaPtr(checkinAt)
	out.CanceladoAt = toLimaPtr(canceladoAt)
	out.ReembolsoResueltoAt = toLimaPtr(reembolsoResueltoAt)
	out.CreatedAt = createdAt.In(limaLocation)
	out.UpdatedAt = updatedAt.In(limaLocation)
	if out.Estado == EstadoPorValidar && porValidarDesde != nil {
		out.AlertaDemora = dbNow.Sub(*porValidarDesde) > AlertaDemoraThreshold
	}

	comprobantes, err := listComprobantesByReserva(ctx, q, out.ID)
	if err != nil {
		return DuenoReservaDetail{}, err
	}
	out.Comprobantes = comprobantes

	return out, nil
}

func getJugadorReservaDetailWithQuerier(ctx context.Context, q dbQuerier, reservaID uuid.UUID) (JugadorReservaDetail, time.Time, error) {
	const detailSQL = `
		SELECT
			r.id,
			r.jugador_id,
			r.estado,
			r.origen,
			lower(r.rango_horario),
			upper(r.rango_horario),
			r.precio_base::float8,
			r.monto_descuento::float8,
			r.monto_total::float8,
			r.monto_adelanto::float8,
			r.saldo_pendiente::float8,
			r.metodo_pago_saldo,
			r.saldo_pagado_at,
			r.terminos_aceptados_at,
			r.hold_expira_at,
			r.por_validar_desde,
			r.codigo_qr,
			r.checkin_at,
			r.cancelado_at,
			r.motivo_cancelacion,
			r.reembolso_pendiente,
			r.reembolso_resuelto_at,
			r.reprogramaciones_count,
			r.created_at,
			r.updated_at,
			c.id,
			c.slug,
			c.nombre,
			c.direccion_escrita,
			c.referencias,
			c.distrito,
			c.provincia,
			c.departamento,
			c.telefono_contacto,
			CASE WHEN c.ubicacion IS NOT NULL THEN ST_Y(c.ubicacion::geometry)::float8 ELSE NULL END,
			CASE WHEN c.ubicacion IS NOT NULL THEN ST_X(c.ubicacion::geometry)::float8 ELSE NULL END,
			c.horas_min_cancelacion,
			ca.id,
			ca.nombre,
			ca.modalidad,
			ca.superficie,
			ca.techada,
			ca.iluminacion,
			NOW()
		FROM reservas r
		INNER JOIN complejos c ON c.id = r.complejo_id
		INNER JOIN canchas ca ON ca.id = r.cancha_id
		WHERE r.id = $1`

	var (
		out                 JugadorReservaDetail
		jugadorIDPtr        *uuid.UUID
		inicioDB, finDB     time.Time
		saldoPagadoAt       *time.Time
		terminosAceptadosAt *time.Time
		holdExpiraAt        *time.Time
		porValidarDesde     *time.Time
		checkinAt           *time.Time
		canceladoAt         *time.Time
		reembolsoResueltoAt *time.Time
		createdAt           time.Time
		updatedAt           time.Time
		dbNow               time.Time
	)

	err := q.QueryRow(ctx, detailSQL, reservaID).Scan(
		&out.ID,
		&jugadorIDPtr,
		&out.Estado,
		&out.Origen,
		&inicioDB,
		&finDB,
		&out.PrecioBase,
		&out.MontoDescuento,
		&out.MontoTotal,
		&out.MontoAdelanto,
		&out.SaldoPendiente,
		&out.MetodoPagoSaldo,
		&saldoPagadoAt,
		&terminosAceptadosAt,
		&holdExpiraAt,
		&porValidarDesde,
		&out.CodigoQR,
		&checkinAt,
		&canceladoAt,
		&out.MotivoCancelacion,
		&out.ReembolsoPendiente,
		&reembolsoResueltoAt,
		&out.ReprogramacionesCount,
		&createdAt,
		&updatedAt,
		&out.Complejo.ID,
		&out.Complejo.Slug,
		&out.Complejo.Nombre,
		&out.Complejo.DireccionEscrita,
		&out.Complejo.Referencias,
		&out.Complejo.Distrito,
		&out.Complejo.Provincia,
		&out.Complejo.Departamento,
		&out.Complejo.TelefonoContacto,
		&out.Complejo.Lat,
		&out.Complejo.Lng,
		&out.Complejo.HorasMinCancelacion,
		&out.Cancha.ID,
		&out.Cancha.Nombre,
		&out.Cancha.Modalidad,
		&out.Cancha.Superficie,
		&out.Cancha.Techada,
		&out.Cancha.Iluminacion,
		&dbNow,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return JugadorReservaDetail{}, time.Time{}, ErrReservaNotFound
		}
		return JugadorReservaDetail{}, time.Time{}, fmt.Errorf("error consultando detalle de reserva: %w", err)
	}

	if jugadorIDPtr != nil {
		out.JugadorID = *jugadorIDPtr
	}
	out.Cancha.ComplejoID = out.Complejo.ID
	out.Inicio = inicioDB.In(limaLocation)
	out.Fin = finDB.In(limaLocation)
	out.DuracionMin = int(finDB.Sub(inicioDB) / time.Minute)
	out.SaldoPagadoAt = toLimaPtr(saldoPagadoAt)
	out.TerminosAceptadosAt = toLimaPtr(terminosAceptadosAt)
	out.HoldExpiraAt = toLimaPtr(holdExpiraAt)
	out.PorValidarDesde = toLimaPtr(porValidarDesde)
	out.CheckinAt = toLimaPtr(checkinAt)
	out.CanceladoAt = toLimaPtr(canceladoAt)
	out.ReembolsoResueltoAt = toLimaPtr(reembolsoResueltoAt)
	out.CreatedAt = createdAt.In(limaLocation)
	out.UpdatedAt = updatedAt.In(limaLocation)

	comprobantes, err := listComprobantesByReserva(ctx, q, out.ID)
	if err != nil {
		return JugadorReservaDetail{}, time.Time{}, err
	}
	out.Comprobantes = comprobantes

	return out, dbNow, nil
}

func getComplejoByIDForDueno(ctx context.Context, q dbQuerier, complejoID uuid.UUID) (ComplejoContext, error) {
	const query = `
		SELECT
			id, dueno_id, slug, nombre, publicado,
			unidad_reserva_min, hold_duracion_min,
			tipo_adelanto, valor_adelanto::float8,
			horas_min_cancelacion, terminos_reserva,
			estado_suscripcion, suscripcion_vence_at
		FROM complejos
		WHERE id = $1`

	var c ComplejoContext
	err := q.QueryRow(ctx, query, complejoID).Scan(
		&c.ID,
		&c.DuenoID,
		&c.Slug,
		&c.Nombre,
		&c.Publicado,
		&c.UnidadReservaMin,
		&c.HoldDuracionMin,
		&c.TipoAdelanto,
		&c.ValorAdelanto,
		&c.HorasMinCancelacion,
		&c.TerminosReserva,
		&c.EstadoSuscripcion,
		&c.SuscripcionVenceAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ComplejoContext{}, ErrComplejoNotFound
		}
		return ComplejoContext{}, fmt.Errorf("error consultando complejo del dueño: %w", err)
	}
	return c, nil
}

func getCuponByID(ctx context.Context, q dbQuerier, complejoID, cuponID uuid.UUID) (CuponRecord, error) {
	const query = `
		SELECT
			id, complejo_id, codigo, descripcion, tipo_descuento,
			valor_descuento::float8, valido_desde, valido_hasta,
			max_usos, usos_actuales, acumulable_hora_muerta, activo
		FROM cupones
		WHERE id = $1 AND complejo_id = $2`

	var c CuponRecord
	if err := q.QueryRow(ctx, query, cuponID, complejoID).Scan(
		&c.ID,
		&c.ComplejoID,
		&c.Codigo,
		&c.Descripcion,
		&c.TipoDescuento,
		&c.ValorDescuento,
		&c.ValidoDesde,
		&c.ValidoHasta,
		&c.MaxUsos,
		&c.UsosActuales,
		&c.AcumulableHoraMuerta,
		&c.Activo,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CuponRecord{}, ErrCuponNotFound
		}
		return CuponRecord{}, fmt.Errorf("error consultando cupón por ID: %w", err)
	}
	return c, nil
}

func revertCuponUsoTx(ctx context.Context, tx pgx.Tx, complejoID, cuponID uuid.UUID) error {
	const sql = `
		UPDATE cupones
		SET usos_actuales = GREATEST(usos_actuales - 1, 0)
		WHERE id = $1 AND complejo_id = $2`

	if _, err := tx.Exec(ctx, sql, cuponID, complejoID); err != nil {
		return fmt.Errorf("error revirtiendo uso de cupón: %w", err)
	}
	return nil
}

type auditInsertParams struct {
	ReservaID        uuid.UUID
	ComplejoID       uuid.UUID
	Accion           string
	EstadoAnterior   *string
	EstadoNuevo      string
	CanchaAnteriorID *uuid.UUID
	CanchaNuevaID    *uuid.UUID
	RangoAnteriorIni *time.Time
	RangoAnteriorFin *time.Time
	RangoNuevoIni    *time.Time
	RangoNuevoFin    *time.Time
	ActorID          *uuid.UUID
	Motivo           string
}

func insertAuditoriaTx(ctx context.Context, tx pgx.Tx, p auditInsertParams) error {
	const sql = `
		INSERT INTO reserva_auditoria (
			reserva_id,
			complejo_id,
			accion,
			estado_anterior,
			estado_nuevo,
			cancha_anterior_id,
			cancha_nueva_id,
			rango_anterior,
			rango_nuevo,
			actor_id,
			motivo
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			CASE WHEN $8::timestamptz IS NOT NULL AND $9::timestamptz IS NOT NULL
			     THEN tstzrange($8::timestamptz, $9::timestamptz, '[)')
			     ELSE NULL END,
			CASE WHEN $10::timestamptz IS NOT NULL AND $11::timestamptz IS NOT NULL
			     THEN tstzrange($10::timestamptz, $11::timestamptz, '[)')
			     ELSE NULL END,
			$12, $13
		)`

	var motivoPtr *string
	if strings.TrimSpace(p.Motivo) != "" {
		m := strings.TrimSpace(p.Motivo)
		motivoPtr = &m
	}

	if _, err := tx.Exec(ctx, sql,
		p.ReservaID,
		p.ComplejoID,
		p.Accion,
		p.EstadoAnterior,
		p.EstadoNuevo,
		p.CanchaAnteriorID,
		p.CanchaNuevaID,
		p.RangoAnteriorIni,
		p.RangoAnteriorFin,
		p.RangoNuevoIni,
		p.RangoNuevoFin,
		p.ActorID,
		motivoPtr,
	); err != nil {
		return fmt.Errorf("error registrando auditoría de reserva (%s): %w", p.Accion, err)
	}
	return nil
}

func insertNotificacionTx(ctx context.Context, tx pgx.Tx, usuarioID uuid.UUID, complejoID, reservaID *uuid.UUID, tipo, titulo, mensaje string) error {
	const sql = `
		INSERT INTO notificaciones (usuario_id, complejo_id, reserva_id, tipo, titulo, mensaje)
		VALUES ($1, $2, $3, $4, $5, $6)`

	if _, err := tx.Exec(ctx, sql, usuarioID, complejoID, reservaID, tipo, titulo, mensaje); err != nil {
		return fmt.Errorf("error creando notificación (%s): %w", tipo, err)
	}
	return nil
}

func strPtr(s string) *string {
	return &s
}
