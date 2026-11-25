package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const pgUniqueViolationCode = "23505"

// Repository define las operaciones de persistencia del módulo auth.
type Repository interface {
	CreateJugador(ctx context.Context, email, passwordHash, nombreCompleto, telefono string) (User, error)
	FindByEmail(ctx context.Context, normalizedEmail string) (User, error)
	FindByID(ctx context.Context, id uuid.UUID) (User, error)
	UpdateLastLogin(ctx context.Context, id uuid.UUID) (time.Time, error)
	FindPrimaryComplejoIDByOwner(ctx context.Context, duenoID uuid.UUID) (*uuid.UUID, error)
}

// PostgresRepository implementa Repository sobre PostgreSQL con pgx/v5.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

// NewRepository crea el repositorio PostgreSQL para autenticación.
func NewRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

// CreateJugador inserta siempre un usuario con rol JUGADOR (nunca SUPER_ADMIN ni DUENO).
func (r *PostgresRepository) CreateJugador(ctx context.Context, email, passwordHash, nombreCompleto, telefono string) (User, error) {
	const query = `
		INSERT INTO usuarios (rol, email, password_hash, nombre_completo, telefono)
		VALUES ('JUGADOR', $1, $2, $3, $4)
		RETURNING id, rol, email, password_hash, nombre_completo, telefono, activo,
		          email_verificado_at, ultimo_login_at, created_at, updated_at
	`

	var u User
	err := r.pool.QueryRow(ctx, query, email, passwordHash, nombreCompleto, telefono).Scan(
		&u.ID,
		&u.Rol,
		&u.Email,
		&u.PasswordHash,
		&u.NombreCompleto,
		&u.Telefono,
		&u.Activo,
		&u.EmailVerificadoAt,
		&u.UltimoLoginAt,
		&u.CreatedAt,
		&u.UpdatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolationCode {
			return User{}, ErrEmailAlreadyExists
		}
		return User{}, fmt.Errorf("error creando usuario jugador: %w", err)
	}

	return u, nil
}

// FindByEmail busca un usuario por correo normalizado aprovechando uq_usuarios_email_lower.
func (r *PostgresRepository) FindByEmail(ctx context.Context, normalizedEmail string) (User, error) {
	const query = `
		SELECT id, rol, email, password_hash, nombre_completo, telefono, activo,
		       email_verificado_at, ultimo_login_at, created_at, updated_at
		FROM usuarios
		WHERE LOWER(email) = $1
	`

	var u User
	err := r.pool.QueryRow(ctx, query, normalizedEmail).Scan(
		&u.ID,
		&u.Rol,
		&u.Email,
		&u.PasswordHash,
		&u.NombreCompleto,
		&u.Telefono,
		&u.Activo,
		&u.EmailVerificadoAt,
		&u.UltimoLoginAt,
		&u.CreatedAt,
		&u.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrUserNotFound
		}
		return User{}, fmt.Errorf("error buscando usuario por email: %w", err)
	}

	return u, nil
}

// FindByID busca un usuario por su UUID.
func (r *PostgresRepository) FindByID(ctx context.Context, id uuid.UUID) (User, error) {
	const query = `
		SELECT id, rol, email, password_hash, nombre_completo, telefono, activo,
		       email_verificado_at, ultimo_login_at, created_at, updated_at
		FROM usuarios
		WHERE id = $1
	`

	var u User
	err := r.pool.QueryRow(ctx, query, id).Scan(
		&u.ID,
		&u.Rol,
		&u.Email,
		&u.PasswordHash,
		&u.NombreCompleto,
		&u.Telefono,
		&u.Activo,
		&u.EmailVerificadoAt,
		&u.UltimoLoginAt,
		&u.CreatedAt,
		&u.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrUserNotFound
		}
		return User{}, fmt.Errorf("error buscando usuario por id: %w", err)
	}

	return u, nil
}

// UpdateLastLogin actualiza ultimo_login_at al instante actual en PostgreSQL.
func (r *PostgresRepository) UpdateLastLogin(ctx context.Context, id uuid.UUID) (time.Time, error) {
	const query = `
		UPDATE usuarios
		SET ultimo_login_at = NOW()
		WHERE id = $1
		RETURNING ultimo_login_at
	`

	var lastLogin time.Time
	if err := r.pool.QueryRow(ctx, query, id).Scan(&lastLogin); err != nil {
		return time.Time{}, fmt.Errorf("error actualizando ultimo_login_at: %w", err)
	}
	return lastLogin, nil
}

// FindPrimaryComplejoIDByOwner obtiene el primer complejo asociado a un DUENO, o nil si aún no tiene uno.
func (r *PostgresRepository) FindPrimaryComplejoIDByOwner(ctx context.Context, duenoID uuid.UUID) (*uuid.UUID, error) {
	const query = `
		SELECT id
		FROM complejos
		WHERE dueno_id = $1
		ORDER BY created_at ASC
		LIMIT 1
	`

	var complejoID uuid.UUID
	err := r.pool.QueryRow(ctx, query, duenoID).Scan(&complejoID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("error consultando complejo del dueño: %w", err)
	}

	return &complejoID, nil
}
