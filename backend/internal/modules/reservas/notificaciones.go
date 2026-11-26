package reservas

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	apphttp "github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/http"
)

// ---------- Contratos API: Notificaciones in-app y Web Push (Decisión 6) ----------

// ListNotificacionesFilter controla la paginación y filtrado por no leídas.
type ListNotificacionesFilter struct {
	SoloNoLeidas bool
	Limit        int
	Offset       int
}

// NotificacionItem representa una notificación in-app del usuario autenticado.
type NotificacionItem struct {
	ID         uuid.UUID  `json:"id"`
	ComplejoID *uuid.UUID `json:"complejo_id,omitempty"`
	ReservaID  *uuid.UUID `json:"reserva_id,omitempty"`
	Tipo       string     `json:"tipo"`
	Titulo     string     `json:"titulo"`
	Mensaje    string     `json:"mensaje"`
	Leida      bool       `json:"leida"`
	LeidaAt    *time.Time `json:"leida_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// NotificacionesListResponse envuelve la bandeja de notificaciones junto al conteo de no leídas.
type NotificacionesListResponse struct {
	Items         []NotificacionItem `json:"items"`
	Total         int                `json:"total"`
	TotalNoLeidas int                `json:"total_no_leidas"`
	Limit         int                `json:"limit"`
	Offset        int                `json:"offset"`
}

// LeerTodasNotificacionesResponse reporta cuántas notificaciones se marcaron como leídas.
type LeerTodasNotificacionesResponse struct {
	MarcadasLeidas int64 `json:"marcadas_leidas"`
}

// UpsertPushSubscriptionRequest representa el cuerpo de POST /api/v1/push-subscriptions.
type UpsertPushSubscriptionRequest struct {
	Endpoint  string `json:"endpoint"`
	P256dh    string `json:"p256dh"`
	Auth      string `json:"auth"`
	UserAgent string `json:"user_agent,omitempty"`
}

// UpsertPushSubscriptionInput contiene los datos normalizados listos para upsert SQL.
type UpsertPushSubscriptionInput struct {
	Endpoint  string
	P256dh    string
	Auth      string
	UserAgent *string
}

// PushSubscriptionResponse confirma el registro o actualización de una suscripción Web Push.
type PushSubscriptionResponse struct {
	ID            uuid.UUID  `json:"id"`
	Endpoint      string     `json:"endpoint"`
	UserAgent     *string    `json:"user_agent,omitempty"`
	Activa        bool       `json:"activa"`
	UltimoEnvioAt *time.Time `json:"ultimo_envio_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// DeletePushSubscriptionRequest permite enviar el endpoint a desactivar en el cuerpo de DELETE.
type DeletePushSubscriptionRequest struct {
	Endpoint string `json:"endpoint"`
}

// DeletePushSubscriptionResponse confirma la desactivación del endpoint del usuario autenticado.
type DeletePushSubscriptionResponse struct {
	Desactivada bool   `json:"desactivada"`
	Endpoint    string `json:"endpoint"`
}

// ---------- Repositorio PostgreSQL ----------

// ListNotificaciones consulta la bandeja del usuario autenticado y su contador total_no_leidas.
func (r *PostgresRepository) ListNotificaciones(ctx context.Context, usuarioID uuid.UUID, filter ListNotificacionesFilter) (NotificacionesListResponse, error) {
	const countsSQL = `
		SELECT
			COUNT(*) FILTER (WHERE $2::boolean = false OR leida_at IS NULL)::int AS total_filtrado,
			COUNT(*) FILTER (WHERE leida_at IS NULL)::int AS total_no_leidas
		FROM notificaciones
		WHERE usuario_id = $1`

	var total, totalNoLeidas int
	if err := r.pool.QueryRow(ctx, countsSQL, usuarioID, filter.SoloNoLeidas).Scan(&total, &totalNoLeidas); err != nil {
		return NotificacionesListResponse{}, fmt.Errorf("error contando notificaciones: %w", err)
	}

	resp := NotificacionesListResponse{
		Items:         []NotificacionItem{},
		Total:         total,
		TotalNoLeidas: totalNoLeidas,
		Limit:         filter.Limit,
		Offset:        filter.Offset,
	}
	if total == 0 {
		return resp, nil
	}

	const listSQL = `
		SELECT id, complejo_id, reserva_id, tipo, titulo, mensaje, leida_at, created_at
		FROM notificaciones
		WHERE usuario_id = $1
		  AND ($2::boolean = false OR leida_at IS NULL)
		ORDER BY created_at DESC, id DESC
		LIMIT $3 OFFSET $4`

	rows, err := r.pool.Query(ctx, listSQL, usuarioID, filter.SoloNoLeidas, filter.Limit, filter.Offset)
	if err != nil {
		return NotificacionesListResponse{}, fmt.Errorf("error listando notificaciones: %w", err)
	}
	defer rows.Close()

	items := make([]NotificacionItem, 0, filter.Limit)
	for rows.Next() {
		var (
			item      NotificacionItem
			leidaAt   *time.Time
			createdAt time.Time
		)
		if err := rows.Scan(
			&item.ID,
			&item.ComplejoID,
			&item.ReservaID,
			&item.Tipo,
			&item.Titulo,
			&item.Mensaje,
			&leidaAt,
			&createdAt,
		); err != nil {
			return NotificacionesListResponse{}, fmt.Errorf("error escaneando notificación: %w", err)
		}
		item.Leida = leidaAt != nil
		item.LeidaAt = toLimaPtr(leidaAt)
		item.CreatedAt = createdAt.In(limaLocation)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return NotificacionesListResponse{}, fmt.Errorf("error iterando notificaciones: %w", err)
	}

	resp.Items = items
	return resp, nil
}

// MarkNotificacionLeida marca una notificación como leída únicamente si pertenece al usuario autenticado.
func (r *PostgresRepository) MarkNotificacionLeida(ctx context.Context, usuarioID, notificacionID uuid.UUID) (NotificacionItem, error) {
	const sql = `
		UPDATE notificaciones
		SET leida_at = COALESCE(leida_at, NOW())
		WHERE id = $1 AND usuario_id = $2
		RETURNING id, complejo_id, reserva_id, tipo, titulo, mensaje, leida_at, created_at`

	var (
		item      NotificacionItem
		leidaAt   *time.Time
		createdAt time.Time
	)
	if err := r.pool.QueryRow(ctx, sql, notificacionID, usuarioID).Scan(
		&item.ID,
		&item.ComplejoID,
		&item.ReservaID,
		&item.Tipo,
		&item.Titulo,
		&item.Mensaje,
		&leidaAt,
		&createdAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return NotificacionItem{}, ErrNotificacionNotFound
		}
		return NotificacionItem{}, fmt.Errorf("error marcando notificación como leída: %w", err)
	}

	item.Leida = leidaAt != nil
	item.LeidaAt = toLimaPtr(leidaAt)
	item.CreatedAt = createdAt.In(limaLocation)
	return item, nil
}

// MarkAllNotificacionesLeidas marca como leídas todas las notificaciones pendientes del usuario.
func (r *PostgresRepository) MarkAllNotificacionesLeidas(ctx context.Context, usuarioID uuid.UUID) (int64, error) {
	const sql = `
		UPDATE notificaciones
		SET leida_at = NOW()
		WHERE usuario_id = $1 AND leida_at IS NULL`

	tag, err := r.pool.Exec(ctx, sql, usuarioID)
	if err != nil {
		return 0, fmt.Errorf("error marcando notificaciones como leídas: %w", err)
	}
	return tag.RowsAffected(), nil
}

// UpsertPushSubscription inserta o actualiza una suscripción Web Push por su endpoint único.
func (r *PostgresRepository) UpsertPushSubscription(ctx context.Context, usuarioID uuid.UUID, in UpsertPushSubscriptionInput) (PushSubscriptionResponse, error) {
	const sql = `
		INSERT INTO push_subscriptions (usuario_id, endpoint, p256dh, auth, user_agent, activa)
		VALUES ($1, $2, $3, $4, $5, true)
		ON CONFLICT (endpoint) DO UPDATE SET
			usuario_id = EXCLUDED.usuario_id,
			p256dh = EXCLUDED.p256dh,
			auth = EXCLUDED.auth,
			user_agent = EXCLUDED.user_agent,
			activa = true,
			updated_at = NOW()
		RETURNING id, endpoint, user_agent, activa, ultimo_envio_at, created_at, updated_at`

	var (
		out           PushSubscriptionResponse
		ultimoEnvioAt *time.Time
		createdAt     time.Time
		updatedAt     time.Time
	)
	if err := r.pool.QueryRow(ctx, sql,
		usuarioID,
		in.Endpoint,
		in.P256dh,
		in.Auth,
		in.UserAgent,
	).Scan(
		&out.ID,
		&out.Endpoint,
		&out.UserAgent,
		&out.Activa,
		&ultimoEnvioAt,
		&createdAt,
		&updatedAt,
	); err != nil {
		return PushSubscriptionResponse{}, fmt.Errorf("error guardando suscripción Web Push: %w", err)
	}

	out.UltimoEnvioAt = toLimaPtr(ultimoEnvioAt)
	out.CreatedAt = createdAt.In(limaLocation)
	out.UpdatedAt = updatedAt.In(limaLocation)
	return out, nil
}

// DeactivatePushSubscription desactiva el endpoint Web Push perteneciente al usuario autenticado.
func (r *PostgresRepository) DeactivatePushSubscription(ctx context.Context, usuarioID uuid.UUID, endpoint string) error {
	const sql = `
		UPDATE push_subscriptions
		SET activa = false,
		    updated_at = NOW()
		WHERE usuario_id = $1 AND endpoint = $2`

	tag, err := r.pool.Exec(ctx, sql, usuarioID, endpoint)
	if err != nil {
		return fmt.Errorf("error desactivando suscripción Web Push: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrPushSubscriptionNotFound
	}
	return nil
}

// ---------- Lógica de Servicio ----------

// ListNotificaciones valida paginación y recupera la bandeja in-app del usuario autenticado.
func (s *Service) ListNotificaciones(ctx context.Context, usuarioID uuid.UUID, filter ListNotificacionesFilter) (NotificacionesListResponse, error) {
	fields := make(map[string]string)

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
		return NotificacionesListResponse{}, &ValidationError{
			Message: "Parámetros de notificaciones inválidos",
			Fields:  fields,
		}
	}

	return s.repo.ListNotificaciones(ctx, usuarioID, ListNotificacionesFilter{
		SoloNoLeidas: filter.SoloNoLeidas,
		Limit:        limit,
		Offset:       filter.Offset,
	})
}

// MarkNotificacionLeida marca una notificación individual como leída para el usuario autenticado.
func (s *Service) MarkNotificacionLeida(ctx context.Context, usuarioID, notificacionID uuid.UUID) (NotificacionItem, error) {
	return s.repo.MarkNotificacionLeida(ctx, usuarioID, notificacionID)
}

// MarkAllNotificacionesLeidas marca todas las notificaciones pendientes del usuario autenticado.
func (s *Service) MarkAllNotificacionesLeidas(ctx context.Context, usuarioID uuid.UUID) (LeerTodasNotificacionesResponse, error) {
	count, err := s.repo.MarkAllNotificacionesLeidas(ctx, usuarioID)
	if err != nil {
		return LeerTodasNotificacionesResponse{}, err
	}
	return LeerTodasNotificacionesResponse{MarcadasLeidas: count}, nil
}

// UpsertPushSubscription valida claves VAPID/W3C y registra/actualiza la suscripción Web Push.
func (s *Service) UpsertPushSubscription(ctx context.Context, usuarioID uuid.UUID, req UpsertPushSubscriptionRequest, fallbackUA string) (PushSubscriptionResponse, error) {
	fields := make(map[string]string)

	endpoint := strings.TrimSpace(req.Endpoint)
	if endpoint == "" || len(endpoint) > 2048 {
		fields["endpoint"] = "endpoint es obligatorio y debe ser una URL válida"
	} else {
		parsed, err := url.ParseRequestURI(endpoint)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			fields["endpoint"] = "endpoint debe ser una URL http/https válida"
		}
	}

	p256dh := strings.TrimSpace(req.P256dh)
	if p256dh == "" || len(p256dh) > 512 {
		fields["p256dh"] = "p256dh es obligatorio"
	}

	authKey := strings.TrimSpace(req.Auth)
	if authKey == "" || len(authKey) > 512 {
		fields["auth"] = "auth es obligatorio"
	}

	if len(fields) > 0 {
		return PushSubscriptionResponse{}, &ValidationError{
			Message: "Datos de suscripción Web Push inválidos",
			Fields:  fields,
		}
	}

	ua := strings.TrimSpace(req.UserAgent)
	if ua == "" {
		ua = strings.TrimSpace(fallbackUA)
	}
	var uaPtr *string
	if ua != "" {
		ua = truncateRunes(ua, 255)
		uaPtr = &ua
	}

	return s.repo.UpsertPushSubscription(ctx, usuarioID, UpsertPushSubscriptionInput{
		Endpoint:  endpoint,
		P256dh:    p256dh,
		Auth:      authKey,
		UserAgent: uaPtr,
	})
}

// DeletePushSubscription valida y desactiva la suscripción Web Push del usuario autenticado.
func (s *Service) DeletePushSubscription(ctx context.Context, usuarioID uuid.UUID, rawEndpoint string) (DeletePushSubscriptionResponse, error) {
	endpoint := strings.TrimSpace(rawEndpoint)
	if endpoint == "" || len(endpoint) > 2048 {
		return DeletePushSubscriptionResponse{}, &ValidationError{
			Message: "Datos inválidos",
			Fields:  map[string]string{"endpoint": "endpoint es obligatorio"},
		}
	}

	if err := s.repo.DeactivatePushSubscription(ctx, usuarioID, endpoint); err != nil {
		return DeletePushSubscriptionResponse{}, err
	}

	return DeletePushSubscriptionResponse{
		Desactivada: true,
		Endpoint:    endpoint,
	}, nil
}

// ---------- Handlers HTTP ----------

func (h *Handler) handleListNotificaciones(w http.ResponseWriter, r *http.Request) {
	user, ok := apphttp.AuthUserFromContext(r.Context())
	if !ok {
		apphttp.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Autenticación requerida")
		return
	}

	q := r.URL.Query()
	fields := make(map[string]string)
	var filter ListNotificacionesFilter

	if rawSolo := strings.TrimSpace(q.Get("solo_no_leidas")); rawSolo != "" {
		parsed, err := strconv.ParseBool(rawSolo)
		if err != nil {
			fields["solo_no_leidas"] = "solo_no_leidas debe ser true o false"
		} else {
			filter.SoloNoLeidas = parsed
		}
	}

	if rawLimit := strings.TrimSpace(q.Get("limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed < 1 || parsed > MaxListLimit {
			fields["limit"] = "limit debe ser un entero entre 1 y 50"
		} else {
			filter.Limit = parsed
		}
	}

	if rawOffset := strings.TrimSpace(q.Get("offset")); rawOffset != "" {
		parsed, err := strconv.Atoi(rawOffset)
		if err != nil || parsed < 0 {
			fields["offset"] = "offset debe ser un entero mayor o igual a 0"
		} else {
			filter.Offset = parsed
		}
	}

	if len(fields) > 0 {
		apphttp.WriteValidationError(w, "Parámetros de notificaciones inválidos", fields)
		return
	}

	res, err := h.service.ListNotificaciones(r.Context(), user.UserID, filter)
	if err != nil {
		h.writeServiceError(w, r, "list_notificaciones_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) handleMarkNotificacionLeida(w http.ResponseWriter, r *http.Request) {
	user, ok := apphttp.AuthUserFromContext(r.Context())
	if !ok {
		apphttp.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Autenticación requerida")
		return
	}

	notificacionID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "notificacionID")))
	if err != nil || notificacionID == uuid.Nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Identificador de notificación inválido")
		return
	}

	res, err := h.service.MarkNotificacionLeida(r.Context(), user.UserID, notificacionID)
	if err != nil {
		h.writeServiceError(w, r, "mark_notificacion_leida_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) handleMarkAllNotificacionesLeidas(w http.ResponseWriter, r *http.Request) {
	user, ok := apphttp.AuthUserFromContext(r.Context())
	if !ok {
		apphttp.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Autenticación requerida")
		return
	}

	res, err := h.service.MarkAllNotificacionesLeidas(r.Context(), user.UserID)
	if err != nil {
		h.writeServiceError(w, r, "mark_all_notificaciones_leidas_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) handleUpsertPushSubscription(w http.ResponseWriter, r *http.Request) {
	user, ok := apphttp.AuthUserFromContext(r.Context())
	if !ok {
		apphttp.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Autenticación requerida")
		return
	}

	var req UpsertPushSubscriptionRequest
	if err := apphttp.DecodeJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	res, err := h.service.UpsertPushSubscription(r.Context(), user.UserID, req, r.UserAgent())
	if err != nil {
		h.writeServiceError(w, r, "upsert_push_subscription_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) handleDeletePushSubscription(w http.ResponseWriter, r *http.Request) {
	user, ok := apphttp.AuthUserFromContext(r.Context())
	if !ok {
		apphttp.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Autenticación requerida")
		return
	}

	var req DeletePushSubscriptionRequest
	if err := apphttp.DecodeOptionalJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	endpoint := strings.TrimSpace(req.Endpoint)
	if endpoint == "" {
		endpoint = strings.TrimSpace(r.URL.Query().Get("endpoint"))
	}

	res, err := h.service.DeletePushSubscription(r.Context(), user.UserID, endpoint)
	if err != nil {
		h.writeServiceError(w, r, "delete_push_subscription_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, res)
}

func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max])
}
