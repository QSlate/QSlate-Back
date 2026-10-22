package repository

import (
	"context"
	"database/sql"
	"errors"

	"qslate/services/auth/internal/notification"
)

func (p *Postgres) ClaimMail(ctx context.Context) (int64, []byte, error) {
	// Clear expired ciphertext; successful delivery also clears its payload.
	if _, err := p.db.ExecContext(ctx, `UPDATE mail_outbox SET payload=NULL WHERE payload IS NOT NULL AND expires_at<=now()`); err != nil {
		return 0, nil, err
	}
	var id int64
	var payload []byte
	err := p.db.QueryRowContext(ctx, `WITH candidate AS (
 SELECT id FROM mail_outbox WHERE sent_at IS NULL AND payload IS NOT NULL AND expires_at>now() AND attempts<10 AND next_attempt_at<=now()
 ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1)
 UPDATE mail_outbox m SET attempts=attempts+1,next_attempt_at=now()+interval '30 seconds' FROM candidate c WHERE m.id=c.id RETURNING m.id,m.payload`).Scan(&id, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, notification.ErrEmpty
	}
	return id, payload, err
}
func (p *Postgres) CompleteMail(ctx context.Context, id int64) error {
	_, err := p.db.ExecContext(ctx, `UPDATE mail_outbox SET sent_at=now(),payload=NULL WHERE id=$1`, id)
	return err
}
