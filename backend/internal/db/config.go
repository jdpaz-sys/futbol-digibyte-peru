package db

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const (
	minJWTSecretLength                = 32
	credentialsEncryptionKeyBytes     = 32
	credentialsEncryptionKeyHexLength = credentialsEncryptionKeyBytes * 2
)

// Config centraliza la configuración validada del backend Go.
type Config struct {
	AppEnv                   string
	Port                     string
	DatabaseURL              string
	DBMaxConns               int32
	DBMinConns               int32
	ValkeyAddr               string
	ValkeyPassword           string
	ValkeyDB                 int
	JWTSecret                string
	JWTAccessTTL             time.Duration
	JWTRefreshTTL            time.Duration
	CredentialsEncryptionKey []byte
	CORSAllowedOrigins       []string
	GeocoderBaseURL          string
	GeocoderUserAgent        string
	UploadDir                string
	VAPIDPublicKey           string
	VAPIDPrivateKey          string
	VAPIDSubject             string
	PushEnabled              bool
}

// LoadConfig carga opcionalmente un archivo .env local y valida estrictamente
// todas las variables obligatorias antes de arrancar el servidor.
func LoadConfig() (Config, error) {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("error cargando archivo .env: %w", err)
	}

	var errs []string

	appEnv := getEnvOrDefault("APP_ENV", "development")

	port := getEnvOrDefault("PORT", "8080")
	if portNum, err := strconv.Atoi(port); err != nil || portNum < 1 || portNum > 65535 {
		errs = append(errs, "PORT debe ser un número entero entre 1 y 65535")
	}

	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		errs = append(errs, "DATABASE_URL es obligatoria")
	}

	valkeyAddr := getEnvOrDefault("VALKEY_ADDR", "127.0.0.1:6379")
	if valkeyAddr == "" {
		errs = append(errs, "VALKEY_ADDR no puede estar vacía")
	}

	valkeyPassword := os.Getenv("VALKEY_PASSWORD")
	if strings.TrimSpace(valkeyPassword) == "" {
		errs = append(errs, "VALKEY_PASSWORD es obligatoria")
	}

	valkeyDB, err := parseIntEnv("VALKEY_DB", 0)
	if err != nil || valkeyDB < 0 {
		errs = append(errs, "VALKEY_DB debe ser un entero mayor o igual a 0")
	}

	dbMaxConns, err := parseIntEnv("DB_MAX_CONNS", 20)
	if err != nil || dbMaxConns < 1 {
		errs = append(errs, "DB_MAX_CONNS debe ser un entero mayor o igual a 1")
	}

	dbMinConns, err := parseIntEnv("DB_MIN_CONNS", 2)
	if err != nil || dbMinConns < 0 {
		errs = append(errs, "DB_MIN_CONNS debe ser un entero mayor o igual a 0")
	}
	if dbMaxConns >= 1 && dbMinConns > dbMaxConns {
		errs = append(errs, "DB_MIN_CONNS no puede ser mayor que DB_MAX_CONNS")
	}

	jwtSecret := os.Getenv("JWT_SECRET")
	if strings.TrimSpace(jwtSecret) == "" {
		errs = append(errs, "JWT_SECRET es obligatoria")
	} else if len(jwtSecret) < minJWTSecretLength {
		errs = append(errs, fmt.Sprintf("JWT_SECRET debe tener al menos %d caracteres", minJWTSecretLength))
	}

	accessTTL, err := parseDurationEnv("JWT_ACCESS_TTL", 15*time.Minute)
	if err != nil || accessTTL <= 0 {
		errs = append(errs, "JWT_ACCESS_TTL debe ser una duración válida mayor a 0 (ej. 15m)")
	}

	refreshTTL, err := parseDurationEnv("JWT_REFRESH_TTL", 7*24*time.Hour)
	if err != nil || refreshTTL <= 0 {
		errs = append(errs, "JWT_REFRESH_TTL debe ser una duración válida mayor a 0 (ej. 168h)")
	}

	rawEncKey := strings.TrimSpace(os.Getenv("CREDENTIALS_ENCRYPTION_KEY"))
	var credentialsEncryptionKey []byte
	if rawEncKey == "" {
		errs = append(errs, "CREDENTIALS_ENCRYPTION_KEY es obligatoria")
	} else if len(rawEncKey) != credentialsEncryptionKeyHexLength {
		errs = append(errs, fmt.Sprintf("CREDENTIALS_ENCRYPTION_KEY debe tener exactamente %d caracteres hexadecimales (32 bytes)", credentialsEncryptionKeyHexLength))
	} else {
		decoded, decodeErr := hex.DecodeString(rawEncKey)
		if decodeErr != nil || len(decoded) != credentialsEncryptionKeyBytes {
			errs = append(errs, "CREDENTIALS_ENCRYPTION_KEY debe ser una cadena hexadecimal válida de 32 bytes (64 caracteres)")
		} else {
			credentialsEncryptionKey = decoded
		}
	}

	corsOrigins := parseCSVEnv("CORS_ALLOWED_ORIGINS", []string{"http://localhost:3000"})
	if len(corsOrigins) == 0 {
		errs = append(errs, "CORS_ALLOWED_ORIGINS debe incluir al menos un origen permitido")
	}

	geocoderBaseURL := strings.TrimRight(getEnvOrDefault("GEOCODER_BASE_URL", "https://nominatim.openstreetmap.org"), "/")
	if parsedGeoURL, urlErr := url.Parse(geocoderBaseURL); urlErr != nil || (parsedGeoURL.Scheme != "http" && parsedGeoURL.Scheme != "https") || parsedGeoURL.Host == "" {
		errs = append(errs, "GEOCODER_BASE_URL debe ser una URL HTTP/HTTPS válida")
	}

	geocoderUserAgent := getEnvOrDefault("GEOCODER_USER_AGENT", "FutbolDigibytePeru/1.0")
	uploadDir := getEnvOrDefault("UPLOAD_DIR", "./uploads")

	vapidPublicKey := strings.TrimSpace(os.Getenv("VAPID_PUBLIC_KEY"))
	vapidPrivateKey := strings.TrimSpace(os.Getenv("VAPID_PRIVATE_KEY"))
	vapidSubject := strings.TrimSpace(os.Getenv("VAPID_SUBJECT"))
	pushEnabled := vapidPublicKey != "" && vapidPrivateKey != "" && vapidSubject != ""
	if pushEnabled {
		if !strings.HasPrefix(vapidSubject, "mailto:") && !strings.HasPrefix(vapidSubject, "https://") {
			errs = append(errs, "VAPID_SUBJECT debe iniciar con mailto: o https:// cuando Web Push está habilitado")
		}
	}

	if len(errs) > 0 {
		return Config{}, fmt.Errorf("configuración inválida:\n - %s", strings.Join(errs, "\n - "))
	}

	return Config{
		AppEnv:                   appEnv,
		Port:                     port,
		DatabaseURL:              databaseURL,
		DBMaxConns:               int32(dbMaxConns),
		DBMinConns:               int32(dbMinConns),
		ValkeyAddr:               valkeyAddr,
		ValkeyPassword:           valkeyPassword,
		ValkeyDB:                 valkeyDB,
		JWTSecret:                jwtSecret,
		JWTAccessTTL:             accessTTL,
		JWTRefreshTTL:            refreshTTL,
		CredentialsEncryptionKey: credentialsEncryptionKey,
		CORSAllowedOrigins:       corsOrigins,
		GeocoderBaseURL:          geocoderBaseURL,
		GeocoderUserAgent:        geocoderUserAgent,
		UploadDir:                uploadDir,
		VAPIDPublicKey:           vapidPublicKey,
		VAPIDPrivateKey:          vapidPrivateKey,
		VAPIDSubject:             vapidSubject,
		PushEnabled:              pushEnabled,
	}, nil
}

func getEnvOrDefault(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func parseIntEnv(key string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	return strconv.Atoi(raw)
}

func parseDurationEnv(key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	return time.ParseDuration(raw)
}

func parseCSVEnv(key string, fallback []string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimRight(strings.TrimSpace(p), "/"); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
