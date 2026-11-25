package suscripciones

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository define las operaciones de persistencia para el módulo suscripciones (Super Admin).
type Repository interface {
	CreatePlan(ctx context.Context, p Plan) (Plan, error)
	ListPlanes(ctx context.Context, activo *bool) ([]Plan, error)
	GetPlanByID(ctx context.Context, id uuid.UUID) (Plan, error)
	UpdatePlan(ctx context.Context, p Plan) (Plan, error)

	CreateComplejoWithOwner(ctx context.Context, params CreateComplejoParams) (ComplejoDetalleResponse, error)
	ListComplejos(ctx context.Context, filter ListComplejosFilter) ([]ComplejoItem, int, error)
	GetComplejoDetail(ctx context.Context, complejoID uuid.UUID) (ComplejoDetalleResponse, error)
	RenovarSuscripcion(ctx context.Context, params RenovarSuscripcionParams) (OperacionSuscripcionResponse, error)
	CambiarEstadoSuscripcion(ctx context.Context, params CambiarEstadoParams) (OperacionSuscripcionResponse, error)

	GetMetricasGlobales(ctx context.Context) (MetricasGlobalesResponse, error)
}

// PostgresRepository implementa Repository sobre pgxpool.Pool.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

// NewRepository crea una instancia de PostgresRepository.
func NewRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) CreatePlan(ctx context.Context, p Plan) (Plan, error) {
	const query = `
		INSERT INTO planes_suscripcion (
			codigo, nombre, periodicidad, unidad_duracion, cantidad_duracion,
			precio, descripcion, activo
		)
		VALUES ($1, $2, $3, $4, $5, $6::numeric(10,2), $7, $8)
		RETURNING id, codigo, nombre, periodicidad, unidad_duracion, cantidad_duracion,
		          precio::float8, descripcion, activo, created_at, updated_at`

	var out Plan
	err := r.pool.QueryRow(ctx, query,
		p.Codigo,
		p.Nombre,
		p.Periodicidad,
		p.UnidadDuracion,
		p.CantidadDuracion,
		formatDecimal(p.Precio),
		p.Descripcion,
		p.Activo,
	).Scan(
		&out.ID,
		&out.Codigo,
		&out.Nombre,
		&out.Periodicidad,
		&out.UnidadDuracion,
		&out.CantidadDuracion,
		&out.Precio,
		&out.Descripcion,
		&out.Activo,
		&out.CreatedAt,
		&out.UpdatedAt,
	)
	if err != nil {
		return Plan{}, mapPgError(err)
	}
	return out, nil
}

func (r *PostgresRepository) ListPlanes(ctx context.Context, activo *bool) ([]Plan, error) {
	const query = `
		SELECT id, codigo, nombre, periodicidad, unidad_duracion, cantidad_duracion,
		       precio::float8, descripcion, activo, created_at, updated_at
		FROM planes_suscripcion
		WHERE ($1::boolean IS NULL OR activo = $1)
		ORDER BY activo DESC, precio ASC, created_at ASC`

	rows, err := r.pool.Query(ctx, query, activo)
	if err != nil {
		return nil, fmt.Errorf("error consultando planes: %w", err)
	}
	defer rows.Close()

	planes := make([]Plan, 0)
	for rows.Next() {
		var p Plan
		if err := rows.Scan(
			&p.ID,
			&p.Codigo,
			&p.Nombre,
			&p.Periodicidad,
			&p.UnidadDuracion,
			&p.CantidadDuracion,
			&p.Precio,
			&p.Descripcion,
			&p.Activo,
			&p.CreatedAt,
			&p.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("error escaneando plan: %w", err)
		}
		planes = append(planes, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterando planes: %w", err)
	}
	return planes, nil
}

func (r *PostgresRepository) GetPlanByID(ctx context.Context, id uuid.UUID) (Plan, error) {
	return getPlanByIDQuerier(ctx, r.pool, id)
}

func (r *PostgresRepository) UpdatePlan(ctx context.Context, p Plan) (Plan, error) {
	const query = `
		UPDATE planes_suscripcion
		SET nombre = $2,
		    precio = $3::numeric(10,2),
		    descripcion = $4,
		    activo = $5
		WHERE id = $1
		RETURNING id, codigo, nombre, periodicidad, unidad_duracion, cantidad_duracion,
		          precio::float8, descripcion, activo, created_at, updated_at`

	var out Plan
	err := r.pool.QueryRow(ctx, query,
		p.ID,
		p.Nombre,
		formatDecimal(p.Precio),
		p.Descripcion,
		p.Activo,
	).Scan(
		&out.ID,
		&out.Codigo,
		&out.Nombre,
		&out.Periodicidad,
		&out.UnidadDuracion,
		&out.CantidadDuracion,
		&out.Precio,
		&out.Descripcion,
		&out.Activo,
		&out.CreatedAt,
		&out.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Plan{}, ErrPlanNotFound
		}
		return Plan{}, mapPgError(err)
	}
	return out, nil
}

func (r *PostgresRepository) CreateComplejoWithOwner(ctx context.Context, params CreateComplejoParams) (ComplejoDetalleResponse, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ComplejoDetalleResponse{}, fmt.Errorf("error iniciando transacción de alta de complejo: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	plan, err := getPlanByIDQuerier(ctx, tx, params.PlanID)
	if err != nil {
		return ComplejoDetalleResponse{}, err
	}
	if !plan.Activo {
		return ComplejoDetalleResponse{}, &ValidationError{
			Message: "No se puede asignar un plan inactivo",
			Fields:  map[string]string{"plan_id": "El plan seleccionado se encuentra inactivo"},
		}
	}

	var dbNow time.Time
	if err := tx.QueryRow(ctx, `SELECT NOW()`).Scan(&dbNow); err != nil {
		return ComplejoDetalleResponse{}, fmt.Errorf("error obteniendo fecha del servidor: %w", err)
	}

	inicioAt, finAt, err := CalcularVigencia(dbNow, nil, plan.UnidadDuracion, int(plan.CantidadDuracion))
	if err != nil {
		return ComplejoDetalleResponse{}, err
	}

	estadoInicial := EstadoActiva
	if plan.Periodicidad == PeriodicidadPrueba {
		estadoInicial = EstadoPrueba
	}

	montoCobrado := plan.Precio
	if params.MontoCobrado != nil {
		montoCobrado = *params.MontoCobrado
	}

	var duenoID uuid.UUID
	const insertDuenoSQL = `
		INSERT INTO usuarios (rol, email, password_hash, nombre_completo, telefono, activo)
		VALUES ('DUENO', $1, $2, $3, $4, true)
		RETURNING id`
	if err := tx.QueryRow(ctx, insertDuenoSQL,
		params.DuenoEmail,
		params.DuenoPassHash,
		params.DuenoNombre,
		params.DuenoTelefono,
	).Scan(&duenoID); err != nil {
		return ComplejoDetalleResponse{}, mapPgError(err)
	}

	var complejoID uuid.UUID
	const insertComplejoSQL = `
		INSERT INTO complejos (
			dueno_id, plan_actual_id, slug, nombre, descripcion, telefono_contacto,
			departamento, provincia, distrito, publicado, estado_suscripcion, suscripcion_vence_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, false, $10, $11)
		RETURNING id`
	if err := tx.QueryRow(ctx, insertComplejoSQL,
		duenoID,
		plan.ID,
		params.Slug,
		params.Nombre,
		params.Descripcion,
		params.TelefonoContacto,
		params.Departamento,
		params.Provincia,
		params.Distrito,
		estadoInicial,
		finAt,
	).Scan(&complejoID); err != nil {
		return ComplejoDetalleResponse{}, mapPgError(err)
	}

	histItem, err := insertHistorialTx(ctx, tx, insertHistorialArgs{
		complejoID:     complejoID,
		planID:         plan.ID,
		planNombre:     plan.Nombre,
		periodicidad:   plan.Periodicidad,
		montoCobrado:   montoCobrado,
		metodoPagoSaaS: params.MetodoPagoSaaS,
		referenciaPago: params.ReferenciaPago,
		estado:         estadoInicial,
		inicioAt:       inicioAt,
		finAt:          finAt,
		registradoPor:  params.AdminID,
		observaciones:  params.Observaciones,
	})
	if err != nil {
		return ComplejoDetalleResponse{}, err
	}

	complejoItem, err := getComplejoItemQuerier(ctx, tx, complejoID)
	if err != nil {
		return ComplejoDetalleResponse{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return ComplejoDetalleResponse{}, fmt.Errorf("error confirmando alta de complejo: %w", err)
	}

	return ComplejoDetalleResponse{
		ComplejoItem: complejoItem,
		Historial:    []SuscripcionHistorialItem{histItem},
	}, nil
}

func (r *PostgresRepository) ListComplejos(ctx context.Context, filter ListComplejosFilter) ([]ComplejoItem, int, error) {
	const countSQL = `
		SELECT COUNT(*)
		FROM complejos c
		WHERE ($1::varchar IS NULL OR c.estado_suscripcion = $1)
		  AND (
		      $2::int IS NULL OR (
		          c.estado_suscripcion IN ('PRUEBA', 'ACTIVA')
		          AND c.suscripcion_vence_at IS NOT NULL
		          AND c.suscripcion_vence_at >= NOW()
		          AND c.suscripcion_vence_at <= NOW() + make_interval(days => $2)
		      )
		  )`

	var total int
	if err := r.pool.QueryRow(ctx, countSQL, filter.EstadoSuscripcion, filter.PorVencerDias).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("error contando complejos: %w", err)
	}

	offset := (filter.Page - 1) * filter.Limit
	const listSQL = `
		SELECT
			c.id, c.slug, c.nombre, c.descripcion, c.telefono_contacto,
			c.departamento, c.provincia, c.distrito, c.publicado,
			c.estado_suscripcion, c.suscripcion_vence_at, c.created_at, c.updated_at,
			u.id, u.email, u.nombre_completo, u.telefono, u.activo,
			p.id, p.codigo, p.nombre, p.periodicidad, p.precio::float8
		FROM complejos c
		INNER JOIN usuarios u ON u.id = c.dueno_id
		LEFT JOIN planes_suscripcion p ON p.id = c.plan_actual_id
		WHERE ($1::varchar IS NULL OR c.estado_suscripcion = $1)
		  AND (
		      $2::int IS NULL OR (
		          c.estado_suscripcion IN ('PRUEBA', 'ACTIVA')
		          AND c.suscripcion_vence_at IS NOT NULL
		          AND c.suscripcion_vence_at >= NOW()
		          AND c.suscripcion_vence_at <= NOW() + make_interval(days => $2)
		      )
		  )
		ORDER BY c.created_at DESC
		LIMIT $3 OFFSET $4`

	rows, err := r.pool.Query(ctx, listSQL, filter.EstadoSuscripcion, filter.PorVencerDias, filter.Limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("error listando complejos: %w", err)
	}
	defer rows.Close()

	items := make([]ComplejoItem, 0)
	for rows.Next() {
		item, err := scanComplejoItem(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("error escaneando complejo: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("error iterando complejos: %w", err)
	}

	return items, total, nil
}

func (r *PostgresRepository) GetComplejoDetail(ctx context.Context, complejoID uuid.UUID) (ComplejoDetalleResponse, error) {
	item, err := getComplejoItemQuerier(ctx, r.pool, complejoID)
	if err != nil {
		return ComplejoDetalleResponse{}, err
	}

	const histSQL = `
		SELECT
			h.id, h.complejo_id, h.plan_id, h.plan_nombre, h.periodicidad,
			h.monto_cobrado::float8, h.metodo_pago_saas, h.referencia_pago, h.estado,
			h.inicio_at, h.fin_at, h.registrado_por, u.nombre_completo,
			h.observaciones, h.created_at
		FROM suscripciones_historial h
		LEFT JOIN usuarios u ON u.id = h.registrado_por
		WHERE h.complejo_id = $1
		ORDER BY h.created_at DESC, h.inicio_at DESC`

	rows, err := r.pool.Query(ctx, histSQL, complejoID)
	if err != nil {
		return ComplejoDetalleResponse{}, fmt.Errorf("error consultando historial de suscripciones: %w", err)
	}
	defer rows.Close()

	historial := make([]SuscripcionHistorialItem, 0)
	for rows.Next() {
		var h SuscripcionHistorialItem
		if err := rows.Scan(
			&h.ID,
			&h.ComplejoID,
			&h.PlanID,
			&h.PlanNombre,
			&h.Periodicidad,
			&h.MontoCobrado,
			&h.MetodoPagoSaaS,
			&h.ReferenciaPago,
			&h.Estado,
			&h.InicioAt,
			&h.FinAt,
			&h.RegistradoPor,
			&h.RegistradoPorNombre,
			&h.Observaciones,
			&h.CreatedAt,
		); err != nil {
			return ComplejoDetalleResponse{}, fmt.Errorf("error escaneando historial de suscripción: %w", err)
		}
		historial = append(historial, h)
	}
	if err := rows.Err(); err != nil {
		return ComplejoDetalleResponse{}, fmt.Errorf("error iterando historial de suscripciones: %w", err)
	}

	return ComplejoDetalleResponse{
		ComplejoItem: item,
		Historial:    historial,
	}, nil
}

func (r *PostgresRepository) RenovarSuscripcion(ctx context.Context, params RenovarSuscripcionParams) (OperacionSuscripcionResponse, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return OperacionSuscripcionResponse{}, fmt.Errorf("error iniciando transacción de renovación: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var venceActual *time.Time
	var dbNow time.Time
	const lockSQL = `
		SELECT suscripcion_vence_at, NOW()
		FROM complejos
		WHERE id = $1
		FOR UPDATE`
	if err := tx.QueryRow(ctx, lockSQL, params.ComplejoID).Scan(&venceActual, &dbNow); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OperacionSuscripcionResponse{}, ErrComplejoNotFound
		}
		return OperacionSuscripcionResponse{}, fmt.Errorf("error bloqueando complejo para renovación: %w", err)
	}

	plan, err := getPlanByIDQuerier(ctx, tx, params.PlanID)
	if err != nil {
		return OperacionSuscripcionResponse{}, err
	}
	if !plan.Activo {
		return OperacionSuscripcionResponse{}, &ValidationError{
			Message: "No se puede renovar con un plan inactivo",
			Fields:  map[string]string{"plan_id": "El plan seleccionado se encuentra inactivo"},
		}
	}

	inicioAt, finAt, err := CalcularVigencia(dbNow, venceActual, plan.UnidadDuracion, int(plan.CantidadDuracion))
	if err != nil {
		return OperacionSuscripcionResponse{}, err
	}

	montoCobrado := plan.Precio
	if params.MontoCobrado != nil {
		montoCobrado = *params.MontoCobrado
	}

	const updateComplejoSQL = `
		UPDATE complejos
		SET plan_actual_id = $2,
		    estado_suscripcion = 'ACTIVA',
		    suscripcion_vence_at = $3
		WHERE id = $1`
	if _, err := tx.Exec(ctx, updateComplejoSQL, params.ComplejoID, plan.ID, finAt); err != nil {
		return OperacionSuscripcionResponse{}, mapPgError(err)
	}

	histItem, err := insertHistorialTx(ctx, tx, insertHistorialArgs{
		complejoID:     params.ComplejoID,
		planID:         plan.ID,
		planNombre:     plan.Nombre,
		periodicidad:   plan.Periodicidad,
		montoCobrado:   montoCobrado,
		metodoPagoSaaS: params.MetodoPagoSaaS,
		referenciaPago: params.ReferenciaPago,
		estado:         EstadoActiva,
		inicioAt:       inicioAt,
		finAt:          finAt,
		registradoPor:  params.AdminID,
		observaciones:  params.Observaciones,
	})
	if err != nil {
		return OperacionSuscripcionResponse{}, err
	}

	complejoItem, err := getComplejoItemQuerier(ctx, tx, params.ComplejoID)
	if err != nil {
		return OperacionSuscripcionResponse{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return OperacionSuscripcionResponse{}, fmt.Errorf("error confirmando renovación de suscripción: %w", err)
	}

	return OperacionSuscripcionResponse{
		ComplejoItem:    complejoItem,
		EventoHistorial: histItem,
	}, nil
}

func (r *PostgresRepository) CambiarEstadoSuscripcion(ctx context.Context, params CambiarEstadoParams) (OperacionSuscripcionResponse, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return OperacionSuscripcionResponse{}, fmt.Errorf("error iniciando transacción de cambio de estado: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var planActualID *uuid.UUID
	var venceActual *time.Time
	var dbNow time.Time
	const lockSQL = `
		SELECT plan_actual_id, suscripcion_vence_at, NOW()
		FROM complejos
		WHERE id = $1
		FOR UPDATE`
	if err := tx.QueryRow(ctx, lockSQL, params.ComplejoID).Scan(&planActualID, &venceActual, &dbNow); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OperacionSuscripcionResponse{}, ErrComplejoNotFound
		}
		return OperacionSuscripcionResponse{}, fmt.Errorf("error bloqueando complejo para cambio de estado: %w", err)
	}

	var prevPlanID *uuid.UUID
	var prevInicio, prevFin *time.Time
	const lastHistSQL = `
		SELECT plan_id, inicio_at, fin_at
		FROM suscripciones_historial
		WHERE complejo_id = $1
		ORDER BY created_at DESC
		LIMIT 1`
	if err := tx.QueryRow(ctx, lastHistSQL, params.ComplejoID).Scan(&prevPlanID, &prevInicio, &prevFin); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return OperacionSuscripcionResponse{}, fmt.Errorf("error consultando último evento de suscripción: %w", err)
	}

	targetPlanID := planActualID
	if targetPlanID == nil {
		targetPlanID = prevPlanID
	}
	if targetPlanID == nil {
		return OperacionSuscripcionResponse{}, &ValidationError{
			Message: "El complejo no tiene un plan asignado",
			Fields:  map[string]string{"estado": "Asigna un plan al complejo antes de cambiar su estado"},
		}
	}

	plan, err := getPlanByIDQuerier(ctx, tx, *targetPlanID)
	if err != nil {
		return OperacionSuscripcionResponse{}, err
	}

	var histInicioAt, histFinAt time.Time
	nuevoVenceAt := venceActual

	if venceActual != nil && venceActual.After(dbNow) {
		histInicioAt = dbNow
		histFinAt = *venceActual
	} else if params.NuevoEstado == EstadoActiva || params.NuevoEstado == EstadoPrueba {
		inicio, fin, calcErr := CalcularVigencia(dbNow, nil, plan.UnidadDuracion, int(plan.CantidadDuracion))
		if calcErr != nil {
			return OperacionSuscripcionResponse{}, calcErr
		}
		histInicioAt = inicio
		histFinAt = fin
		nuevoVenceAt = &fin
	} else if prevInicio != nil && prevFin != nil && prevInicio.Before(*prevFin) {
		histInicioAt = *prevInicio
		histFinAt = *prevFin
	} else {
		histInicioAt = dbNow.Add(-time.Second)
		histFinAt = dbNow
	}

	// RN-02: Solo actualiza complejos; jamás altera ni cancela reservas confirmadas.
	const updateEstadoSQL = `
		UPDATE complejos
		SET estado_suscripcion = $2,
		    suscripcion_vence_at = $3
		WHERE id = $1`
	if _, err := tx.Exec(ctx, updateEstadoSQL, params.ComplejoID, params.NuevoEstado, nuevoVenceAt); err != nil {
		return OperacionSuscripcionResponse{}, mapPgError(err)
	}

	obs := params.Observaciones
	histItem, err := insertHistorialTx(ctx, tx, insertHistorialArgs{
		complejoID:     params.ComplejoID,
		planID:         plan.ID,
		planNombre:     plan.Nombre,
		periodicidad:   plan.Periodicidad,
		montoCobrado:   0,
		metodoPagoSaaS: nil,
		referenciaPago: nil,
		estado:         params.NuevoEstado,
		inicioAt:       histInicioAt,
		finAt:          histFinAt,
		registradoPor:  params.AdminID,
		observaciones:  &obs,
	})
	if err != nil {
		return OperacionSuscripcionResponse{}, err
	}

	complejoItem, err := getComplejoItemQuerier(ctx, tx, params.ComplejoID)
	if err != nil {
		return OperacionSuscripcionResponse{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return OperacionSuscripcionResponse{}, fmt.Errorf("error confirmando cambio de estado de suscripción: %w", err)
	}

	return OperacionSuscripcionResponse{
		ComplejoItem:    complejoItem,
		EventoHistorial: histItem,
	}, nil
}

func (r *PostgresRepository) GetMetricasGlobales(ctx context.Context) (MetricasGlobalesResponse, error) {
	const query = `
		WITH complejos_agg AS (
			SELECT
				COUNT(*) AS total_complejos,
				COUNT(*) FILTER (WHERE estado_suscripcion = 'PRUEBA') AS en_prueba,
				COUNT(*) FILTER (WHERE estado_suscripcion = 'ACTIVA') AS activos,
				COUNT(*) FILTER (WHERE estado_suscripcion = 'VENCIDA') AS vencidos,
				COUNT(*) FILTER (WHERE estado_suscripcion = 'SUSPENDIDA') AS suspendidos,
				COUNT(*) FILTER (
					WHERE estado_suscripcion IN ('PRUEBA', 'ACTIVA')
					  AND suscripcion_vence_at IS NOT NULL
					  AND suscripcion_vence_at >= NOW()
					  AND suscripcion_vence_at <= NOW() + make_interval(days => 7)
				) AS por_vencer_7_dias
			FROM complejos
		),
		ingresos_agg AS (
			SELECT
				COALESCE(SUM(monto_cobrado), 0)::float8 AS ingresos_totales,
				COALESCE(
					SUM(monto_cobrado) FILTER (
						WHERE date_trunc('month', created_at AT TIME ZONE 'America/Lima') =
						      date_trunc('month', NOW() AT TIME ZONE 'America/Lima')
					),
					0
				)::float8 AS ingresos_mes_actual
			FROM suscripciones_historial
		),
		reservas_agg AS (
			SELECT
				COUNT(*) AS total_reservas,
				COUNT(*) FILTER (WHERE estado = 'CONFIRMADA') AS reservas_confirmadas,
				COUNT(*) FILTER (WHERE estado = 'COMPLETADA') AS reservas_completadas,
				COUNT(*) FILTER (WHERE estado IN ('CONFIRMADA', 'COMPLETADA')) AS reservas_confirmadas_completadas
			FROM reservas
		)
		SELECT
			c.total_complejos,
			c.en_prueba,
			c.activos,
			c.vencidos,
			c.suspendidos,
			c.por_vencer_7_dias,
			i.ingresos_totales,
			i.ingresos_mes_actual,
			r.total_reservas,
			r.reservas_confirmadas,
			r.reservas_completadas,
			r.reservas_confirmadas_completadas,
			NOW()
		FROM complejos_agg c
		CROSS JOIN ingresos_agg i
		CROSS JOIN reservas_agg r`

	var out MetricasGlobalesResponse
	out.IngresosSuscripciones.Moneda = "PEN"

	err := r.pool.QueryRow(ctx, query).Scan(
		&out.Complejos.Total,
		&out.Complejos.Prueba,
		&out.Complejos.Activa,
		&out.Complejos.Vencida,
		&out.Complejos.Suspendida,
		&out.Complejos.PorVencer7Dias,
		&out.IngresosSuscripciones.Total,
		&out.IngresosSuscripciones.MesActual,
		&out.Reservas.Total,
		&out.Reservas.Confirmadas,
		&out.Reservas.Completadas,
		&out.Reservas.ConfirmadasYCompletadas,
		&out.GeneradoAt,
	)
	if err != nil {
		return MetricasGlobalesResponse{}, fmt.Errorf("error consultando métricas globales SaaS: %w", err)
	}

	return out, nil
}

type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func getPlanByIDQuerier(ctx context.Context, q rowQuerier, id uuid.UUID) (Plan, error) {
	const query = `
		SELECT id, codigo, nombre, periodicidad, unidad_duracion, cantidad_duracion,
		       precio::float8, descripcion, activo, created_at, updated_at
		FROM planes_suscripcion
		WHERE id = $1`

	var p Plan
	err := q.QueryRow(ctx, query, id).Scan(
		&p.ID,
		&p.Codigo,
		&p.Nombre,
		&p.Periodicidad,
		&p.UnidadDuracion,
		&p.CantidadDuracion,
		&p.Precio,
		&p.Descripcion,
		&p.Activo,
		&p.CreatedAt,
		&p.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Plan{}, ErrPlanNotFound
		}
		return Plan{}, fmt.Errorf("error consultando plan por ID: %w", err)
	}
	return p, nil
}

func getComplejoItemQuerier(ctx context.Context, q rowQuerier, complejoID uuid.UUID) (ComplejoItem, error) {
	const query = `
		SELECT
			c.id, c.slug, c.nombre, c.descripcion, c.telefono_contacto,
			c.departamento, c.provincia, c.distrito, c.publicado,
			c.estado_suscripcion, c.suscripcion_vence_at, c.created_at, c.updated_at,
			u.id, u.email, u.nombre_completo, u.telefono, u.activo,
			p.id, p.codigo, p.nombre, p.periodicidad, p.precio::float8
		FROM complejos c
		INNER JOIN usuarios u ON u.id = c.dueno_id
		LEFT JOIN planes_suscripcion p ON p.id = c.plan_actual_id
		WHERE c.id = $1`

	item, err := scanComplejoItem(q.QueryRow(ctx, query, complejoID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ComplejoItem{}, ErrComplejoNotFound
		}
		return ComplejoItem{}, fmt.Errorf("error consultando complejo por ID: %w", err)
	}
	return item, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanComplejoItem(scanner rowScanner) (ComplejoItem, error) {
	var item ComplejoItem
	var planID *uuid.UUID
	var planCodigo *string
	var planNombre *string
	var planPeriodicidad *string
	var planPrecio *float64

	err := scanner.Scan(
		&item.ID,
		&item.Slug,
		&item.Nombre,
		&item.Descripcion,
		&item.TelefonoContacto,
		&item.Departamento,
		&item.Provincia,
		&item.Distrito,
		&item.Publicado,
		&item.EstadoSuscripcion,
		&item.SuscripcionVenceAt,
		&item.CreatedAt,
		&item.UpdatedAt,
		&item.Dueno.ID,
		&item.Dueno.Email,
		&item.Dueno.NombreCompleto,
		&item.Dueno.Telefono,
		&item.Dueno.Activo,
		&planID,
		&planCodigo,
		&planNombre,
		&planPeriodicidad,
		&planPrecio,
	)
	if err != nil {
		return ComplejoItem{}, err
	}

	if planID != nil && planCodigo != nil && planNombre != nil && planPeriodicidad != nil && planPrecio != nil {
		item.PlanActual = &PlanVigenteResumen{
			ID:           *planID,
			Codigo:       *planCodigo,
			Nombre:       *planNombre,
			Periodicidad: *planPeriodicidad,
			Precio:       *planPrecio,
		}
	}

	return item, nil
}

type insertHistorialArgs struct {
	complejoID     uuid.UUID
	planID         uuid.UUID
	planNombre     string
	periodicidad   string
	montoCobrado   float64
	metodoPagoSaaS *string
	referenciaPago *string
	estado         string
	inicioAt       time.Time
	finAt          time.Time
	registradoPor  uuid.UUID
	observaciones  *string
}

func insertHistorialTx(ctx context.Context, tx pgx.Tx, args insertHistorialArgs) (SuscripcionHistorialItem, error) {
	const query = `
		WITH inserted AS (
			INSERT INTO suscripciones_historial (
				complejo_id, plan_id, plan_nombre, periodicidad, monto_cobrado,
				metodo_pago_saas, referencia_pago, estado, inicio_at, fin_at,
				registrado_por, observaciones
			)
			VALUES ($1, $2, $3, $4, $5::numeric(10,2), $6, $7, $8, $9, $10, $11, $12)
			RETURNING id, complejo_id, plan_id, plan_nombre, periodicidad,
			          monto_cobrado::float8 AS monto_cobrado, metodo_pago_saas, referencia_pago, estado,
			          inicio_at, fin_at, registrado_por, observaciones, created_at
		)
		SELECT
			i.id, i.complejo_id, i.plan_id, i.plan_nombre, i.periodicidad,
			i.monto_cobrado, i.metodo_pago_saas, i.referencia_pago, i.estado,
			i.inicio_at, i.fin_at, i.registrado_por, u.nombre_completo,
			i.observaciones, i.created_at
		FROM inserted i
		LEFT JOIN usuarios u ON u.id = i.registrado_por`

	var h SuscripcionHistorialItem
	err := tx.QueryRow(ctx, query,
		args.complejoID,
		args.planID,
		args.planNombre,
		args.periodicidad,
		formatDecimal(args.montoCobrado),
		args.metodoPagoSaaS,
		args.referenciaPago,
		args.estado,
		args.inicioAt,
		args.finAt,
		args.registradoPor,
		args.observaciones,
	).Scan(
		&h.ID,
		&h.ComplejoID,
		&h.PlanID,
		&h.PlanNombre,
		&h.Periodicidad,
		&h.MontoCobrado,
		&h.MetodoPagoSaaS,
		&h.ReferenciaPago,
		&h.Estado,
		&h.InicioAt,
		&h.FinAt,
		&h.RegistradoPor,
		&h.RegistradoPorNombre,
		&h.Observaciones,
		&h.CreatedAt,
	)
	if err != nil {
		return SuscripcionHistorialItem{}, mapPgError(err)
	}
	return h, nil
}

func formatDecimal(v float64) string {
	return fmt.Sprintf("%.2f", v)
}

func mapPgError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		constraint := strings.ToLower(pgErr.ConstraintName)
		switch {
		case strings.Contains(constraint, "email"):
			return ErrEmailAlreadyExists
		case strings.Contains(constraint, "slug"):
			return ErrSlugAlreadyExists
		case strings.Contains(constraint, "codigo"):
			return ErrPlanCodeAlreadyExists
		default:
			return ErrConflict
		}
	}
	return err
}
