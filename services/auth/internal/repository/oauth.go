package repository

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"qslate/services/auth/internal/domain"
)

func (p *Postgres) SaveOAuthState(ctx context.Context, hash, provider string, verifier []byte, expires time.Time) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM oauth_states WHERE expires_at<=now()`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO oauth_states(hash,provider,encrypted_verifier,expires_at) VALUES($1,$2,$3,$4)`, hash, provider, verifier, expires); err != nil {
		return err
	}
	return tx.Commit()
}
func (p *Postgres) ConsumeOAuthState(ctx context.Context, hash, provider string) ([]byte, error) {
	var verifier []byte
	err := p.db.QueryRowContext(ctx, `DELETE FROM oauth_states WHERE hash=$1 AND provider=$2 AND expires_at>now() RETURNING encrypted_verifier`, hash, provider).Scan(&verifier)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrActionToken
	}
	return verifier, err
}

func (p *Postgres) ExternalAccount(ctx context.Context, identity domain.ExternalIdentity) (domain.User, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.User{}, err
	}
	defer tx.Rollback()
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT user_id FROM external_identities WHERE provider=$1 AND subject=$2`, identity.Provider, identity.Subject).Scan(&id)
	if err == nil {
		user, err := scan(tx.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id=$1`, id))
		if err != nil {
			return user, err
		}
		return user, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.User{}, err
	}
	// Never link an existing password account using an email claim alone.
	random := make([]byte, 12)
	if _, err = rand.Read(random); err != nil {
		return domain.User{}, err
	}
	username := "u_" + hex.EncodeToString(random)
	user, err := scan(tx.QueryRowContext(ctx, `INSERT INTO users(email,username,password_hash,display_name,email_verified) VALUES($1,$2,'!oauth',$3,$4) RETURNING `+userColumns, identity.Email, username, identity.DisplayName, identity.EmailVerified))
	if err != nil {
		return user, mapConflict(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO user_preferences(user_id) VALUES($1)`, user.ID); err != nil {
		return user, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO external_identities(provider,subject,user_id) VALUES($1,$2,$3)`, identity.Provider, identity.Subject, user.ID); err != nil {
		return user, mapConflict(err)
	}
	return user, tx.Commit()
}
