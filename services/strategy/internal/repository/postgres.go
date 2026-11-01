package repository

import (
	"context"
	"database/sql"
	"errors"
	"qslate/services/strategy/internal/domain"
	"strconv"
)

type Postgres struct{ db *sql.DB }

func New(db *sql.DB) *Postgres { return &Postgres{db} }

var _ domain.Repository = (*Postgres)(nil)

const summaryColumns = `s.id,v.id,s.revision,v.name,s.created_at,s.updated_at`
const columns = summaryColumns + `,v.source,v.parameters`
const currentJoin = ` FROM strategies s JOIN strategy_versions v ON v.strategy_id=s.id AND v.id=s.current_version_id `

type scanner interface{ Scan(...any) error }

func scan(row scanner, full bool) (domain.Strategy, error) {
	var s domain.Strategy
	args := []any{&s.ID, &s.VersionID, &s.Revision, &s.Name, &s.CreatedAt, &s.UpdatedAt}
	if full {
		args = append(args, &s.Source, &s.Parameters)
	}
	err := row.Scan(args...)
	if errors.Is(err, sql.ErrNoRows) {
		err = domain.ErrNotFound
	}
	return s, err
}
func insertVersion(ctx context.Context, tx *sql.Tx, sid, vid string, revision int64, in domain.Input, restored string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO strategy_versions(id,strategy_id,revision,name,source,parameters,restored_from) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''))`, vid, sid, revision, in.Name, in.Source, string(in.Parameters), restored)
	return err
}
func (p *Postgres) Create(ctx context.Context, uid int64, sid, vid string, in domain.Input) (domain.Strategy, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Strategy{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO strategies(id,owner_id,current_version_id) VALUES($1,$2,$3)`, sid, uid, vid); err != nil {
		return domain.Strategy{}, err
	}
	if err = insertVersion(ctx, tx, sid, vid, 1, in, ""); err != nil {
		return domain.Strategy{}, err
	}
	result, err := scan(tx.QueryRowContext(ctx, `SELECT `+columns+currentJoin+`WHERE s.owner_id=$1 AND s.id=$2`, uid, sid), true)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}
func (p *Postgres) Get(ctx context.Context, uid int64, sid string) (domain.Strategy, error) {
	return scan(p.db.QueryRowContext(ctx, `SELECT `+columns+currentJoin+`WHERE s.owner_id=$1 AND s.id=$2 AND s.deleted_at IS NULL`, uid, sid), true)
}

func (p *Postgres) Snapshot(ctx context.Context, uid int64, sid, version string) (domain.Input, error) {
	var in domain.Input
	err := p.db.QueryRowContext(ctx, `SELECT v.name,v.source,v.parameters FROM strategies s JOIN strategy_versions v ON v.strategy_id=s.id WHERE s.owner_id=$1 AND s.id=$2 AND v.id=$3 AND s.deleted_at IS NULL`, uid, sid, version).Scan(&in.Name, &in.Source, &in.Parameters)
	if errors.Is(err, sql.ErrNoRows) {
		err = domain.ErrNotFound
	}
	return in, err
}
func (p *Postgres) List(ctx context.Context, uid int64, cursor string, limit int) (domain.Page, error) {
	page := domain.Page{Items: []domain.Summary{}}
	rows, err := p.db.QueryContext(ctx, `SELECT `+summaryColumns+currentJoin+`WHERE s.owner_id=$1 AND s.deleted_at IS NULL AND ($2='' OR s.id COLLATE "C"<$2) ORDER BY s.id COLLATE "C" DESC LIMIT $3`, uid, cursor, limit+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		s, err := scan(rows, false)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, s.Summary)
	}
	if err = rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		page.NextCursor = page.Items[limit-1].ID
	}
	return page, nil
}
func (p *Postgres) mutate(ctx context.Context, uid int64, sid, vid, base string, input *domain.Input, restore string) (domain.Strategy, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Strategy{}, err
	}
	defer tx.Rollback()
	var current string
	var revision int64
	err = tx.QueryRowContext(ctx, `SELECT current_version_id,revision FROM strategies WHERE owner_id=$1 AND id=$2 AND deleted_at IS NULL FOR UPDATE`, uid, sid).Scan(&current, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Strategy{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Strategy{}, err
	}
	if current != base {
		return domain.Strategy{}, domain.ErrConflict
	}
	var in domain.Input
	if input != nil {
		in = *input
	} else {
		err = tx.QueryRowContext(ctx, `SELECT name,source,parameters FROM strategy_versions WHERE strategy_id=$1 AND id=$2`, sid, restore).Scan(&in.Name, &in.Source, &in.Parameters)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Strategy{}, domain.ErrNotFound
		}
		if err != nil {
			return domain.Strategy{}, err
		}
	}
	if err = insertVersion(ctx, tx, sid, vid, revision+1, in, restore); err != nil {
		return domain.Strategy{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE strategies SET current_version_id=$3,revision=revision+1,updated_at=clock_timestamp() WHERE owner_id=$1 AND id=$2`, uid, sid, vid); err != nil {
		return domain.Strategy{}, err
	}
	result, err := scan(tx.QueryRowContext(ctx, `SELECT `+columns+currentJoin+`WHERE s.owner_id=$1 AND s.id=$2`, uid, sid), true)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}
func (p *Postgres) Save(ctx context.Context, uid int64, sid, vid string, in domain.SaveInput) (domain.Strategy, error) {
	return p.mutate(ctx, uid, sid, vid, in.BaseVersionID, &in.Input, "")
}
func (p *Postgres) Restore(ctx context.Context, uid int64, sid, vid string, in domain.RestoreInput) (domain.Strategy, error) {
	return p.mutate(ctx, uid, sid, vid, in.BaseVersionID, nil, in.VersionID)
}
func (p *Postgres) Delete(ctx context.Context, uid int64, sid string) error {
	r, err := p.db.ExecContext(ctx, `UPDATE strategies SET deleted_at=clock_timestamp(),updated_at=clock_timestamp() WHERE owner_id=$1 AND id=$2 AND deleted_at IS NULL`, uid, sid)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err == nil && n == 0 {
		return domain.ErrNotFound
	}
	return err
}
func (p *Postgres) Versions(ctx context.Context, uid int64, sid string, before int64, limit int) (domain.VersionPage, error) {
	page := domain.VersionPage{Items: []domain.Version{}}
	// One statement snapshot checks ownership even if the requested page is empty.
	rows, err := p.db.QueryContext(ctx, `SELECT v.id,v.revision,v.name,v.source,v.parameters,COALESCE(v.restored_from,''),v.created_at FROM strategies s LEFT JOIN LATERAL (SELECT * FROM strategy_versions WHERE strategy_id=s.id AND ($3=0 OR revision<$3) ORDER BY revision DESC LIMIT $4) v ON true WHERE s.owner_id=$1 AND s.id=$2 AND s.deleted_at IS NULL ORDER BY v.revision DESC`, uid, sid, before, limit+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		found = true
		var v domain.Version
		var id sql.NullString
		var revision sql.NullInt64
		var name, source, restored sql.NullString
		var created sql.NullTime
		var params []byte
		if err = rows.Scan(&id, &revision, &name, &source, &params, &restored, &created); err != nil {
			return page, err
		}
		if !id.Valid {
			continue
		}
		v.VersionID = id.String
		v.Revision = revision.Int64
		v.Name = name.String
		v.Source = source.String
		v.Parameters = params
		v.RestoredFrom = restored.String
		v.CreatedAt = created.Time
		page.Items = append(page.Items, v)
	}
	if err = rows.Err(); err != nil {
		return page, err
	}
	if !found {
		return page, domain.ErrNotFound
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		page.NextCursor = strconv.FormatInt(page.Items[limit-1].Revision, 10)
	}
	return page, nil
}
