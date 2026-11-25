package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPostgresPool inicializa un pool pgx/v5 con timeouts explícitos y verifica
// conectividad real mediante Ping antes de devolver el pool.
func NewPostgresPool(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("error parseando DATABASE_URL: %w", err)
	}

	poolCfg.MaxConns = cfg.DBMaxConns
	poolCfg.MinConns = cfg.DBMinConns
	poolCfg.MaxConnLifetime = 30 * time.Minute
	poolCfg.MaxConnIdleTime = 5 * time.Minute
	poolCfg.HealthCheckPeriod = 30 * time.Second
	poolCfg.ConnConfig.ConnectTimeout = 5 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("error creando pool de PostgreSQL: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("error verificando conexión inicial a PostgreSQL: %w", err)
	}

	return pool, nil
}
