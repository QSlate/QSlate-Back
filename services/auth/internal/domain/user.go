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
)

type User struct {
	ID           int64     `json:"id"`
	Email        string    `json:"email"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

// Repository is an auth-owned port. Implementations must respect cancellation.
type Repository interface {
	Create(context.Context, string, string, string) (User, error)
	FindByIdentifier(context.Context, string) (User, error)
	FindByID(context.Context, int64) (User, error)
}
