package db

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/maintnotifications"
)

// NewValkeyClient inicializa un cliente autenticado compatible con Valkey 8
// con timeouts explícitos y ping de verificación inicial.
func NewValkeyClient(ctx context.Context, cfg Config) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:            cfg.ValkeyAddr,
		Password:        cfg.ValkeyPassword,
		DB:              cfg.ValkeyDB,
		DialTimeout:     5 * time.Second,
		ReadTimeout:     3 * time.Second,
		WriteTimeout:    3 * time.Second,
		PoolSize:        20,
		MinIdleConns:    2,
		DisableIdentity: true,
		MaintNotificationsConfig: &maintnotifications.Config{
			Mode: maintnotifications.ModeDisabled,
		},
	})

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("error verificando conexión inicial a Valkey: %w", err)
	}

	return client, nil
}
