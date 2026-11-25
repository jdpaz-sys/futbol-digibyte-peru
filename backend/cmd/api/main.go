package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/db"
	apphttp "github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/http"
	"github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/modules/auth"
	"github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/modules/suscripciones"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server_startup_failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run() error {
	seedAdmin := flag.Bool("seed-admin", false, "Ejecuta el bootstrap idempotente del primer SUPER_ADMIN y finaliza")
	flag.Parse()

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

	if *seedAdmin {
		return bootstrapSuperAdmin(rootCtx, pgPool, logger)
	}

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

	suscripcionesRepo := suscripciones.NewRepository(pgPool)
	metricasCache := suscripciones.NewValkeyMetricsCache(valkeyClient)
	suscripcionesService := suscripciones.NewService(suscripcionesRepo, metricasCache)
	suscripcionesHandler := suscripciones.NewHandler(suscripcionesService, logger)

	router := apphttp.NewRouter(apphttp.RouterConfig{
		CORSAllowedOrigins: cfg.CORSAllowedOrigins,
		Logger:             logger,
		HealthHandler:      healthHandler,
		TokenParser:        authService.ParseAccessToken,
		MountV1: func(v1 chi.Router) {
			authHandler.RegisterRoutes(v1)
		},
		MountAdmin: func(admin chi.Router) {
			suscripcionesHandler.RegisterAdminRoutes(admin)
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

func bootstrapSuperAdmin(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	email := auth.NormalizeEmail(os.Getenv("BOOTSTRAP_ADMIN_EMAIL"))
	password := os.Getenv("BOOTSTRAP_ADMIN_PASSWORD")
	nombre := strings.TrimSpace(os.Getenv("BOOTSTRAP_ADMIN_NOMBRE"))

	if !auth.ValidateEmail(email) {
		return errors.New("BOOTSTRAP_ADMIN_EMAIL es obligatorio y debe tener formato válido")
	}
	if passErr := auth.ValidatePassword(password); passErr != "" {
		return fmt.Errorf("BOOTSTRAP_ADMIN_PASSWORD inválido: %s", passErr)
	}
	if n := utf8.RuneCountInString(nombre); n < 2 || n > 150 {
		return errors.New("BOOTSTRAP_ADMIN_NOMBRE debe tener entre 2 y 150 caracteres")
	}

	var existingID uuid.UUID
	err := pool.QueryRow(ctx, `SELECT id FROM usuarios WHERE LOWER(email) = $1`, email).Scan(&existingID)
	if err == nil {
		logger.Info("bootstrap_super_admin_already_exists",
			slog.String("id", existingID.String()),
			slog.String("email", email),
		)
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("error verificando existencia de SUPER_ADMIN: %w", err)
	}

	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("error hasheando contraseña de SUPER_ADMIN: %w", err)
	}

	var createdID uuid.UUID
	const insertSQL = `
		INSERT INTO usuarios (rol, email, password_hash, nombre_completo, activo, email_verificado_at)
		VALUES ('SUPER_ADMIN', $1, $2, $3, true, NOW())
		ON CONFLICT (LOWER(email)) DO NOTHING
		RETURNING id`

	err = pool.QueryRow(ctx, insertSQL, email, passwordHash, nombre).Scan(&createdID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			logger.Info("bootstrap_super_admin_already_exists", slog.String("email", email))
			return nil
		}
		return fmt.Errorf("error insertando SUPER_ADMIN: %w", err)
	}

	logger.Info("bootstrap_super_admin_created",
		slog.String("id", createdID.String()),
		slog.String("email", email),
	)
	return nil
}

func newLogger(appEnv string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if appEnv == "production" {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}
