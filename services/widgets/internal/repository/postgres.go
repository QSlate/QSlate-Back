package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"qslate/services/widgets/internal/domain"
)

type Postgres struct{ db *sql.DB }

func New(db *sql.DB) *Postgres { return &Postgres{db} }

var _ domain.Repository = (*Postgres)(nil)

const summaryColumns = `w.id,v.id,COALESCE(w.owner_id,0),w.kind,v.name,v.language,w.created_at,w.updated_at`
const columns = summaryColumns + `,v.source,v.configuration`
const currentJoin = ` FROM widgets w JOIN widget_versions v ON v.widget_id=w.id AND v.id=w.current_version_id `

type scanner interface{ Scan(...any) error }

func scan(row scanner, full bool) (domain.Widget, error) {
	var w domain.Widget
	args := []any{&w.ID, &w.VersionID, &w.OwnerID, &w.Kind, &w.Name, &w.Language, &w.CreatedAt, &w.UpdatedAt}
	if full {
		args = append(args, &w.Source, &w.Configuration)
	}
	err := row.Scan(args...)
	if errors.Is(err, sql.ErrNoRows) {
		err = domain.ErrNotFound
	}
	return w, err
}
func insertVersion(ctx context.Context, tx *sql.Tx, wid, version string, in domain.Input) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO widget_versions(id,widget_id,name,language,source,configuration) VALUES($1,$2,$3,$4,$5,$6)`, version, wid, in.Name, in.Language, in.Source, string(in.Configuration))
	return err
}
func (p *Postgres) Create(ctx context.Context, uid int64, wid, version string, in domain.Input) (domain.Widget, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Widget{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO widgets(id,owner_id,kind,current_version_id) VALUES($1,$2,'custom',$3)`, wid, uid, version); err != nil {
		return domain.Widget{}, err
	}
	if err = insertVersion(ctx, tx, wid, version, in); err != nil {
		return domain.Widget{}, err
	}
	result, err := scan(tx.QueryRowContext(ctx, `SELECT `+columns+currentJoin+`WHERE w.owner_id=$1 AND w.id=$2`, uid, wid), true)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}
func (p *Postgres) List(ctx context.Context, uid int64, installed bool, cursor string, limit int) (domain.Page, error) {
	page := domain.Page{Items: []domain.Summary{}}
	rows, err := p.db.QueryContext(ctx, `SELECT `+summaryColumns+currentJoin+`WHERE w.deleted_at IS NULL AND (w.owner_id=$1 OR ($2 AND w.kind='installed')) AND ($3='' OR w.id<$3) ORDER BY w.id DESC LIMIT $4`, uid, installed, cursor, limit+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		w, err := scan(rows, false)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, w.Summary)
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
func (p *Postgres) Get(ctx context.Context, uid int64, wid string) (domain.Widget, error) {
	return scan(p.db.QueryRowContext(ctx, `SELECT `+columns+currentJoin+`WHERE w.kind='custom' AND w.owner_id=$1 AND w.id=$2 AND w.deleted_at IS NULL`, uid, wid), true)
}
func (p *Postgres) Replace(ctx context.Context, uid int64, wid, version string, in domain.Input) (domain.Widget, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Widget{}, err
	}
	defer tx.Rollback()
	var id string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM widgets WHERE id=$1 AND owner_id=$2 AND kind='custom' AND deleted_at IS NULL FOR UPDATE`, wid, uid).Scan(&id); errors.Is(err, sql.ErrNoRows) {
		return domain.Widget{}, domain.ErrNotFound
	} else if err != nil {
		return domain.Widget{}, err
	}
	if err = insertVersion(ctx, tx, wid, version, in); err != nil {
		return domain.Widget{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE widgets SET current_version_id=$3,updated_at=clock_timestamp() WHERE owner_id=$1 AND id=$2`, uid, wid, version); err != nil {
		return domain.Widget{}, err
	}
	result, err := scan(tx.QueryRowContext(ctx, `SELECT `+columns+currentJoin+`WHERE w.owner_id=$1 AND w.id=$2`, uid, wid), true)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}
func (p *Postgres) Delete(ctx context.Context, uid int64, wid string) error {
	r, err := p.db.ExecContext(ctx, `UPDATE widgets SET deleted_at=clock_timestamp(),updated_at=clock_timestamp() WHERE owner_id=$1 AND id=$2 AND kind='custom' AND deleted_at IS NULL`, uid, wid)
	if err != nil {
		return err
	}
	count, err := r.RowsAffected()
	if err == nil && count == 0 {
		return domain.ErrNotFound
	}
	return err
}
func (p *Postgres) Resolve(ctx context.Context, uid int64, wid, version string, existing bool) (domain.Reference, error) {
	var ref domain.Reference
	err := p.db.QueryRowContext(ctx, `SELECT w.id,v.id FROM widgets w JOIN widget_versions v ON v.widget_id=w.id WHERE w.id=$1 AND v.id=$2 AND (w.owner_id=$3 OR w.kind='installed') AND ($4 OR w.deleted_at IS NULL)`, wid, version, uid, existing).Scan(&ref.WidgetID, &ref.VersionID)
	if errors.Is(err, sql.ErrNoRows) {
		err = domain.ErrNotFound
	}
	return ref, err
}

// Installed IDs/versions are operator-managed, globally available and immutable.
// Removed definitions are archived; previously pinned versions remain resolvable.
func (p *Postgres) SyncInstalled(ctx context.Context, entries []domain.Installed) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(71849124)`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE widgets SET deleted_at=COALESCE(deleted_at,now()) WHERE kind='installed'`); err != nil {
		return err
	}
	for _, entry := range entries {
		if _, err = tx.ExecContext(ctx, `INSERT INTO widgets(id,kind,current_version_id) VALUES($1,'installed',$2) ON CONFLICT(id) DO NOTHING`, entry.ID, entry.VersionID); err != nil {
			return err
		}
		var kind string
		if err = tx.QueryRowContext(ctx, `SELECT kind FROM widgets WHERE id=$1`, entry.ID).Scan(&kind); err != nil {
			return err
		}
		if kind != "installed" {
			return fmt.Errorf("installed widget ID collides with custom definition")
		}
		var old domain.Input
		var oldWidgetID string
		err = tx.QueryRowContext(ctx, `SELECT widget_id,name,language,source,configuration FROM widget_versions WHERE id=$1`, entry.VersionID).Scan(&oldWidgetID, &old.Name, &old.Language, &old.Source, &old.Configuration)
		if errors.Is(err, sql.ErrNoRows) {
			if err = insertVersion(ctx, tx, entry.ID, entry.VersionID, entry.Input); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			var same bool
			if err = tx.QueryRowContext(ctx, `SELECT $1::jsonb=$2::jsonb`, string(old.Configuration), string(entry.Configuration)).Scan(&same); err != nil {
				return err
			}
			if oldWidgetID != entry.ID || old.Name != entry.Name || old.Language != entry.Language || old.Source != entry.Source || !same {
				return errors.New("installed version changed; assign a new version ID")
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE widgets SET current_version_id=$2,deleted_at=NULL WHERE id=$1 AND kind='installed'`, entry.ID, entry.VersionID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
