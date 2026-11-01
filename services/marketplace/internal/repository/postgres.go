package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/lib/pq"
	"qslate/services/marketplace/internal/domain"
)

type Postgres struct{ db *sql.DB }

func New(db *sql.DB) *Postgres { return &Postgres{db} }

var _ domain.Repository = (*Postgres)(nil)

const columns = `i.id,i.kind,i.resource_id,i.current_version_id,i.owner_id,i.name,i.description,i.category,i.visibility,i.revision,
 (SELECT count(*) FROM market_installations x WHERE x.item_id=i.id AND x.removed_at IS NULL),
 (SELECT count(*) FROM market_reviews r WHERE r.item_id=i.id),
 COALESCE((SELECT avg(r.rating)::float8 FROM market_reviews r WHERE r.item_id=i.id),0),i.created_at,i.updated_at`

type scanner interface{ Scan(...any) error }

func scan(row scanner) (domain.Item, error) {
	var i domain.Item
	e := row.Scan(&i.ID, &i.Kind, &i.ResourceID, &i.VersionID, &i.AuthorID, &i.Name, &i.Description, &i.Category, &i.Visibility, &i.Revision, &i.InstallCount, &i.ReviewCount, &i.AverageRating, &i.CreatedAt, &i.UpdatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		e = domain.ErrNotFound
	}
	return i, e
}
func conflict(e error) error {
	var p *pq.Error
	if errors.As(e, &p) && p.Code == "23505" {
		return domain.ErrConflict
	}
	return e
}
func (p *Postgres) Get(ctx context.Context, key string) (domain.Item, error) {
	return scan(p.db.QueryRowContext(ctx, `SELECT `+columns+` FROM market_items i WHERE i.id=$1 AND i.deleted_at IS NULL`, key))
}
func (p *Postgres) Owned(ctx context.Context, uid int64, key string) (domain.Item, error) {
	return scan(p.db.QueryRowContext(ctx, `SELECT `+columns+` FROM market_items i WHERE i.id=$1 AND i.owner_id=$2`, key, uid))
}
func (p *Postgres) List(ctx context.Context, f domain.Filter) (domain.Page, error) {
	page := domain.Page{Items: []domain.Item{}}
	rows, e := p.db.QueryContext(ctx, `SELECT `+columns+` FROM market_items i WHERE i.deleted_at IS NULL AND i.visibility='public'
 AND ($1='' OR strpos(lower(i.name||' '||i.description),lower($1))>0) AND ($2='' OR i.kind=$2) AND ($3='' OR i.category=$3)
 AND ($4='' OR i.id<$4) AND (NOT $5 OR i.id=ANY($6) OR EXISTS(SELECT 1 FROM market_installations x WHERE x.item_id=i.id AND x.removed_at IS NULL)) ORDER BY i.id DESC LIMIT $7`, f.Query, f.Kind, f.Category, f.Cursor, f.Featured, pq.Array(f.FeaturedIDs), f.Limit+1)
	if e != nil {
		return page, e
	}
	defer rows.Close()
	for rows.Next() {
		v, e := scan(rows)
		if e != nil {
			return page, e
		}
		page.Items = append(page.Items, v)
	}
	if e = rows.Err(); e != nil {
		return page, e
	}
	if len(page.Items) > f.Limit {
		page.Items = page.Items[:f.Limit]
		page.NextCursor = page.Items[f.Limit-1].ID
	}
	return page, nil
}
func insertSnapshot(ctx context.Context, tx *sql.Tx, key, version string, s domain.Snapshot) error {
	b, e := json.Marshal(s)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO market_versions(item_id,version_id,snapshot) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, key, version, string(b)); e != nil {
		return e
	}
	var same bool
	if e = tx.QueryRowContext(ctx, `SELECT snapshot=$3::jsonb FROM market_versions WHERE item_id=$1 AND version_id=$2`, key, version, string(b)).Scan(&same); e != nil {
		return e
	}
	if !same {
		return domain.ErrConflict
	}
	return nil
}
func (p *Postgres) Publish(ctx context.Context, uid int64, key string, in domain.PublishInput, s domain.Snapshot) (domain.Item, error) {
	tx, e := p.db.BeginTx(ctx, nil)
	if e != nil {
		return domain.Item{}, e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `INSERT INTO market_items(id,owner_id,kind,resource_id,current_version_id,name,description,category,visibility) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, key, uid, in.Kind, in.ResourceID, in.VersionID, in.Name, in.Description, in.Category, in.Visibility); e != nil {
		return domain.Item{}, conflict(e)
	}
	if e = insertSnapshot(ctx, tx, key, in.VersionID, s); e != nil {
		return domain.Item{}, e
	}
	v, e := scan(tx.QueryRowContext(ctx, `SELECT `+columns+` FROM market_items i WHERE i.id=$1`, key))
	if e != nil {
		return v, e
	}
	return v, tx.Commit()
}
func (p *Postgres) Patch(ctx context.Context, uid int64, key string, in domain.Patch, s *domain.Snapshot) (domain.Item, error) {
	tx, e := p.db.BeginTx(ctx, nil)
	if e != nil {
		return domain.Item{}, e
	}
	defer tx.Rollback()
	var revision int64
	e = tx.QueryRowContext(ctx, `SELECT revision FROM market_items WHERE id=$1 AND owner_id=$2 FOR UPDATE`, key, uid).Scan(&revision)
	if errors.Is(e, sql.ErrNoRows) {
		return domain.Item{}, domain.ErrNotFound
	}
	if e != nil {
		return domain.Item{}, e
	}
	if revision != in.BaseRevision {
		return domain.Item{}, domain.ErrConflict
	}
	if s != nil {
		if in.VersionID == nil {
			return domain.Item{}, domain.ErrInvalid
		}
		if e = insertSnapshot(ctx, tx, key, *in.VersionID, *s); e != nil {
			return domain.Item{}, e
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE market_items SET name=COALESCE($3,name),description=COALESCE($4,description),category=COALESCE($5,category),visibility=COALESCE($6,visibility),current_version_id=COALESCE($7,current_version_id),revision=revision+1,updated_at=clock_timestamp(),deleted_at=CASE WHEN $6::text IS NOT NULL THEN NULL ELSE deleted_at END WHERE id=$1 AND owner_id=$2`, key, uid, in.Name, in.Description, in.Category, in.Visibility, in.VersionID); e != nil {
		return domain.Item{}, e
	}
	v, e := scan(tx.QueryRowContext(ctx, `SELECT `+columns+` FROM market_items i WHERE i.id=$1`, key))
	if e != nil {
		return v, e
	}
	return v, tx.Commit()
}
func (p *Postgres) Delete(ctx context.Context, uid int64, key string) error {
	r, e := p.db.ExecContext(ctx, `UPDATE market_items SET deleted_at=clock_timestamp(),updated_at=clock_timestamp(),revision=revision+1 WHERE id=$1 AND owner_id=$2 AND deleted_at IS NULL`, key, uid)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e == nil && n == 0 {
		return domain.ErrNotFound
	}
	return e
}

const installationColumns = `x.item_id,x.version_id,i.kind,x.installed_at,v.snapshot`

func installation(row scanner) (domain.Installation, error) {
	var v domain.Installation
	var b []byte
	e := row.Scan(&v.ItemID, &v.VersionID, &v.Kind, &v.InstalledAt, &b)
	if errors.Is(e, sql.ErrNoRows) {
		return v, domain.ErrNotFound
	}
	if e != nil {
		return v, e
	}
	e = json.Unmarshal(b, &v.Snapshot)
	v.CatalogID = "market-" + v.ItemID
	return v, e
}

const installationJoin = ` FROM market_installations x JOIN market_items i ON i.id=x.item_id JOIN market_versions v ON v.item_id=x.item_id AND v.version_id=x.version_id `

func (p *Postgres) Install(ctx context.Context, uid int64, key, version string) (domain.Installation, error) {
	tx, e := p.db.BeginTx(ctx, nil)
	if e != nil {
		return domain.Installation{}, e
	}
	defer tx.Rollback()
	var deleted sql.NullTime
	e = tx.QueryRowContext(ctx, `SELECT deleted_at FROM market_items WHERE id=$1 FOR UPDATE`, key).Scan(&deleted)
	if errors.Is(e, sql.ErrNoRows) {
		return domain.Installation{}, domain.ErrNotFound
	}
	if e != nil {
		return domain.Installation{}, e
	}
	// An exact retry can retrieve its private snapshot even after unpublication.
	old, e := installation(tx.QueryRowContext(ctx, `SELECT `+installationColumns+installationJoin+`WHERE x.owner_id=$1 AND x.item_id=$2 AND x.version_id=$3 AND x.removed_at IS NULL`, uid, key, version))
	if e == nil {
		return old, tx.Commit()
	}
	if !errors.Is(e, domain.ErrNotFound) {
		return old, e
	}
	if deleted.Valid {
		return old, domain.ErrNotFound
	}
	var found bool
	if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM market_versions WHERE item_id=$1 AND version_id=$2)`, key, version).Scan(&found); e != nil {
		return old, e
	}
	if !found {
		return old, domain.ErrNotFound
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO market_installations(owner_id,item_id,version_id) VALUES($1,$2,$3) ON CONFLICT(owner_id,item_id) DO UPDATE SET version_id=EXCLUDED.version_id,removed_at=NULL,installed_at=clock_timestamp()`, uid, key, version); e != nil {
		return old, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO market_grants(owner_id,item_id,version_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, uid, key, version); e != nil {
		return old, e
	}
	v, e := installation(tx.QueryRowContext(ctx, `SELECT `+installationColumns+installationJoin+`WHERE x.owner_id=$1 AND x.item_id=$2`, uid, key))
	if e != nil {
		return v, e
	}
	return v, tx.Commit()
}
func (p *Postgres) Uninstall(ctx context.Context, uid int64, key string) error {
	r, e := p.db.ExecContext(ctx, `UPDATE market_installations SET removed_at=clock_timestamp() WHERE owner_id=$1 AND item_id=$2 AND removed_at IS NULL`, uid, key)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e == nil && n == 0 {
		return domain.ErrNotFound
	}
	return e
}
func (p *Postgres) Installations(ctx context.Context, uid int64, k, cursor string, limit int) (domain.InstallationPage, error) {
	page := domain.InstallationPage{Items: []domain.Installation{}}
	rows, e := p.db.QueryContext(ctx, `SELECT x.item_id,x.version_id,i.kind,x.installed_at,jsonb_build_object('name',v.snapshot->'name','language',v.snapshot->'language')`+installationJoin+`WHERE x.owner_id=$1 AND x.removed_at IS NULL AND ($2='' OR i.kind=$2 OR ($2='widgets' AND i.kind IN ('widget','indicator'))) AND ($3='' OR x.item_id COLLATE "C"<$3) ORDER BY x.item_id COLLATE "C" DESC LIMIT $4`, uid, k, cursor, limit+1)
	if e != nil {
		return page, e
	}
	defer rows.Close()
	for rows.Next() {
		v, e := installation(rows)
		if e != nil {
			return page, e
		}
		page.Items = append(page.Items, v)
	}
	if e = rows.Err(); e != nil {
		return page, e
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		page.NextCursor = page.Items[limit-1].ItemID
	}
	return page, nil
}
func (p *Postgres) Installed(ctx context.Context, uid int64, key, version string, existing bool) (domain.Installation, error) {
	if !existing {
		return installation(p.db.QueryRowContext(ctx, `SELECT `+installationColumns+installationJoin+`WHERE x.owner_id=$1 AND x.item_id=$2 AND ($3='' OR x.version_id=$3) AND x.removed_at IS NULL`, uid, key, version))
	}
	return installation(p.db.QueryRowContext(ctx, `SELECT g.item_id,g.version_id,i.kind, v.created_at,v.snapshot FROM market_grants g JOIN market_items i ON i.id=g.item_id JOIN market_versions v ON v.item_id=g.item_id AND v.version_id=g.version_id WHERE g.owner_id=$1 AND g.item_id=$2 AND g.version_id=$3`, uid, key, version))
}
func review(row scanner) (domain.Review, error) {
	var r domain.Review
	e := row.Scan(&r.ID, &r.AuthorID, &r.Rating, &r.Comment, &r.CreatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		e = domain.ErrNotFound
	}
	return r, e
}
func (p *Postgres) Reviews(ctx context.Context, key, cursor string, limit int) (domain.ReviewPage, error) {
	page := domain.ReviewPage{Items: []domain.Review{}}
	// A single statement snapshot checks visibility even for an empty page.
	rows, e := p.db.QueryContext(ctx, `SELECT r.id,r.owner_id,r.rating,r.comment,r.created_at FROM market_items i LEFT JOIN LATERAL(SELECT * FROM market_reviews WHERE item_id=i.id AND ($2='' OR id<$2) ORDER BY id DESC LIMIT $3)r ON true WHERE i.id=$1 AND i.deleted_at IS NULL ORDER BY r.id DESC`, key, cursor, limit+1)
	if e != nil {
		return page, e
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		found = true
		var id, comment sql.NullString
		var uid, rating sql.NullInt64
		var created sql.NullTime
		if e = rows.Scan(&id, &uid, &rating, &comment, &created); e != nil {
			return page, e
		}
		if id.Valid {
			page.Items = append(page.Items, domain.Review{ID: id.String, AuthorID: uid.Int64, ReviewInput: domain.ReviewInput{Rating: int(rating.Int64), Comment: comment.String}, CreatedAt: created.Time})
		}
	}
	if e = rows.Err(); e != nil {
		return page, e
	}
	if !found {
		return page, domain.ErrNotFound
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		page.NextCursor = page.Items[limit-1].ID
	}
	return page, nil
}
func (p *Postgres) Review(ctx context.Context, uid int64, key, rid string, in domain.ReviewInput) (domain.Review, error) {
	tx, e := p.db.BeginTx(ctx, nil)
	if e != nil {
		return domain.Review{}, e
	}
	defer tx.Rollback()
	var author int64
	e = tx.QueryRowContext(ctx, `SELECT owner_id FROM market_items WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, key).Scan(&author)
	if errors.Is(e, sql.ErrNoRows) {
		return domain.Review{}, domain.ErrNotFound
	}
	if e != nil {
		return domain.Review{}, e
	}
	if author == uid {
		return domain.Review{}, domain.ErrForbidden
	}
	// Share-lock the entitlement so concurrent uninstall cannot race the review.
	var version string
	e = tx.QueryRowContext(ctx, `SELECT version_id FROM market_installations WHERE owner_id=$1 AND item_id=$2 AND removed_at IS NULL FOR SHARE`, uid, key).Scan(&version)
	if errors.Is(e, sql.ErrNoRows) {
		return domain.Review{}, domain.ErrForbidden
	}
	if e != nil {
		return domain.Review{}, e
	}
	r, e := review(tx.QueryRowContext(ctx, `INSERT INTO market_reviews(id,item_id,owner_id,rating,comment) VALUES($1,$2,$3,$4,$5) RETURNING id,owner_id,rating,comment,created_at`, rid, key, uid, in.Rating, in.Comment))
	if e != nil {
		return r, conflict(e)
	}
	return r, tx.Commit()
}
