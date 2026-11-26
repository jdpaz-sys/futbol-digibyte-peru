package reportes

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store abstrae las consultas agregadas del módulo reportes para desacoplar pruebas unitarias.
type Store interface {
	GetPeriodoAggregates(ctx context.Context, complejoID uuid.UUID, rangeStart, rangeEnd time.Time) (PeriodoAggregates, error)
}

// PostgresRepository implementa Store sobre PostgreSQL 16 mediante pgx/v5.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

// NewRepository crea el repositorio PostgreSQL de reportes.
func NewRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

// GetPeriodoAggregates obtiene finanzas, conteos por estado/origen, horarios semanales y ocupación
// por cancha filtrados estrictamente por complejo_id (RN-01) en [rangeStart, rangeEnd).
func (r *PostgresRepository) GetPeriodoAggregates(ctx context.Context, complejoID uuid.UUID, rangeStart, rangeEnd time.Time) (PeriodoAggregates, error) {
	agg := PeriodoAggregates{
		PorEstado: make(map[string]int, len(allEstados)),
		PorOrigen: make(map[string]int, len(allOrigenes)),
		Horarios:  make([]HorarioDia, 0, 7),
		Canchas:   make([]CanchaOcupacionRaw, 0),
	}

	// 1. Finanzas del periodo
	const finanzasSQL = `
		SELECT
			COALESCE(SUM(CASE
				WHEN origen = 'BLOQUEO_MANTENIMIENTO' THEN 0
				WHEN estado = 'COMPLETADA' THEN monto_total
				WHEN estado IN ('CONFIRMADA', 'NO_SHOW') THEN monto_adelanto
				ELSE 0
			END), 0)::float8 AS ingresos_confirmados_total,
			COALESCE(SUM(CASE
				WHEN origen <> 'BLOQUEO_MANTENIMIENTO' AND estado IN ('CONFIRMADA', 'COMPLETADA', 'NO_SHOW') THEN monto_adelanto
				ELSE 0
			END), 0)::float8 AS adelantos_cobrados,
			COALESCE(SUM(CASE
				WHEN origen <> 'BLOQUEO_MANTENIMIENTO' AND estado = 'COMPLETADA' THEN (monto_total - monto_adelanto)
				ELSE 0
			END), 0)::float8 AS saldos_cobrados,
			COALESCE(SUM(CASE
				WHEN origen <> 'BLOQUEO_MANTENIMIENTO' AND estado = 'CONFIRMADA' THEN saldo_pendiente
				ELSE 0
			END), 0)::float8 AS saldos_pendientes_por_cobrar,
			COUNT(*) FILTER (WHERE reembolso_pendiente = true)::int AS reembolsos_pendientes_count
		FROM reservas
		WHERE complejo_id = $1
		  AND lower(rango_horario) >= $2::timestamptz
		  AND lower(rango_horario) < $3::timestamptz`

	if err := r.pool.QueryRow(ctx, finanzasSQL, complejoID, rangeStart, rangeEnd).Scan(
		&agg.Finanzas.IngresosConfirmadosTotal,
		&agg.Finanzas.AdelantosCobrados,
		&agg.Finanzas.SaldosCobrados,
		&agg.Finanzas.SaldosPendientesPorCobrar,
		&agg.Finanzas.ReembolsosPendientesCount,
	); err != nil {
		return PeriodoAggregates{}, fmt.Errorf("error consultando finanzas del periodo: %w", err)
	}

	// 2. Conteos por estado
	const estadosSQL = `
		SELECT estado, COUNT(*)::int
		FROM reservas
		WHERE complejo_id = $1
		  AND lower(rango_horario) >= $2::timestamptz
		  AND lower(rango_horario) < $3::timestamptz
		GROUP BY estado`

	estadoRows, err := r.pool.Query(ctx, estadosSQL, complejoID, rangeStart, rangeEnd)
	if err != nil {
		return PeriodoAggregates{}, fmt.Errorf("error consultando conteo por estado: %w", err)
	}
	defer estadoRows.Close()

	for estadoRows.Next() {
		var estado string
		var cnt int
		if err := estadoRows.Scan(&estado, &cnt); err != nil {
			return PeriodoAggregates{}, fmt.Errorf("error escaneando conteo por estado: %w", err)
		}
		agg.PorEstado[estado] = cnt
	}
	if err := estadoRows.Err(); err != nil {
		return PeriodoAggregates{}, fmt.Errorf("error iterando conteo por estado: %w", err)
	}

	// 3. Conteos por origen
	const origenesSQL = `
		SELECT origen, COUNT(*)::int
		FROM reservas
		WHERE complejo_id = $1
		  AND lower(rango_horario) >= $2::timestamptz
		  AND lower(rango_horario) < $3::timestamptz
		GROUP BY origen`

	origenRows, err := r.pool.Query(ctx, origenesSQL, complejoID, rangeStart, rangeEnd)
	if err != nil {
		return PeriodoAggregates{}, fmt.Errorf("error consultando conteo por origen: %w", err)
	}
	defer origenRows.Close()

	for origenRows.Next() {
		var origen string
		var cnt int
		if err := origenRows.Scan(&origen, &cnt); err != nil {
			return PeriodoAggregates{}, fmt.Errorf("error escaneando conteo por origen: %w", err)
		}
		agg.PorOrigen[origen] = cnt
	}
	if err := origenRows.Err(); err != nil {
		return PeriodoAggregates{}, fmt.Errorf("error iterando conteo por origen: %w", err)
	}

	// 4. Horarios semanales del complejo
	const horariosSQL = `
		SELECT dia_semana, minuto_apertura, minuto_cierre, cerrado
		FROM horarios_atencion
		WHERE complejo_id = $1
		ORDER BY dia_semana ASC`

	horarioRows, err := r.pool.Query(ctx, horariosSQL, complejoID)
	if err != nil {
		return PeriodoAggregates{}, fmt.Errorf("error consultando horarios de atención: %w", err)
	}
	defer horarioRows.Close()

	for horarioRows.Next() {
		var h HorarioDia
		if err := horarioRows.Scan(&h.DiaSemana, &h.MinutoApertura, &h.MinutoCierre, &h.Cerrado); err != nil {
			return PeriodoAggregates{}, fmt.Errorf("error escaneando horario de atención: %w", err)
		}
		agg.Horarios = append(agg.Horarios, h)
	}
	if err := horarioRows.Err(); err != nil {
		return PeriodoAggregates{}, fmt.Errorf("error iterando horarios de atención: %w", err)
	}

	// 5. Ocupación por cancha (CONFIRMADA + COMPLETADA excluyendo bloqueos, y bloqueos separados)
	const ocupacionSQL = `
		SELECT
			ca.id,
			ca.nombre,
			ca.modalidad,
			ca.activa,
			COUNT(r.id) FILTER (
				WHERE r.estado IN ('CONFIRMADA', 'COMPLETADA')
				  AND r.origen <> 'BLOQUEO_MANTENIMIENTO'
			)::int AS reservas_efectivas,
			COALESCE(SUM(CASE
				WHEN r.estado IN ('CONFIRMADA', 'COMPLETADA') AND r.origen <> 'BLOQUEO_MANTENIMIENTO'
				THEN EXTRACT(EPOCH FROM (upper(r.rango_horario) - lower(r.rango_horario))) / 60.0
				ELSE 0
			END), 0)::float8 AS minutos_ocupados,
			COALESCE(SUM(CASE
				WHEN r.estado = 'CONFIRMADA' AND r.origen = 'BLOQUEO_MANTENIMIENTO'
				THEN EXTRACT(EPOCH FROM (upper(r.rango_horario) - lower(r.rango_horario))) / 60.0
				ELSE 0
			END), 0)::float8 AS minutos_bloqueados
		FROM canchas ca
		LEFT JOIN reservas r
			ON r.cancha_id = ca.id
			AND r.complejo_id = ca.complejo_id
			AND lower(r.rango_horario) >= $2::timestamptz
			AND lower(r.rango_horario) < $3::timestamptz
		WHERE ca.complejo_id = $1
		  AND (ca.activa = true OR r.id IS NOT NULL)
		GROUP BY ca.id, ca.nombre, ca.modalidad, ca.activa, ca.orden
		ORDER BY ca.orden ASC, ca.nombre ASC`

	canchaRows, err := r.pool.Query(ctx, ocupacionSQL, complejoID, rangeStart, rangeEnd)
	if err != nil {
		return PeriodoAggregates{}, fmt.Errorf("error consultando ocupación por cancha: %w", err)
	}
	defer canchaRows.Close()

	for canchaRows.Next() {
		var c CanchaOcupacionRaw
		if err := canchaRows.Scan(
			&c.CanchaID,
			&c.CanchaNombre,
			&c.Modalidad,
			&c.Activa,
			&c.ReservasEfectivas,
			&c.MinutosOcupados,
			&c.MinutosBloqueados,
		); err != nil {
			return PeriodoAggregates{}, fmt.Errorf("error escaneando ocupación por cancha: %w", err)
		}
		agg.Canchas = append(agg.Canchas, c)
	}
	if err := canchaRows.Err(); err != nil {
		return PeriodoAggregates{}, fmt.Errorf("error iterando ocupación por cancha: %w", err)
	}

	return agg, nil
}
