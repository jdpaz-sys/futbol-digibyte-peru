package apphttp

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// HealthResponse representa el estado real de conectividad del servicio y sus dependencias.
type HealthResponse struct {
	Status    string            `json:"status"`
	Service   string            `json:"service"`
	Checks    map[string]string `json:"checks"`
	Timestamp string            `json:"timestamp"`
}

// NewHealthHandler expone GET /api/v1/health verificando conectividad real a PostgreSQL y Valkey.
func NewHealthHandler(pgPool *pgxpool.Pool, valkey *redis.Client, logger *slog.Logger) http.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}

	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		checks := map[string]string{
			"postgres": "ok",
			"valkey":   "ok",
		}
		healthy := true

		if pgPool == nil {
			checks["postgres"] = "error"
			healthy = false
		} else if err := pgPool.Ping(ctx); err != nil {
			logger.Error("healthcheck_postgres_failed", slog.String("error", err.Error()))
			checks["postgres"] = "error"
			healthy = false
		}

		if valkey == nil {
			checks["valkey"] = "error"
			healthy = false
		} else if err := valkey.Ping(ctx).Err(); err != nil {
			logger.Error("healthcheck_valkey_failed", slog.String("error", err.Error()))
			checks["valkey"] = "error"
			healthy = false
		}

		statusCode := http.StatusOK
		overall := "ok"
		if !healthy {
			statusCode = http.StatusServiceUnavailable
			overall = "degraded"
		}

		WriteRawJSON(w, statusCode, HealthResponse{
			Status:    overall,
			Service:   "futbol-digibyte-api",
			Checks:    checks,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}
}
