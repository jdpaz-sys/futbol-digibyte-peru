package db

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var (
	// ErrObjectNotFound indica que la clave solicitada no existe en el almacenamiento.
	ErrObjectNotFound = errors.New("archivo no encontrado en almacenamiento")
	// ErrInvalidStorageKey indica que la clave de archivo contiene caracteres o rutas no permitidas.
	ErrInvalidStorageKey = errors.New("clave de almacenamiento inválida")
)

// FileStorage define la interfaz desacoplada de almacenamiento privado de archivos
// (vouchers de pago, adjuntos), intercambiable entre disco local y Cloudflare R2 / S3.
type FileStorage interface {
	Save(ctx context.Context, key string, contentType string, r io.Reader) error
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
}

// LocalFileStorage implementa FileStorage sobre una carpeta local protegida contra path traversal.
type LocalFileStorage struct {
	baseDir string
}

// NewLocalFileStorage inicializa el almacenamiento local creando la carpeta raíz si no existe.
func NewLocalFileStorage(baseDir string) (*LocalFileStorage, error) {
	dir := strings.TrimSpace(baseDir)
	if dir == "" {
		dir = "./uploads"
	}

	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("error resolviendo UPLOAD_DIR: %w", err)
	}

	if err := os.MkdirAll(absDir, 0o750); err != nil {
		return nil, fmt.Errorf("error creando directorio de almacenamiento %s: %w", absDir, err)
	}

	return &LocalFileStorage{baseDir: absDir}, nil
}

// Save persiste un archivo de forma atómica (archivo temporal + rename) bajo la clave indicada.
func (s *LocalFileStorage) Save(ctx context.Context, key string, _ string, r io.Reader) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r == nil {
		return errors.New("lector de archivo nulo")
	}

	fullPath, err := s.resolvePath(key)
	if err != nil {
		return err
	}

	parentDir := filepath.Dir(fullPath)
	if err := os.MkdirAll(parentDir, 0o750); err != nil {
		return fmt.Errorf("error creando subdirectorio de almacenamiento: %w", err)
	}

	tmpFile, err := os.CreateTemp(parentDir, ".upload-*")
	if err != nil {
		return fmt.Errorf("error creando archivo temporal: %w", err)
	}
	tmpPath := tmpFile.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := io.Copy(tmpFile, r); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("error escribiendo contenido del archivo: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("error sincronizando archivo en disco: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("error cerrando archivo temporal: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o640); err != nil {
		return fmt.Errorf("error aplicando permisos al archivo: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, fullPath); err != nil {
		return fmt.Errorf("error confirmando archivo en almacenamiento: %w", err)
	}
	cleanupTmp = false
	return nil
}

// Open abre un archivo privado existente para lectura en streaming.
func (s *LocalFileStorage) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	fullPath, err := s.resolvePath(key)
	if err != nil {
		return nil, err
	}

	f, err := os.Open(fullPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrObjectNotFound
		}
		return nil, fmt.Errorf("error abriendo archivo del almacenamiento: %w", err)
	}

	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("error inspeccionando archivo del almacenamiento: %w", err)
	}
	if info.IsDir() {
		_ = f.Close()
		return nil, ErrInvalidStorageKey
	}

	return f, nil
}

// Delete elimina de forma idempotente un archivo almacenado.
func (s *LocalFileStorage) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	fullPath, err := s.resolvePath(key)
	if err != nil {
		return err
	}

	if err := os.Remove(fullPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("error eliminando archivo del almacenamiento: %w", err)
	}
	return nil
}

func (s *LocalFileStorage) resolvePath(key string) (string, error) {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" || strings.HasPrefix(trimmed, "/") || strings.ContainsAny(trimmed, "\\\x00") {
		return "", ErrInvalidStorageKey
	}

	for _, seg := range strings.Split(trimmed, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", ErrInvalidStorageKey
		}
	}

	cleaned := strings.TrimPrefix(path.Clean("/"+trimmed), "/")
	if cleaned == "" || cleaned == "." || strings.HasPrefix(cleaned, "..") {
		return "", ErrInvalidStorageKey
	}

	fullPath := filepath.Join(s.baseDir, filepath.FromSlash(cleaned))
	rel, err := filepath.Rel(s.baseDir, fullPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrInvalidStorageKey
	}

	return fullPath, nil
}
