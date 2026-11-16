package worker

import (
	"context"
	"errors"
	"log/slog"
	"qslate/services/market-data/internal/domain"
	"qslate/services/market-data/internal/repository"
	"qslate/services/market-data/internal/service"
	"time"
)

type Worker struct {
	Repo    *repository.Postgres
	Decoder service.Decoder
	Config  service.Config
	Logger  *slog.Logger
}

func (w Worker) Run(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		c, cancel := context.WithTimeout(ctx, 3*time.Second)
		e := w.Repo.Recover(c)
		cancel()
		if e != nil {
			w.Logger.Warn("validation recovery unavailable")
		}
		c, cancel = context.WithTimeout(ctx, 3*time.Second)
		claim, e := w.Repo.Claim(c, w.Config.ParserTimeoutSeconds)
		cancel()
		if e == nil {
			w.execute(ctx, claim)
			continue
		}
		if !errors.Is(e, domain.ErrNotFound) {
			w.Logger.Warn("validation queue unavailable")
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
func (w Worker) execute(parent context.Context, c domain.Claim) {
	ctx, cancel := context.WithTimeout(parent, time.Duration(w.Config.ParserTimeoutSeconds)*time.Second)
	parsed, e := w.Decoder.Parse(ctx, c.Format, c.Raw, &c.Mapping)
	cancel()
	failure := ""
	if e != nil {
		failure = "dataset validation failed or decoder unavailable"
	}
	ctx, cancel = context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	if failure != "" {
		parsed.Rows = nil
	}
	e = w.Repo.Finish(ctx, c, parsed.Rows, failure, w.Config.MaxSnapshotBytes)
	if e != nil && !errors.Is(e, domain.ErrConflict) {
		_ = w.Repo.Finish(ctx, c, nil, "indexing failed or normalized dataset limit exceeded", w.Config.MaxSnapshotBytes)
		w.Logger.Warn("validation result could not be persisted", "dataset_id", c.DatasetID)
	}
}
