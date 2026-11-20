package repository

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"qslate/services/lab/internal/domain"
	"strconv"
	"time"
)

type Postgres struct{ db *sql.DB }

func New(db *sql.DB) *Postgres { return &Postgres{db} }

const columns = `id,status,progress,input,optimization_id,created_at,updated_at,error,runtime_image,frozen->>'engine_revision',frozen->>'source_sha256',frozen->>'dataset_sha256'`

type scanner interface{ Scan(...any) error }

func decode(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d.Decode(out)
}

func scan(s scanner) (domain.Job, error) {
	var j domain.Job
	var input []byte
	e := s.Scan(&j.ID, &j.Status, &j.Progress, &input, &j.OptimizationID, &j.CreatedAt, &j.UpdatedAt, &j.Error, &j.EngineImage, &j.EngineRevision, &j.StrategySHA256, &j.DatasetSHA256)
	if errors.Is(e, sql.ErrNoRows) {
		return j, domain.ErrNotFound
	}
	if e != nil {
		return j, e
	}
	e = decode(input, &j.Input)
	return j, e
}
func (p *Postgres) Get(ctx context.Context, owner int64, id string) (domain.Job, error) {
	return scan(p.db.QueryRowContext(ctx, `SELECT `+columns+` FROM lab_runs WHERE owner_id=$1 AND id=$2`, owner, id))
}
func (p *Postgres) Replay(ctx context.Context, owner int64, op, key, hash string) (domain.Batch, bool, error) {
	var out domain.Batch
	var stored string
	var raw []byte
	e := p.db.QueryRowContext(ctx, `SELECT hash,response FROM lab_requests WHERE owner_id=$1 AND operation=$2 AND key=$3`, owner, op, key).Scan(&stored, &raw)
	if errors.Is(e, sql.ErrNoRows) {
		return out, false, nil
	}
	if e != nil {
		return out, false, e
	}
	if stored != hash {
		return out, false, domain.ErrConflict
	}
	e = decode(raw, &out)
	return out, true, e
}
func (p *Postgres) Enqueue(ctx context.Context, owner int64, op, key, hash, executionHash string, b domain.Batch, frozen []domain.Frozen, maxOutstanding int) (domain.Batch, error) {
	tx, e := p.db.BeginTx(ctx, nil)
	if e != nil {
		return b, e
	}
	defer tx.Rollback()
	// Serialize per owner, including concurrent API replicas and optimization batches.
	if _, e = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, owner); e != nil {
		return b, e
	}
	var stored string
	var raw []byte
	e = tx.QueryRowContext(ctx, `SELECT hash,response FROM lab_requests WHERE owner_id=$1 AND operation=$2 AND key=$3`, owner, op, key).Scan(&stored, &raw)
	if e == nil {
		if stored != hash {
			return b, domain.ErrConflict
		}
		e = decode(raw, &b)
		return b, e
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return b, e
	}
	var count int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM lab_runs WHERE owner_id=$1 AND status IN ('queued','running')`, owner).Scan(&count); e != nil {
		return b, e
	}
	if count+len(frozen) > maxOutstanding {
		return b, domain.ErrQuota
	}
	b.Backtests = make([]domain.Job, 0, len(frozen))
	for _, f := range frozen {
		input, _ := json.Marshal(f.Input)
		snapshot, _ := json.Marshal(f)
		id := domain.ID()
		j, e := scan(tx.QueryRowContext(ctx, `INSERT INTO lab_runs(id,owner_id,status,input,frozen,execution_hash,optimization_id) VALUES($1,$2,'queued',$3,$4,$5,$6) RETURNING `+columns, id, owner, string(input), string(snapshot), executionHash, b.OptimizationID))
		if e != nil {
			return b, e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO lab_events(run_id,type,data) VALUES($1,'status','{"status":"queued","progress":0}')`, id); e != nil {
			return b, e
		}
		b.Backtests = append(b.Backtests, j)
	}
	raw, _ = json.Marshal(b)
	if _, e = tx.ExecContext(ctx, `INSERT INTO lab_requests(owner_id,operation,key,hash,response) VALUES($1,$2,$3,$4,$5)`, owner, op, key, hash, string(raw)); e != nil {
		return b, e
	}
	return b, tx.Commit()
}
func (p *Postgres) List(ctx context.Context, owner int64, cursor string, limit int) (domain.Page, error) {
	out := domain.Page{Items: []domain.Job{}}
	// A monotonic event ID is not a run ordering key; use creation time and ID.
	var before time.Time
	if cursor != "" {
		j, e := p.Get(ctx, owner, cursor)
		if e != nil {
			return out, domain.ErrInvalid
		}
		before = j.CreatedAt
	}
	rows, e := p.db.QueryContext(ctx, `SELECT `+columns+` FROM lab_runs WHERE owner_id=$1 AND ($2='' OR (created_at,id)<($3,$2)) ORDER BY created_at DESC,id DESC LIMIT $4`, owner, cursor, before, limit+1)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		j, e := scan(rows)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, j)
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
func (p *Postgres) Cancel(ctx context.Context, owner int64, id string) (domain.Job, error) {
	tx, e := p.db.BeginTx(ctx, nil)
	if e != nil {
		return domain.Job{}, e
	}
	defer tx.Rollback()
	j, e := scan(tx.QueryRowContext(ctx, `SELECT `+columns+` FROM lab_runs WHERE owner_id=$1 AND id=$2 FOR UPDATE`, owner, id))
	if e != nil {
		return j, e
	}
	if j.Status == "cancelled" {
		return j, nil
	}
	if j.Terminal() {
		return j, domain.ErrConflict
	}
	j, e = scan(tx.QueryRowContext(ctx, `UPDATE lab_runs SET status='cancelled',updated_at=now() WHERE id=$1 RETURNING `+columns, id))
	if e != nil {
		return j, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO lab_events(run_id,type,data) VALUES($1,'status','{"status":"cancelled"}')`, id); e != nil {
		return j, e
	}
	return j, tx.Commit()
}
func (p *Postgres) Delete(ctx context.Context, owner int64, id string) error {
	r, e := p.db.ExecContext(ctx, `DELETE FROM lab_runs WHERE owner_id=$1 AND id=$2 AND status IN ('completed','failed','cancelled') AND claim_token=''`, owner, id)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 1 {
		return nil
	}
	_, e = p.Get(ctx, owner, id)
	if e != nil {
		return e
	}
	return domain.ErrConflict
}
func (p *Postgres) Result(ctx context.Context, owner int64, id string) (domain.Result, error) {
	var out domain.Result
	var raw []byte
	e := p.db.QueryRowContext(ctx, `SELECT result FROM lab_runs WHERE owner_id=$1 AND id=$2`, owner, id).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return out, domain.ErrNotFound
	}
	if e != nil {
		return out, e
	}
	if len(raw) == 0 {
		return out, domain.ErrConflict
	}
	e = json.Unmarshal(raw, &out)
	return out, e
}
func (p *Postgres) Events(ctx context.Context, owner int64, id string, after int64) ([]domain.Event, error) {
	if _, e := p.Get(ctx, owner, id); e != nil {
		return nil, e
	}
	rows, e := p.db.QueryContext(ctx, `SELECT e.id,e.type,e.data,e.created_at FROM lab_events e JOIN lab_runs r ON r.id=e.run_id WHERE r.owner_id=$1 AND r.id=$2 AND e.id>$3 ORDER BY e.id LIMIT 100`, owner, id, after)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Event{}
	for rows.Next() {
		var v domain.Event
		if e = rows.Scan(&v.ID, &v.Type, &v.Data, &v.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (p *Postgres) Heartbeat(ctx context.Context, id, hash string, ready bool) error {
	_, e := p.db.ExecContext(ctx, `INSERT INTO lab_workers(id,execution_hash,ready) VALUES($1,$2,$3) ON CONFLICT(id) DO UPDATE SET execution_hash=$2,ready=$3,seen_at=now()`, id, hash, ready)
	return e
}
func (p *Postgres) Ready(ctx context.Context, hash string) error {
	var ready bool
	e := p.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM lab_workers WHERE execution_hash=$1 AND ready AND seen_at>now()-interval '10 seconds')`, hash).Scan(&ready)
	if e != nil {
		return e
	}
	if !ready {
		return domain.ErrUnavailable
	}
	return nil
}
func (p *Postgres) Claim(ctx context.Context, hash string, timeout int, imageID string) (domain.Claim, error) {
	var c domain.Claim
	c.Token = domain.ID()
	var raw []byte
	tx, e := p.db.BeginTx(ctx, nil)
	if e != nil {
		return c, e
	}
	defer tx.Rollback()
	e = tx.QueryRowContext(ctx, `UPDATE lab_runs SET status='running',claim_token=$2,lease_until=now()+make_interval(secs=>$3),runtime_image=$4,updated_at=now() WHERE id=(SELECT id FROM lab_runs WHERE status='queued' AND execution_hash=$1 ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id,frozen`, hash, c.Token, timeout+30, imageID).Scan(&c.ID, &raw)
	if errors.Is(e, sql.ErrNoRows) {
		return c, domain.ErrNotFound
	}
	if e != nil {
		return c, e
	}
	if e = decode(raw, &c.Frozen); e != nil {
		return c, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO lab_events(run_id,type,data) VALUES($1,'status','{"status":"running","progress":0}')`, c.ID); e != nil {
		return c, e
	}
	return c, tx.Commit()
}
func (p *Postgres) Active(ctx context.Context, c domain.Claim) (bool, error) {
	var active bool
	e := p.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM lab_runs WHERE id=$1 AND claim_token=$2 AND status='running' AND lease_until>now())`, c.ID, c.Token).Scan(&active)
	return active, e
}
func (p *Postgres) Progress(ctx context.Context, c domain.Claim, percent int) error {
	tx, e := p.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	r, e := tx.ExecContext(ctx, `UPDATE lab_runs SET progress=$3,updated_at=now() WHERE id=$1 AND claim_token=$2 AND status='running' AND progress<$3 AND lease_until>now()`, c.ID, c.Token, percent)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return nil
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO lab_events(run_id,type,data) VALUES($1,'progress',$2)`, c.ID, `{"progress":`+strconv.Itoa(percent)+`}`); e != nil {
		return e
	}
	return tx.Commit()
}

// Finish is fenced: a stale worker cannot overwrite cancellation, recovery or a newer claim.
func (p *Postgres) Finish(ctx context.Context, c domain.Claim, result domain.Result) error {
	tx, e := p.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	raw, e := json.Marshal(result)
	if e != nil {
		return e
	}
	progress := 100
	if result.Status != "completed" {
		progress = 0
	}
	r, e := tx.ExecContext(ctx, `UPDATE lab_runs SET status=$3,result=$4,error=$5,progress=$6,updated_at=now() WHERE id=$1 AND claim_token=$2 AND status='running' AND lease_until>now()`, c.ID, c.Token, result.Status, string(raw), result.Error, progress)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return nil
	}
	if result.Logs != "" {
		log, _ := json.Marshal(map[string]string{"logs": result.Logs})
		if _, e = tx.ExecContext(ctx, `INSERT INTO lab_events(run_id,type,data) VALUES($1,'logs',$2)`, c.ID, string(log)); e != nil {
			return e
		}
	}
	// Preserve strategy telemetry in bounded replayable frames before the terminal event.
	for offset := 0; offset < len(result.Telemetry); offset += 100 {
		end := offset + 100
		if end > len(result.Telemetry) {
			end = len(result.Telemetry)
		}
		frame, _ := json.Marshal(map[string]any{"items": result.Telemetry[offset:end]})
		if _, e = tx.ExecContext(ctx, `INSERT INTO lab_events(run_id,type,data) VALUES($1,'telemetry',$2)`, c.ID, string(frame)); e != nil {
			return e
		}
	}
	data, _ := json.Marshal(map[string]any{"status": result.Status, "progress": progress})
	if _, e = tx.ExecContext(ctx, `INSERT INTO lab_events(run_id,type,data) VALUES($1,'status',$2)`, c.ID, string(data)); e != nil {
		return e
	}
	return tx.Commit()
}
func (p *Postgres) Release(ctx context.Context, c domain.Claim) error {
	_, e := p.db.ExecContext(ctx, `UPDATE lab_runs SET claim_token='',lease_until=NULL WHERE id=$1 AND claim_token=$2 AND status IN ('completed','failed','cancelled')`, c.ID, c.Token)
	return e
}

// Expired claims are never automatically re-executed: arbitrary strategies can have side effects.
func (p *Postgres) Expired(ctx context.Context) ([]domain.Claim, error) {
	rows, e := p.db.QueryContext(ctx, `SELECT id,claim_token FROM lab_runs WHERE claim_token<>'' AND lease_until<now()`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Claim{}
	for rows.Next() {
		var c domain.Claim
		if e = rows.Scan(&c.ID, &c.Token); e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (p *Postgres) Recover(ctx context.Context, c domain.Claim) error {
	tx, e := p.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	r, e := tx.ExecContext(ctx, `UPDATE lab_runs SET status=CASE WHEN status='running' THEN 'failed' ELSE status END,error=CASE WHEN status='running' THEN 'execution interrupted' ELSE error END,claim_token='',lease_until=NULL,updated_at=now() WHERE id=$1 AND claim_token=$2 AND lease_until<now()`, c.ID, c.Token)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n > 0 {
		if _, e = tx.ExecContext(ctx, `INSERT INTO lab_events(run_id,type,data) SELECT id,'status',jsonb_build_object('status',status,'error',error) FROM lab_runs WHERE id=$1`, c.ID); e != nil {
			return e
		}
	}
	return tx.Commit()
}
