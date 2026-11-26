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
	if cfg.PushEnabled {
		t.Errorf("PushEnabled debía ser false cuando faltan claves VAPID")
	}
}

func TestLoadConfigVAPIDAndCORSNormalization(t *testing.T) {
	t.Run("sin VAPID arranca normal con PushEnabled=false", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://user:pass@127.0.0.1:5434/db?sslmode=disable")
		t.Setenv("VALKEY_PASSWORD", "test-valkey-pass")
		t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
		t.Setenv("CREDENTIALS_ENCRYPTION_KEY", testValidEncKeyHex)
		t.Setenv("VAPID_PUBLIC_KEY", "")
		t.Setenv("VAPID_PRIVATE_KEY", "")
		t.Setenv("VAPID_SUBJECT", "")
		t.Setenv("CORS_ALLOWED_ORIGINS", "http://localhost:3000/, https://app.digibyte.pe/")

		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig falló: %v", err)
		}
		if cfg.PushEnabled {
			t.Fatal("esperado PushEnabled = false")
		}
		if len(cfg.CORSAllowedOrigins) != 2 || cfg.CORSAllowedOrigins[0] != "http://localhost:3000" || cfg.CORSAllowedOrigins[1] != "https://app.digibyte.pe" {
			t.Fatalf("CORSAllowedOrigins mal normalizado: %v", cfg.CORSAllowedOrigins)
		}
	})

	t.Run("VAPID parcial mantiene PushEnabled=false sin romper arranque", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://user:pass@127.0.0.1:5434/db?sslmode=disable")
		t.Setenv("VALKEY_PASSWORD", "test-valkey-pass")
		t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
		t.Setenv("CREDENTIALS_ENCRYPTION_KEY", testValidEncKeyHex)
		t.Setenv("VAPID_PUBLIC_KEY", "pub-only")
		t.Setenv("VAPID_PRIVATE_KEY", "")
		t.Setenv("VAPID_SUBJECT", "mailto:soporte@digibyte.pe")

		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig no debía fallar con VAPID parcial: %v", err)
		}
		if cfg.PushEnabled {
			t.Fatal("esperado PushEnabled = false con VAPID parcial")
		}
	})

	t.Run("VAPID completo válido activa PushEnabled=true", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://user:pass@127.0.0.1:5434/db?sslmode=disable")
		t.Setenv("VALKEY_PASSWORD", "test-valkey-pass")
		t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
		t.Setenv("CREDENTIALS_ENCRYPTION_KEY", testValidEncKeyHex)
		t.Setenv("VAPID_PUBLIC_KEY", "test-public-key")
		t.Setenv("VAPID_PRIVATE_KEY", "test-private-key")
		t.Setenv("VAPID_SUBJECT", "mailto:soporte@digibyte.pe")

		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig con VAPID válido falló: %v", err)
		}
		if !cfg.PushEnabled {
			t.Fatal("esperado PushEnabled = true")
		}
		if cfg.VAPIDPublicKey != "test-public-key" || cfg.VAPIDPrivateKey != "test-private-key" || cfg.VAPIDSubject != "mailto:soporte@digibyte.pe" {
			t.Fatalf("valores VAPID inesperados: %+v", cfg)
		}
	})

	t.Run("VAPID_SUBJECT inválido falla validación cuando las 3 variables están presentes", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://user:pass@127.0.0.1:5434/db?sslmode=disable")
		t.Setenv("VALKEY_PASSWORD", "test-valkey-pass")
		t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
		t.Setenv("CREDENTIALS_ENCRYPTION_KEY", testValidEncKeyHex)
		t.Setenv("VAPID_PUBLIC_KEY", "test-public-key")
		t.Setenv("VAPID_PRIVATE_KEY", "test-private-key")
		t.Setenv("VAPID_SUBJECT", "soporte@digibyte.pe")

		if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "VAPID_SUBJECT") {
			t.Fatalf("esperado error por VAPID_SUBJECT sin mailto:/https://, obtenido: %v", err)
		}
	})
}

func TestStorageKeyAndImageMIMEHelpers(t *testing.T) {
	if !IsPublicMediaKey("public/complejos/abc/fotos/1.jpg") {
		t.Fatal("clave pública válida rechazada")
	}
	for _, bad := range []string{
		"",
		"public/",
		"comprobantes/abc/1.jpg",
		"public/../comprobantes/1.jpg",
		"/public/complejos/1.jpg",
		"public\\complejos\\1.jpg",
		"public/complejos/a//b.jpg",
	} {
		if IsPublicMediaKey(bad) {
			t.Fatalf("clave no permitida aceptada como pública: %q", bad)
		}
	}

	key, ok := ExtractPublicStorageKeyFromURL("/api/v1/public/media/public/complejos/abc/canchas/1.png")
	if !ok || key != "public/complejos/abc/canchas/1.png" {
		t.Fatalf("ExtractPublicStorageKeyFromURL falló: ok=%v key=%q", ok, key)
	}
	if _, ok := ExtractPublicStorageKeyFromURL("https://cdn.externo.com/foto.png"); ok {
		t.Fatal("URL externa no debe reconocerse como storage interno")
	}
	if _, ok := ExtractPublicStorageKeyFromURL("/api/v1/public/media/comprobantes/secret.jpg"); ok {
		t.Fatal("URL hacia comprobantes privados no debe reconocerse como clave pública")
	}

	jpegBytes := append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, make([]byte, 16)...)
	if mime, ext, ok := DetectAllowedImageMIME(jpegBytes); !ok || mime != "image/jpeg" || ext != ".jpg" {
		t.Fatalf("JPEG no detectado: mime=%s ext=%s ok=%v", mime, ext, ok)
	}
	pngBytes := append([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, make([]byte, 16)...)
	if mime, ext, ok := DetectAllowedImageMIME(pngBytes); !ok || mime != "image/png" || ext != ".png" {
		t.Fatalf("PNG no detectado: mime=%s ext=%s ok=%v", mime, ext, ok)
	}
	webpBytes := []byte("RIFF\x24\x00\x00\x00WEBPVP8 \x10\x00\x00\x00")
	if mime, ext, ok := DetectAllowedImageMIME(webpBytes); !ok || mime != "image/webp" || ext != ".webp" {
		t.Fatalf("WEBP no detectado: mime=%s ext=%s ok=%v", mime, ext, ok)
	}
	if _, _, ok := DetectAllowedImageMIME([]byte("<html><script>alert(1)</script></html>")); ok {
		t.Fatal("HTML no debe ser aceptado como imagen")
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
