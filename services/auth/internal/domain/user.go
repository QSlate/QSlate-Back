package domain

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound     = errors.New("user not found")
	ErrConflict     = errors.New("email or username already registered")
	ErrInvalidInput = errors.New("invalid input")
	ErrCredentials  = errors.New("invalid credentials")
	ErrActionToken  = errors.New("invalid or expired action token")
)

type User struct {
	ID            int64     `json:"id"`
	Email         string    `json:"email"`
	Username      string    `json:"username"`
	PasswordHash  string    `json:"-"`
	CreatedAt     time.Time `json:"created_at"`
	DisplayName   string    `json:"display_name"`
	EmailVerified bool      `json:"email_verified"`
}

type ProfilePatch struct {
	Username    *string `json:"username,omitempty"`
	DisplayName *string `json:"display_name,omitempty"`
}

type Preferences struct {
	Theme          string `json:"theme"`
	ColorblindMode bool   `json:"colorblind_mode"`
}

type PreferencesPatch struct {
	Theme          *string `json:"theme,omitempty"`
	ColorblindMode *bool   `json:"colorblind_mode,omitempty"`
}

type Session struct {
	ID           string
	UserID       int64
	ExpiresAt    time.Time
	PasswordHash string
}

type Action struct {
	UserID        int64
	Kind          string
	Hash          string
	ExpiresAt     time.Time
	EncryptedMail []byte
}

type APIKey struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Scopes    []string   `json:"scopes"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	Hash      string     `json:"-"`
}

// Store owns persistent account state. Secrets are digests; email payloads are encrypted.
type Store interface {
	Repository
	CreateAccount(context.Context, string, string, string, Action) (User, error)
	UpdateProfile(context.Context, int64, ProfilePatch) (User, error)
	Preferences(context.Context, int64) (Preferences, error)
	UpdatePreferences(context.Context, int64, PreferencesPatch) (Preferences, error)
	CreateSession(context.Context, Session, string) error
	RotateSession(context.Context, string, string) (Session, User, error)
	ActiveSession(context.Context, int64, string) error
	RevokeSession(context.Context, string) error
	SaveAction(context.Context, Action) error
	ConsumeAction(context.Context, string, string, string) error
	CreateAPIKey(context.Context, int64, APIKey) (APIKey, error)
	ListAPIKeys(context.Context, int64) ([]APIKey, error)
	RevokeAPIKey(context.Context, int64, string) error
	ExternalAccount(context.Context, ExternalIdentity) (User, error)
}

type ExternalIdentity struct {
	Provider, Subject, Email, DisplayName string
	EmailVerified                         bool
}

// Repository is an auth-owned port. Implementations must respect cancellation.
type Repository interface {
	Create(context.Context, string, string, string) (User, error)
	FindByIdentifier(context.Context, string) (User, error)
	FindByID(context.Context, int64) (User, error)
}
