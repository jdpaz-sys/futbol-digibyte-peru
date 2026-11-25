package geo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store abstrae el acceso de solo lectura al catálogo público y geoespacial en PostgreSQL/PostGIS.
type Store interface {
	ExplorePublicComplejos(ctx context.Context, q ExploreComplejosQuery) ([]PublicComplejoItem, int, error)
	GetComplejoBaseBySlug(ctx context.Context, slug string) (ComplejoBaseRecord, error)
	GetComplejoPublicCatalog(ctx context.Context, complejoID uuid.UUID) (
		fotos []PublicFoto,
		canchas []PublicCancha,
		horarios []PublicHorarioAtencion,
		tarifas []PublicTarifaFranja,
		tiposMetodosPago []string,
		err error,
	)
}

// Repository implementa Store sobre un pool pgx/v5 conectado a PostgreSQL 16 + PostGIS.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository crea un repositorio geoespacial y de catálogo público.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// ExplorePublicComplejos consulta exclusivamente complejos afiliados publicados y vigentes (RN-02).
// En modo GPS calcula cercanía con PostGIS (ST_DWithin + ST_Distance) sin persistir jamás la posición del jugador (RN-19 / RNF-04).
func (r *Repository) ExplorePublicComplejos(ctx context.Context, q ExploreComplejosQuery) ([]PublicComplejoItem, int, error) {
	whereParts := []string{
		"c.publicado = true",
		"c.ubicacion IS NOT NULL",
		"c.estado_suscripcion IN ('PRUEBA', 'ACTIVA')",
		"c.suscripcion_vence_at IS NOT NULL",
		"c.suscripcion_vence_at > NOW()",
	}
	args := make([]any, 0, 8)

	distExpr := "NULL::float8"
	orderBy := "ORDER BY LOWER(c.nombre) ASC, c.id ASC"

	if q.UseGPS {
		args = append(args, q.Lng, q.Lat, q.RadioMetros)
		pointGeog := "ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography"
		whereParts = append(whereParts, fmt.Sprintf("ST_DWithin(c.ubicacion, %s, $3)", pointGeog))
		distExpr = fmt.Sprintf("ROUND(ST_Distance(c.ubicacion, %s)::numeric, 2)::float8", pointGeog)
		orderBy = fmt.Sprintf("ORDER BY ST_Distance(c.ubicacion, %s) ASC, LOWER(c.nombre) ASC, c.id ASC", pointGeog)
	}

	if q.Departamento != "" {
		args = append(args, q.Departamento)
		whereParts = append(whereParts, fmt.Sprintf("LOWER(c.departamento) = LOWER($%d)", len(args)))
	}
	if q.Provincia != "" {
		args = append(args, q.Provincia)
		whereParts = append(whereParts, fmt.Sprintf("LOWER(c.provincia) = LOWER($%d)", len(args)))
	}
	if q.Distrito != "" {
		args = append(args, q.Distrito)
		whereParts = append(whereParts, fmt.Sprintf("LOWER(c.distrito) = LOWER($%d)", len(args)))
	}
	if q.QueryLike != "" {
		args = append(args, q.QueryLike)
		idx := len(args)
		whereParts = append(whereParts, fmt.Sprintf(
			`(c.nombre ILIKE $%d ESCAPE '\' OR COALESCE(c.direccion_escrita, '') ILIKE $%d ESCAPE '\' OR COALESCE(c.referencias, '') ILIKE $%d ESCAPE '\')`,
			idx, idx, idx,
		))
	}
	if q.Modalidad != "" {
		args = append(args, q.Modalidad)
		whereParts = append(whereParts, fmt.Sprintf(
			"EXISTS (SELECT 1 FROM canchas ca WHERE ca.complejo_id = c.id AND ca.activa = true AND ca.modalidad = $%d)",
			len(args),
		))
	}

	whereSQL := strings.Join(whereParts, " AND ")

	countSQL := fmt.Sprintf("SELECT COUNT(*) FROM complejos c WHERE %s", whereSQL)
	var total int
	if err := r.pool.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("error contando complejos públicos: %w", err)
	}
	if total == 0 {
		return []PublicComplejoItem{}, 0, nil
	}

	queryArgs := make([]any, 0, len(args)+2)
	queryArgs = append(queryArgs, args...)
	queryArgs = append(queryArgs, q.Limit, q.Offset)
	limitPos := len(args) + 1
	offsetPos := len(args) + 2

	selectSQL := fmt.Sprintf(`
		SELECT
			c.id,
			c.slug,
			c.nombre,
			COALESCE(c.direccion_escrita, ''),
			c.referencias,
			c.distrito,
			c.provincia,
			c.departamento,
			ST_Y(c.ubicacion::geometry)::float8 AS lat,
			ST_X(c.ubicacion::geometry)::float8 AS lng,
			%s AS distancia_metros,
			(
				SELECT cf.url
				FROM complejo_fotos cf
				WHERE cf.complejo_id = c.id
				ORDER BY cf.es_portada DESC, cf.orden ASC, cf.created_at ASC
				LIMIT 1
			) AS foto_portada_url,
			(
				SELECT MIN(tf.precio_hora)::float8
				FROM tarifas_franja tf
				LEFT JOIN canchas ca ON ca.id = tf.cancha_id AND ca.complejo_id = tf.complejo_id
				WHERE tf.complejo_id = c.id
				  AND tf.activa = true
				  AND (tf.cancha_id IS NULL OR ca.activa = true)
			) AS tarifa_desde,
			COALESCE(
				ARRAY(
					SELECT DISTINCT ca.modalidad
					FROM canchas ca
					WHERE ca.complejo_id = c.id AND ca.activa = true
					ORDER BY ca.modalidad ASC
				),
				ARRAY[]::text[]
			) AS modalidades,
			c.servicios
		FROM complejos c
		WHERE %s
		%s
		LIMIT $%d OFFSET $%d`,
		distExpr, whereSQL, orderBy, limitPos, offsetPos,
	)

	rows, err := r.pool.Query(ctx, selectSQL, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("error consultando complejos públicos: %w", err)
	}
	defer rows.Close()

	items := make([]PublicComplejoItem, 0, q.Limit)
	for rows.Next() {
		var item PublicComplejoItem
		var rawServicios []byte

		if scanErr := rows.Scan(
			&item.ID,
			&item.Slug,
			&item.Nombre,
			&item.DireccionEscrita,
			&item.Referencias,
			&item.Distrito,
			&item.Provincia,
			&item.Departamento,
			&item.Lat,
			&item.Lng,
			&item.DistanciaMetros,
			&item.FotoPortadaURL,
			&item.TarifaDesde,
			&item.Modalidades,
			&rawServicios,
		); scanErr != nil {
			return nil, 0, fmt.Errorf("error escaneando complejo público: %w", scanErr)
		}

		if item.Modalidades == nil {
			item.Modalidades = []string{}
		}
		item.Servicios = decodeServiciosJSON(rawServicios)

		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("error iterando complejos públicos: %w", err)
	}

	return items, total, nil
}

// GetComplejoBaseBySlug obtiene los datos base de un complejo publicado por su slug único.
// Si no existe o publicado = false retorna ErrComplejoNotFound.
func (r *Repository) GetComplejoBaseBySlug(ctx context.Context, slug string) (ComplejoBaseRecord, error) {
	const query = `
		SELECT
			id,
			slug,
			nombre,
			descripcion,
			telefono_contacto,
			servicios,
			departamento,
			provincia,
			distrito,
			CASE WHEN ubicacion IS NOT NULL THEN ST_Y(ubicacion::geometry)::float8 END AS lat,
			CASE WHEN ubicacion IS NOT NULL THEN ST_X(ubicacion::geometry)::float8 END AS lng,
			direccion_escrita,
			referencias,
			publicado,
			unidad_reserva_min,
			hold_duracion_min,
			tipo_adelanto,
			valor_adelanto::float8,
			horas_min_cancelacion,
			terminos_reserva,
			estado_suscripcion,
			suscripcion_vence_at
		FROM complejos
		WHERE slug = $1`

	var rec ComplejoBaseRecord
	var rawServicios []byte

	err := r.pool.QueryRow(ctx, query, slug).Scan(
		&rec.ID,
		&rec.Slug,
		&rec.Nombre,
		&rec.Descripcion,
		&rec.TelefonoContacto,
		&rawServicios,
		&rec.Departamento,
		&rec.Provincia,
		&rec.Distrito,
		&rec.Lat,
		&rec.Lng,
		&rec.DireccionEscrita,
		&rec.Referencias,
		&rec.Publicado,
		&rec.UnidadReservaMin,
		&rec.HoldDuracionMin,
		&rec.TipoAdelanto,
		&rec.ValorAdelanto,
		&rec.HorasMinCancelacion,
		&rec.TerminosReserva,
		&rec.EstadoSuscripcion,
		&rec.SuscripcionVenceAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ComplejoBaseRecord{}, ErrComplejoNotFound
		}
		return ComplejoBaseRecord{}, fmt.Errorf("error consultando complejo por slug: %w", err)
	}

	if !rec.Publicado {
		return ComplejoBaseRecord{}, ErrComplejoNotFound
	}

	rec.Servicios = decodeServiciosJSON(rawServicios)
	return rec, nil
}

// GetComplejoPublicCatalog recupera en un único batch SQL las fotos ordenadas, canchas activas,
// horarios de atención, tarifas activas y exclusivamente los tipos de métodos de pago activos.
func (r *Repository) GetComplejoPublicCatalog(ctx context.Context, complejoID uuid.UUID) (
	[]PublicFoto,
	[]PublicCancha,
	[]PublicHorarioAtencion,
	[]PublicTarifaFranja,
	[]string,
	error,
) {
	batch := &pgx.Batch{}

	batch.Queue(`
		SELECT id, url, descripcion, orden, es_portada
		FROM complejo_fotos
		WHERE complejo_id = $1
		ORDER BY es_portada DESC, orden ASC, created_at ASC`, complejoID)

	batch.Queue(`
		SELECT id, nombre, modalidad, superficie, techada, iluminacion, foto_url, orden
		FROM canchas
		WHERE complejo_id = $1 AND activa = true
		ORDER BY orden ASC, nombre ASC`, complejoID)

	batch.Queue(`
		SELECT dia_semana, minuto_apertura, minuto_cierre, cerrado
		FROM horarios_atencion
		WHERE complejo_id = $1
		ORDER BY dia_semana ASC`, complejoID)

	batch.Queue(`
		SELECT tf.id, tf.cancha_id, tf.dia_semana, tf.minuto_inicio, tf.minuto_fin, tf.precio_hora::float8, tf.etiqueta, tf.es_hora_muerta
		FROM tarifas_franja tf
		LEFT JOIN canchas ca ON ca.id = tf.cancha_id AND ca.complejo_id = tf.complejo_id
		WHERE tf.complejo_id = $1
		  AND tf.activa = true
		  AND (tf.cancha_id IS NULL OR ca.activa = true)
		ORDER BY tf.dia_semana ASC, tf.minuto_inicio ASC, tf.cancha_id NULLS FIRST`, complejoID)

	batch.Queue(`
		SELECT DISTINCT tipo
		FROM metodos_pago_complejo
		WHERE complejo_id = $1 AND activo = true
		ORDER BY tipo ASC`, complejoID)

	br := r.pool.SendBatch(ctx, batch)
	defer func() {
		_ = br.Close()
	}()

	// 1. Fotos
	fotosRows, err := br.Query()
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("error consultando fotos públicas: %w", err)
	}
	fotos := make([]PublicFoto, 0)
	for fotosRows.Next() {
		var f PublicFoto
		if scanErr := fotosRows.Scan(&f.ID, &f.URL, &f.Descripcion, &f.Orden, &f.EsPortada); scanErr != nil {
			fotosRows.Close()
			return nil, nil, nil, nil, nil, fmt.Errorf("error escaneando foto pública: %w", scanErr)
		}
		fotos = append(fotos, f)
	}
	if rowErr := fotosRows.Err(); rowErr != nil {
		fotosRows.Close()
		return nil, nil, nil, nil, nil, fmt.Errorf("error iterando fotos públicas: %w", rowErr)
	}
	fotosRows.Close()

	// 2. Canchas activas
	canchasRows, err := br.Query()
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("error consultando canchas públicas: %w", err)
	}
	canchas := make([]PublicCancha, 0)
	for canchasRows.Next() {
		var ca PublicCancha
		if scanErr := canchasRows.Scan(
			&ca.ID,
			&ca.Nombre,
			&ca.Modalidad,
			&ca.Superficie,
			&ca.Techada,
			&ca.Iluminacion,
			&ca.FotoURL,
			&ca.Orden,
		); scanErr != nil {
			canchasRows.Close()
			return nil, nil, nil, nil, nil, fmt.Errorf("error escaneando cancha pública: %w", scanErr)
		}
		canchas = append(canchas, ca)
	}
	if rowErr := canchasRows.Err(); rowErr != nil {
		canchasRows.Close()
		return nil, nil, nil, nil, nil, fmt.Errorf("error iterando canchas públicas: %w", rowErr)
	}
	canchasRows.Close()

	// 3. Horarios de atención
	horariosRows, err := br.Query()
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("error consultando horarios públicos: %w", err)
	}
	horarios := make([]PublicHorarioAtencion, 0, 7)
	for horariosRows.Next() {
		var h PublicHorarioAtencion
		if scanErr := horariosRows.Scan(&h.DiaSemana, &h.MinutoApertura, &h.MinutoCierre, &h.Cerrado); scanErr != nil {
			horariosRows.Close()
			return nil, nil, nil, nil, nil, fmt.Errorf("error escaneando horario público: %w", scanErr)
		}
		horarios = append(horarios, h)
	}
	if rowErr := horariosRows.Err(); rowErr != nil {
		horariosRows.Close()
		return nil, nil, nil, nil, nil, fmt.Errorf("error iterando horarios públicos: %w", rowErr)
	}
	horariosRows.Close()

	// 4. Tarifas activas
	tarifasRows, err := br.Query()
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("error consultando tarifas públicas: %w", err)
	}
	tarifas := make([]PublicTarifaFranja, 0)
	for tarifasRows.Next() {
		var tf PublicTarifaFranja
		if scanErr := tarifasRows.Scan(
			&tf.ID,
			&tf.CanchaID,
			&tf.DiaSemana,
			&tf.MinutoInicio,
			&tf.MinutoFin,
			&tf.PrecioHora,
			&tf.Etiqueta,
			&tf.EsHoraMuerta,
		); scanErr != nil {
			tarifasRows.Close()
			return nil, nil, nil, nil, nil, fmt.Errorf("error escaneando tarifa pública: %w", scanErr)
		}
		tarifas = append(tarifas, tf)
	}
	if rowErr := tarifasRows.Err(); rowErr != nil {
		tarifasRows.Close()
		return nil, nil, nil, nil, nil, fmt.Errorf("error iterando tarifas públicas: %w", rowErr)
	}
	tarifasRows.Close()

	// 5. Tipos de métodos de pago activos (cero exposición de cuentas ni secretos)
	metodosRows, err := br.Query()
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("error consultando tipos de métodos de pago: %w", err)
	}
	tiposMetodosPago := make([]string, 0)
	for metodosRows.Next() {
		var tipo string
		if scanErr := metodosRows.Scan(&tipo); scanErr != nil {
			metodosRows.Close()
			return nil, nil, nil, nil, nil, fmt.Errorf("error escaneando tipo de método de pago: %w", scanErr)
		}
		tiposMetodosPago = append(tiposMetodosPago, tipo)
	}
	if rowErr := metodosRows.Err(); rowErr != nil {
		metodosRows.Close()
		return nil, nil, nil, nil, nil, fmt.Errorf("error iterando tipos de métodos de pago: %w", rowErr)
	}
	metodosRows.Close()

	if err := br.Close(); err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("error cerrando batch de catálogo público: %w", err)
	}

	return fotos, canchas, horarios, tarifas, tiposMetodosPago, nil
}

func decodeServiciosJSON(raw []byte) []string {
	if len(raw) == 0 {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return []string{}
	}
	return out
}
