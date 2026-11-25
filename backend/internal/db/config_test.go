package db

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

const testValidEncKeyHex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestLoadConfigRejectsMissingRequiredVars(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("VALKEY_PASSWORD", "")
	t.Setenv("JWT_SECRET", "")
	t.Setenv("CREDENTIALS_ENCRYPTION_KEY", "")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("LoadConfig debía fallar cuando faltan variables obligatorias")
	}

	msg := err.Error()
	for _, required := range []string{"DATABASE_URL", "VALKEY_PASSWORD", "JWT_SECRET", "CREDENTIALS_ENCRYPTION_KEY"} {
		if !strings.Contains(msg, required) {
			t.Errorf("el mensaje de error debería mencionar %s, obtenido: %s", required, msg)
		}
	}
}

func TestLoadConfigRejectsShortJWTSecret(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@127.0.0.1:5434/db?sslmode=disable")
	t.Setenv("VALKEY_PASSWORD", "test-valkey-pass")
	t.Setenv("JWT_SECRET", "corto")
	t.Setenv("CREDENTIALS_ENCRYPTION_KEY", testValidEncKeyHex)

	_, err := LoadConfig()
	if err == nil || !strings.Contains(err.Error(), "JWT_SECRET") {
		t.Fatalf("LoadConfig debía rechazar JWT_SECRET menor a 32 caracteres, obtenido: %v", err)
	}
}

func TestLoadConfigRejectsInvalidEncryptionKey(t *testing.T) {
	cases := []struct {
		name string
		key  string
	}{
		{name: "demasiado corta", key: "0123456789abcdef"},
		{name: "no hexadecimal de 64 chars", key: strings.Repeat("z", 64)},
		{name: "demasiado larga", key: testValidEncKeyHex + "aa"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://user:pass@127.0.0.1:5434/db?sslmode=disable")
			t.Setenv("VALKEY_PASSWORD", "test-valkey-pass")
			t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
			t.Setenv("CREDENTIALS_ENCRYPTION_KEY", tc.key)

			_, err := LoadConfig()
			if err == nil || !strings.Contains(err.Error(), "CREDENTIALS_ENCRYPTION_KEY") {
				t.Fatalf("LoadConfig debía rechazar CREDENTIALS_ENCRYPTION_KEY (%s), obtenido: %v", tc.name, err)
			}
		})
	}
}

func TestLoadConfigValid(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@127.0.0.1:5434/db?sslmode=disable")
	t.Setenv("VALKEY_PASSWORD", "test-valkey-pass")
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("CREDENTIALS_ENCRYPTION_KEY", testValidEncKeyHex)
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
	if len(cfg.CredentialsEncryptionKey) != 32 {
		t.Errorf("len(CredentialsEncryptionKey) = %d, esperado 32", len(cfg.CredentialsEncryptionKey))
	}
}

func TestCredentialsCipherAES256GCM(t *testing.T) {
	key, err := hex.DecodeString(testValidEncKeyHex)
	if err != nil {
		t.Fatalf("hex.DecodeString falló: %v", err)
	}

	cipherObj, err := NewCredentialsCipher(key)
	if err != nil {
		t.Fatalf("NewCredentialsCipher falló: %v", err)
	}

	plaintext := []byte(`{"secret_key":"sk_test_culqi_123456","webhook_secret":"whsec_abc"}`)

	encrypted1, err := cipherObj.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt #1 falló: %v", err)
	}
	encrypted2, err := cipherObj.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt #2 falló: %v", err)
	}

	if bytes.Contains(encrypted1, []byte("sk_test_culqi_123456")) {
		t.Fatal("el texto cifrado no debe contener el secreto en claro")
	}
	if bytes.Equal(encrypted1, encrypted2) {
		t.Fatal("dos cifrados del mismo texto deben usar nonces distintos")
	}

	decrypted, err := cipherObj.Decrypt(encrypted1)
	if err != nil {
		t.Fatalf("Decrypt falló: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("Decrypt = %q, esperado %q", decrypted, plaintext)
	}

	// Alteración de integridad (tampering) debe fallar autenticación GCM
	tampered := append([]byte(nil), encrypted1...)
	tampered[len(tampered)-1] ^= 0xFF
	if _, err := cipherObj.Decrypt(tampered); !errors.Is(err, ErrDecryptFailed) {
		t.Fatalf("Decrypt con datos alterados debía retornar ErrDecryptFailed, obtenido: %v", err)
	}

	// Clave distinta debe fallar descifrado
	otherKey := append([]byte(nil), key...)
	otherKey[0] ^= 0x01
	otherCipher, err := NewCredentialsCipher(otherKey)
	if err != nil {
		t.Fatalf("NewCredentialsCipher(otherKey) falló: %v", err)
	}
	if _, err := otherCipher.Decrypt(encrypted1); !errors.Is(err, ErrDecryptFailed) {
		t.Fatalf("Decrypt con clave distinta debía retornar ErrDecryptFailed, obtenido: %v", err)
	}

	// Entradas inválidas
	if _, err := NewCredentialsCipher([]byte("short")); !errors.Is(err, ErrInvalidEncryptionKey) {
		t.Fatalf("clave corta debía retornar ErrInvalidEncryptionKey, obtenido: %v", err)
	}
	if _, err := cipherObj.Encrypt(nil); !errors.Is(err, ErrEmptyPlaintext) {
		t.Fatalf("plaintext vacío debía retornar ErrEmptyPlaintext, obtenido: %v", err)
	}
	if _, err := cipherObj.Decrypt([]byte("corto")); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("ciphertext truncado debía retornar ErrInvalidCiphertext, obtenido: %v", err)
	}
}
