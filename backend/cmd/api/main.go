package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/db"
	apphttp "github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/http"
	"github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/modules/auth"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server_startup_failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run() error {
	cfg, err := db.LoadConfig()
	if err != nil {
		return err
	}

	logger := newLogger(cfg.AppEnv)
	slog.SetDefault(logger)

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pgPool, err := db.NewPostgresPool(rootCtx, cfg)
	if err != nil {
		return err
	}
	defer pgPool.Close()

	valkeyClient, err := db.NewValkeyClient(rootCtx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := valkeyClient.Close(); closeErr != nil {
			logger.Warn("valkey_close_error", slog.String("error", closeErr.Error()))
		}
	}()

	rateLimiter := apphttp.NewValkeyRateLimiter(valkeyClient)
	healthHandler := apphttp.NewHealthHandler(pgPool, valkeyClient, logger)

	authRepo := auth.NewRepository(pgPool)
	authSessions := auth.NewValkeySessionStore(valkeyClient)
	authService := auth.NewService(authRepo, authSessions, cfg.JWTSecret, cfg.JWTAccessTTL, cfg.JWTRefreshTTL)
	authHandler := auth.NewHandler(authService, rateLimiter, logger)

	router := apphttp.NewRouter(apphttp.RouterConfig{
		CORSAllowedOrigins: cfg.CORSAllowedOrigins,
		Logger:             logger,
		HealthHandler:      healthHandler,
		MountV1: func(v1 chi.Router) {
			authHandler.RegisterRoutes(v1)
		},
	})

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("api_server_started",
			slog.String("env", cfg.AppEnv),
			slog.String("addr", server.Addr),
		)
		if serveErr := server.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
		}
		close(errCh)
	}()

	select {
	case serveErr := <-errCh:
		return serveErr
	case <-rootCtx.Done():
		logger.Info("api_server_shutting_down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return err
	}

	logger.Info("api_server_stopped")
	return nil
}

func newLogger(appEnv string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if appEnv == "production" {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}
