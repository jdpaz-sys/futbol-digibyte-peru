package db

import (
	"strings"
	"testing"
)

func TestLoadConfigRejectsMissingRequiredVars(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("VALKEY_PASSWORD", "")
	t.Setenv("JWT_SECRET", "")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("LoadConfig debía fallar cuando faltan variables obligatorias")
	}

	msg := err.Error()
	for _, required := range []string{"DATABASE_URL", "VALKEY_PASSWORD", "JWT_SECRET"} {
		if !strings.Contains(msg, required) {
			t.Errorf("el mensaje de error debería mencionar %s, obtenido: %s", required, msg)
		}
	}
}

func TestLoadConfigRejectsShortJWTSecret(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@127.0.0.1:5434/db?sslmode=disable")
	t.Setenv("VALKEY_PASSWORD", "test-valkey-pass")
	t.Setenv("JWT_SECRET", "corto")

	_, err := LoadConfig()
	if err == nil || !strings.Contains(err.Error(), "JWT_SECRET") {
		t.Fatalf("LoadConfig debía rechazar JWT_SECRET menor a 32 caracteres, obtenido: %v", err)
	}
}

func TestLoadConfigValid(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@127.0.0.1:5434/db?sslmode=disable")
	t.Setenv("VALKEY_PASSWORD", "test-valkey-pass")
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("PORT", "8081")
	t.Setenv("CORS_ALLOWED_ORIGINS", "http://localhost:3000, https://app.digibyte.pe")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig válido falló: %v", err)
	}
	if cfg.Port != "8081" {
		t.Errorf("Port = %q, esperado 8081", cfg.Port)
	}
	if len(cfg.CORSAllowedOrigins) != 2 {
		t.Errorf("CORSAllowedOrigins = %v, esperado 2 orígenes", cfg.CORSAllowedOrigins)
	}
}
