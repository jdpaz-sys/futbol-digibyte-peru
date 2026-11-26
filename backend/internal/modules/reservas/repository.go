package reservas

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CreateHoldTxInput encapsula los datos validados para crear un Hold atómico en PostgreSQL.
type CreateHoldTxInput struct {
	ComplejoID  uuid.UUID
	CanchaID    uuid.UUID
	JugadorID   uuid.UUID
	Inicio      time.Time
	Fin         time.Time
	CodigoCupon string
}

// CreateHoldTxOutput devuelve los resultados persistidos dentro de la transacción de Hold.
type CreateHoldTxOutput struct {
	Reserva     ReservaResumen
	Pricing     PricingResult
	MetodosPago []MetodoPagoPublico
	ServerNow   time.Time
}

// Store abstrae la persistencia y consultas transaccionales del módulo reservas.
type Store interface {
	GetComplejoBySlug(ctx context.Context, slug string) (ComplejoContext, error)
	GetActiveCancha(ctx context.Context, complejoID, canchaID uuid.UUID) (CanchaInfo, error)
	GetHorarioByDia(ctx context.Context, complejoID uuid.UUID, diaSemana int16) (*HorarioAtencion, error)
	ListActiveTarifasForDay(ctx context.Context, complejoID, canchaID uuid.UUID, diaSemana int16) ([]TarifaFranja, error)
	ListOcupacionesRange(ctx context.Context, canchaID uuid.UUID, rangeStart, rangeEnd, now time.Time) ([]OcupacionIntervalo, error)
	GetCuponByCodigo(ctx context.Context, complejoID uuid.UUID, codigo string) (CuponRecord, error)

	CreateHoldTx(ctx context.Context, in CreateHoldTxInput) (CreateHoldTxOutput, error)
	PrecheckUploadComprobante(ctx context.Context, reservaID, jugadorID, metodoPagoID uuid.UUID) (uuid.UUID, error)
	RegisterComprobanteTx(ctx context.Context, params RegisterComprobanteParams) (UploadComprobanteResponse, error)

	ListJugadorReservas(ctx context.Context, jugadorID uuid.UUID, filter ListJugadorReservasFilter) ([]JugadorReservaItem, int, time.Time, error)
	GetJugadorReservaDetail(ctx context.Context, reservaID uuid.UUID) (JugadorReservaDetail, time.Time, error)
	GetComprobanteAccess(ctx context.Context, comprobanteID uuid.UUID) (ComprobanteAccessRecord, error)

	ExpireStaleHolds(ctx context.Context, batchSize int) (int, error)
}

// HoldExpirer representa el contrato mínimo requerido por el worker automático de expiración.
type HoldExpirer interface {
	ExpireStaleHolds(ctx context.Context, batchSize int) (int, error)
}

// PostgresRepository implementa Store sobre PostgreSQL 16 + PostGIS mediante pgx/v5.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

// NewRepository crea el repositorio PostgreSQL del módulo reservas.
func NewRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) GetComplejoBySlug(ctx context.Context, slug string) (ComplejoContext, error) {
	const query = `
		SELECT
			id, dueno_id, slug, nombre, publicado,
			unidad_reserva_min, hold_duracion_min,
			tipo_adelanto, valor_adelanto::float8,
			horas_min_cancelacion, terminos_reserva,
			estado_suscripcion, suscripcion_vence_at
		FROM complejos
		WHERE slug = $1 AND publicado = true`

	var c ComplejoContext
	err := r.pool.QueryRow(ctx, query, slug).Scan(
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
		return ComplejoContext{}, fmt.Errorf("error consultando complejo por slug: %w", err)
	}
	return c, nil
}

func (r *PostgresRepository) GetActiveCancha(ctx context.Context, complejoID, canchaID uuid.UUID) (CanchaInfo, error) {
	return getActiveCancha(ctx, r.pool, complejoID, canchaID)
}

func (r *PostgresRepository) GetHorarioByDia(ctx context.Context, complejoID uuid.UUID, diaSemana int16) (*HorarioAtencion, error) {
	return getHorarioByDia(ctx, r.pool, complejoID, diaSemana)
}

func (r *PostgresRepository) ListActiveTarifasForDay(ctx context.Context, complejoID, canchaID uuid.UUID, diaSemana int16) ([]TarifaFranja, error) {
	return listActiveTarifasForDay(ctx, r.pool, complejoID, canchaID, diaSemana)
}

func (r *PostgresRepository) ListOcupacionesRange(ctx context.Context, canchaID uuid.UUID, rangeStart, rangeEnd, now time.Time) ([]OcupacionIntervalo, error) {
	const query = `
		SELECT lower(rango_horario), upper(rango_horario), estado
		FROM reservas
		WHERE cancha_id = $1
		  AND rango_horario && tstzrange($2::timestamptz, $3::timestamptz, '[)')
		  AND (
			estado IN ('POR_VALIDAR', 'CONFIRMADA')
			OR (estado = 'PENDIENTE_PAGO' AND hold_expira_at > $4::timestamptz)
		  )
		ORDER BY lower(rango_horario) ASC`

	rows, err := r.pool.Query(ctx, query, canchaID, rangeStart, rangeEnd, now)
	if err != nil {
		return nil, fmt.Errorf("error consultando ocupaciones de cancha: %w", err)
	}
	defer rows.Close()

	out := make([]OcupacionIntervalo, 0)
	for rows.Next() {
		var oc OcupacionIntervalo
		if err := rows.Scan(&oc.Inicio, &oc.Fin, &oc.Estado); err != nil {
			return nil, fmt.Errorf("error escaneando ocupación de cancha: %w", err)
		}
		out = append(out, oc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterando ocupaciones de cancha: %w", err)
	}
	return out, nil
}

func (r *PostgresRepository) GetCuponByCodigo(ctx context.Context, complejoID uuid.UUID, codigo string) (CuponRecord, error) {
	return getCuponByCodigo(ctx, r.pool, complejoID, codigo)
}

// CreateHoldTx ejecuta en una única transacción SQL: validación de vigencia y cancha,
// recálculo en servidor, incremento atómico del cupón, expiración perezosa de holds vencidos
// en conflicto, inserción GIST anticolisión, auditoría y lectura de medios de cobro directos.
func (r *PostgresRepository) CreateHoldTx(ctx context.Context, in CreateHoldTxInput) (CreateHoldTxOutput, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return CreateHoldTxOutput{}, fmt.Errorf("error iniciando transacción de reserva: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var dbNow time.Time
	if err := tx.QueryRow(ctx, `SELECT NOW()`).Scan(&dbNow); err != nil {
		return CreateHoldTxOutput{}, fmt.Errorf("error obteniendo hora del servidor: %w", err)
	}

	complejo, err := getComplejoByID(ctx, tx, in.ComplejoID)
	if err != nil {
		return CreateHoldTxOutput{}, err
	}
	if !isSuscripcionVigente(complejo, dbNow) {
		return CreateHoldTxOutput{}, ErrComplejoNoDisponible
	}

	cancha, err := getActiveCancha(ctx, tx, complejo.ID, in.CanchaID)
	if err != nil {
		return CreateHoldTxOutput{}, err
	}

	_, diaSemana, _, _, err := ExtractRangeMinutesLima(in.Inicio, in.Fin)
	if err != nil {
		return CreateHoldTxOutput{}, err
	}

	horario, err := getHorarioByDia(ctx, tx, complejo.ID, diaSemana)
	if err != nil {
		return CreateHoldTxOutput{}, err
	}

	tarifas, err := listActiveTarifasForDay(ctx, tx, complejo.ID, cancha.ID, diaSemana)
	if err != nil {
		return CreateHoldTxOutput{}, err
	}

	var cuponPtr *CuponRecord
	if in.CodigoCupon != "" {
		cuponRec, err := getCuponByCodigo(ctx, tx, complejo.ID, in.CodigoCupon)
		if err != nil {
			return CreateHoldTxOutput{}, err
		}
		cuponPtr = &cuponRec
	}

	pricing, err := CalculatePricing(complejo, cancha.ID, horario, tarifas, in.Inicio, in.Fin, cuponPtr, dbNow)
	if err != nil {
		return CreateHoldTxOutput{}, err
	}

	var cuponID *uuid.UUID
	if cuponPtr != nil {
		const incCuponSQL = `
			UPDATE cupones
			SET usos_actuales = usos_actuales + 1
			WHERE id = $1
			  AND complejo_id = $2
			  AND activo = true
			  AND valido_desde <= $3::timestamptz
			  AND (valido_hasta IS NULL OR valido_hasta >= $3::timestamptz)
			  AND (max_usos IS NULL OR usos_actuales < max_usos)
			RETURNING id`

		var updatedCuponID uuid.UUID
		if err := tx.QueryRow(ctx, incCuponSQL, cuponPtr.ID, complejo.ID, dbNow).Scan(&updatedCuponID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return CreateHoldTxOutput{}, ErrCuponExhausted
			}
			return CreateHoldTxOutput{}, fmt.Errorf("error reservando uso de cupón: %w", err)
		}
		cuponID = &updatedCuponID
	}

	// Libera de forma atómica holds vencidos que aún no fueron barridos por el worker de 30s
	// para evitar falsos conflictos 409 cuando un slot ya venció y se muestra disponible.
	if err := expireOverlappingStaleHoldsTx(ctx, tx, cancha.ID, pricing.Inicio, pricing.Fin, dbNow); err != nil {
		return CreateHoldTxOutput{}, err
	}

	const insertReservaSQL = `
		INSERT INTO reservas (
			complejo_id,
			cancha_id,
			jugador_id,
			creado_por,
			origen,
			rango_horario,
			estado,
			cupon_id,
			precio_base,
			monto_descuento,
			monto_total,
			monto_adelanto,
			saldo_pendiente,
			terminos_aceptados_at,
			hold_expira_at
		)
		VALUES (
			$1, $2, $3, $3, 'APP',
			tstzrange($4::timestamptz, $5::timestamptz, '[)'),
			'PENDIENTE_PAGO',
			$6,
			$7::numeric(10,2),
			$8::numeric(10,2),
			$9::numeric(10,2),
			$10::numeric(10,2),
			$11::numeric(10,2),
			$12::timestamptz,
			$12::timestamptz + make_interval(mins => $13::integer)
		)
		RETURNING
			id,
			lower(rango_horario),
			upper(rango_horario),
			terminos_aceptados_at,
			hold_expira_at,
			created_at`

	var (
		reservaID           uuid.UUID
		inicioDB, finDB     time.Time
		terminosAceptadosAt time.Time
		holdExpiraAt        time.Time
		createdAt           time.Time
	)

	err = tx.QueryRow(ctx, insertReservaSQL,
		complejo.ID,
		cancha.ID,
		in.JugadorID,
		pricing.Inicio,
		pricing.Fin,
		cuponID,
		formatDecimal(pricing.PrecioBase),
		formatDecimal(pricing.MontoDescuento),
		formatDecimal(pricing.MontoTotal),
		formatDecimal(pricing.MontoAdelanto),
		formatDecimal(pricing.SaldoPendiente),
		dbNow,
		int(complejo.HoldDuracionMin),
	).Scan(
		&reservaID,
		&inicioDB,
		&finDB,
		&terminosAceptadosAt,
		&holdExpiraAt,
		&createdAt,
	)
	if err != nil {
		return CreateHoldTxOutput{}, mapReservaPgError(err)
	}

	const insertAuditSQL = `
		INSERT INTO reserva_auditoria (
			reserva_id,
			complejo_id,
			accion,
			estado_anterior,
			estado_nuevo,
			cancha_nueva_id,
			rango_nuevo,
			actor_id,
			motivo
		)
		VALUES (
			$1, $2, $3, NULL, 'PENDIENTE_PAGO', $4,
			tstzrange($5::timestamptz, $6::timestamptz, '[)'),
			$7, $8
		)`

	if _, err := tx.Exec(ctx, insertAuditSQL,
		reservaID,
		complejo.ID,
		AccionHoldCreado,
		cancha.ID,
		inicioDB,
		finDB,
		in.JugadorID,
		"Bloqueo temporal creado por jugador tras aceptar términos de reserva",
	); err != nil {
		return CreateHoldTxOutput{}, fmt.Errorf("error registrando auditoría de hold: %w", err)
	}

	metodos, err := listActiveMetodosPagoPublico(ctx, tx, complejo.ID)
	if err != nil {
		return CreateHoldTxOutput{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return CreateHoldTxOutput{}, fmt.Errorf("error confirmando transacción de reserva: %w", err)
	}

	resumen := ReservaResumen{
		ID:                  reservaID,
		ComplejoID:          complejo.ID,
		ComplejoNombre:      complejo.Nombre,
		CanchaID:            cancha.ID,
		CanchaNombre:        cancha.Nombre,
		JugadorID:           in.JugadorID,
		Origen:              OrigenApp,
		Estado:              EstadoPendientePago,
		Inicio:              inicioDB.In(limaLocation),
		Fin:                 finDB.In(limaLocation),
		DuracionMin:         pricing.DuracionMin,
		CuponID:             cuponID,
		PrecioBase:          pricing.PrecioBase,
		MontoDescuento:      pricing.MontoDescuento,
		MontoTotal:          pricing.MontoTotal,
		MontoAdelanto:       pricing.MontoAdelanto,
		SaldoPendiente:      pricing.SaldoPendiente,
		TerminosAceptadosAt: terminosAceptadosAt.In(limaLocation),
		HoldExpiraAt:        holdExpiraAt.In(limaLocation),
		CreatedAt:           createdAt.In(limaLocation),
	}

	return CreateHoldTxOutput{
		Reserva:     resumen,
		Pricing:     pricing,
		MetodosPago: metodos,
		ServerNow:   dbNow,
	}, nil
}

// PrecheckUploadComprobante verifica titularidad, vigencia del Hold y método de pago manual
// antes de escribir archivos en Storage para evitar archivos huérfanos por peticiones inválidas.
func (r *PostgresRepository) PrecheckUploadComprobante(ctx context.Context, reservaID, jugadorID, metodoPagoID uuid.UUID) (uuid.UUID, error) {
	const reservaSQL = `
		SELECT complejo_id, jugador_id, estado, hold_expira_at, NOW()
		FROM reservas
		WHERE id = $1`

	var (
		complejoID   uuid.UUID
		ownerID      *uuid.UUID
		estado       string
		holdExpiraAt *time.Time
		dbNow        time.Time
	)
	if err := r.pool.QueryRow(ctx, reservaSQL, reservaID).Scan(
		&complejoID, &ownerID, &estado, &holdExpiraAt, &dbNow,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrReservaNotFound
		}
		return uuid.Nil, fmt.Errorf("error verificando reserva para comprobante: %w", err)
	}

	if ownerID == nil || *ownerID != jugadorID {
		return uuid.Nil, ErrReservaForbidden
	}
	if err := validateHoldAcceptsComprobante(estado, holdExpiraAt, dbNow); err != nil {
		return uuid.Nil, err
	}

	if _, err := getActiveManualMetodoPago(ctx, r.pool, complejoID, metodoPagoID); err != nil {
		return uuid.Nil, err
	}

	return complejoID, nil
}

// RegisterComprobanteTx bloquea la fila de reserva con FOR UPDATE para evitar carreras
// con el worker de expiración, registra el comprobante manual, pasa la reserva a POR_VALIDAR,
// escribe auditoría y crea una notificación in-app para el dueño del complejo.
func (r *PostgresRepository) RegisterComprobanteTx(ctx context.Context, params RegisterComprobanteParams) (UploadComprobanteResponse, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return UploadComprobanteResponse{}, fmt.Errorf("error iniciando transacción de comprobante: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	const lockReservaSQL = `
		SELECT
			r.complejo_id,
			r.cancha_id,
			r.jugador_id,
			r.estado,
			r.hold_expira_at,
			lower(r.rango_horario),
			upper(r.rango_horario),
			c.dueno_id,
			ca.nombre,
			NOW()
		FROM reservas r
		INNER JOIN complejos c ON c.id = r.complejo_id
		INNER JOIN canchas ca ON ca.id = r.cancha_id
		WHERE r.id = $1
		FOR UPDATE OF r`

	var (
		complejoID   uuid.UUID
		canchaID     uuid.UUID
		ownerID      *uuid.UUID
		estado       string
		holdExpiraAt *time.Time
		inicioDB     time.Time
		finDB        time.Time
		duenoID      uuid.UUID
		canchaNombre string
		dbNow        time.Time
	)

	if err := tx.QueryRow(ctx, lockReservaSQL, params.ReservaID).Scan(
		&complejoID,
		&canchaID,
		&ownerID,
		&estado,
		&holdExpiraAt,
		&inicioDB,
		&finDB,
		&duenoID,
		&canchaNombre,
		&dbNow,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UploadComprobanteResponse{}, ErrReservaNotFound
		}
		return UploadComprobanteResponse{}, fmt.Errorf("error bloqueando reserva para comprobante: %w", err)
	}

	if ownerID == nil || *ownerID != params.JugadorID {
		return UploadComprobanteResponse{}, ErrReservaForbidden
	}
	if err := validateHoldAcceptsComprobante(estado, holdExpiraAt, dbNow); err != nil {
		return UploadComprobanteResponse{}, err
	}

	metodoTipo, err := getActiveManualMetodoPago(ctx, tx, complejoID, params.MetodoPagoID)
	if err != nil {
		return UploadComprobanteResponse{}, err
	}

	const insertCompSQL = `
		INSERT INTO comprobantes_pago (
			id,
			reserva_id,
			complejo_id,
			metodo_pago_id,
			es_automatico,
			estado_revision,
			monto_declarado,
			codigo_operacion,
			evidencia_storage_key
		)
		VALUES ($1, $2, $3, $4, false, 'PENDIENTE', $5::numeric(10,2), $6, $7)
		RETURNING created_at`

	var compCreatedAt time.Time
	if err := tx.QueryRow(ctx, insertCompSQL,
		params.ComprobanteID,
		params.ReservaID,
		complejoID,
		params.MetodoPagoID,
		formatDecimal(params.MontoDeclarado),
		params.CodigoOperacion,
		params.EvidenciaStorageKey,
	).Scan(&compCreatedAt); err != nil {
		return UploadComprobanteResponse{}, fmt.Errorf("error insertando comprobante de pago: %w", err)
	}

	const updateReservaSQL = `
		UPDATE reservas
		SET estado = 'POR_VALIDAR', por_validar_desde = $2::timestamptz
		WHERE id = $1 AND estado = 'PENDIENTE_PAGO'
		RETURNING por_validar_desde`

	var porValidarDesde time.Time
	if err := tx.QueryRow(ctx, updateReservaSQL, params.ReservaID, dbNow).Scan(&porValidarDesde); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UploadComprobanteResponse{}, ErrInvalidReservaState
		}
		return UploadComprobanteResponse{}, fmt.Errorf("error actualizando reserva a POR_VALIDAR: %w", err)
	}

	const insertAuditSQL = `
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
			$1, $2, $3, 'PENDIENTE_PAGO', 'POR_VALIDAR', $4, $4,
			tstzrange($5::timestamptz, $6::timestamptz, '[)'),
			tstzrange($5::timestamptz, $6::timestamptz, '[)'),
			$7, $8
		)`

	auditMotivo := fmt.Sprintf("Comprobante manual registrado (%s, op. %s, S/ %.2f)", metodoTipo, params.CodigoOperacion, params.MontoDeclarado)
	if _, err := tx.Exec(ctx, insertAuditSQL,
		params.ReservaID,
		complejoID,
		AccionComprobanteSubido,
		canchaID,
		inicioDB,
		finDB,
		params.JugadorID,
		auditMotivo,
	); err != nil {
		return UploadComprobanteResponse{}, fmt.Errorf("error registrando auditoría de comprobante: %w", err)
	}

	inicioLima := inicioDB.In(limaLocation)
	finLima := finDB.In(limaLocation)
	notifTitulo := "Nuevo comprobante por validar"
	notifMensaje := fmt.Sprintf(
		"Un jugador registró un comprobante (%s, op. %s) por S/ %.2f para %s el %s de %s a %s.",
		metodoTipo,
		params.CodigoOperacion,
		params.MontoDeclarado,
		canchaNombre,
		inicioLima.Format("2006-01-02"),
		inicioLima.Format("15:04"),
		finLima.Format("15:04"),
	)

	const insertNotifSQL = `
		INSERT INTO notificaciones (usuario_id, complejo_id, reserva_id, tipo, titulo, mensaje)
		VALUES ($1, $2, $3, $4, $5, $6)`

	if _, err := tx.Exec(ctx, insertNotifSQL,
		duenoID,
		complejoID,
		params.ReservaID,
		NotifComprobantePorValidar,
		notifTitulo,
		notifMensaje,
	); err != nil {
		return UploadComprobanteResponse{}, fmt.Errorf("error creando notificación para dueño: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return UploadComprobanteResponse{}, fmt.Errorf("error confirmando transacción de comprobante: %w", err)
	}

	codOp := params.CodigoOperacion
	evidenciaEndpoint := BuildEvidenciaEndpoint(params.ComprobanteID)

	return UploadComprobanteResponse{
		ReservaID:       params.ReservaID,
		EstadoReserva:   EstadoPorValidar,
		PorValidarDesde: porValidarDesde.In(limaLocation),
		Comprobante: ComprobanteInfo{
			ID:                params.ComprobanteID,
			ReservaID:         params.ReservaID,
			MetodoPagoID:      params.MetodoPagoID,
			MetodoPagoTipo:    metodoTipo,
			EsAutomatico:      false,
			EstadoRevision:    RevisionPendiente,
			MontoDeclarado:    fromCents(toCents(params.MontoDeclarado)),
			CodigoOperacion:   &codOp,
			EvidenciaEndpoint: &evidenciaEndpoint,
			CreatedAt:         compCreatedAt.In(limaLocation),
		},
	}, nil
}

// ListJugadorReservas devuelve las reservas del jugador autenticado paginadas y filtrables por estado.
// Respeta RN-02: nunca oculta reservas existentes si el complejo cambió de estado de suscripción.
func (r *PostgresRepository) ListJugadorReservas(ctx context.Context, jugadorID uuid.UUID, filter ListJugadorReservasFilter) ([]JugadorReservaItem, int, time.Time, error) {
	var estadoParam *string
	if filter.Estado != "" {
		e := filter.Estado
		estadoParam = &e
	}

	const countSQL = `
		SELECT COUNT(*), NOW()
		FROM reservas
		WHERE jugador_id = $1
		  AND ($2::varchar IS NULL OR estado = $2)`

	var total int
	var dbNow time.Time
	if err := r.pool.QueryRow(ctx, countSQL, jugadorID, estadoParam).Scan(&total, &dbNow); err != nil {
		return nil, 0, time.Time{}, fmt.Errorf("error contando reservas del jugador: %w", err)
	}
	if total == 0 {
		return []JugadorReservaItem{}, 0, dbNow, nil
	}

	const listSQL = `
		SELECT
			r.id,
			r.complejo_id,
			c.slug,
			c.nombre,
			c.direccion_escrita,
			r.cancha_id,
			ca.nombre,
			ca.modalidad,
			r.estado,
			r.origen,
			lower(r.rango_horario),
			upper(r.rango_horario),
			r.monto_total::float8,
			r.monto_adelanto::float8,
			r.saldo_pendiente::float8,
			r.hold_expira_at,
			r.por_validar_desde,
			CASE WHEN r.estado IN ('CONFIRMADA', 'COMPLETADA') THEN r.codigo_qr ELSE NULL END,
			r.created_at
		FROM reservas r
		INNER JOIN complejos c ON c.id = r.complejo_id
		INNER JOIN canchas ca ON ca.id = r.cancha_id
		WHERE r.jugador_id = $1
		  AND ($2::varchar IS NULL OR r.estado = $2)
		ORDER BY r.created_at DESC, r.id DESC
		LIMIT $3 OFFSET $4`

	rows, err := r.pool.Query(ctx, listSQL, jugadorID, estadoParam, filter.Limit, filter.Offset)
	if err != nil {
		return nil, 0, time.Time{}, fmt.Errorf("error listando reservas del jugador: %w", err)
	}
	defer rows.Close()

	items := make([]JugadorReservaItem, 0, filter.Limit)
	for rows.Next() {
		var (
			item            JugadorReservaItem
			inicioDB, finDB time.Time
			holdExpiraAt    *time.Time
			porValidarDesde *time.Time
			createdAt       time.Time
		)
		if err := rows.Scan(
			&item.ID,
			&item.ComplejoID,
			&item.ComplejoSlug,
			&item.ComplejoNombre,
			&item.ComplejoDireccion,
			&item.CanchaID,
			&item.CanchaNombre,
			&item.CanchaModalidad,
			&item.Estado,
			&item.Origen,
			&inicioDB,
			&finDB,
			&item.MontoTotal,
			&item.MontoAdelanto,
			&item.SaldoPendiente,
			&holdExpiraAt,
			&porValidarDesde,
			&item.CodigoQR,
			&createdAt,
		); err != nil {
			return nil, 0, time.Time{}, fmt.Errorf("error escaneando reserva del jugador: %w", err)
		}

		item.Inicio = inicioDB.In(limaLocation)
		item.Fin = finDB.In(limaLocation)
		item.DuracionMin = int(finDB.Sub(inicioDB) / time.Minute)
		item.HoldExpiraAt = toLimaPtr(holdExpiraAt)
		item.PorValidarDesde = toLimaPtr(porValidarDesde)
		item.CreatedAt = createdAt.In(limaLocation)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, time.Time{}, fmt.Errorf("error iterando reservas del jugador: %w", err)
	}

	return items, total, dbNow, nil
}

// GetJugadorReservaDetail recupera el detalle completo de una reserva con coordenadas exactas
// del complejo (Modo En Ruta), comprobantes y medios de cobro si sigue en PENDIENTE_PAGO.
func (r *PostgresRepository) GetJugadorReservaDetail(ctx context.Context, reservaID uuid.UUID) (JugadorReservaDetail, time.Time, error) {
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
		createdAt           time.Time
		updatedAt           time.Time
		dbNow               time.Time
	)

	err := r.pool.QueryRow(ctx, detailSQL, reservaID).Scan(
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
	out.CreatedAt = createdAt.In(limaLocation)
	out.UpdatedAt = updatedAt.In(limaLocation)

	comprobantes, err := listComprobantesByReserva(ctx, r.pool, out.ID)
	if err != nil {
		return JugadorReservaDetail{}, time.Time{}, err
	}
	out.Comprobantes = comprobantes

	if out.Estado == EstadoPendientePago {
		metodos, err := listActiveMetodosPagoPublico(ctx, r.pool, out.Complejo.ID)
		if err != nil {
			return JugadorReservaDetail{}, time.Time{}, err
		}
		out.MetodosPago = metodos
	}

	return out, dbNow, nil
}

// GetComprobanteAccess devuelve el jugador y dueño autorizados junto con la clave de almacenamiento privada.
func (r *PostgresRepository) GetComprobanteAccess(ctx context.Context, comprobanteID uuid.UUID) (ComprobanteAccessRecord, error) {
	const query = `
		SELECT
			cp.id,
			cp.reserva_id,
			cp.complejo_id,
			r.jugador_id,
			c.dueno_id,
			cp.evidencia_storage_key
		FROM comprobantes_pago cp
		INNER JOIN reservas r ON r.id = cp.reserva_id
		INNER JOIN complejos c ON c.id = cp.complejo_id
		WHERE cp.id = $1`

	var rec ComprobanteAccessRecord
	if err := r.pool.QueryRow(ctx, query, comprobanteID).Scan(
		&rec.ID,
		&rec.ReservaID,
		&rec.ComplejoID,
		&rec.JugadorID,
		&rec.DuenoID,
		&rec.EvidenciaStorageKey,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ComprobanteAccessRecord{}, ErrComprobanteNotFound
		}
		return ComprobanteAccessRecord{}, fmt.Errorf("error consultando acceso a comprobante: %w", err)
	}
	return rec, nil
}

// ExpireStaleHolds expira en una transacción atómica hasta batchSize reservas en PENDIENTE_PAGO
// con hold_expira_at <= NOW() (usando FOR UPDATE SKIP LOCKED), revierte cupones y registra auditoría.
// Nunca modifica reservas en POR_VALIDAR ni otros estados (Decisión 3).
func (r *PostgresRepository) ExpireStaleHolds(ctx context.Context, batchSize int) (int, error) {
	if batchSize <= 0 {
		batchSize = DefaultWorkerBatchSize
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("error iniciando transacción de expiración: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	const selectSQL = `
		SELECT
			id,
			complejo_id,
			cancha_id,
			cupon_id,
			lower(rango_horario),
			upper(rango_horario)
		FROM reservas
		WHERE estado = 'PENDIENTE_PAGO'
		  AND hold_expira_at <= NOW()
		ORDER BY hold_expira_at ASC
		LIMIT $1
		FOR UPDATE SKIP LOCKED`

	expiredRows, err := scanStaleHoldRows(ctx, tx, selectSQL, batchSize)
	if err != nil {
		return 0, err
	}
	if len(expiredRows) == 0 {
		return 0, nil
	}

	if err := applyHoldExpirationsTx(ctx, tx, expiredRows, "Hold expirado automáticamente por tiempo límite de pago"); err != nil {
		return 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("error confirmando expiración de holds: %w", err)
	}

	return len(expiredRows), nil
}

// ---------- Helpers internos de consultas compartidas entre pool y tx ----------

type dbQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type staleHoldRow struct {
	ID         uuid.UUID
	ComplejoID uuid.UUID
	CanchaID   uuid.UUID
	CuponID    *uuid.UUID
	Inicio     time.Time
	Fin        time.Time
}

func scanStaleHoldRows(ctx context.Context, q dbQuerier, sql string, args ...any) ([]staleHoldRow, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("error buscando holds expirados: %w", err)
	}
	defer rows.Close()

	out := make([]staleHoldRow, 0)
	for rows.Next() {
		var row staleHoldRow
		if err := rows.Scan(
			&row.ID,
			&row.ComplejoID,
			&row.CanchaID,
			&row.CuponID,
			&row.Inicio,
			&row.Fin,
		); err != nil {
			return nil, fmt.Errorf("error escaneando hold expirado: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterando holds expirados: %w", err)
	}
	return out, nil
}

func expireOverlappingStaleHoldsTx(ctx context.Context, tx pgx.Tx, canchaID uuid.UUID, inicio, fin, dbNow time.Time) error {
	const selectConflictSQL = `
		SELECT
			id,
			complejo_id,
			cancha_id,
			cupon_id,
			lower(rango_horario),
			upper(rango_horario)
		FROM reservas
		WHERE cancha_id = $1
		  AND estado = 'PENDIENTE_PAGO'
		  AND hold_expira_at <= $2::timestamptz
		  AND rango_horario && tstzrange($3::timestamptz, $4::timestamptz, '[)')
		ORDER BY hold_expira_at ASC
		FOR UPDATE SKIP LOCKED`

	staleRows, err := scanStaleHoldRows(ctx, tx, selectConflictSQL, canchaID, dbNow, inicio, fin)
	if err != nil || len(staleRows) == 0 {
		return err
	}

	return applyHoldExpirationsTx(ctx, tx, staleRows, "Hold expirado automáticamente al liberar horario vencido")
}

func applyHoldExpirationsTx(ctx context.Context, tx pgx.Tx, expiredRows []staleHoldRow, motivo string) error {
	type cuponKey struct {
		cuponID    uuid.UUID
		complejoID uuid.UUID
	}
	cuponDecrements := make(map[cuponKey]int)

	const updateSQL = `
		UPDATE reservas
		SET estado = 'EXPIRADA'
		WHERE id = $1 AND estado = 'PENDIENTE_PAGO'`

	const auditSQL = `
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
			$1, $2, $3, 'PENDIENTE_PAGO', 'EXPIRADA', $4, $4,
			tstzrange($5::timestamptz, $6::timestamptz, '[)'),
			tstzrange($5::timestamptz, $6::timestamptz, '[)'),
			NULL, $7
		)`

	for _, row := range expiredRows {
		tag, err := tx.Exec(ctx, updateSQL, row.ID)
		if err != nil {
			return fmt.Errorf("error marcando reserva %s como EXPIRADA: %w", row.ID, err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}

		if row.CuponID != nil {
			key := cuponKey{cuponID: *row.CuponID, complejoID: row.ComplejoID}
			cuponDecrements[key]++
		}

		if _, err := tx.Exec(ctx, auditSQL,
			row.ID,
			row.ComplejoID,
			AccionHoldExpirado,
			row.CanchaID,
			row.Inicio,
			row.Fin,
			motivo,
		); err != nil {
			return fmt.Errorf("error registrando auditoría de expiración para reserva %s: %w", row.ID, err)
		}
	}

	if len(cuponDecrements) > 0 {
		keys := make([]cuponKey, 0, len(cuponDecrements))
		for k := range cuponDecrements {
			keys = append(keys, k)
		}
		// Orden determinístico por UUID para evitar interbloqueos al revertir cupones concurrentemente.
		sort.Slice(keys, func(i, j int) bool {
			return keys[i].cuponID.String() < keys[j].cuponID.String()
		})

		const revertCuponSQL = `
			UPDATE cupones
			SET usos_actuales = GREATEST(usos_actuales - $3, 0)
			WHERE id = $1 AND complejo_id = $2`

		for _, k := range keys {
			if _, err := tx.Exec(ctx, revertCuponSQL, k.cuponID, k.complejoID, cuponDecrements[k]); err != nil {
				return fmt.Errorf("error revirtiendo usos de cupón %s: %w", k.cuponID, err)
			}
		}
	}

	return nil
}

func getComplejoByID(ctx context.Context, q dbQuerier, complejoID uuid.UUID) (ComplejoContext, error) {
	const query = `
		SELECT
			id, dueno_id, slug, nombre, publicado,
			unidad_reserva_min, hold_duracion_min,
			tipo_adelanto, valor_adelanto::float8,
			horas_min_cancelacion, terminos_reserva,
			estado_suscripcion, suscripcion_vence_at
		FROM complejos
		WHERE id = $1 AND publicado = true`

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
		return ComplejoContext{}, fmt.Errorf("error consultando complejo: %w", err)
	}
	return c, nil
}

func getActiveCancha(ctx context.Context, q dbQuerier, complejoID, canchaID uuid.UUID) (CanchaInfo, error) {
	const query = `
		SELECT id, complejo_id, nombre, modalidad, superficie, techada, iluminacion, activa
		FROM canchas
		WHERE id = $1 AND complejo_id = $2 AND activa = true`

	var c CanchaInfo
	if err := q.QueryRow(ctx, query, canchaID, complejoID).Scan(
		&c.ID,
		&c.ComplejoID,
		&c.Nombre,
		&c.Modalidad,
		&c.Superficie,
		&c.Techada,
		&c.Iluminacion,
		&c.Activa,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CanchaInfo{}, ErrCanchaNotFound
		}
		return CanchaInfo{}, fmt.Errorf("error consultando cancha activa: %w", err)
	}
	return c, nil
}

func getHorarioByDia(ctx context.Context, q dbQuerier, complejoID uuid.UUID, diaSemana int16) (*HorarioAtencion, error) {
	const query = `
		SELECT dia_semana, minuto_apertura, minuto_cierre, cerrado
		FROM horarios_atencion
		WHERE complejo_id = $1 AND dia_semana = $2`

	var h HorarioAtencion
	if err := q.QueryRow(ctx, query, complejoID, diaSemana).Scan(
		&h.DiaSemana,
		&h.MinutoApertura,
		&h.MinutoCierre,
		&h.Cerrado,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("error consultando horario de atención: %w", err)
	}
	return &h, nil
}

func listActiveTarifasForDay(ctx context.Context, q dbQuerier, complejoID, canchaID uuid.UUID, diaSemana int16) ([]TarifaFranja, error) {
	const query = `
		SELECT id, cancha_id, dia_semana, minuto_inicio, minuto_fin, precio_hora::float8, etiqueta, es_hora_muerta
		FROM tarifas_franja
		WHERE complejo_id = $1
		  AND dia_semana = $2
		  AND activa = true
		  AND (cancha_id IS NULL OR cancha_id = $3)
		ORDER BY cancha_id NULLS LAST, minuto_inicio ASC`

	rows, err := q.Query(ctx, query, complejoID, diaSemana, canchaID)
	if err != nil {
		return nil, fmt.Errorf("error listando tarifas activas del día: %w", err)
	}
	defer rows.Close()

	tarifas := make([]TarifaFranja, 0)
	for rows.Next() {
		var t TarifaFranja
		if err := rows.Scan(
			&t.ID,
			&t.CanchaID,
			&t.DiaSemana,
			&t.MinutoInicio,
			&t.MinutoFin,
			&t.PrecioHora,
			&t.Etiqueta,
			&t.EsHoraMuerta,
		); err != nil {
			return nil, fmt.Errorf("error escaneando tarifa por franja: %w", err)
		}
		tarifas = append(tarifas, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterando tarifas del día: %w", err)
	}
	return tarifas, nil
}

func getCuponByCodigo(ctx context.Context, q dbQuerier, complejoID uuid.UUID, codigo string) (CuponRecord, error) {
	const query = `
		SELECT
			id, complejo_id, codigo, descripcion, tipo_descuento,
			valor_descuento::float8, valido_desde, valido_hasta,
			max_usos, usos_actuales, acumulable_hora_muerta, activo
		FROM cupones
		WHERE complejo_id = $1 AND codigo = $2`

	var c CuponRecord
	if err := q.QueryRow(ctx, query, complejoID, strings.ToUpper(strings.TrimSpace(codigo))).Scan(
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
		return CuponRecord{}, fmt.Errorf("error consultando cupón: %w", err)
	}
	return c, nil
}

func listActiveMetodosPagoPublico(ctx context.Context, q dbQuerier, complejoID uuid.UUID) ([]MetodoPagoPublico, error) {
	// Nunca selecciona credenciales_cifradas ni qr_storage_key (RN-10).
	const query = `
		SELECT
			id, tipo, etiqueta, titular, entidad_financiera,
			numero_destino, cci, qr_url, llave_publica, instrucciones
		FROM metodos_pago_complejo
		WHERE complejo_id = $1 AND activo = true
		ORDER BY created_at ASC, id ASC`

	rows, err := q.Query(ctx, query, complejoID)
	if err != nil {
		return nil, fmt.Errorf("error listando medios de pago activos: %w", err)
	}
	defer rows.Close()

	out := make([]MetodoPagoPublico, 0)
	for rows.Next() {
		var m MetodoPagoPublico
		if err := rows.Scan(
			&m.ID,
			&m.Tipo,
			&m.Etiqueta,
			&m.Titular,
			&m.EntidadFinanciera,
			&m.NumeroDestino,
			&m.CCI,
			&m.QRURL,
			&m.LlavePublica,
			&m.Instrucciones,
		); err != nil {
			return nil, fmt.Errorf("error escaneando medio de pago: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterando medios de pago: %w", err)
	}
	return out, nil
}

func getActiveManualMetodoPago(ctx context.Context, q dbQuerier, complejoID, metodoPagoID uuid.UUID) (string, error) {
	const query = `
		SELECT tipo
		FROM metodos_pago_complejo
		WHERE id = $1 AND complejo_id = $2 AND activo = true`

	var tipo string
	if err := q.QueryRow(ctx, query, metodoPagoID, complejoID).Scan(&tipo); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrMetodoPagoNotFound
		}
		return "", fmt.Errorf("error verificando método de pago: %w", err)
	}
	switch tipo {
	case MetodoPagoYape, MetodoPagoPlin, MetodoPagoTransferencia:
		return tipo, nil
	default:
		return "", ErrMetodoPagoNoManual
	}
}

func listComprobantesByReserva(ctx context.Context, q dbQuerier, reservaID uuid.UUID) ([]ComprobanteInfo, error) {
	const query = `
		SELECT
			cp.id,
			cp.reserva_id,
			cp.metodo_pago_id,
			mp.tipo,
			cp.es_automatico,
			cp.estado_revision,
			cp.monto_declarado::float8,
			cp.codigo_operacion,
			(cp.evidencia_storage_key IS NOT NULL AND btrim(cp.evidencia_storage_key) <> '') AS tiene_evidencia,
			cp.validado_at,
			cp.motivo_rechazo,
			cp.created_at
		FROM comprobantes_pago cp
		INNER JOIN metodos_pago_complejo mp ON mp.id = cp.metodo_pago_id
		WHERE cp.reserva_id = $1
		ORDER BY cp.created_at DESC`

	rows, err := q.Query(ctx, query, reservaID)
	if err != nil {
		return nil, fmt.Errorf("error listando comprobantes de la reserva: %w", err)
	}
	defer rows.Close()

	out := make([]ComprobanteInfo, 0)
	for rows.Next() {
		var (
			c              ComprobanteInfo
			tieneEvidencia bool
			validadoAt     *time.Time
			createdAt      time.Time
		)
		if err := rows.Scan(
			&c.ID,
			&c.ReservaID,
			&c.MetodoPagoID,
			&c.MetodoPagoTipo,
			&c.EsAutomatico,
			&c.EstadoRevision,
			&c.MontoDeclarado,
			&c.CodigoOperacion,
			&tieneEvidencia,
			&validadoAt,
			&c.MotivoRechazo,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("error escaneando comprobante de la reserva: %w", err)
		}
		if tieneEvidencia {
			ep := BuildEvidenciaEndpoint(c.ID)
			c.EvidenciaEndpoint = &ep
		}
		c.ValidadoAt = toLimaPtr(validadoAt)
		c.CreatedAt = createdAt.In(limaLocation)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterando comprobantes de la reserva: %w", err)
	}
	return out, nil
}

func validateHoldAcceptsComprobante(estado string, holdExpiraAt *time.Time, now time.Time) error {
	if estado == EstadoExpirada {
		return ErrHoldExpired
	}
	if estado != EstadoPendientePago {
		return ErrInvalidReservaState
	}
	if holdExpiraAt == nil || !holdExpiraAt.After(now) {
		return ErrHoldExpired
	}
	return nil
}

func isSuscripcionVigente(c ComplejoContext, now time.Time) bool {
	if !c.Publicado {
		return false
	}
	if c.EstadoSuscripcion != EstadoSuscripcionPrueba && c.EstadoSuscripcion != EstadoSuscripcionActiva {
		return false
	}
	return c.SuscripcionVenceAt != nil && c.SuscripcionVenceAt.After(now)
}

func mapReservaPgError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.Code == "23P01" {
			return ErrSlotNotAvailable
		}
	}
	return fmt.Errorf("error persistiendo reserva: %w", err)
}

func formatDecimal(v float64) string {
	return fmt.Sprintf("%.2f", fromCents(toCents(v)))
}

func toLimaPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	converted := t.In(limaLocation)
	return &converted
}

// BuildEvidenciaEndpoint construye la ruta autenticada para descargar un comprobante privado.
func BuildEvidenciaEndpoint(comprobanteID uuid.UUID) string {
	return fmt.Sprintf("/api/v1/comprobantes/%s/evidencia", comprobanteID.String())
}
