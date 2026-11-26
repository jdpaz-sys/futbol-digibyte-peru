package reservas

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PublicCacheInvalidator invalida de forma desacoplada la caché pública por slug en Valkey.
type PublicCacheInvalidator interface {
	InvalidatePublicComplejo(ctx context.Context, slug string) error
}

// ExpiredSubscriptionResult resume un complejo cuya suscripción venció automáticamente en CU-14.
type ExpiredSubscriptionResult struct {
	ComplejoID uuid.UUID
	Slug       string
	Push       PushMessage
}

// MaintenanceResult agrupa el conteo de acciones ejecutadas en un ciclo de mantenimiento CU-14.
type MaintenanceResult struct {
	SuscripcionesVencidas int
	AvisosVencimiento     int
	RecordatoriosPartido  int
	InvalidatedSlugs      []string
}

// MaintenanceStore encapsula las tareas periódicas de mantenimiento CU-14 en PostgreSQL.
type MaintenanceStore interface {
	ExpireOverdueSubscriptions(ctx context.Context, batchSize int) ([]ExpiredSubscriptionResult, error)
	NotifyExpiringSubscriptions(ctx context.Context, within time.Duration, batchSize int) ([]PushMessage, error)
	NotifyUpcomingMatchReminders(ctx context.Context, within time.Duration, batchSize int) ([]PushMessage, error)
}

// ExpirationWorker ejecuta periódicamente:
// 1) Cada 30s: expiración de Holds vencidos en estado PENDIENTE_PAGO (sin tocar POR_VALIDAR, Decisión 3).
// 2) Cada 5m: mantenimiento CU-14 (vencer suscripciones vencidas RN-02, avisos <=3 días y recordatorios <2h).
type ExpirationWorker struct {
	expirer             HoldExpirer
	maintenance         MaintenanceStore
	publicCache         PublicCacheInvalidator
	pushDispatcher      PushDispatcher
	interval            time.Duration
	maintenanceInterval time.Duration
	batchSize           int
	logger              *slog.Logger
}

// NewExpirationWorker crea un worker controlado por context.Context.
// Si interval <= 0 usa DefaultWorkerInterval (30 segundos).
func NewExpirationWorker(expirer HoldExpirer, interval time.Duration, logger *slog.Logger) *ExpirationWorker {
	if interval <= 0 {
		interval = DefaultWorkerInterval
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ExpirationWorker{
		expirer:             expirer,
		interval:            interval,
		maintenanceInterval: DefaultMaintenanceInterval,
		batchSize:           DefaultWorkerBatchSize,
		logger:              logger,
	}
}

// WithMaintenance habilita el ciclo periódico de mantenimiento CU-14 (default 5 minutos).
func (w *ExpirationWorker) WithMaintenance(store MaintenanceStore, interval time.Duration) *ExpirationWorker {
	w.maintenance = store
	if interval > 0 {
		w.maintenanceInterval = interval
	}
	return w
}

// WithPublicCacheInvalidator inyecta el invalidador de caché pública por slug para complejos vencidos.
func (w *ExpirationWorker) WithPublicCacheInvalidator(inv PublicCacheInvalidator) *ExpirationWorker {
	w.publicCache = inv
	return w
}

// WithPushDispatcher inyecta el despachador opcional Web Push para notificaciones emitidas por CU-14.
func (w *ExpirationWorker) WithPushDispatcher(dispatcher PushDispatcher) *ExpirationWorker {
	w.pushDispatcher = dispatcher
	return w
}

// Start inicia los bucles periódicos (30s holds + 5m mantenimiento CU-14) y finaliza limpiamente con ctx.
func (w *ExpirationWorker) Start(ctx context.Context) {
	if w.expirer == nil && w.maintenance == nil {
		return
	}

	w.logger.Info("reservas_expiration_worker_started",
		slog.Duration("hold_interval", w.interval),
		slog.Duration("maintenance_interval", w.maintenanceInterval),
		slog.Bool("maintenance_enabled", w.maintenance != nil),
	)

	// Barrido inicial tras el arranque para drenar eventos ocurridos durante un reinicio.
	if w.expirer != nil {
		w.runTick(ctx)
	}
	if w.maintenance != nil {
		w.runMaintenanceTick(ctx)
	}

	var holdCh <-chan time.Time
	if w.expirer != nil {
		holdTicker := time.NewTicker(w.interval)
		defer holdTicker.Stop()
		holdCh = holdTicker.C
	}

	var maintCh <-chan time.Time
	if w.maintenance != nil {
		maintTicker := time.NewTicker(w.maintenanceInterval)
		defer maintTicker.Stop()
		maintCh = maintTicker.C
	}

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("reservas_expiration_worker_stopped")
			return
		case <-holdCh:
			w.runTick(ctx)
		case <-maintCh:
			w.runMaintenanceTick(ctx)
		}
	}
}

// RunOnce drena en lotes acotados todos los Holds PENDIENTE_PAGO vencidos hasta el momento.
func (w *ExpirationWorker) RunOnce(ctx context.Context) (int, error) {
	if w.expirer == nil {
		return 0, nil
	}

	totalExpired := 0
	for {
		if err := ctx.Err(); err != nil {
			return totalExpired, err
		}

		expired, err := w.expirer.ExpireStaleHolds(ctx, w.batchSize)
		if err != nil {
			return totalExpired, err
		}

		totalExpired += expired
		if expired < w.batchSize {
			break
		}
	}

	return totalExpired, nil
}

// RunMaintenanceOnce ejecuta directamente un ciclo completo de mantenimiento CU-14
// (invocable de forma determinística en pruebas unitarias e integración):
//  1. Vence suscripciones vencidas -> VENCIDA + suscripciones_historial + invalidación Valkey + notificación al dueño (sin tocar reservas confirmadas, RN-02).
//  2. Emite aviso idempotente de vencimiento próximo (<= 3 días) al dueño.
//  3. Emite recordatorio idempotente de partido próximo (<= 2 horas) al jugador.
func (w *ExpirationWorker) RunMaintenanceOnce(ctx context.Context) (MaintenanceResult, error) {
	res := MaintenanceResult{
		InvalidatedSlugs: []string{},
	}
	if w.maintenance == nil {
		return res, nil
	}

	var errs []error

	// 1. Suscripciones vencidas
	for {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			return res, errors.Join(errs...)
		}
		expired, err := w.maintenance.ExpireOverdueSubscriptions(ctx, w.batchSize)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, item := range expired {
			res.SuscripcionesVencidas++
			if slug := strings.TrimSpace(item.Slug); slug != "" {
				w.invalidatePublicSlug(ctx, slug)
				res.InvalidatedSlugs = append(res.InvalidatedSlugs, slug)
			}
			w.dispatchPush(ctx, item.Push)
		}
		if len(expired) < w.batchSize {
			break
		}
	}

	// 2. Aviso de vencimiento próximo (<= 3 días) con idempotencia
	for {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			return res, errors.Join(errs...)
		}
		notifs, err := w.maintenance.NotifyExpiringSubscriptions(ctx, ExpiringSubscriptionNoticeWindow, w.batchSize)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, msg := range notifs {
			res.AvisosVencimiento++
			w.dispatchPush(ctx, msg)
		}
		if len(notifs) < w.batchSize {
			break
		}
	}

	// 3. Recordatorio previo de partido (<= 2 horas) con idempotencia
	for {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			return res, errors.Join(errs...)
		}
		reminders, err := w.maintenance.NotifyUpcomingMatchReminders(ctx, UpcomingMatchReminderWindow, w.batchSize)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, msg := range reminders {
			res.RecordatoriosPartido++
			w.dispatchPush(ctx, msg)
		}
		if len(reminders) < w.batchSize {
			break
		}
	}

	return res, errors.Join(errs...)
}

func (w *ExpirationWorker) runTick(parentCtx context.Context) {
	if parentCtx.Err() != nil {
		return
	}

	tickCtx, cancel := context.WithTimeout(parentCtx, 15*time.Second)
	defer cancel()

	expired, err := w.RunOnce(tickCtx)
	if err != nil {
		if parentCtx.Err() != nil {
			return
		}
		w.logger.Error("reservas_expiration_worker_error",
			slog.String("error", err.Error()),
		)
		return
	}

	if expired > 0 {
		w.logger.Info("reservas_holds_expired",
			slog.Int("count", expired),
		)
	}
}

func (w *ExpirationWorker) runMaintenanceTick(parentCtx context.Context) {
	if parentCtx.Err() != nil {
		return
	}

	tickCtx, cancel := context.WithTimeout(parentCtx, 30*time.Second)
	defer cancel()

	res, err := w.RunMaintenanceOnce(tickCtx)
	if err != nil {
		if parentCtx.Err() != nil {
			return
		}
		w.logger.Error("cu14_maintenance_worker_error",
			slog.String("error", err.Error()),
		)
	}

	if res.SuscripcionesVencidas > 0 || res.AvisosVencimiento > 0 || res.RecordatoriosPartido > 0 {
		w.logger.Info("cu14_maintenance_tick_completed",
			slog.Int("suscripciones_vencidas", res.SuscripcionesVencidas),
			slog.Int("avisos_vencimiento", res.AvisosVencimiento),
			slog.Int("recordatorios_partido", res.RecordatoriosPartido),
		)
	}
}

func (w *ExpirationWorker) invalidatePublicSlug(ctx context.Context, slug string) {
	if w.publicCache == nil || strings.TrimSpace(slug) == "" {
		return
	}
	invCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := w.publicCache.InvalidatePublicComplejo(invCtx, slug); err != nil {
		w.logger.Warn("cu14_public_cache_invalidate_error",
			slog.String("slug", slug),
			slog.String("error", err.Error()),
		)
	}
}

func (w *ExpirationWorker) dispatchPush(ctx context.Context, msg PushMessage) {
	if w.pushDispatcher != nil && w.pushDispatcher.Enabled() {
		w.pushDispatcher.DispatchAsync(ctx, msg)
	}
}

// ---------- Repositorio PostgreSQL: Automatizaciones CU-14 ----------

type overdueComplejoRow struct {
	ID           uuid.UUID
	DuenoID      uuid.UUID
	Slug         string
	Nombre       string
	PlanActualID *uuid.UUID
	VenceAt      time.Time
}

// ExpireOverdueSubscriptions actualiza a VENCIDA complejos con suscripción vencida sin afectar reservas confirmadas (RN-02),
// registra evento en suscripciones_historial y crea notificación in-app al dueño.
func (r *PostgresRepository) ExpireOverdueSubscriptions(ctx context.Context, batchSize int) ([]ExpiredSubscriptionResult, error) {
	if batchSize <= 0 {
		batchSize = DefaultWorkerBatchSize
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("error iniciando transacción de vencimiento de suscripciones: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	const selectSQL = `
		SELECT
			c.id,
			c.dueno_id,
			c.slug,
			c.nombre,
			c.plan_actual_id,
			c.suscripcion_vence_at
		FROM complejos c
		WHERE c.estado_suscripcion IN ('PRUEBA', 'ACTIVA')
		  AND c.suscripcion_vence_at IS NOT NULL
		  AND c.suscripcion_vence_at <= NOW()
		ORDER BY c.suscripcion_vence_at ASC
		LIMIT $1
		FOR UPDATE SKIP LOCKED`

	rows, err := tx.Query(ctx, selectSQL, batchSize)
	if err != nil {
		return nil, fmt.Errorf("error consultando suscripciones vencidas: %w", err)
	}

	targets := make([]overdueComplejoRow, 0)
	for rows.Next() {
		var row overdueComplejoRow
		if scanErr := rows.Scan(
			&row.ID,
			&row.DuenoID,
			&row.Slug,
			&row.Nombre,
			&row.PlanActualID,
			&row.VenceAt,
		); scanErr != nil {
			rows.Close()
			return nil, fmt.Errorf("error escaneando complejo con suscripción vencida: %w", scanErr)
		}
		targets = append(targets, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("error iterando complejos con suscripción vencida: %w", err)
	}
	rows.Close()

	if len(targets) == 0 {
		return nil, nil
	}

	var fallbackAdminID *uuid.UUID
	var adminUUID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM usuarios WHERE rol = 'SUPER_ADMIN' ORDER BY created_at ASC LIMIT 1`).Scan(&adminUUID); err == nil {
		fallbackAdminID = &adminUUID
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("error consultando actor SUPER_ADMIN para historial: %w", err)
	}

	results := make([]ExpiredSubscriptionResult, 0, len(targets))

	for _, c := range targets {
		// RN-02: Solo actualiza complejos.estado_suscripcion; jamás toca reservas confirmadas.
		if _, err := tx.Exec(ctx,
			`UPDATE complejos SET estado_suscripcion = 'VENCIDA' WHERE id = $1`,
			c.ID,
		); err != nil {
			return nil, fmt.Errorf("error actualizando estado_suscripcion a VENCIDA: %w", err)
		}

		// Recuperar contexto del último evento comercial para poblar suscripciones_historial respetando todos los constraints.
		var (
			prevPlanID        *uuid.UUID
			prevPlanNombre    *string
			prevPeriodicidad  *string
			prevInicioAt      *time.Time
			prevRegistradoPor *uuid.UUID
		)
		err := tx.QueryRow(ctx, `
			SELECT plan_id, plan_nombre, periodicidad, inicio_at, registrado_por
			FROM suscripciones_historial
			WHERE complejo_id = $1
			ORDER BY created_at DESC
			LIMIT 1`,
			c.ID,
		).Scan(&prevPlanID, &prevPlanNombre, &prevPeriodicidad, &prevInicioAt, &prevRegistradoPor)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("error consultando historial previo de suscripción: %w", err)
		}

		planID := c.PlanActualID
		if planID == nil {
			planID = prevPlanID
		}

		if planID != nil {
			var planNombre, periodicidad string
			if err := tx.QueryRow(ctx,
				`SELECT nombre, periodicidad FROM planes_suscripcion WHERE id = $1`,
				*planID,
			).Scan(&planNombre, &periodicidad); err != nil {
				if prevPlanNombre != nil && prevPeriodicidad != nil {
					planNombre = *prevPlanNombre
					periodicidad = *prevPeriodicidad
				} else {
					return nil, fmt.Errorf("error consultando plan de suscripción %s: %w", planID.String(), err)
				}
			}

			finAt := c.VenceAt
			inicioAt := finAt.Add(-time.Second)
			if prevInicioAt != nil && prevInicioAt.Before(finAt) {
				inicioAt = *prevInicioAt
			}
			if !inicioAt.Before(finAt) {
				inicioAt = finAt.Add(-time.Second)
			}

			registradoPor := c.DuenoID
			if prevRegistradoPor != nil {
				registradoPor = *prevRegistradoPor
			} else if fallbackAdminID != nil {
				registradoPor = *fallbackAdminID
			}

			const insertHistSQL = `
				INSERT INTO suscripciones_historial (
					complejo_id, plan_id, plan_nombre, periodicidad, monto_cobrado,
					estado, inicio_at, fin_at, registrado_por, observaciones
				)
				VALUES ($1, $2, $3, $4, 0.00, 'VENCIDA', $5, $6, $7, $8)`
			if _, err := tx.Exec(ctx, insertHistSQL,
				c.ID,
				*planID,
				planNombre,
				periodicidad,
				inicioAt,
				finAt,
				registradoPor,
				"Suscripción vencida automáticamente por el sistema (CU-14)",
			); err != nil {
				return nil, fmt.Errorf("error insertando historial de vencimiento de suscripción: %w", err)
			}
		}

		complejoIDCopy := c.ID
		venceLima := c.VenceAt.In(limaLocation)
		titulo := "Tu suscripción ha vencido"
		mensaje := fmt.Sprintf(
			"La suscripción de %s venció el %s. Tu complejo dejó de mostrarse en el catálogo público hasta renovar el plan (las reservas confirmadas siguen vigentes).",
			c.Nombre,
			venceLima.Format("02/01/2006 15:04"),
		)
		if err := insertNotificacionTx(ctx, tx, c.DuenoID, &complejoIDCopy, nil, NotifSuscripcionVencida, titulo, mensaje); err != nil {
			return nil, err
		}

		results = append(results, ExpiredSubscriptionResult{
			ComplejoID: c.ID,
			Slug:       c.Slug,
			Push: PushMessage{
				UsuarioID:  c.DuenoID,
				ComplejoID: &complejoIDCopy,
				Tipo:       NotifSuscripcionVencida,
				Titulo:     titulo,
				Mensaje:    mensaje,
			},
		})
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("error confirmando vencimiento de suscripciones: %w", err)
	}
	return results, nil
}

type expiringComplejoRow struct {
	ID      uuid.UUID
	DuenoID uuid.UUID
	Nombre  string
	VenceAt time.Time
}

// NotifyExpiringSubscriptions crea notificaciones in-app idempotentes para complejos activos/prueba
// cuya suscripción vence dentro de la ventana indicada (<= 3 días), sin duplicar alertas del mismo vencimiento.
func (r *PostgresRepository) NotifyExpiringSubscriptions(ctx context.Context, within time.Duration, batchSize int) ([]PushMessage, error) {
	if within <= 0 {
		within = ExpiringSubscriptionNoticeWindow
	}
	if batchSize <= 0 {
		batchSize = DefaultWorkerBatchSize
	}

	windowSeconds := int64(within.Seconds())

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("error iniciando transacción de avisos de vencimiento: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	const selectSQL = `
		SELECT
			c.id,
			c.dueno_id,
			c.nombre,
			c.suscripcion_vence_at
		FROM complejos c
		WHERE c.estado_suscripcion IN ('PRUEBA', 'ACTIVA')
		  AND c.suscripcion_vence_at IS NOT NULL
		  AND c.suscripcion_vence_at > NOW()
		  AND c.suscripcion_vence_at <= NOW() + ($1::bigint * INTERVAL '1 second')
		  AND NOT EXISTS (
			  SELECT 1
			  FROM notificaciones n
			  WHERE n.usuario_id = c.dueno_id
			    AND n.complejo_id = c.id
			    AND n.tipo = 'SUSCRIPCION_POR_VENCER'
			    AND n.created_at >= c.suscripcion_vence_at - (($1::bigint + 300) * INTERVAL '1 second')
			    AND n.created_at <= c.suscripcion_vence_at
		  )
		ORDER BY c.suscripcion_vence_at ASC
		LIMIT $2
		FOR UPDATE SKIP LOCKED`

	rows, err := tx.Query(ctx, selectSQL, windowSeconds, batchSize)
	if err != nil {
		return nil, fmt.Errorf("error consultando complejos por vencer: %w", err)
	}

	targets := make([]expiringComplejoRow, 0)
	for rows.Next() {
		var row expiringComplejoRow
		if scanErr := rows.Scan(&row.ID, &row.DuenoID, &row.Nombre, &row.VenceAt); scanErr != nil {
			rows.Close()
			return nil, fmt.Errorf("error escaneando complejo por vencer: %w", scanErr)
		}
		targets = append(targets, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("error iterando complejos por vencer: %w", err)
	}
	rows.Close()

	if len(targets) == 0 {
		return nil, nil
	}

	out := make([]PushMessage, 0, len(targets))
	for _, c := range targets {
		complejoIDCopy := c.ID
		venceLima := c.VenceAt.In(limaLocation)
		titulo := "Tu suscripción vence pronto"
		mensaje := fmt.Sprintf(
			"La suscripción de %s vence el %s. Renueva a tiempo para mantener tu complejo visible en el catálogo público.",
			c.Nombre,
			venceLima.Format("02/01/2006 15:04"),
		)
		if err := insertNotificacionTx(ctx, tx, c.DuenoID, &complejoIDCopy, nil, NotifSuscripcionPorVencer, titulo, mensaje); err != nil {
			return nil, err
		}
		out = append(out, PushMessage{
			UsuarioID:  c.DuenoID,
			ComplejoID: &complejoIDCopy,
			Tipo:       NotifSuscripcionPorVencer,
			Titulo:     titulo,
			Mensaje:    mensaje,
		})
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("error confirmando avisos de vencimiento: %w", err)
	}
	return out, nil
}

type upcomingMatchRow struct {
	ReservaID      uuid.UUID
	ComplejoID     uuid.UUID
	JugadorID      uuid.UUID
	Inicio         time.Time
	ComplejoNombre string
	CanchaNombre   string
}

// NotifyUpcomingMatchReminders crea recordatorios in-app idempotentes al jugador_id para reservas APP
// confirmadas que inician en las próximas 2 horas, sin repetir por la misma reserva.
func (r *PostgresRepository) NotifyUpcomingMatchReminders(ctx context.Context, within time.Duration, batchSize int) ([]PushMessage, error) {
	if within <= 0 {
		within = UpcomingMatchReminderWindow
	}
	if batchSize <= 0 {
		batchSize = DefaultWorkerBatchSize
	}

	windowSeconds := int64(within.Seconds())

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("error iniciando transacción de recordatorios de partido: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	const selectSQL = `
		SELECT
			r.id,
			r.complejo_id,
			r.jugador_id,
			lower(r.rango_horario) AS inicio,
			c.nombre AS complejo_nombre,
			ca.nombre AS cancha_nombre
		FROM reservas r
		INNER JOIN complejos c ON c.id = r.complejo_id
		INNER JOIN canchas ca ON ca.id = r.cancha_id
		WHERE r.origen = 'APP'
		  AND r.estado = 'CONFIRMADA'
		  AND r.jugador_id IS NOT NULL
		  AND lower(r.rango_horario) > NOW()
		  AND lower(r.rango_horario) <= NOW() + ($1::bigint * INTERVAL '1 second')
		  AND NOT EXISTS (
			  SELECT 1
			  FROM notificaciones n
			  WHERE n.reserva_id = r.id
			    AND n.tipo = 'RECORDATORIO_PARTIDO'
		  )
		ORDER BY lower(r.rango_horario) ASC
		LIMIT $2
		FOR UPDATE OF r SKIP LOCKED`

	rows, err := tx.Query(ctx, selectSQL, windowSeconds, batchSize)
	if err != nil {
		return nil, fmt.Errorf("error consultando reservas próximas para recordatorio: %w", err)
	}

	targets := make([]upcomingMatchRow, 0)
	for rows.Next() {
		var row upcomingMatchRow
		if scanErr := rows.Scan(
			&row.ReservaID,
			&row.ComplejoID,
			&row.JugadorID,
			&row.Inicio,
			&row.ComplejoNombre,
			&row.CanchaNombre,
		); scanErr != nil {
			rows.Close()
			return nil, fmt.Errorf("error escaneando reserva próxima: %w", scanErr)
		}
		targets = append(targets, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("error iterando reservas próximas: %w", err)
	}
	rows.Close()

	if len(targets) == 0 {
		return nil, nil
	}

	out := make([]PushMessage, 0, len(targets))
	for _, m := range targets {
		complejoIDCopy := m.ComplejoID
		reservaIDCopy := m.ReservaID
		inicioLima := m.Inicio.In(limaLocation)
		titulo := "Tu partido comienza pronto"
		mensaje := fmt.Sprintf(
			"Recuerda tu reserva en %s (%s) a las %s. Ten listo tu código QR al llegar.",
			m.ComplejoNombre,
			m.CanchaNombre,
			inicioLima.Format("15:04"),
		)
		if err := insertNotificacionTx(ctx, tx, m.JugadorID, &complejoIDCopy, &reservaIDCopy, NotifRecordatorioPartido, titulo, mensaje); err != nil {
			return nil, err
		}
		out = append(out, PushMessage{
			UsuarioID:  m.JugadorID,
			ComplejoID: &complejoIDCopy,
			ReservaID:  &reservaIDCopy,
			Tipo:       NotifRecordatorioPartido,
			Titulo:     titulo,
			Mensaje:    mensaje,
		})
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("error confirmando recordatorios de partido: %w", err)
	}
	return out, nil
}
