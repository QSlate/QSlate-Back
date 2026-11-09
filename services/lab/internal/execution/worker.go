package execution

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"qslate/services/lab/internal/domain"
	"qslate/services/lab/internal/repository"
	"qslate/services/lab/internal/service"
	"sync/atomic"
	"time"
)

type Worker struct {
	Repo   *repository.Postgres
	Runner *Docker
	Config service.Config
	Logger *slog.Logger
}

func (w *Worker) Run(ctx context.Context) error {
	workerID := domain.ID()
	hash := w.Config.ExecutionHash()
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = w.Repo.Heartbeat(c, workerID, hash, false)
	}()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	// Heartbeats continue during long runs without sharing the runner's image state.
	heartbeatCtx, cancelHeartbeat := context.WithCancel(ctx)
	heartbeatStopped := make(chan struct{})
	defer func() { cancelHeartbeat(); <-heartbeatStopped }()
	var ready atomic.Bool
	lastError := ""
	go func() {
		defer close(heartbeatStopped)
		t := time.NewTicker(3 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-t.C:
				c, cancel := context.WithTimeout(heartbeatCtx, 2*time.Second)
				_ = w.Repo.Heartbeat(c, workerID, hash, ready.Load())
				cancel()
			}
		}
	}()
	for {
		if ctx.Err() != nil {
			return nil
		}
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		e := w.Runner.Preflight(c)
		cancel()
		ready.Store(e == nil)
		if e != nil && e.Error() != lastError {
			w.Logger.Warn("execution admission unavailable", "reason", e.Error())
			lastError = e.Error()
		}
		if e == nil && lastError != "" {
			w.Logger.Info("execution admission ready")
			lastError = ""
		}
		c, cancel = context.WithTimeout(ctx, 3*time.Second)
		_ = w.Repo.Heartbeat(c, workerID, hash, e == nil)
		cancel()
		c, cancel = context.WithTimeout(ctx, 5*time.Second)
		expired, err := w.Repo.Expired(c)
		cancel()
		if err != nil {
			w.Logger.Warn("claim recovery unavailable")
		}
		for _, claim := range expired {
			c, cancel = context.WithTimeout(ctx, 5*time.Second)
			err = w.Runner.Cleanup(c, claim)
			if err == nil {
				err = w.Repo.Recover(c, claim)
			}
			cancel()
			if err != nil {
				w.Logger.Warn("claim cleanup pending", "backtest_id", claim.ID)
			}
		}
		if e == nil {
			c, cancel = context.WithTimeout(ctx, 5*time.Second)
			claim, err := w.Repo.Claim(c, hash, w.Config.Execution.TimeoutSeconds, w.Runner.ImageID())
			cancel()
			if err == nil {
				w.execute(ctx, claim)
				continue
			}
			if !errors.Is(err, domain.ErrNotFound) {
				w.Logger.Warn("claim unavailable")
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}
func (w *Worker) execute(parent context.Context, c domain.Claim) {
	ctx, cancel := context.WithTimeout(parent, time.Duration(w.Config.Execution.TimeoutSeconds)*time.Second)
	defer cancel()
	stopped := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(stopped)
		t := time.NewTicker(300 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				active, e := w.Repo.Active(ctx, c)
				if e != nil || !active {
					cancel()
					return
				}
			}
		}
	}()
	result, e := w.Runner.Run(ctx, c, func(v int) error { return w.Repo.Progress(ctx, c, v) })
	close(done)
	<-stopped
	// Kill the container before releasing the claim, including when the docker CLI was killed.
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cleanupCancel()
	if err := w.Runner.Cleanup(cleanupCtx, c); err != nil {
		w.Logger.Warn("container cleanup pending", "backtest_id", c.ID)
		return
	}
	if e != nil {
		result = domain.Result{Status: "failed", Error: "execution failed or resource/deadline limit exceeded", Summary: []byte(`{}`), Trades: []json.RawMessage{}, Equity: []json.RawMessage{}, Telemetry: []json.RawMessage{}}
	}
	if e = w.Repo.Finish(cleanupCtx, c, result); e != nil {
		w.Logger.Warn("result persistence pending", "backtest_id", c.ID)
		return
	}
	if e = w.Repo.Release(cleanupCtx, c); e != nil {
		w.Logger.Warn("claim release pending", "backtest_id", c.ID)
	}
}
