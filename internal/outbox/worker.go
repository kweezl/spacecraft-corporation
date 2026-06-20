package outbox

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

const (
	// taskTimeout bounds a single handler run.
	taskTimeout = 30 * time.Second
	// backoffBase / backoffMax bound the exponential retry schedule.
	backoffBase = 10 * time.Second
	backoffMax  = time.Hour
)

// Worker scans due tasks on a ticker and runs their handlers, plus a slower
// cleanup pass that purges terminal tasks past their retention. It is a single
// background goroutine (single-instance bot), so it needs no row lease: a task it
// has selected can't be re-selected until it is marked terminal or rescheduled.
type Worker struct {
	store    *store
	handlers map[string]Handler
	cfg      Config
	log      *zap.Logger

	cancel context.CancelFunc
	done   chan struct{}
}

func newWorker(pool *pgxpool.Pool, regs []Registration, cfg Config, log *zap.Logger) *Worker {
	handlers := make(map[string]Handler, len(regs))
	for _, r := range regs {
		handlers[r.Kind] = r.Handler
	}
	return &Worker{store: &store{pool: pool}, handlers: handlers, cfg: cfg, log: log}
}

// Start launches the polling goroutine.
func (w *Worker) Start(context.Context) error {
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.done = make(chan struct{})
	go w.loop(ctx)
	w.log.Info("outbox worker started",
		zap.Duration("poll", w.cfg.PollInterval), zap.Int("handlers", len(w.handlers)))
	return nil
}

// Stop cancels polling and waits for the in-flight pass to unwind.
func (w *Worker) Stop(context.Context) error {
	if w.cancel != nil {
		w.cancel()
	}
	if w.done != nil {
		<-w.done
	}
	return nil
}

func (w *Worker) loop(ctx context.Context) {
	defer close(w.done)
	poll := time.NewTicker(w.cfg.PollInterval)
	defer poll.Stop()
	clean := time.NewTicker(w.cfg.CleanupInterval)
	defer clean.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
			w.processBatch(ctx)
		case <-clean.C:
			w.cleanup(ctx)
		}
	}
}

// processBatch runs the newest task per (kind, chronometric_id) group and
// supersedes the older ones — so a rush of changes to one contract collapses to a
// single effect that reflects the latest state.
func (w *Worker) processBatch(ctx context.Context) {
	due, err := w.store.due(ctx, time.Now(), w.cfg.BatchSize)
	if err != nil {
		w.log.Error("outbox: scan due tasks", zap.Error(err))
		return
	}
	// due is ordered by id ascending, so the last id seen per group is the newest.
	newest := make(map[string]string, len(due))
	for _, d := range due {
		newest[d.group()] = d.ID.String()
	}
	for _, d := range due {
		if ctx.Err() != nil {
			return
		}
		if newest[d.group()] != d.ID.String() {
			if e := w.store.markSuperseded(ctx, d.ID); e != nil {
				w.log.Error("outbox: mark superseded", zap.String("task_id", d.ID.String()), zap.Error(e))
			}
			continue
		}
		w.execute(ctx, d.Task)
	}
}

func (w *Worker) execute(ctx context.Context, t Task) {
	h, ok := w.handlers[t.Kind]
	if !ok {
		// No handler (feature disabled, or a stale kind): abandon rather than spin.
		w.log.Error("outbox: no handler for kind",
			zap.String("task_id", t.ID.String()), zap.String("kind", t.Kind))
		w.fail(ctx, t, t.Attempts, "no handler for kind "+t.Kind)
		return
	}

	hctx, cancel := context.WithTimeout(ctx, taskTimeout)
	err := h(hctx, t)
	cancel()
	if err == nil {
		if e := w.store.markDone(ctx, t.ID); e != nil {
			w.log.Error("outbox: mark done", zap.String("task_id", t.ID.String()), zap.Error(e))
		}
		return
	}

	attempts := t.Attempts + 1
	if isPermanent(err) || attempts >= w.cfg.MaxAttempts {
		w.log.Error("outbox: task abandoned",
			zap.String("task_id", t.ID.String()), zap.String("kind", t.Kind),
			zap.Int("attempts", attempts), zap.Bool("permanent", isPermanent(err)), zap.Error(err))
		w.fail(ctx, t, attempts, err.Error())
		return
	}
	w.log.Warn("outbox: task retry",
		zap.String("task_id", t.ID.String()), zap.String("kind", t.Kind),
		zap.Int("attempts", attempts), zap.Error(err))
	if e := w.store.markRetry(ctx, t.ID, attempts, err.Error(), time.Now().Add(backoff(attempts))); e != nil {
		w.log.Error("outbox: mark retry", zap.String("task_id", t.ID.String()), zap.Error(e))
	}
}

func (w *Worker) fail(ctx context.Context, t Task, attempts int, msg string) {
	if e := w.store.markFailed(ctx, t.ID, attempts, msg); e != nil {
		w.log.Error("outbox: mark failed", zap.String("task_id", t.ID.String()), zap.Error(e))
	}
}

func (w *Worker) cleanup(ctx context.Context) {
	now := time.Now()
	n, err := w.store.cleanup(ctx, now.Add(-w.cfg.DoneRetention), now.Add(-w.cfg.FailedRetention))
	if err != nil {
		w.log.Error("outbox: cleanup", zap.Error(err))
		return
	}
	if n > 0 {
		w.log.Info("outbox: cleaned up tasks", zap.Int64("deleted", n))
	}
}

// backoff is exponential in the attempt count, capped at backoffMax.
func backoff(attempts int) time.Duration {
	d := backoffBase
	for i := 1; i < attempts; i++ {
		d *= 2
		if d >= backoffMax {
			return backoffMax
		}
	}
	if d > backoffMax {
		d = backoffMax
	}
	return d
}
