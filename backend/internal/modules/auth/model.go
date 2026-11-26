package auth

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrEmailAlreadyExists     = errors.New("el correo electrónico ya está registrado")
	ErrInvalidCredentials     = errors.New("credenciales inválidas")
	ErrInvalidCurrentPassword = errors.New("la contraseña actual es incorrecta")
	ErrAccountInactive        = errors.New("la cuenta se encuentra desactivada")
	ErrInvalidToken           = errors.New("token inválido o expirado")
	ErrUserNotFound           = errors.New("usuario no encontrado")
)

// ValidationError agrupa errores de validación de entrada por campo.
type ValidationError struct {
	Message string
	Fields  map[string]string
}

func (e *ValidationError) Error() string {
	return e.Message
}

// User mapea la tabla usuarios respetando exactamente sus columnas y nulabilidad.
type User struct {
	ID                uuid.UUID  `json:"id"`
	Rol               string     `json:"rol"`
	Email             string     `json:"email"`
	PasswordHash      string     `json:"-"`
	NombreCompleto    string     `json:"nombre_completo"`
	Telefono          *string    `json:"telefono"`
	Activo            bool       `json:"activo"`
	EmailVerificadoAt *time.Time `json:"email_verificado_at"`
	UltimoLoginAt     *time.Time `json:"ultimo_login_at"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// UserProfile representa el perfil público limpio del usuario (nunca incluye password_hash).
type UserProfile struct {
	ID                uuid.UUID  `json:"id"`
	Rol               string     `json:"rol"`
	Email             string     `json:"email"`
	NombreCompleto    string     `json:"nombre_completo"`
	Telefono          *string    `json:"telefono"`
	Activo            bool       `json:"activo"`
	ComplejoID        *uuid.UUID `json:"complejo_id"`
	EmailVerificadoAt *time.Time `json:"email_verificado_at"`
	UltimoLoginAt     *time.Time `json:"ultimo_login_at"`
	CreatedAt         time.Time  `json:"created_at"`
}

func toUserProfile(u User, complejoID *uuid.UUID) UserProfile {
	return UserProfile{
		ID:                u.ID,
		Rol:               u.Rol,
		Email:             u.Email,
		NombreCompleto:    u.NombreCompleto,
		Telefono:          u.Telefono,
		Activo:            u.Activo,
		ComplejoID:        complejoID,
		EmailVerificadoAt: u.EmailVerificadoAt,
		UltimoLoginAt:     u.UltimoLoginAt,
		CreatedAt:         u.CreatedAt,
	}
}

// RegisterRequest captura únicamente los datos permitidos para autorregistro de JUGADOR.
type RegisterRequest struct {
	Email          string `json:"email"`
	Password       string `json:"password"`
	NombreCompleto string `json:"nombre_completo"`
	Telefono       string `json:"telefono"`
}

// LoginRequest contiene las credenciales de acceso.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// RefreshRequest contiene el Refresh Token opaco a rotar.
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// LogoutRequest contiene el Refresh Token opaco a revocar.
type LogoutRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// AuthTokensResponse devuelve el par de tokens y el perfil autenticado.
type AuthTokensResponse struct {
	AccessToken  string      `json:"access_token"`
	RefreshToken string      `json:"refresh_token"`
	TokenType    string      `json:"token_type"`
	ExpiresIn    int64       `json:"expires_in"`
	User         UserProfile `json:"user"`
}

// RegisterResponse envuelve el perfil recién registrado.
type RegisterResponse struct {
	User UserProfile `json:"user"`
}

// MeResponse envuelve el perfil del usuario autenticado.
type MeResponse struct {
	User UserProfile `json:"user"`
}

// MessageResponse devuelve una confirmación textual simple.
type MessageResponse struct {
	Message string `json:"message"`
}

// UpdateProfileRequest contiene los campos editables por el usuario en PATCH /api/v1/auth/me.
type UpdateProfileRequest struct {
	NombreCompleto *string `json:"nombre_completo"`
	Telefono       *string `json:"telefono"`
}

// ChangePasswordRequest exige la contraseña actual y la nueva contraseña en POST /api/v1/auth/change-password.
type ChangePasswordRequest struct {
	PasswordActual string `json:"password_actual"`
	PasswordNueva  string `json:"password_nueva"`
}

// ChangePasswordResponse confirma el cambio de contraseña y el estado de revocación de sesiones activas.
type ChangePasswordResponse struct {
	Message           string `json:"message"`
	SesionesRevocadas bool   `json:"sesiones_revocadas"`
}
