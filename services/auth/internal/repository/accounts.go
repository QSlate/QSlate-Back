package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lib/pq"
	"qslate/services/auth/internal/domain"
)

var _ domain.Store = (*Postgres)(nil)

func mapConflict(err error) error {
	var pg *pq.Error
	if errors.As(err, &pg) && pg.Code == "23505" {
		return domain.ErrConflict
	}
	return err
}

func (p *Postgres) CreateAccount(ctx context.Context, email, username, hash string, action domain.Action) (domain.User, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.User{}, err
	}
	defer tx.Rollback()
	user, err := scan(tx.QueryRowContext(ctx, `INSERT INTO users(email,username,password_hash) VALUES($1,$2,$3) RETURNING `+userColumns, email, username, hash))
	if err != nil {
		return user, mapConflict(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO user_preferences(user_id) VALUES($1)`, user.ID); err != nil {
		return user, err
	}
	action.UserID = user.ID
	if err = saveAction(ctx, tx, action); err != nil {
		return user, err
	}
	return user, tx.Commit()
}

func (p *Postgres) UpdateProfile(ctx context.Context, id int64, patch domain.ProfilePatch) (domain.User, error) {
	user, err := scan(p.db.QueryRowContext(ctx, `UPDATE users SET username=COALESCE($2,username), display_name=COALESCE($3,display_name) WHERE id=$1 RETURNING `+userColumns, id, patch.Username, patch.DisplayName))
	return user, mapConflict(err)
}

func (p *Postgres) Preferences(ctx context.Context, id int64) (domain.Preferences, error) {
	var prefs domain.Preferences
	err := p.db.QueryRowContext(ctx, `SELECT theme,colorblind_mode FROM user_preferences WHERE user_id=$1`, id).Scan(&prefs.Theme, &prefs.ColorblindMode)
	if errors.Is(err, sql.ErrNoRows) {
		return prefs, domain.ErrNotFound
	}
	return prefs, err
}

func (p *Postgres) UpdatePreferences(ctx context.Context, id int64, patch domain.PreferencesPatch) (domain.Preferences, error) {
	var prefs domain.Preferences
	err := p.db.QueryRowContext(ctx, `UPDATE user_preferences SET theme=COALESCE($2,theme), colorblind_mode=COALESCE($3,colorblind_mode) WHERE user_id=$1 RETURNING theme,colorblind_mode`, id, patch.Theme, patch.ColorblindMode).Scan(&prefs.Theme, &prefs.ColorblindMode)
	if errors.Is(err, sql.ErrNoRows) {
		return prefs, domain.ErrNotFound
	}
	return prefs, err
}

func (p *Postgres) CreateSession(ctx context.Context, session domain.Session, hash string) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Serialize admission against password reset, which locks the same account row.
	var currentHash string
	if err = tx.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id=$1 FOR UPDATE`, session.UserID).Scan(&currentHash); err != nil {
		return err
	}
	if currentHash != session.PasswordHash {
		return domain.ErrCredentials
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sessions(id,user_id,expires_at) VALUES($1,$2,$3)`, session.ID, session.UserID, session.ExpiresAt); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO refresh_tokens(hash,session_id) VALUES($1,$2)`, hash, session.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func (p *Postgres) RotateSession(ctx context.Context, previous, replacement string) (domain.Session, domain.User, error) {
	var session domain.Session
	var user domain.User
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return session, user, err
	}
	defer tx.Rollback()
	var consumed, revoked sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT s.id,s.user_id,s.expires_at,s.revoked_at,t.consumed_at FROM refresh_tokens t JOIN sessions s ON s.id=t.session_id WHERE t.hash=$1 FOR UPDATE OF s`, previous).Scan(&session.ID, &session.UserID, &session.ExpiresAt, &revoked, &consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return session, user, domain.ErrCredentials
	}
	if err != nil {
		return session, user, err
	}
	// Re-read after acquiring the family lock: another refresh may have consumed this token.
	if err = tx.QueryRowContext(ctx, `SELECT consumed_at FROM refresh_tokens WHERE hash=$1`, previous).Scan(&consumed); err != nil {
		return session, user, err
	}
	if revoked.Valid || !session.ExpiresAt.After(time.Now()) {
		return session, user, domain.ErrCredentials
	}
	if consumed.Valid {
		if _, err = tx.ExecContext(ctx, `UPDATE sessions SET revoked_at=now() WHERE id=$1`, session.ID); err != nil {
			return session, user, err
		}
		if err = tx.Commit(); err != nil {
			return session, user, err
		}
		return session, user, domain.ErrCredentials
	}
	if _, err = tx.ExecContext(ctx, `UPDATE refresh_tokens SET consumed_at=now() WHERE hash=$1`, previous); err != nil {
		return session, user, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO refresh_tokens(hash,session_id) VALUES($1,$2)`, replacement, session.ID); err != nil {
		return session, user, err
	}
	user, err = scan(tx.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id=$1`, session.UserID))
	if err != nil {
		return session, user, err
	}
	return session, user, tx.Commit()
}

func (p *Postgres) ActiveSession(ctx context.Context, id int64, sid string) error {
	var active bool
	err := p.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sessions WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL AND expires_at>now())`, sid, id).Scan(&active)
	if err != nil {
		return err
	}
	if !active {
		return domain.ErrCredentials
	}
	return nil
}

func (p *Postgres) RevokeSession(ctx context.Context, hash string) error {
	_, err := p.db.ExecContext(ctx, `UPDATE sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE id=(SELECT session_id FROM refresh_tokens WHERE hash=$1)`, hash)
	return err
}

func saveAction(ctx context.Context, tx *sql.Tx, action domain.Action) error {
	// Lock the account before replacing tokens so concurrent resends are serialized.
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, action.UserID).Scan(&id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE action_tokens SET consumed_at=now() WHERE user_id=$1 AND kind=$2 AND consumed_at IS NULL`, action.UserID, action.Kind); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO action_tokens(hash,user_id,kind,expires_at) VALUES($1,$2,$3,$4)`, action.Hash, action.UserID, action.Kind, action.ExpiresAt); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO mail_outbox(user_id,payload,expires_at) VALUES($1,$2,$3)`, action.UserID, action.EncryptedMail, action.ExpiresAt)
	return err
}

func (p *Postgres) SaveAction(ctx context.Context, action domain.Action) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = saveAction(ctx, tx, action); err != nil {
		return err
	}
	return tx.Commit()
}

func (p *Postgres) ConsumeAction(ctx context.Context, kind, hash, passwordHash string) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id int64
	// Consuming and issuing actions share an account lock, always acquired before token locks.
	if err = tx.QueryRowContext(ctx, `SELECT user_id FROM action_tokens WHERE hash=$1 AND kind=$2`, hash, kind).Scan(&id); errors.Is(err, sql.ErrNoRows) {
		return domain.ErrActionToken
	} else if err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, id).Scan(&id); err != nil {
		return err
	}
	var expires time.Time
	var used sql.NullTime
	if err = tx.QueryRowContext(ctx, `SELECT expires_at,consumed_at FROM action_tokens WHERE hash=$1 AND kind=$2 FOR UPDATE`, hash, kind).Scan(&expires, &used); err != nil {
		return err
	}
	if used.Valid || !expires.After(time.Now()) {
		return domain.ErrActionToken
	}
	if kind == "reset_password" {
		if _, err = tx.ExecContext(ctx, `UPDATE users SET password_hash=$2 WHERE id=$1`, id, passwordHash); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, id); err != nil {
			return err
		}
	} else {
		if _, err = tx.ExecContext(ctx, `UPDATE users SET email_verified=TRUE WHERE id=$1`, id); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE action_tokens SET consumed_at=now() WHERE user_id=$1 AND kind=$2 AND consumed_at IS NULL`, id, kind); err != nil {
		return err
	}
	return tx.Commit()
}

func (p *Postgres) CreateAPIKey(ctx context.Context, id int64, key domain.APIKey) (domain.APIKey, error) {
	err := p.db.QueryRowContext(ctx, `INSERT INTO api_keys(id,user_id,name,hash,scopes,expires_at) VALUES($1,$2,$3,$4,$5,$6) RETURNING created_at`, key.ID, id, key.Name, key.Hash, pq.Array(key.Scopes), key.ExpiresAt).Scan(&key.CreatedAt)
	return key, err
}

func (p *Postgres) ListAPIKeys(ctx context.Context, id int64) ([]domain.APIKey, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT id,name,scopes,created_at,expires_at,revoked_at FROM api_keys WHERE user_id=$1 ORDER BY created_at DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []domain.APIKey{}
	for rows.Next() {
		var key domain.APIKey
		if err = rows.Scan(&key.ID, &key.Name, pq.Array(&key.Scopes), &key.CreatedAt, &key.ExpiresAt, &key.RevokedAt); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (p *Postgres) RevokeAPIKey(ctx context.Context, userID int64, id string) error {
	result, err := p.db.ExecContext(ctx, `UPDATE api_keys SET revoked_at=COALESCE(revoked_at,now()) WHERE id=$1 AND user_id=$2`, id, userID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return domain.ErrNotFound
	}
	return nil
}
