package complejos

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/db"
	apphttp "github.com/jdpaz-sys/futbol-digibyte-peru/backend/internal/http"
)

const (
	maxMultipartMediaBytes int64 = db.MaxImageBytes + (512 << 10) // 5 MiB imagen + 512 KiB campos/cabeceras
	multipartMediaMemLimit int64 = db.MaxImageBytes + (256 << 10)
	maxFotoDescRunes             = 150
	publicMediaCacheHeader       = "public, max-age=86400"
)

// ---------- Repositorio PostgreSQL: Multimedia ----------

func (r *PostgresRepository) ListFotos(ctx context.Context, complejoID uuid.UUID) ([]ComplejoFoto, error) {
	const query = `
		SELECT id, complejo_id, url, storage_key, descripcion, orden, es_portada, created_at
		FROM complejo_fotos
		WHERE complejo_id = $1
		ORDER BY orden ASC, created_at ASC`

	rows, err := r.pool.Query(ctx, query, complejoID)
	if err != nil {
		return nil, fmt.Errorf("error listando fotos del complejo: %w", err)
	}
	defer rows.Close()

	fotos := make([]ComplejoFoto, 0)
	for rows.Next() {
		var f ComplejoFoto
		if scanErr := rows.Scan(
			&f.ID,
			&f.ComplejoID,
			&f.URL,
			&f.StorageKey,
			&f.Descripcion,
			&f.Orden,
			&f.EsPortada,
			&f.CreatedAt,
		); scanErr != nil {
			return nil, fmt.Errorf("error escaneando foto del complejo: %w", scanErr)
		}
		fotos = append(fotos, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterando fotos del complejo: %w", err)
	}
	return fotos, nil
}

func (r *PostgresRepository) CreateFoto(ctx context.Context, foto ComplejoFoto) (ComplejoFoto, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ComplejoFoto{}, fmt.Errorf("error iniciando transacción de foto: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// Bloqueo del complejo padre para serializar cambios de es_portada sin violar uq_complejo_fotos_portada.
	var lockID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM complejos WHERE id = $1 FOR UPDATE`, foto.ComplejoID).Scan(&lockID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ComplejoFoto{}, ErrComplejoNotFound
		}
		return ComplejoFoto{}, fmt.Errorf("error bloqueando complejo para foto: %w", err)
	}

	if foto.EsPortada {
		if _, err := tx.Exec(ctx,
			`UPDATE complejo_fotos SET es_portada = false WHERE complejo_id = $1 AND es_portada = true`,
			foto.ComplejoID,
		); err != nil {
			return ComplejoFoto{}, mapPgError(err)
		}
	}

	const insertQuery = `
		INSERT INTO complejo_fotos (id, complejo_id, url, storage_key, descripcion, orden, es_portada)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, complejo_id, url, storage_key, descripcion, orden, es_portada, created_at`

	fotoID := foto.ID
	if fotoID == uuid.Nil {
		fotoID = uuid.New()
	}

	var out ComplejoFoto
	if err := tx.QueryRow(ctx, insertQuery,
		fotoID,
		foto.ComplejoID,
		foto.URL,
		foto.StorageKey,
		foto.Descripcion,
		foto.Orden,
		foto.EsPortada,
	).Scan(
		&out.ID,
		&out.ComplejoID,
		&out.URL,
		&out.StorageKey,
		&out.Descripcion,
		&out.Orden,
		&out.EsPortada,
		&out.CreatedAt,
	); err != nil {
		return ComplejoFoto{}, mapPgError(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return ComplejoFoto{}, fmt.Errorf("error confirmando creación de foto: %w", err)
	}
	return out, nil
}

func (r *PostgresRepository) UpdateFoto(
	ctx context.Context,
	complejoID, fotoID uuid.UUID,
	descripcion *string,
	updateDescripcion bool,
	orden *int16,
	esPortada *bool,
) (ComplejoFoto, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ComplejoFoto{}, fmt.Errorf("error iniciando transacción de actualización de foto: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var lockID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM complejos WHERE id = $1 FOR UPDATE`, complejoID).Scan(&lockID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ComplejoFoto{}, ErrComplejoNotFound
		}
		return ComplejoFoto{}, fmt.Errorf("error bloqueando complejo para foto: %w", err)
	}

	const selectSQL = `
		SELECT id, complejo_id, url, storage_key, descripcion, orden, es_portada, created_at
		FROM complejo_fotos
		WHERE id = $1 AND complejo_id = $2
		FOR UPDATE`

	var current ComplejoFoto
	if err := tx.QueryRow(ctx, selectSQL, fotoID, complejoID).Scan(
		&current.ID,
		&current.ComplejoID,
		&current.URL,
		&current.StorageKey,
		&current.Descripcion,
		&current.Orden,
		&current.EsPortada,
		&current.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ComplejoFoto{}, ErrFotoNotFound
		}
		return ComplejoFoto{}, fmt.Errorf("error consultando foto para actualizar: %w", err)
	}

	if updateDescripcion {
		current.Descripcion = descripcion
	}
	if orden != nil {
		current.Orden = *orden
	}
	if esPortada != nil {
		current.EsPortada = *esPortada
	}

	if esPortada != nil && *esPortada {
		if _, err := tx.Exec(ctx,
			`UPDATE complejo_fotos SET es_portada = false WHERE complejo_id = $1 AND es_portada = true AND id <> $2`,
			complejoID, fotoID,
		); err != nil {
			return ComplejoFoto{}, mapPgError(err)
		}
	}

	const updateSQL = `
		UPDATE complejo_fotos
		SET descripcion = $3,
		    orden = $4,
		    es_portada = $5
		WHERE id = $1 AND complejo_id = $2
		RETURNING id, complejo_id, url, storage_key, descripcion, orden, es_portada, created_at`

	var out ComplejoFoto
	if err := tx.QueryRow(ctx, updateSQL,
		fotoID,
		complejoID,
		current.Descripcion,
		current.Orden,
		current.EsPortada,
	).Scan(
		&out.ID,
		&out.ComplejoID,
		&out.URL,
		&out.StorageKey,
		&out.Descripcion,
		&out.Orden,
		&out.EsPortada,
		&out.CreatedAt,
	); err != nil {
		return ComplejoFoto{}, mapPgError(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return ComplejoFoto{}, fmt.Errorf("error confirmando actualización de foto: %w", err)
	}
	return out, nil
}

func (r *PostgresRepository) DeleteFoto(ctx context.Context, complejoID, fotoID uuid.UUID) (*string, error) {
	const query = `
		DELETE FROM complejo_fotos
		WHERE id = $1 AND complejo_id = $2
		RETURNING storage_key`

	var storageKey *string
	if err := r.pool.QueryRow(ctx, query, fotoID, complejoID).Scan(&storageKey); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrFotoNotFound
		}
		return nil, mapPgError(err)
	}
	return storageKey, nil
}

func (r *PostgresRepository) SetCanchaFoto(
	ctx context.Context,
	complejoID, canchaID uuid.UUID,
	fotoURL, fotoStorageKey *string,
) (Cancha, *string, *string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Cancha{}, nil, nil, fmt.Errorf("error iniciando transacción de foto de cancha: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var prevKey, prevURL *string
	if err := tx.QueryRow(ctx,
		`SELECT foto_storage_key, foto_url FROM canchas WHERE id = $1 AND complejo_id = $2 FOR UPDATE`,
		canchaID, complejoID,
	).Scan(&prevKey, &prevURL); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Cancha{}, nil, nil, ErrCanchaNotFound
		}
		return Cancha{}, nil, nil, fmt.Errorf("error consultando cancha para foto: %w", err)
	}

	const updateSQL = `
		UPDATE canchas
		SET foto_url = $3,
		    foto_storage_key = $4
		WHERE id = $1 AND complejo_id = $2
		RETURNING
			id, complejo_id, nombre, modalidad, superficie, techada, iluminacion,
			foto_url, foto_storage_key, orden, activa, created_at, updated_at`

	var out Cancha
	if err := tx.QueryRow(ctx, updateSQL, canchaID, complejoID, fotoURL, fotoStorageKey).Scan(
		&out.ID,
		&out.ComplejoID,
		&out.Nombre,
		&out.Modalidad,
		&out.Superficie,
		&out.Techada,
		&out.Iluminacion,
		&out.FotoURL,
		&out.FotoStorageKey,
		&out.Orden,
		&out.Activa,
		&out.CreatedAt,
		&out.UpdatedAt,
	); err != nil {
		return Cancha{}, nil, nil, mapPgError(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Cancha{}, nil, nil, fmt.Errorf("error confirmando foto de cancha: %w", err)
	}
	return out, prevKey, prevURL, nil
}

func (r *PostgresRepository) SetMetodoPagoQR(
	ctx context.Context,
	complejoID, metodoID uuid.UUID,
	qrURL, qrStorageKey *string,
	requireWalletType bool,
) (MetodoPago, *string, *string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return MetodoPago{}, nil, nil, fmt.Errorf("error iniciando transacción de QR de método de pago: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var tipo string
	var prevKey, prevURL *string
	if err := tx.QueryRow(ctx,
		`SELECT tipo, qr_storage_key, qr_url FROM metodos_pago_complejo WHERE id = $1 AND complejo_id = $2 FOR UPDATE`,
		metodoID, complejoID,
	).Scan(&tipo, &prevKey, &prevURL); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MetodoPago{}, nil, nil, ErrMetodoPagoNotFound
		}
		return MetodoPago{}, nil, nil, fmt.Errorf("error consultando método de pago para QR: %w", err)
	}

	if requireWalletType && tipo != MetodoPagoYape && tipo != MetodoPagoPlin {
		return MetodoPago{}, nil, nil, ErrMetodoPagoNoQR
	}

	updateSQL := `
		UPDATE metodos_pago_complejo
		SET qr_url = $3,
		    qr_storage_key = $4
		WHERE id = $1 AND complejo_id = $2
		RETURNING` + metodoPagoColumns

	out, err := scanMetodoPago(tx.QueryRow(ctx, updateSQL, metodoID, complejoID, qrURL, qrStorageKey))
	if err != nil {
		return MetodoPago{}, nil, nil, mapPgError(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return MetodoPago{}, nil, nil, fmt.Errorf("error confirmando QR de método de pago: %w", err)
	}
	return out, prevKey, prevURL, nil
}

// ---------- Lógica de Servicio: Multimedia y Lectura Pública Segura ----------

func (s *Service) ListFotos(ctx context.Context, complejoID uuid.UUID) ([]ComplejoFoto, error) {
	fotos, err := s.repo.ListFotos(ctx, complejoID)
	if err != nil {
		return nil, err
	}
	if fotos == nil {
		return []ComplejoFoto{}, nil
	}
	return fotos, nil
}

func (s *Service) UploadFoto(ctx context.Context, in UploadComplejoFotoInput) (ComplejoFoto, error) {
	if s.storage == nil {
		return ComplejoFoto{}, errors.New("almacenamiento de archivos no configurado")
	}

	fields := make(map[string]string)
	if len(in.FileData) == 0 {
		fields["archivo"] = "El archivo de imagen es obligatorio"
	}
	if in.Orden < 0 || in.Orden > maxShortint {
		fields["orden"] = "El orden debe ser un entero entre 0 y 32767"
	}
	desc := normalizeOptionalString(in.Descripcion)
	if desc != nil && utf8.RuneCountInString(*desc) > maxFotoDescRunes {
		fields["descripcion"] = fmt.Sprintf("La descripción no puede exceder los %d caracteres", maxFotoDescRunes)
	}
	if len(fields) > 0 {
		return ComplejoFoto{}, &ValidationError{
			Message: "Datos inválidos para subir foto del complejo",
			Fields:  fields,
		}
	}

	mimeType, ext, err := validateImageBytes(in.FileData)
	if err != nil {
		return ComplejoFoto{}, err
	}

	fotoID := uuid.New()
	storageKey := fmt.Sprintf("%scomplejos/%s/fotos/%s%s", db.PublicMediaPrefix, in.ComplejoID.String(), fotoID.String(), ext)
	publicURL := db.PublicMediaURLPrefix + storageKey

	if err := s.storage.Save(ctx, storageKey, mimeType, bytes.NewReader(in.FileData)); err != nil {
		return ComplejoFoto{}, fmt.Errorf("error guardando foto del complejo: %w", err)
	}

	keyCopy := storageKey
	created, err := s.repo.CreateFoto(ctx, ComplejoFoto{
		ID:          fotoID,
		ComplejoID:  in.ComplejoID,
		URL:         publicURL,
		StorageKey:  &keyCopy,
		Descripcion: desc,
		Orden:       int16(in.Orden),
		EsPortada:   in.EsPortada,
	})
	if err != nil {
		s.cleanupStorageKey(ctx, storageKey)
		return ComplejoFoto{}, err
	}

	s.invalidatePublicCacheByComplejoID(ctx, in.ComplejoID)
	return created, nil
}

func (s *Service) UpdateFoto(ctx context.Context, complejoID, fotoID uuid.UUID, req UpdateComplejoFotoRequest) (ComplejoFoto, error) {
	if req.Descripcion == nil && req.Orden == nil && req.EsPortada == nil {
		return ComplejoFoto{}, &ValidationError{
			Message: "Debe enviar al menos un campo para actualizar la foto",
			Fields:  map[string]string{"body": "Envía orden, es_portada o descripcion"},
		}
	}

	fields := make(map[string]string)

	var desc *string
	updateDesc := false
	if req.Descripcion != nil {
		updateDesc = true
		desc = normalizeOptionalString(req.Descripcion)
		if desc != nil && utf8.RuneCountInString(*desc) > maxFotoDescRunes {
			fields["descripcion"] = fmt.Sprintf("La descripción no puede exceder los %d caracteres", maxFotoDescRunes)
		}
	}

	var orden *int16
	if req.Orden != nil {
		if *req.Orden < 0 || *req.Orden > maxShortint {
			fields["orden"] = "El orden debe ser un entero entre 0 y 32767"
		} else {
			o := int16(*req.Orden)
			orden = &o
		}
	}

	if len(fields) > 0 {
		return ComplejoFoto{}, &ValidationError{
			Message: "Datos inválidos para actualizar la foto",
			Fields:  fields,
		}
	}

	updated, err := s.repo.UpdateFoto(ctx, complejoID, fotoID, desc, updateDesc, orden, req.EsPortada)
	if err != nil {
		return ComplejoFoto{}, err
	}

	s.invalidatePublicCacheByComplejoID(ctx, complejoID)
	return updated, nil
}

func (s *Service) DeleteFoto(ctx context.Context, complejoID, fotoID uuid.UUID) error {
	storageKey, err := s.repo.DeleteFoto(ctx, complejoID, fotoID)
	if err != nil {
		return err
	}

	if storageKey != nil && strings.TrimSpace(*storageKey) != "" {
		s.cleanupStorageKey(ctx, *storageKey)
	}

	s.invalidatePublicCacheByComplejoID(ctx, complejoID)
	return nil
}

func (s *Service) UploadCanchaFoto(ctx context.Context, complejoID, canchaID uuid.UUID, fileData []byte) (Cancha, error) {
	if s.storage == nil {
		return Cancha{}, errors.New("almacenamiento de archivos no configurado")
	}
	if len(fileData) == 0 {
		return Cancha{}, &ValidationError{
			Message: "Datos inválidos para subir foto de cancha",
			Fields:  map[string]string{"archivo": "El archivo de imagen es obligatorio"},
		}
	}

	mimeType, ext, err := validateImageBytes(fileData)
	if err != nil {
		return Cancha{}, err
	}

	if _, err := s.repo.GetCancha(ctx, complejoID, canchaID); err != nil {
		return Cancha{}, err
	}

	fileID := uuid.New()
	storageKey := fmt.Sprintf("%scomplejos/%s/canchas/%s/%s%s", db.PublicMediaPrefix, complejoID.String(), canchaID.String(), fileID.String(), ext)
	publicURL := db.PublicMediaURLPrefix + storageKey

	if err := s.storage.Save(ctx, storageKey, mimeType, bytes.NewReader(fileData)); err != nil {
		return Cancha{}, fmt.Errorf("error guardando foto de cancha: %w", err)
	}

	urlCopy := publicURL
	keyCopy := storageKey
	updated, prevKey, prevURL, err := s.repo.SetCanchaFoto(ctx, complejoID, canchaID, &urlCopy, &keyCopy)
	if err != nil {
		s.cleanupStorageKey(ctx, storageKey)
		return Cancha{}, err
	}

	if oldKey, ok := resolveOwnedStorageKey(prevKey, prevURL); ok && oldKey != storageKey {
		s.cleanupStorageKey(ctx, oldKey)
	}

	s.invalidatePublicCacheByComplejoID(ctx, complejoID)
	return updated, nil
}

func (s *Service) DeleteCanchaFoto(ctx context.Context, complejoID, canchaID uuid.UUID) (Cancha, error) {
	updated, prevKey, prevURL, err := s.repo.SetCanchaFoto(ctx, complejoID, canchaID, nil, nil)
	if err != nil {
		return Cancha{}, err
	}

	if oldKey, ok := resolveOwnedStorageKey(prevKey, prevURL); ok {
		s.cleanupStorageKey(ctx, oldKey)
	}

	s.invalidatePublicCacheByComplejoID(ctx, complejoID)
	return updated, nil
}

func (s *Service) UploadMetodoPagoQR(ctx context.Context, complejoID, metodoID uuid.UUID, fileData []byte) (MetodoPago, error) {
	if s.storage == nil {
		return MetodoPago{}, errors.New("almacenamiento de archivos no configurado")
	}
	if len(fileData) == 0 {
		return MetodoPago{}, &ValidationError{
			Message: "Datos inválidos para subir QR de método de pago",
			Fields:  map[string]string{"archivo": "El archivo de imagen del QR es obligatorio"},
		}
	}

	mimeType, ext, err := validateImageBytes(fileData)
	if err != nil {
		return MetodoPago{}, err
	}

	current, err := s.repo.GetMetodoPago(ctx, complejoID, metodoID)
	if err != nil {
		return MetodoPago{}, err
	}
	if current.Tipo != MetodoPagoYape && current.Tipo != MetodoPagoPlin {
		return MetodoPago{}, ErrMetodoPagoNoQR
	}

	fileID := uuid.New()
	storageKey := fmt.Sprintf("%scomplejos/%s/qr/%s/%s%s", db.PublicMediaPrefix, complejoID.String(), metodoID.String(), fileID.String(), ext)
	publicURL := db.PublicMediaURLPrefix + storageKey

	if err := s.storage.Save(ctx, storageKey, mimeType, bytes.NewReader(fileData)); err != nil {
		return MetodoPago{}, fmt.Errorf("error guardando QR del método de pago: %w", err)
	}

	urlCopy := publicURL
	keyCopy := storageKey
	updated, prevKey, prevURL, err := s.repo.SetMetodoPagoQR(ctx, complejoID, metodoID, &urlCopy, &keyCopy, true)
	if err != nil {
		s.cleanupStorageKey(ctx, storageKey)
		return MetodoPago{}, err
	}

	if oldKey, ok := resolveOwnedStorageKey(prevKey, prevURL); ok && oldKey != storageKey {
		s.cleanupStorageKey(ctx, oldKey)
	}

	s.invalidatePublicCacheByComplejoID(ctx, complejoID)
	return updated, nil
}

func (s *Service) DeleteMetodoPagoQR(ctx context.Context, complejoID, metodoID uuid.UUID) (MetodoPago, error) {
	updated, prevKey, prevURL, err := s.repo.SetMetodoPagoQR(ctx, complejoID, metodoID, nil, nil, false)
	if err != nil {
		return MetodoPago{}, err
	}

	if oldKey, ok := resolveOwnedStorageKey(prevKey, prevURL); ok {
		s.cleanupStorageKey(ctx, oldKey)
	}

	s.invalidatePublicCacheByComplejoID(ctx, complejoID)
	return updated, nil
}

// OpenPublicMedia abre exclusivamente archivos almacenados bajo el prefijo public/,
// bloqueando cualquier path traversal o intento de lectura de comprobantes privados.
func (s *Service) OpenPublicMedia(ctx context.Context, rawKey string) (PublicMediaStream, error) {
	if s.storage == nil {
		return PublicMediaStream{}, errors.New("almacenamiento de archivos no configurado")
	}

	cleanKey, err := db.NormalizeStorageKey(rawKey)
	if err != nil {
		return PublicMediaStream{}, ErrMediaForbidden
	}
	if !db.IsPublicMediaKey(cleanKey) {
		return PublicMediaStream{}, ErrMediaForbidden
	}

	rc, err := s.storage.Open(ctx, cleanKey)
	if err != nil {
		if errors.Is(err, db.ErrObjectNotFound) {
			return PublicMediaStream{}, ErrMediaNotFound
		}
		if errors.Is(err, db.ErrInvalidStorageKey) {
			return PublicMediaStream{}, ErrMediaForbidden
		}
		return PublicMediaStream{}, fmt.Errorf("error abriendo archivo multimedia público: %w", err)
	}

	head := make([]byte, 512)
	n, readErr := io.ReadFull(rc, head)
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		_ = rc.Close()
		return PublicMediaStream{}, fmt.Errorf("error leyendo cabecera de archivo multimedia: %w", readErr)
	}
	prefix := head[:n]

	mimeType, _, ok := db.DetectAllowedImageMIME(prefix)
	isInline := true
	if !ok {
		mimeType = "application/octet-stream"
		isInline = false
	}

	return PublicMediaStream{
		Reader:      rc,
		Prefix:      prefix,
		ContentType: mimeType,
		IsInline:    isInline,
	}, nil
}

func validateImageBytes(data []byte) (string, string, error) {
	if len(data) > db.MaxImageBytes {
		return "", "", ErrFileTooLarge
	}
	mimeType, ext, ok := db.DetectAllowedImageMIME(data)
	if !ok {
		return "", "", ErrInvalidFileType
	}
	return mimeType, ext, nil
}

func resolveOwnedStorageKey(storageKey, rawURL *string) (string, bool) {
	if storageKey != nil && strings.TrimSpace(*storageKey) != "" {
		if cleaned, err := db.NormalizeStorageKey(*storageKey); err == nil {
			return cleaned, true
		}
	}
	if rawURL != nil && strings.TrimSpace(*rawURL) != "" {
		if cleaned, ok := db.ExtractPublicStorageKeyFromURL(*rawURL); ok {
			return cleaned, true
		}
	}
	return "", false
}

func (s *Service) cleanupStorageKey(ctx context.Context, key string) {
	if s.storage == nil {
		return
	}
	cleaned, err := db.NormalizeStorageKey(key)
	if err != nil {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if delErr := s.storage.Delete(cleanupCtx, cleaned); delErr != nil && s.logger != nil {
		s.logger.Warn("complejos_storage_delete_error",
			slog.String("storage_key", cleaned),
			slog.String("error", delErr.Error()),
		)
	}
}

// ---------- Handlers HTTP: Multimedia Pública y del Dueño ----------

// RegisterPublicRoutes monta GET /api/v1/public/media/* bajo /api/v1/public.
func (h *Handler) RegisterPublicRoutes(public chi.Router) {
	public.Get("/media/*", h.handleGetPublicMedia)
}

func (h *Handler) handleGetPublicMedia(w http.ResponseWriter, r *http.Request) {
	if hasSuspiciousMediaPath(r.URL.EscapedPath()) {
		apphttp.WriteError(w, http.StatusForbidden, "FORBIDDEN", ErrMediaForbidden.Error())
		return
	}

	rawKey := strings.TrimSpace(chi.URLParam(r, "*"))
	if rawKey == "" {
		apphttp.WriteError(w, http.StatusNotFound, "MEDIA_NOT_FOUND", ErrMediaNotFound.Error())
		return
	}

	stream, err := h.service.OpenPublicMedia(r.Context(), rawKey)
	if err != nil {
		h.writeServiceError(w, r, "get_public_media_error", err)
		return
	}
	defer func() {
		_ = stream.Reader.Close()
	}()

	w.Header().Set("Content-Type", stream.ContentType)
	w.Header().Set("Cache-Control", publicMediaCacheHeader)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !stream.IsInline {
		w.Header().Set("Content-Disposition", "attachment")
	}
	w.WriteHeader(http.StatusOK)

	if len(stream.Prefix) > 0 {
		if _, err := w.Write(stream.Prefix); err != nil {
			return
		}
	}
	_, _ = io.Copy(w, stream.Reader)
}

func (h *Handler) handleListFotos(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := extractTenantID(w, r)
	if !ok {
		return
	}

	fotos, err := h.service.ListFotos(r.Context(), complejoID)
	if err != nil {
		h.writeServiceError(w, r, "list_fotos_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, fotos)
}

func (h *Handler) handleUploadFoto(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := extractTenantID(w, r)
	if !ok {
		return
	}

	fileBytes, ok := parseMultipartImage(w, r, "archivo", "foto", "imagen", "file")
	if !ok {
		return
	}

	fields := make(map[string]string)

	esPortada := false
	if rawPortada := strings.TrimSpace(r.FormValue("es_portada")); rawPortada != "" {
		parsed, err := strconv.ParseBool(rawPortada)
		if err != nil {
			fields["es_portada"] = "es_portada debe ser true o false"
		} else {
			esPortada = parsed
		}
	}

	orden := 0
	if rawOrden := strings.TrimSpace(r.FormValue("orden")); rawOrden != "" {
		parsed, err := strconv.Atoi(rawOrden)
		if err != nil || parsed < 0 || parsed > maxShortint {
			fields["orden"] = "orden debe ser un entero entre 0 y 32767"
		} else {
			orden = parsed
		}
	}

	var descripcion *string
	if rawDesc := strings.TrimSpace(r.FormValue("descripcion")); rawDesc != "" {
		d := rawDesc
		descripcion = &d
	}

	if len(fields) > 0 {
		apphttp.WriteValidationError(w, "Datos inválidos para subir foto", fields)
		return
	}

	foto, err := h.service.UploadFoto(r.Context(), UploadComplejoFotoInput{
		ComplejoID:  complejoID,
		FileData:    fileBytes,
		Descripcion: descripcion,
		Orden:       orden,
		EsPortada:   esPortada,
	})
	if err != nil {
		h.writeServiceError(w, r, "upload_foto_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusCreated, foto)
}

func (h *Handler) handleUpdateFoto(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := extractTenantID(w, r)
	if !ok {
		return
	}

	fotoID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "fotoID")))
	if err != nil || fotoID == uuid.Nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Identificador de foto inválido")
		return
	}

	var req UpdateComplejoFotoRequest
	if err := apphttp.DecodeJSON(w, r, &req); err != nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_JSON", err.Error())
		return
	}

	foto, err := h.service.UpdateFoto(r.Context(), complejoID, fotoID, req)
	if err != nil {
		h.writeServiceError(w, r, "update_foto_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, foto)
}

func (h *Handler) handleDeleteFoto(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := extractTenantID(w, r)
	if !ok {
		return
	}

	fotoID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "fotoID")))
	if err != nil || fotoID == uuid.Nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Identificador de foto inválido")
		return
	}

	if err := h.service.DeleteFoto(r.Context(), complejoID, fotoID); err != nil {
		h.writeServiceError(w, r, "delete_foto_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, DeleteResult{
		ID:      fotoID,
		Deleted: true,
	})
}

func (h *Handler) handleUploadCanchaFoto(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := extractTenantID(w, r)
	if !ok {
		return
	}

	canchaID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "canchaID")))
	if err != nil || canchaID == uuid.Nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Identificador de cancha inválido")
		return
	}

	fileBytes, ok := parseMultipartImage(w, r, "archivo", "foto", "imagen", "file")
	if !ok {
		return
	}

	cancha, err := h.service.UploadCanchaFoto(r.Context(), complejoID, canchaID, fileBytes)
	if err != nil {
		h.writeServiceError(w, r, "upload_cancha_foto_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, cancha)
}

func (h *Handler) handleDeleteCanchaFoto(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := extractTenantID(w, r)
	if !ok {
		return
	}

	canchaID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "canchaID")))
	if err != nil || canchaID == uuid.Nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Identificador de cancha inválido")
		return
	}

	cancha, err := h.service.DeleteCanchaFoto(r.Context(), complejoID, canchaID)
	if err != nil {
		h.writeServiceError(w, r, "delete_cancha_foto_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, cancha)
}

func (h *Handler) handleUploadMetodoPagoQR(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := extractTenantID(w, r)
	if !ok {
		return
	}

	metodoID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "metodoID")))
	if err != nil || metodoID == uuid.Nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Identificador de método de pago inválido")
		return
	}

	fileBytes, ok := parseMultipartImage(w, r, "archivo", "qr", "imagen", "file")
	if !ok {
		return
	}

	metodo, err := h.service.UploadMetodoPagoQR(r.Context(), complejoID, metodoID, fileBytes)
	if err != nil {
		h.writeServiceError(w, r, "upload_metodo_pago_qr_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, metodo)
}

func (h *Handler) handleDeleteMetodoPagoQR(w http.ResponseWriter, r *http.Request) {
	complejoID, ok := extractTenantID(w, r)
	if !ok {
		return
	}

	metodoID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "metodoID")))
	if err != nil || metodoID == uuid.Nil {
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Identificador de método de pago inválido")
		return
	}

	metodo, err := h.service.DeleteMetodoPagoQR(r.Context(), complejoID, metodoID)
	if err != nil {
		h.writeServiceError(w, r, "delete_metodo_pago_qr_error", err)
		return
	}

	apphttp.WriteJSON(w, http.StatusOK, metodo)
}

func parseMultipartImage(w http.ResponseWriter, r *http.Request, fieldNames ...string) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxMultipartMediaBytes)
	if err := r.ParseMultipartForm(multipartMediaMemLimit); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			apphttp.WriteError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", ErrFileTooLarge.Error())
			return nil, false
		}
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_MULTIPART", "La solicitud debe enviarse en formato multipart/form-data válido")
		return nil, false
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	var file io.ReadCloser
	var fileErr error
	for _, name := range fieldNames {
		f, _, err := r.FormFile(name)
		if err == nil {
			file = f
			fileErr = nil
			break
		}
		if !errors.Is(err, http.ErrMissingFile) {
			fileErr = err
			break
		}
		fileErr = err
	}

	if fileErr != nil || file == nil {
		msg := "El archivo de imagen es obligatorio"
		if fileErr != nil && !errors.Is(fileErr, http.ErrMissingFile) {
			msg = "No se pudo leer el archivo de imagen"
		}
		apphttp.WriteValidationError(w, "Archivo de imagen requerido", map[string]string{
			"archivo": msg,
		})
		return nil, false
	}
	defer func() {
		_ = file.Close()
	}()

	fileBytes, readErr := io.ReadAll(io.LimitReader(file, db.MaxImageBytes+1))
	if readErr != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(readErr, &maxBytesErr) {
			apphttp.WriteError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", ErrFileTooLarge.Error())
			return nil, false
		}
		apphttp.WriteError(w, http.StatusBadRequest, "INVALID_FILE", "No se pudo leer la imagen enviada")
		return nil, false
	}
	if len(fileBytes) > db.MaxImageBytes {
		apphttp.WriteError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", ErrFileTooLarge.Error())
		return nil, false
	}
	if len(fileBytes) == 0 {
		apphttp.WriteValidationError(w, "Archivo de imagen vacío", map[string]string{
			"archivo": "El archivo de imagen no puede estar vacío",
		})
		return nil, false
	}

	return fileBytes, true
}

func hasSuspiciousMediaPath(escapedPath string) bool {
	lower := strings.ToLower(escapedPath)
	return strings.Contains(lower, "..") ||
		strings.Contains(lower, "%2e") ||
		strings.Contains(lower, "%5c") ||
		strings.Contains(lower, "%00") ||
		strings.Contains(lower, "\\")
}
