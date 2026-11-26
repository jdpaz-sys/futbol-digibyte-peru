package reservas

import (
	"context"
	"log/slog"
	"time"
)

// ExpirationWorker ejecuta periódicamente la expiración de bloqueos temporales (Holds)
// vencidos en estado PENDIENTE_PAGO, sin afectar jamás reservas en POR_VALIDAR (CU-14, RN-04, Decisión 3).
type ExpirationWorker struct {
	expirer   HoldExpirer
	interval  time.Duration
	batchSize int
	logger    *slog.Logger
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
		expirer:   expirer,
		interval:  interval,
		batchSize: DefaultWorkerBatchSize,
		logger:    logger,
	}
}

// Start inicia el bucle periódico y finaliza limpiamente cuando ctx es cancelado.
func (w *ExpirationWorker) Start(ctx context.Context) {
	if w.expirer == nil {
		return
	}

	w.logger.Info("reservas_expiration_worker_started",
		slog.Duration("interval", w.interval),
	)

	// Barrido inicial para liberar holds que expiraron durante un reinicio del servicio.
	w.runTick(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("reservas_expiration_worker_stopped")
			return
		case <-ticker.C:
			w.runTick(ctx)
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
