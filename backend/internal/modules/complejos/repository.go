package complejos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository define las operaciones de persistencia aisladas por complejo_id (RN-01).
type Repository interface {
	GetComplejo(ctx context.Context, complejoID uuid.UUID) (ComplejoPerfil, error)
	UpdateComplejo(ctx context.Context, params UpdateComplejoParams) (ComplejoPerfil, error)
	PublishComplejo(ctx context.Context, complejoID uuid.UUID) (ComplejoPerfil, error)
	UnpublishComplejo(ctx context.Context, complejoID uuid.UUID) (ComplejoPerfil, error)

	ListCanchas(ctx context.Context, complejoID uuid.UUID, activa *bool) ([]Cancha, error)
	GetCancha(ctx context.Context, complejoID, canchaID uuid.UUID) (Cancha, error)
	CreateCancha(ctx context.Context, c Cancha) (Cancha, error)
	UpdateCancha(ctx context.Context, c Cancha) (Cancha, error)
	DeactivateCancha(ctx context.Context, complejoID, canchaID uuid.UUID) (Cancha, error)

	ListHorarios(ctx context.Context, complejoID uuid.UUID) ([]HorarioAtencion, error)
	UpsertHorarios(ctx context.Context, complejoID uuid.UUID, horarios []HorarioAtencion) ([]HorarioAtencion, error)

	ListTarifas(ctx context.Context, complejoID uuid.UUID, filter TarifasFilter) ([]TarifaFranja, error)
	GetTarifa(ctx context.Context, complejoID, tarifaID uuid.UUID) (TarifaFranja, error)
	CreateTarifa(ctx context.Context, t TarifaFranja) (TarifaFranja, error)
	UpdateTarifa(ctx context.Context, t TarifaFranja) (TarifaFranja, error)
	DeleteTarifa(ctx context.Context, complejoID, tarifaID uuid.UUID) error

	ListCupones(ctx context.Context, complejoID uuid.UUID, activo *bool) ([]Cupon, error)
	GetCupon(ctx context.Context, complejoID, cuponID uuid.UUID) (Cupon, error)
	CreateCupon(ctx context.Context, c Cupon) (Cupon, error)
	UpdateCupon(ctx context.Context, c Cupon) (Cupon, error)

	ListMetodosPago(ctx context.Context, complejoID uuid.UUID, activo *bool) ([]MetodoPago, error)
	GetMetodoPago(ctx context.Context, complejoID, metodoID uuid.UUID) (MetodoPago, error)
	CreateMetodoPago(ctx context.Context, p SaveMetodoPagoParams) (MetodoPago, error)
	UpdateMetodoPago(ctx context.Context, p SaveMetodoPagoParams) (MetodoPago, error)
	DeactivateMetodoPago(ctx context.Context, complejoID, metodoID uuid.UUID) (MetodoPago, error)
}

type rowScanner interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// PostgresRepository implementa Repository sobre pgxpool.Pool con consultas parametrizadas.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

// NewRepository crea una instancia de PostgresRepository.
func NewRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

const complejoSelectBase = `
	SELECT
		c.id,
		c.dueno_id,
		c.plan_actual_id,
		c.slug,
		c.nombre,
		c.descripcion,
		c.telefono_contacto,
		c.servicios,
		c.departamento,
		c.provincia,
		c.distrito,
		ST_Y(c.ubicacion::geometry) AS lat,
		ST_X(c.ubicacion::geometry) AS lng,
		c.direccion_escrita,
		c.referencias,
		c.publicado,
		c.unidad_reserva_min,
		c.hold_duracion_min,
		c.tipo_adelanto,
		c.valor_adelanto::float8,
		c.horas_min_cancelacion,
		c.terminos_reserva,
		c.estado_suscripcion,
		c.suscripcion_vence_at,
		c.created_at,
		c.updated_at,
		(SELECT COUNT(*) FROM canchas ca WHERE ca.complejo_id = c.id AND ca.activa = true)::int AS canchas_activas,
		(SELECT COUNT(*) FROM horarios_atencion h WHERE h.complejo_id = c.id AND h.cerrado = false)::int AS horarios_abiertos,
		(SELECT COUNT(*) FROM tarifas_franja t WHERE t.complejo_id = c.id AND t.activa = true)::int AS tarifas_activas,
		(SELECT COUNT(*) FROM metodos_pago_complejo m WHERE m.complejo_id = c.id AND m.activo = true)::int AS metodos_pago_activos
	FROM complejos c
	WHERE c.id = $1`

func (r *PostgresRepository) GetComplejo(ctx context.Context, complejoID uuid.UUID) (ComplejoPerfil, error) {
	return queryComplejoPerfil(ctx, r.pool, complejoSelectBase, complejoID)
}

func (r *PostgresRepository) UpdateComplejo(ctx context.Context, params UpdateComplejoParams) (ComplejoPerfil, error) {
	serviciosJSON, err := json.Marshal(normalizeServiciosSlice(params.Servicios))
	if err != nil {
		return ComplejoPerfil{}, fmt.Errorf("error serializando servicios: %w", err)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ComplejoPerfil{}, fmt.Errorf("error iniciando transacción de actualización de complejo: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	const updateQuery = `
		UPDATE complejos
		SET
			nombre = $2,
			descripcion = $3,
			telefono_contacto = $4,
			servicios = $5::jsonb,
			departamento = $6,
			provincia = $7,
			distrito = $8,
			ubicacion = CASE
				WHEN $9::double precision IS NOT NULL AND $10::double precision IS NOT NULL
				THEN ST_SetSRID(ST_MakePoint($10::double precision, $9::double precision), 4326)::geography
				ELSE ubicacion
			END,
			direccion_escrita = $11,
			referencias = $12,
			unidad_reserva_min = $13,
			hold_duracion_min = $14,
			tipo_adelanto = $15,
			valor_adelanto = $16::numeric(10,2),
			horas_min_cancelacion = $17,
			terminos_reserva = $18
		WHERE id = $1
		RETURNING id`

	var updatedID uuid.UUID
	err = tx.QueryRow(ctx, updateQuery,
		params.ComplejoID,
		params.Nombre,
		params.Descripcion,
		params.TelefonoContacto,
		serviciosJSON,
		params.Departamento,
		params.Provincia,
		params.Distrito,
		params.Lat,
		params.Lng,
		params.DireccionEscrita,
		params.Referencias,
		params.UnidadReservaMin,
		params.HoldDuracionMin,
		params.TipoAdelanto,
		formatDecimal(params.ValorAdelanto),
		params.HorasMinCancelacion,
		params.TerminosReserva,
	).Scan(&updatedID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ComplejoPerfil{}, ErrComplejoNotFound
		}
		return ComplejoPerfil{}, mapPgError(err)
	}

	perfil, err := queryComplejoPerfil(ctx, tx, complejoSelectBase, params.ComplejoID)
	if err != nil {
		return ComplejoPerfil{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return ComplejoPerfil{}, fmt.Errorf("error confirmando actualización de complejo: %w", err)
	}

	return perfil, nil
}

func (r *PostgresRepository) PublishComplejo(ctx context.Context, complejoID uuid.UUID) (ComplejoPerfil, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ComplejoPerfil{}, fmt.Errorf("error iniciando transacción de publicación: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	perfil, err := queryComplejoPerfil(ctx, tx, complejoSelectBase+"\n\tFOR UPDATE", complejoID)
	if err != nil {
		return ComplejoPerfil{}, err
	}

	if !perfil.ChecklistPublicacion.ListoParaPublicar {
		return ComplejoPerfil{}, &PublishRequirementsError{
			Message:             "El complejo no cumple los requisitos mínimos para publicarse (RN-03)",
			RequisitosFaltantes: perfil.ChecklistPublicacion.RequisitosFaltantes,
		}
	}

	var updatedAt time.Time
	err = tx.QueryRow(ctx,
		`UPDATE complejos SET publicado = true WHERE id = $1 RETURNING updated_at`,
		complejoID,
	).Scan(&updatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ComplejoPerfil{}, ErrComplejoNotFound
		}
		return ComplejoPerfil{}, mapPgError(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return ComplejoPerfil{}, fmt.Errorf("error confirmando publicación de complejo: %w", err)
	}

	perfil.Publicado = true
	perfil.UpdatedAt = updatedAt
	return perfil, nil
}

func (r *PostgresRepository) UnpublishComplejo(ctx context.Context, complejoID uuid.UUID) (ComplejoPerfil, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ComplejoPerfil{}, fmt.Errorf("error iniciando transacción de despublicación: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var id uuid.UUID
	err = tx.QueryRow(ctx,
		`UPDATE complejos SET publicado = false WHERE id = $1 RETURNING id`,
		complejoID,
	).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ComplejoPerfil{}, ErrComplejoNotFound
		}
		return ComplejoPerfil{}, mapPgError(err)
	}

	perfil, err := queryComplejoPerfil(ctx, tx, complejoSelectBase, complejoID)
	if err != nil {
		return ComplejoPerfil{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return ComplejoPerfil{}, fmt.Errorf("error confirmando despublicación de complejo: %w", err)
	}

	return perfil, nil
}

func queryComplejoPerfil(ctx context.Context, q rowScanner, query string, complejoID uuid.UUID) (ComplejoPerfil, error) {
	var perfil ComplejoPerfil
	var serviciosRaw []byte
	var counts ChecklistCounts

	err := q.QueryRow(ctx, query, complejoID).Scan(
		&perfil.ID,
		&perfil.DuenoID,
		&perfil.PlanActualID,
		&perfil.Slug,
		&perfil.Nombre,
		&perfil.Descripcion,
		&perfil.TelefonoContacto,
		&serviciosRaw,
		&perfil.Departamento,
		&perfil.Provincia,
		&perfil.Distrito,
		&perfil.Lat,
		&perfil.Lng,
		&perfil.DireccionEscrita,
		&perfil.Referencias,
		&perfil.Publicado,
		&perfil.UnidadReservaMin,
		&perfil.HoldDuracionMin,
		&perfil.TipoAdelanto,
		&perfil.ValorAdelanto,
		&perfil.HorasMinCancelacion,
		&perfil.TerminosReserva,
		&perfil.EstadoSuscripcion,
		&perfil.SuscripcionVenceAt,
		&perfil.CreatedAt,
		&perfil.UpdatedAt,
		&counts.CanchasActivas,
		&counts.HorariosAbiertos,
		&counts.TarifasActivas,
		&counts.MetodosPagoActivos,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ComplejoPerfil{}, ErrComplejoNotFound
		}
		return ComplejoPerfil{}, fmt.Errorf("error consultando perfil del complejo: %w", err)
	}

	perfil.Servicios = make([]string, 0)
	if len(serviciosRaw) > 0 {
		if unmarshalErr := json.Unmarshal(serviciosRaw, &perfil.Servicios); unmarshalErr != nil {
			return ComplejoPerfil{}, fmt.Errorf("error leyendo servicios JSONB: %w", unmarshalErr)
		}
		if perfil.Servicios == nil {
			perfil.Servicios = make([]string, 0)
		}
	}

	perfil.ChecklistPublicacion = EvaluatePublishChecklist(perfil, counts)
	return perfil, nil
}

// ---------- Canchas ----------

func (r *PostgresRepository) ListCanchas(ctx context.Context, complejoID uuid.UUID, activa *bool) ([]Cancha, error) {
	const query = `
		SELECT
			id, complejo_id, nombre, modalidad, superficie, techada, iluminacion,
			foto_url, foto_storage_key, orden, activa, created_at, updated_at
		FROM canchas
		WHERE complejo_id = $1
		  AND ($2::boolean IS NULL OR activa = $2)
		ORDER BY activa DESC, orden ASC, created_at ASC`

	rows, err := r.pool.Query(ctx, query, complejoID, activa)
	if err != nil {
		return nil, fmt.Errorf("error listando canchas: %w", err)
	}
	defer rows.Close()

	canchas := make([]Cancha, 0)
	for rows.Next() {
		var c Cancha
		if err := rows.Scan(
			&c.ID,
			&c.ComplejoID,
			&c.Nombre,
			&c.Modalidad,
			&c.Superficie,
			&c.Techada,
			&c.Iluminacion,
			&c.FotoURL,
			&c.FotoStorageKey,
			&c.Orden,
			&c.Activa,
			&c.CreatedAt,
			&c.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("error escaneando cancha: %w", err)
		}
		canchas = append(canchas, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterando canchas: %w", err)
	}
	return canchas, nil
}

func (r *PostgresRepository) GetCancha(ctx context.Context, complejoID, canchaID uuid.UUID) (Cancha, error) {
	const query = `
		SELECT
			id, complejo_id, nombre, modalidad, superficie, techada, iluminacion,
			foto_url, foto_storage_key, orden, activa, created_at, updated_at
		FROM canchas
		WHERE id = $1 AND complejo_id = $2`

	var c Cancha
	err := r.pool.QueryRow(ctx, query, canchaID, complejoID).Scan(
		&c.ID,
		&c.ComplejoID,
		&c.Nombre,
		&c.Modalidad,
		&c.Superficie,
		&c.Techada,
		&c.Iluminacion,
		&c.FotoURL,
		&c.FotoStorageKey,
		&c.Orden,
		&c.Activa,
		&c.CreatedAt,
		&c.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Cancha{}, ErrCanchaNotFound
		}
		return Cancha{}, fmt.Errorf("error consultando cancha: %w", err)
	}
	return c, nil
}

func (r *PostgresRepository) CreateCancha(ctx context.Context, c Cancha) (Cancha, error) {
	const query = `
		INSERT INTO canchas (
			complejo_id, nombre, modalidad, superficie, techada, iluminacion,
			foto_url, foto_storage_key, orden, activa
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING
			id, complejo_id, nombre, modalidad, superficie, techada, iluminacion,
			foto_url, foto_storage_key, orden, activa, created_at, updated_at`

	var out Cancha
	err := r.pool.QueryRow(ctx, query,
		c.ComplejoID,
		c.Nombre,
		c.Modalidad,
		c.Superficie,
		c.Techada,
		c.Iluminacion,
		c.FotoURL,
		c.FotoStorageKey,
		c.Orden,
		c.Activa,
	).Scan(
		&out.ID,
		&out.ComplejoID,
		&out.Nombre,
		&out.Modalidad,
		&out.Superficie,
		&out.Techada,
		&out.Iluminacion,
		&out.FotoURL,
		&out.FotoStorageKey,
		&out.Orden,
		&out.Activa,
		&out.CreatedAt,
		&out.UpdatedAt,
	)
	if err != nil {
		return Cancha{}, mapPgError(err)
	}
	return out, nil
}

func (r *PostgresRepository) UpdateCancha(ctx context.Context, c Cancha) (Cancha, error) {
	const query = `
		UPDATE canchas
		SET
			nombre = $3,
			modalidad = $4,
			superficie = $5,
			techada = $6,
			iluminacion = $7,
			foto_url = $8,
			foto_storage_key = $9,
			orden = $10,
			activa = $11
		WHERE id = $1 AND complejo_id = $2
		RETURNING
			id, complejo_id, nombre, modalidad, superficie, techada, iluminacion,
			foto_url, foto_storage_key, orden, activa, created_at, updated_at`

	var out Cancha
	err := r.pool.QueryRow(ctx, query,
		c.ID,
		c.ComplejoID,
		c.Nombre,
		c.Modalidad,
		c.Superficie,
		c.Techada,
		c.Iluminacion,
		c.FotoURL,
		c.FotoStorageKey,
		c.Orden,
		c.Activa,
	).Scan(
		&out.ID,
		&out.ComplejoID,
		&out.Nombre,
		&out.Modalidad,
		&out.Superficie,
		&out.Techada,
		&out.Iluminacion,
		&out.FotoURL,
		&out.FotoStorageKey,
		&out.Orden,
		&out.Activa,
		&out.CreatedAt,
		&out.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Cancha{}, ErrCanchaNotFound
		}
		return Cancha{}, mapPgError(err)
	}
	return out, nil
}

func (r *PostgresRepository) DeactivateCancha(ctx context.Context, complejoID, canchaID uuid.UUID) (Cancha, error) {
	const query = `
		UPDATE canchas
		SET activa = false
		WHERE id = $1 AND complejo_id = $2
		RETURNING
			id, complejo_id, nombre, modalidad, superficie, techada, iluminacion,
			foto_url, foto_storage_key, orden, activa, created_at, updated_at`

	var out Cancha
	err := r.pool.QueryRow(ctx, query, canchaID, complejoID).Scan(
		&out.ID,
		&out.ComplejoID,
		&out.Nombre,
		&out.Modalidad,
		&out.Superficie,
		&out.Techada,
		&out.Iluminacion,
		&out.FotoURL,
		&out.FotoStorageKey,
		&out.Orden,
		&out.Activa,
		&out.CreatedAt,
		&out.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Cancha{}, ErrCanchaNotFound
		}
		return Cancha{}, mapPgError(err)
	}
	return out, nil
}

// ---------- Horarios de Atención ----------

func (r *PostgresRepository) ListHorarios(ctx context.Context, complejoID uuid.UUID) ([]HorarioAtencion, error) {
	const query = `
		SELECT
			id, complejo_id, dia_semana, minuto_apertura, minuto_cierre, cerrado,
			created_at, updated_at
		FROM horarios_atencion
		WHERE complejo_id = $1
		ORDER BY dia_semana ASC`

	rows, err := r.pool.Query(ctx, query, complejoID)
	if err != nil {
		return nil, fmt.Errorf("error listando horarios de atención: %w", err)
	}
	defer rows.Close()

	horarios := make([]HorarioAtencion, 0, 7)
	for rows.Next() {
		var h HorarioAtencion
		if err := rows.Scan(
			&h.ID,
			&h.ComplejoID,
			&h.DiaSemana,
			&h.MinutoApertura,
			&h.MinutoCierre,
			&h.Cerrado,
			&h.CreatedAt,
			&h.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("error escaneando horario de atención: %w", err)
		}
		horarios = append(horarios, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterando horarios de atención: %w", err)
	}
	return horarios, nil
}

func (r *PostgresRepository) UpsertHorarios(ctx context.Context, complejoID uuid.UUID, horarios []HorarioAtencion) ([]HorarioAtencion, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("error iniciando transacción de horarios: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	const upsertQuery = `
		INSERT INTO horarios_atencion (
			complejo_id, dia_semana, minuto_apertura, minuto_cierre, cerrado
		)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (complejo_id, dia_semana)
		DO UPDATE SET
			minuto_apertura = EXCLUDED.minuto_apertura,
			minuto_cierre = EXCLUDED.minuto_cierre,
			cerrado = EXCLUDED.cerrado
		RETURNING
			id, complejo_id, dia_semana, minuto_apertura, minuto_cierre, cerrado,
			created_at, updated_at`

	result := make([]HorarioAtencion, 0, len(horarios))
	for _, h := range horarios {
		var out HorarioAtencion
		err := tx.QueryRow(ctx, upsertQuery,
			complejoID,
			h.DiaSemana,
			h.MinutoApertura,
			h.MinutoCierre,
			h.Cerrado,
		).Scan(
			&out.ID,
			&out.ComplejoID,
			&out.DiaSemana,
			&out.MinutoApertura,
			&out.MinutoCierre,
			&out.Cerrado,
			&out.CreatedAt,
			&out.UpdatedAt,
		)
		if err != nil {
			return nil, mapPgError(err)
		}
		result = append(result, out)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("error confirmando upsert de horarios: %w", err)
	}

	return result, nil
}

// ---------- Tarifas por Franja ----------

func (r *PostgresRepository) ListTarifas(ctx context.Context, complejoID uuid.UUID, filter TarifasFilter) ([]TarifaFranja, error) {
	const query = `
		SELECT
			id, complejo_id, cancha_id, dia_semana, minuto_inicio, minuto_fin,
			precio_hora::float8, etiqueta, es_hora_muerta, activa, created_at, updated_at
		FROM tarifas_franja
		WHERE complejo_id = $1
		  AND ($2::uuid IS NULL OR cancha_id = $2)
		  AND ($3::smallint IS NULL OR dia_semana = $3)
		  AND ($4::boolean IS NULL OR activa = $4)
		ORDER BY dia_semana ASC, cancha_id NULLS FIRST, minuto_inicio ASC`

	rows, err := r.pool.Query(ctx, query, complejoID, filter.CanchaID, filter.DiaSemana, filter.Activa)
	if err != nil {
		return nil, fmt.Errorf("error listando tarifas por franja: %w", err)
	}
	defer rows.Close()

	tarifas := make([]TarifaFranja, 0)
	for rows.Next() {
		var t TarifaFranja
		if err := rows.Scan(
			&t.ID,
			&t.ComplejoID,
			&t.CanchaID,
			&t.DiaSemana,
			&t.MinutoInicio,
			&t.MinutoFin,
			&t.PrecioHora,
			&t.Etiqueta,
			&t.EsHoraMuerta,
			&t.Activa,
			&t.CreatedAt,
			&t.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("error escaneando tarifa por franja: %w", err)
		}
		tarifas = append(tarifas, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterando tarifas por franja: %w", err)
	}
	return tarifas, nil
}

func (r *PostgresRepository) GetTarifa(ctx context.Context, complejoID, tarifaID uuid.UUID) (TarifaFranja, error) {
	const query = `
		SELECT
			id, complejo_id, cancha_id, dia_semana, minuto_inicio, minuto_fin,
			precio_hora::float8, etiqueta, es_hora_muerta, activa, created_at, updated_at
		FROM tarifas_franja
		WHERE id = $1 AND complejo_id = $2`

	var t TarifaFranja
	err := r.pool.QueryRow(ctx, query, tarifaID, complejoID).Scan(
		&t.ID,
		&t.ComplejoID,
		&t.CanchaID,
		&t.DiaSemana,
		&t.MinutoInicio,
		&t.MinutoFin,
		&t.PrecioHora,
		&t.Etiqueta,
		&t.EsHoraMuerta,
		&t.Activa,
		&t.CreatedAt,
		&t.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TarifaFranja{}, ErrTarifaNotFound
		}
		return TarifaFranja{}, fmt.Errorf("error consultando tarifa por franja: %w", err)
	}
	return t, nil
}

func (r *PostgresRepository) CreateTarifa(ctx context.Context, t TarifaFranja) (TarifaFranja, error) {
	const query = `
		INSERT INTO tarifas_franja (
			complejo_id, cancha_id, dia_semana, minuto_inicio, minuto_fin,
			precio_hora, etiqueta, es_hora_muerta, activa
		)
		VALUES ($1, $2, $3, $4, $5, $6::numeric(10,2), $7, $8, $9)
		RETURNING
			id, complejo_id, cancha_id, dia_semana, minuto_inicio, minuto_fin,
			precio_hora::float8, etiqueta, es_hora_muerta, activa, created_at, updated_at`

	var out TarifaFranja
	err := r.pool.QueryRow(ctx, query,
		t.ComplejoID,
		t.CanchaID,
		t.DiaSemana,
		t.MinutoInicio,
		t.MinutoFin,
		formatDecimal(t.PrecioHora),
		t.Etiqueta,
		t.EsHoraMuerta,
		t.Activa,
	).Scan(
		&out.ID,
		&out.ComplejoID,
		&out.CanchaID,
		&out.DiaSemana,
		&out.MinutoInicio,
		&out.MinutoFin,
		&out.PrecioHora,
		&out.Etiqueta,
		&out.EsHoraMuerta,
		&out.Activa,
		&out.CreatedAt,
		&out.UpdatedAt,
	)
	if err != nil {
		return TarifaFranja{}, mapPgError(err)
	}
	return out, nil
}

func (r *PostgresRepository) UpdateTarifa(ctx context.Context, t TarifaFranja) (TarifaFranja, error) {
	const query = `
		UPDATE tarifas_franja
		SET
			cancha_id = $3,
			dia_semana = $4,
			minuto_inicio = $5,
			minuto_fin = $6,
			precio_hora = $7::numeric(10,2),
			etiqueta = $8,
			es_hora_muerta = $9,
			activa = $10
		WHERE id = $1 AND complejo_id = $2
		RETURNING
			id, complejo_id, cancha_id, dia_semana, minuto_inicio, minuto_fin,
			precio_hora::float8, etiqueta, es_hora_muerta, activa, created_at, updated_at`

	var out TarifaFranja
	err := r.pool.QueryRow(ctx, query,
		t.ID,
		t.ComplejoID,
		t.CanchaID,
		t.DiaSemana,
		t.MinutoInicio,
		t.MinutoFin,
		formatDecimal(t.PrecioHora),
		t.Etiqueta,
		t.EsHoraMuerta,
		t.Activa,
	).Scan(
		&out.ID,
		&out.ComplejoID,
		&out.CanchaID,
		&out.DiaSemana,
		&out.MinutoInicio,
		&out.MinutoFin,
		&out.PrecioHora,
		&out.Etiqueta,
		&out.EsHoraMuerta,
		&out.Activa,
		&out.CreatedAt,
		&out.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TarifaFranja{}, ErrTarifaNotFound
		}
		return TarifaFranja{}, mapPgError(err)
	}
	return out, nil
}

func (r *PostgresRepository) DeleteTarifa(ctx context.Context, complejoID, tarifaID uuid.UUID) error {
	const query = `DELETE FROM tarifas_franja WHERE id = $1 AND complejo_id = $2 RETURNING id`

	var deletedID uuid.UUID
	err := r.pool.QueryRow(ctx, query, tarifaID, complejoID).Scan(&deletedID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTarifaNotFound
		}
		return mapPgError(err)
	}
	return nil
}

// ---------- Cupones ----------

func (r *PostgresRepository) ListCupones(ctx context.Context, complejoID uuid.UUID, activo *bool) ([]Cupon, error) {
	const query = `
		SELECT
			id, complejo_id, codigo, descripcion, tipo_descuento,
			valor_descuento::float8, valido_desde, valido_hasta, max_usos,
			usos_actuales, acumulable_hora_muerta, activo, created_at, updated_at
		FROM cupones
		WHERE complejo_id = $1
		  AND ($2::boolean IS NULL OR activo = $2)
		ORDER BY activo DESC, created_at DESC`

	rows, err := r.pool.Query(ctx, query, complejoID, activo)
	if err != nil {
		return nil, fmt.Errorf("error listando cupones: %w", err)
	}
	defer rows.Close()

	cupones := make([]Cupon, 0)
	for rows.Next() {
		var c Cupon
		if err := rows.Scan(
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
			&c.CreatedAt,
			&c.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("error escaneando cupón: %w", err)
		}
		cupones = append(cupones, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterando cupones: %w", err)
	}
	return cupones, nil
}

func (r *PostgresRepository) GetCupon(ctx context.Context, complejoID, cuponID uuid.UUID) (Cupon, error) {
	const query = `
		SELECT
			id, complejo_id, codigo, descripcion, tipo_descuento,
			valor_descuento::float8, valido_desde, valido_hasta, max_usos,
			usos_actuales, acumulable_hora_muerta, activo, created_at, updated_at
		FROM cupones
		WHERE id = $1 AND complejo_id = $2`

	var c Cupon
	err := r.pool.QueryRow(ctx, query, cuponID, complejoID).Scan(
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
		&c.CreatedAt,
		&c.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Cupon{}, ErrCuponNotFound
		}
		return Cupon{}, fmt.Errorf("error consultando cupón: %w", err)
	}
	return c, nil
}

func (r *PostgresRepository) CreateCupon(ctx context.Context, c Cupon) (Cupon, error) {
	const query = `
		INSERT INTO cupones (
			complejo_id, codigo, descripcion, tipo_descuento, valor_descuento,
			valido_desde, valido_hasta, max_usos, acumulable_hora_muerta, activo
		)
		VALUES ($1, $2, $3, $4, $5::numeric(10,2), $6, $7, $8, $9, $10)
		RETURNING
			id, complejo_id, codigo, descripcion, tipo_descuento,
			valor_descuento::float8, valido_desde, valido_hasta, max_usos,
			usos_actuales, acumulable_hora_muerta, activo, created_at, updated_at`

	var out Cupon
	err := r.pool.QueryRow(ctx, query,
		c.ComplejoID,
		c.Codigo,
		c.Descripcion,
		c.TipoDescuento,
		formatDecimal(c.ValorDescuento),
		c.ValidoDesde,
		c.ValidoHasta,
		c.MaxUsos,
		c.AcumulableHoraMuerta,
		c.Activo,
	).Scan(
		&out.ID,
		&out.ComplejoID,
		&out.Codigo,
		&out.Descripcion,
		&out.TipoDescuento,
		&out.ValorDescuento,
		&out.ValidoDesde,
		&out.ValidoHasta,
		&out.MaxUsos,
		&out.UsosActuales,
		&out.AcumulableHoraMuerta,
		&out.Activo,
		&out.CreatedAt,
		&out.UpdatedAt,
	)
	if err != nil {
		return Cupon{}, mapPgError(err)
	}
	return out, nil
}

func (r *PostgresRepository) UpdateCupon(ctx context.Context, c Cupon) (Cupon, error) {
	const query = `
		UPDATE cupones
		SET
			codigo = $3,
			descripcion = $4,
			tipo_descuento = $5,
			valor_descuento = $6::numeric(10,2),
			valido_desde = $7,
			valido_hasta = $8,
			max_usos = $9,
			acumulable_hora_muerta = $10,
			activo = $11
		WHERE id = $1 AND complejo_id = $2
		RETURNING
			id, complejo_id, codigo, descripcion, tipo_descuento,
			valor_descuento::float8, valido_desde, valido_hasta, max_usos,
			usos_actuales, acumulable_hora_muerta, activo, created_at, updated_at`

	var out Cupon
	err := r.pool.QueryRow(ctx, query,
		c.ID,
		c.ComplejoID,
		c.Codigo,
		c.Descripcion,
		c.TipoDescuento,
		formatDecimal(c.ValorDescuento),
		c.ValidoDesde,
		c.ValidoHasta,
		c.MaxUsos,
		c.AcumulableHoraMuerta,
		c.Activo,
	).Scan(
		&out.ID,
		&out.ComplejoID,
		&out.Codigo,
		&out.Descripcion,
		&out.TipoDescuento,
		&out.ValorDescuento,
		&out.ValidoDesde,
		&out.ValidoHasta,
		&out.MaxUsos,
		&out.UsosActuales,
		&out.AcumulableHoraMuerta,
		&out.Activo,
		&out.CreatedAt,
		&out.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Cupon{}, ErrCuponNotFound
		}
		return Cupon{}, mapPgError(err)
	}
	return out, nil
}

// ---------- Métodos de Pago ----------

const metodoPagoColumns = `
	id, complejo_id, tipo, etiqueta, titular, entidad_financiera,
	numero_destino, cci, qr_url, qr_storage_key, llave_publica,
	(credenciales_cifradas IS NOT NULL AND octet_length(credenciales_cifradas) > 0) AS tiene_credenciales,
	instrucciones, activo, created_at, updated_at`

func (r *PostgresRepository) ListMetodosPago(ctx context.Context, complejoID uuid.UUID, activo *bool) ([]MetodoPago, error) {
	query := `
		SELECT` + metodoPagoColumns + `
		FROM metodos_pago_complejo
		WHERE complejo_id = $1
		  AND ($2::boolean IS NULL OR activo = $2)
		ORDER BY activo DESC, created_at ASC`

	rows, err := r.pool.Query(ctx, query, complejoID, activo)
	if err != nil {
		return nil, fmt.Errorf("error listando métodos de pago: %w", err)
	}
	defer rows.Close()

	metodos := make([]MetodoPago, 0)
	for rows.Next() {
		m, scanErr := scanMetodoPago(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("error escaneando método de pago: %w", scanErr)
		}
		metodos = append(metodos, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterando métodos de pago: %w", err)
	}
	return metodos, nil
}

func (r *PostgresRepository) GetMetodoPago(ctx context.Context, complejoID, metodoID uuid.UUID) (MetodoPago, error) {
	query := `
		SELECT` + metodoPagoColumns + `
		FROM metodos_pago_complejo
		WHERE id = $1 AND complejo_id = $2`

	m, err := scanMetodoPago(r.pool.QueryRow(ctx, query, metodoID, complejoID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MetodoPago{}, ErrMetodoPagoNotFound
		}
		return MetodoPago{}, fmt.Errorf("error consultando método de pago: %w", err)
	}
	return m, nil
}

func (r *PostgresRepository) CreateMetodoPago(ctx context.Context, p SaveMetodoPagoParams) (MetodoPago, error) {
	query := `
		INSERT INTO metodos_pago_complejo (
			complejo_id, tipo, etiqueta, titular, entidad_financiera,
			numero_destino, cci, qr_url, qr_storage_key, llave_publica,
			credenciales_cifradas, instrucciones, activo
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING` + metodoPagoColumns

	m, err := scanMetodoPago(r.pool.QueryRow(ctx, query,
		p.ComplejoID,
		p.Tipo,
		p.Etiqueta,
		p.Titular,
		p.EntidadFinanciera,
		p.NumeroDestino,
		p.CCI,
		p.QRURL,
		p.QRStorageKey,
		p.LlavePublica,
		p.CredencialesCifradas,
		p.Instrucciones,
		p.Activo,
	))
	if err != nil {
		return MetodoPago{}, mapPgError(err)
	}
	return m, nil
}

func (r *PostgresRepository) UpdateMetodoPago(ctx context.Context, p SaveMetodoPagoParams) (MetodoPago, error) {
	query := `
		UPDATE metodos_pago_complejo
		SET
			etiqueta = $3,
			titular = $4,
			entidad_financiera = $5,
			numero_destino = $6,
			cci = $7,
			qr_url = $8,
			qr_storage_key = $9,
			llave_publica = $10,
			credenciales_cifradas = CASE WHEN $11::boolean THEN $12::bytea ELSE credenciales_cifradas END,
			instrucciones = $13,
			activo = $14
		WHERE id = $1 AND complejo_id = $2
		RETURNING` + metodoPagoColumns

	m, err := scanMetodoPago(r.pool.QueryRow(ctx, query,
		p.ID,
		p.ComplejoID,
		p.Etiqueta,
		p.Titular,
		p.EntidadFinanciera,
		p.NumeroDestino,
		p.CCI,
		p.QRURL,
		p.QRStorageKey,
		p.LlavePublica,
		p.UpdateCredenciales,
		p.CredencialesCifradas,
		p.Instrucciones,
		p.Activo,
	))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MetodoPago{}, ErrMetodoPagoNotFound
		}
		return MetodoPago{}, mapPgError(err)
	}
	return m, nil
}

func (r *PostgresRepository) DeactivateMetodoPago(ctx context.Context, complejoID, metodoID uuid.UUID) (MetodoPago, error) {
	query := `
		UPDATE metodos_pago_complejo
		SET activo = false
		WHERE id = $1 AND complejo_id = $2
		RETURNING` + metodoPagoColumns

	m, err := scanMetodoPago(r.pool.QueryRow(ctx, query, metodoID, complejoID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MetodoPago{}, ErrMetodoPagoNotFound
		}
		return MetodoPago{}, mapPgError(err)
	}
	return m, nil
}

func scanMetodoPago(row pgx.Row) (MetodoPago, error) {
	var m MetodoPago
	err := row.Scan(
		&m.ID,
		&m.ComplejoID,
		&m.Tipo,
		&m.Etiqueta,
		&m.Titular,
		&m.EntidadFinanciera,
		&m.NumeroDestino,
		&m.CCI,
		&m.QRURL,
		&m.QRStorageKey,
		&m.LlavePublica,
		&m.TieneCredenciales,
		&m.Instrucciones,
		&m.Activo,
		&m.CreatedAt,
		&m.UpdatedAt,
	)
	return m, err
}

func formatDecimal(v float64) string {
	return fmt.Sprintf("%.2f", v)
}

func normalizeServiciosSlice(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func mapPgError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}

	constraint := strings.ToLower(pgErr.ConstraintName)
	switch pgErr.Code {
	case "23P01":
		return ErrTarifaOverlap
	case "23505":
		if strings.Contains(constraint, "cupones") || strings.Contains(constraint, "codigo") {
			return ErrCuponCodeAlreadyExists
		}
		return ErrConflict
	case "23503":
		if strings.Contains(constraint, "fk_tarifas_cancha_complejo") {
			return ErrCanchaNotFound
		}
		return fmt.Errorf("%w (%s)", ErrCheckViolation, pgErr.ConstraintName)
	case "23514":
		return fmt.Errorf("%w (%s)", ErrCheckViolation, pgErr.ConstraintName)
	default:
		return err
	}
}
