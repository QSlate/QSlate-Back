package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/lib/pq"
	"qslate/services/auth/internal/domain"
)

type Postgres struct{ db *sql.DB }

func New(db *sql.DB) *Postgres { return &Postgres{db: db} }

func (p *Postgres) Create(ctx context.Context, email, username, hash string) (domain.User, error) {
	user, err := scan(p.db.QueryRowContext(ctx,
		`INSERT INTO users (email, username, password_hash) VALUES ($1, $2, $3) RETURNING id, email, username, password_hash, created_at`, email, username, hash))
	var pg *pq.Error
	if errors.As(err, &pg) && pg.Code == "23505" {
		return domain.User{}, domain.ErrConflict
	}
	return user, err
}

func (p *Postgres) FindByIdentifier(ctx context.Context, identifier string) (domain.User, error) {
	return scan(p.db.QueryRowContext(ctx, `SELECT id, email, username, password_hash, created_at FROM users WHERE email = $1 OR username = $1`, identifier))
}

func (p *Postgres) FindByID(ctx context.Context, id int64) (domain.User, error) {
	return scan(p.db.QueryRowContext(ctx, `SELECT id, email, username, password_hash, created_at FROM users WHERE id = $1`, id))
}

func scan(row *sql.Row) (domain.User, error) {
	var user domain.User
	err := row.Scan(&user.ID, &user.Email, &user.Username, &user.PasswordHash, &user.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = domain.ErrNotFound
	}
	return user, err
}
