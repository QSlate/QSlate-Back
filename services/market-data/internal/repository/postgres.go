package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/lib/pq"
	"qslate/services/market-data/internal/domain"
	"strings"
	"time"
)

type Postgres struct{ db *sql.DB }

func New(db *sql.DB) *Postgres { return &Postgres{db} }

const fields = `d.id,v.id,d.name,v.status,d.format,d.columns,d.preview,v.row_count,v.mapping,v.sha256,v.error,d.created_at,d.updated_at`

type scanner interface{ Scan(...any) error }

func scan(s scanner) (domain.Dataset, error) {
	var d domain.Dataset
	var cols, preview, mapping []byte
	e := s.Scan(&d.ID, &d.VersionID, &d.Name, &d.Status, &d.Format, &cols, &preview, &d.RowCount, &mapping, &d.SHA256, &d.Error, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return d, domain.ErrNotFound
	}
	if e != nil {
		return d, e
	}
	if e = json.Unmarshal(cols, &d.Columns); e != nil {
		return d, e
	}
	if e = json.Unmarshal(preview, &d.Preview); e != nil {
		return d, e
	}
	if len(mapping) > 0 {
		if e = json.Unmarshal(mapping, &d.Mapping); e != nil {
			return d, e
		}
	}
	d.Symbol = "custom:" + d.ID
	return d, nil
}
func (p *Postgres) Get(ctx context.Context, owner int64, id string) (domain.Dataset, error) {
	return scan(p.db.QueryRowContext(ctx, `SELECT `+fields+` FROM md_datasets d JOIN md_versions v ON v.id=d.current_version WHERE d.id=$1 AND d.owner_id=$2`, id, owner))
}
func (p *Postgres) Create(ctx context.Context, owner int64, name, format string, raw []byte, parsed domain.Parsed, maxDatasets int, maxBytes int64) (domain.Dataset, error) {
	tx, e := p.db.BeginTx(ctx, nil)
	if e != nil {
		return domain.Dataset{}, e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, owner); e != nil {
		return domain.Dataset{}, e
	}
	var count int
	var size int64
	if e = tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(octet_length(raw)),0) FROM md_datasets WHERE owner_id=$1`, owner).Scan(&count, &size); e != nil {
		return domain.Dataset{}, e
	}
	if count >= maxDatasets || size+int64(len(raw)) > maxBytes {
		return domain.Dataset{}, domain.ErrQuota
	}
	id, version := domain.ID(), domain.ID()
	cols, _ := json.Marshal(parsed.Columns)
	preview, _ := json.Marshal(parsed.Preview)
	if _, e = tx.ExecContext(ctx, `INSERT INTO md_datasets(id,owner_id,name,format,raw,columns,preview,current_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, owner, name, format, raw, string(cols), string(preview), version); e != nil {
		return domain.Dataset{}, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO md_versions(id,dataset_id,status,row_count) VALUES($1,$2,'uploaded',$3)`, version, id, parsed.RowCount); e != nil {
		return domain.Dataset{}, e
	}
	if e = tx.Commit(); e != nil {
		return domain.Dataset{}, e
	}
	return p.Get(ctx, owner, id)
}
func (p *Postgres) List(ctx context.Context, owner int64, cursor string, limit int) (domain.Page, error) {
	out := domain.Page{Items: []domain.Dataset{}}
	rows, e := p.db.QueryContext(ctx, `SELECT `+fields+` FROM md_datasets d JOIN md_versions v ON v.id=d.current_version WHERE owner_id=$1 AND ($2='' OR d.id<$2) ORDER BY d.id DESC LIMIT $3`, owner, cursor, limit+1)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		d, e := scan(rows)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, d)
	}
	if e = rows.Err(); e != nil {
		return out, e
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextCursor = out.Items[limit-1].ID
	}
	return out, nil
}
func (p *Postgres) Map(ctx context.Context, owner int64, id, base string, m domain.Mapping, maxVersions int) (domain.Dataset, error) {
	tx, e := p.db.BeginTx(ctx, nil)
	if e != nil {
		return domain.Dataset{}, e
	}
	defer tx.Rollback()
	var current, status string
	e = tx.QueryRowContext(ctx, `SELECT d.current_version,v.status FROM md_datasets d JOIN md_versions v ON v.id=d.current_version WHERE d.id=$1 AND owner_id=$2 FOR UPDATE OF d`, id, owner).Scan(&current, &status)
	if errors.Is(e, sql.ErrNoRows) {
		return domain.Dataset{}, domain.ErrNotFound
	}
	if e != nil {
		return domain.Dataset{}, e
	}
	if current != base || status == "indexing" {
		return domain.Dataset{}, domain.ErrConflict
	}
	var count int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM md_versions WHERE dataset_id=$1`, id).Scan(&count); e != nil {
		return domain.Dataset{}, e
	}
	if count >= maxVersions {
		return domain.Dataset{}, domain.ErrQuota
	}
	v := domain.ID()
	raw, _ := json.Marshal(m)
	if _, e = tx.ExecContext(ctx, `INSERT INTO md_versions(id,dataset_id,status,mapping) VALUES($1,$2,'mapped',$3)`, v, id, string(raw)); e != nil {
		return domain.Dataset{}, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE md_datasets SET current_version=$2,updated_at=now() WHERE id=$1`, id, v); e != nil {
		return domain.Dataset{}, e
	}
	if e = tx.Commit(); e != nil {
		return domain.Dataset{}, e
	}
	return p.Get(ctx, owner, id)
}
func (p *Postgres) Enqueue(ctx context.Context, owner int64, id, key string) (domain.Job, error) {
	var job domain.Job
	tx, e := p.db.BeginTx(ctx, nil)
	if e != nil {
		return job, e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, owner); e != nil {
		return job, e
	}
	e = tx.QueryRowContext(ctx, `SELECT id,dataset_id,version_id,status,error FROM md_jobs WHERE owner_id=$1 AND key=$2`, owner, key).Scan(&job.ID, &job.DatasetID, &job.VersionID, &job.Status, &job.Error)
	if e == nil {
		if job.DatasetID != id {
			return job, domain.ErrConflict
		}
		return job, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return job, e
	}
	var status string
	e = tx.QueryRowContext(ctx, `SELECT v.id,v.status FROM md_datasets d JOIN md_versions v ON v.id=d.current_version WHERE d.id=$1 AND d.owner_id=$2 FOR UPDATE OF d,v`, id, owner).Scan(&job.VersionID, &status)
	if errors.Is(e, sql.ErrNoRows) {
		return job, domain.ErrNotFound
	}
	if e != nil {
		return job, e
	}
	if status != "mapped" && status != "failed" {
		return job, domain.ErrConflict
	}
	job.ID = domain.ID()
	job.DatasetID = id
	job.Status = "queued"
	if _, e = tx.ExecContext(ctx, `INSERT INTO md_jobs(id,owner_id,dataset_id,version_id,key,status) VALUES($1,$2,$3,$4,$5,'queued')`, job.ID, owner, id, job.VersionID, key); e != nil {
		return job, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE md_versions SET status='indexing',error='' WHERE id=$1`, job.VersionID); e != nil {
		return job, e
	}
	return job, tx.Commit()
}
func (p *Postgres) Delete(ctx context.Context, owner int64, id string) error {
	r, e := p.db.ExecContext(ctx, `DELETE FROM md_datasets WHERE owner_id=$1 AND id=$2`, owner, id)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
func (p *Postgres) Claim(ctx context.Context, seconds int) (domain.Claim, error) {
	var c domain.Claim
	c.Token = domain.ID()
	tx, e := p.db.BeginTx(ctx, nil)
	if e != nil {
		return c, e
	}
	defer tx.Rollback()
	e = tx.QueryRowContext(ctx, `UPDATE md_jobs SET status='running',claim_token=$1,lease_until=now()+make_interval(secs=>$2) WHERE id=(SELECT id FROM md_jobs WHERE status='queued' ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id,dataset_id,version_id,status`, c.Token, seconds+15).Scan(&c.ID, &c.DatasetID, &c.VersionID, &c.Status)
	if errors.Is(e, sql.ErrNoRows) {
		return c, domain.ErrNotFound
	}
	if e != nil {
		return c, e
	}
	var mapping []byte
	if e = tx.QueryRowContext(ctx, `SELECT d.raw,d.format,v.mapping FROM md_datasets d JOIN md_versions v ON v.dataset_id=d.id WHERE v.id=$1`, c.VersionID).Scan(&c.Raw, &c.Format, &mapping); e != nil {
		return c, e
	}
	if e = json.Unmarshal(mapping, &c.Mapping); e != nil {
		return c, e
	}
	return c, tx.Commit()
}
func (p *Postgres) Recover(ctx context.Context) error {
	_, e := p.db.ExecContext(ctx, `WITH expired AS (UPDATE md_jobs SET status='failed',error='validation interrupted',claim_token='' WHERE status='running' AND lease_until<now() RETURNING version_id) UPDATE md_versions SET status='failed',error='validation interrupted' WHERE id IN (SELECT version_id FROM expired) AND status='indexing'`)
	return e
}
func (p *Postgres) Finish(ctx context.Context, c domain.Claim, rows [][]string, failure string, maxCSV int) error {
	tx, e := p.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var valid bool
	e = tx.QueryRowContext(ctx, `SELECT true FROM md_jobs WHERE id=$1 AND claim_token=$2 AND status='running' AND lease_until>now() FOR UPDATE`, c.ID, c.Token).Scan(&valid)
	if errors.Is(e, sql.ErrNoRows) {
		return domain.ErrConflict
	}
	if e != nil {
		return e
	}
	status := "completed"
	versionStatus := "ready"
	var csvBody, sum string
	if failure == "" {
		var buf strings.Builder
		w := csv.NewWriter(&buf)
		_ = w.Write([]string{"Datetime", "Open", "High", "Low", "Close", "Volume"})
		for _, row := range rows {
			if len(row) != 6 {
				return domain.ErrInvalid
			}
			_ = w.Write(row)
		}
		w.Flush()
		if e = w.Error(); e != nil {
			return e
		}
		if buf.Len() > maxCSV {
			return domain.ErrInvalid
		}
		csvBody = buf.String()
		digest := sha256.Sum256([]byte(csvBody))
		sum = hex.EncodeToString(digest[:])
		stmt, e := tx.PrepareContext(ctx, pq.CopyIn("md_points", "dataset_id", "version_id", "ts", "o", "h", "l", "c", "v"))
		if e != nil {
			return e
		}
		defer stmt.Close()
		for _, r := range rows {
			if _, e = stmt.ExecContext(ctx, c.DatasetID, c.VersionID, r[0], r[1], r[2], r[3], r[4], r[5]); e != nil {
				return e
			}
		}
		if _, e = stmt.ExecContext(ctx); e != nil {
			return e
		}
		if e = stmt.Close(); e != nil {
			return e
		}
	} else {
		status = "failed"
		versionStatus = "failed"
	}
	if _, e = tx.ExecContext(ctx, `UPDATE md_versions SET status=$2,row_count=$3,csv=$4,sha256=$5,error=$6 WHERE id=$1`, c.VersionID, versionStatus, len(rows), csvBody, sum, failure); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE md_jobs SET status=$2,error=$3,claim_token='',lease_until=NULL WHERE id=$1`, c.ID, status, failure); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE md_datasets SET updated_at=now() WHERE id=$1`, c.DatasetID); e != nil {
		return e
	}
	return tx.Commit()
}
func (p *Postgres) Snapshot(ctx context.Context, owner int64, id, version string) (domain.Snapshot, error) {
	var out domain.Snapshot
	var status string
	e := p.db.QueryRowContext(ctx, `SELECT d.id,v.id,v.status,v.csv,v.sha256,v.row_count FROM md_datasets d JOIN md_versions v ON v.dataset_id=d.id WHERE d.owner_id=$1 AND d.id=$2 AND v.id=$3`, owner, id, version).Scan(&out.DatasetID, &out.VersionID, &status, &out.CSV, &out.SHA256, &out.RowCount)
	if errors.Is(e, sql.ErrNoRows) {
		return out, domain.ErrNotFound
	}
	if e != nil {
		return out, e
	}
	if status != "ready" {
		return out, domain.ErrConflict
	}
	return out, nil
}
func (p *Postgres) Symbols(ctx context.Context, owner int64) ([]domain.Symbol, error) {
	rows, e := p.db.QueryContext(ctx, `SELECT d.id,v.id,d.name FROM md_datasets d JOIN md_versions v ON v.id=d.current_version WHERE d.owner_id=$1 AND v.status='ready' ORDER BY d.id`, owner)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Symbol{}
	for rows.Next() {
		var s domain.Symbol
		if e = rows.Scan(&s.DatasetID, &s.VersionID, &s.Name); e != nil {
			return nil, e
		}
		s.Symbol = "custom:" + s.DatasetID
		out = append(out, s)
	}
	return out, rows.Err()
}
func (p *Postgres) History(ctx context.Context, owner int64, symbol, version, resolution string, start, end time.Time, seconds int, limit int, cursor *time.Time) (domain.History, error) {
	out := domain.History{Symbol: symbol, Resolution: resolution, Items: []domain.Candle{}}
	id := strings.TrimPrefix(symbol, "custom:")
	if id == symbol || !domain.ValidID(id) {
		return out, domain.ErrNotFound
	}
	if version == "" {
		d, e := p.Get(ctx, owner, id)
		if e != nil {
			return out, e
		}
		version = d.VersionID
	}
	if _, e := p.Snapshot(ctx, owner, id, version); e != nil {
		return out, e
	}
	after := start.Add(-time.Microsecond)
	if seconds > 0 {
		after = start.Truncate(time.Duration(seconds) * time.Second).Add(-time.Microsecond)
	}
	if cursor != nil {
		after = *cursor
	}
	var rows *sql.Rows
	var e error
	if seconds == 0 {
		rows, e = p.db.QueryContext(ctx, `SELECT ts,o::text,h::text,l::text,c::text,v::text FROM md_points WHERE dataset_id=$1 AND version_id=$2 AND ts>=$3 AND ts<$4 AND ts>$5 ORDER BY ts LIMIT $6`, id, version, start, end, after, limit+1)
	} else {
		rows, e = p.db.QueryContext(ctx, `WITH source AS (SELECT *,date_bin(make_interval(secs=>$6),ts,'1970-01-01'::timestamptz) AS bucket FROM md_points WHERE dataset_id=$1 AND version_id=$2 AND ts>=$3 AND ts<$4),aggregated AS(SELECT bucket,(array_agg(o ORDER BY ts))[1] AS o,max(h) AS h,min(l) AS l,(array_agg(c ORDER BY ts DESC))[1] AS c,sum(v) AS v FROM source GROUP BY bucket) SELECT bucket,o::text,h::text,l::text,c::text,v::text FROM aggregated WHERE bucket>$5 ORDER BY bucket LIMIT $7`, id, version, start, end, after, seconds, limit+1)
	}
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var c domain.Candle
		if e = rows.Scan(&c.Time, &c.Open, &c.High, &c.Low, &c.Close, &c.Volume); e != nil {
			return out, e
		}
		out.Items = append(out.Items, c)
	}
	if e = rows.Err(); e != nil {
		return out, e
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		s := out.Items[limit-1].Time.UTC().Format(time.RFC3339Nano)
		out.NextCursor = &s
	}
	return out, nil
}
