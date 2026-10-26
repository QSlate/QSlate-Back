package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"qslate/services/workspace/internal/domain"
)

type Postgres struct{ db *sql.DB }

func New(db *sql.DB) *Postgres { return &Postgres{db: db} }

var _ domain.Repository = (*Postgres)(nil)

const summaryColumns = `id,owner_id,title,description,tags,revision,created_at,updated_at`
const columns = summaryColumns + `,widgets,layout`

type scanner interface{ Scan(...any) error }

func scan(row scanner, full bool) (domain.Dashboard, error) {
	var d domain.Dashboard
	var tags, widgets, layout []byte
	args := []any{&d.ID, &d.OwnerID, &d.Title, &d.Description, &tags, &d.Revision, &d.CreatedAt, &d.UpdatedAt}
	if full {
		args = append(args, &widgets, &layout)
	}
	err := row.Scan(args...)
	if errors.Is(err, sql.ErrNoRows) {
		return d, domain.ErrNotFound
	}
	if err != nil {
		return d, err
	}
	if err = json.Unmarshal(tags, &d.Tags); err != nil {
		return d, err
	}
	if full {
		if err = json.Unmarshal(widgets, &d.Widgets); err != nil {
			return d, err
		}
		if err = json.Unmarshal(layout, &d.Layout); err != nil {
			return d, err
		}
	}
	return d, nil
}

func encode(d domain.Dashboard) (string, string, string, error) {
	tags, err := json.Marshal(d.Tags)
	if err != nil {
		return "", "", "", err
	}
	widgets, err := json.Marshal(d.Widgets)
	if err != nil {
		return "", "", "", err
	}
	layout, err := json.Marshal(d.Layout)
	return string(tags), string(widgets), string(layout), err
}

func (p *Postgres) Create(ctx context.Context, input domain.Dashboard) (domain.Dashboard, error) {
	tags, widgets, layout, err := encode(input)
	if err != nil {
		return domain.Dashboard{}, err
	}
	return scan(p.db.QueryRowContext(ctx, `INSERT INTO dashboards(id,owner_id,title,description,tags,widgets,layout) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING `+columns, input.ID, input.OwnerID, input.Title, input.Description, tags, widgets, layout), true)
}

// List uses an opaque ID keyset, not an offset. Ordering is lexicographic ID DESC.
// Large widget configurations are not loaded for collection summaries.
func (p *Postgres) List(ctx context.Context, owner int64, cursor string, limit int) (domain.Page, error) {
	page := domain.Page{Items: []domain.Summary{}}
	rows, err := p.db.QueryContext(ctx, `SELECT `+summaryColumns+` FROM dashboards WHERE owner_id=$1 AND ($2='' OR id<$2) ORDER BY id DESC LIMIT $3`, owner, cursor, limit+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scan(rows, false)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, item.Summary)
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

func (p *Postgres) Get(ctx context.Context, owner int64, id string) (domain.Dashboard, error) {
	return scan(p.db.QueryRowContext(ctx, `SELECT `+columns+` FROM dashboards WHERE owner_id=$1 AND id=$2`, owner, id), true)
}

func (p *Postgres) Mutate(ctx context.Context, owner int64, id string, change func(*domain.Dashboard) error) (domain.Dashboard, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Dashboard{}, err
	}
	defer tx.Rollback()
	d, err := scan(tx.QueryRowContext(ctx, `SELECT `+columns+` FROM dashboards WHERE owner_id=$1 AND id=$2 FOR UPDATE`, owner, id), true)
	if err != nil {
		return domain.Dashboard{}, err
	}
	if err = change(&d); err != nil {
		return domain.Dashboard{}, err
	}
	tags, widgets, layout, err := encode(d)
	if err != nil {
		return domain.Dashboard{}, err
	}
	result, err := scan(tx.QueryRowContext(ctx, `UPDATE dashboards SET title=$3,description=$4,tags=$5,widgets=$6,layout=$7,revision=revision+1,updated_at=clock_timestamp() WHERE owner_id=$1 AND id=$2 RETURNING `+columns, owner, id, d.Title, d.Description, tags, widgets, layout), true)
	if err != nil {
		return domain.Dashboard{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.Dashboard{}, err
	}
	return result, nil
}

func (p *Postgres) ReplaceMetadata(ctx context.Context, owner int64, id string, input domain.Metadata) (domain.Dashboard, error) {
	return p.Mutate(ctx, owner, id, func(d *domain.Dashboard) error { d.Metadata = input; return nil })
}

func (p *Postgres) Delete(ctx context.Context, owner int64, id string) error {
	result, err := p.db.ExecContext(ctx, `DELETE FROM dashboards WHERE owner_id=$1 AND id=$2`, owner, id)
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
