package db

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

var (
	ErrInvalidEncryptionKey = errors.New("la clave de cifrado debe tener exactamente 32 bytes (AES-256)")
	ErrEmptyPlaintext       = errors.New("el contenido a cifrar no puede estar vacío")
	ErrInvalidCiphertext    = errors.New("los datos cifrados están truncados o corruptos")
	ErrDecryptFailed        = errors.New("no se pudieron descifrar ni autenticar las credenciales")
)

// CredentialsCipher cifra y descifra credenciales sensibles de pasarelas de pago
// usando AES-256-GCM con nonce aleatorio de 96 bits antepuesto al texto cifrado.
type CredentialsCipher struct {
	aead cipher.AEAD
}

// NewCredentialsCipher construye un CredentialsCipher validando que la clave sea de 32 bytes (AES-256).
func NewCredentialsCipher(key []byte) (*CredentialsCipher, error) {
	if len(key) != credentialsEncryptionKeyBytes {
		return nil, ErrInvalidEncryptionKey
	}

	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)

	block, err := aes.NewCipher(keyCopy)
	if err != nil {
		return nil, fmt.Errorf("error inicializando bloque AES-256: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("error inicializando modo GCM: %w", err)
	}

	return &CredentialsCipher{aead: aead}, nil
}

// Encrypt cifra plaintext con AES-256-GCM generando un nonce criptográficamente seguro.
// El resultado tiene formato [nonce || ciphertext || tag] apto para BYTEA en PostgreSQL.
func (c *CredentialsCipher) Encrypt(plaintext []byte) ([]byte, error) {
	if c == nil || c.aead == nil {
		return nil, ErrInvalidEncryptionKey
	}
	if len(plaintext) == 0 {
		return nil, ErrEmptyPlaintext
	}

	nonceSize := c.aead.NonceSize()
	out := make([]byte, nonceSize, nonceSize+len(plaintext)+c.aead.Overhead())
	if _, err := io.ReadFull(rand.Reader, out); err != nil {
		return nil, fmt.Errorf("error generando nonce AES-GCM: %w", err)
	}

	return c.aead.Seal(out, out, plaintext, nil), nil
}

// Decrypt verifica la etiqueta GCM y descifra un bloque producido por Encrypt.
func (c *CredentialsCipher) Decrypt(ciphertext []byte) ([]byte, error) {
	if c == nil || c.aead == nil {
		return nil, ErrInvalidEncryptionKey
	}

	nonceSize := c.aead.NonceSize()
	minLen := nonceSize + c.aead.Overhead()
	if len(ciphertext) < minLen {
		return nil, ErrInvalidCiphertext
	}

	nonce := ciphertext[:nonceSize]
	sealed := ciphertext[nonceSize:]

	plaintext, err := c.aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecryptFailed, err)
	}
	return plaintext, nil
}
