package contracts

import (
	"context"
	"time"

	"go.uber.org/zap"
)

const (
	// sweepBatch caps how many due contracts one tick processes.
	sweepBatch = 100
	// sweepTimeout bounds the work of a single tick.
	sweepTimeout = 30 * time.Second
)

// Sweeper is the background ticker that closes contracts whose deadline has
// passed. It is the bot's first scheduled worker; it owns a session-lifetime
// context (cancelled on Stop), not the fx OnStart context.
type Sweeper struct {
	feature  *Feature
	interval time.Duration
	log      *zap.Logger

	cancel context.CancelFunc
	done   chan struct{}
}

func newSweeper(f *Feature, cfg Config, log *zap.Logger) *Sweeper {
	return &Sweeper{feature: f, interval: cfg.SweepInterval, log: log}
}

// Start launches the ticker goroutine.
func (s *Sweeper) Start(context.Context) error {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})
	go s.loop(ctx)
	s.log.Info("contract sweeper started", zap.Duration("interval", s.interval))
	return nil
}

func (s *Sweeper) loop(ctx context.Context) {
	defer close(s.done)
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sctx, cancel := context.WithTimeout(ctx, sweepTimeout)
			s.feature.sweep(sctx)
			cancel()
		}
	}
}

// Stop cancels the ticker and waits for an in-flight sweep to unwind.
func (s *Sweeper) Stop(context.Context) error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.done != nil {
		<-s.done
	}
	return nil
}

// sweep expires every open contract past its deadline. MarkExpired is the
// authoritative, idempotent transition: in one transaction it flips the status
// and enqueues the thread-close outbox task, so the Discord side effect is
// durable and runs on the worker (off this path). Only the winning update
// enqueues, so a double-run or second instance can't double-close.
func (h *Feature) sweep(ctx context.Context) {
	due, err := h.repo.DueContracts(ctx, time.Now(), sweepBatch)
	if err != nil {
		h.log.Error("sweep: list due contracts", zap.Error(err))
		return
	}
	for _, id := range due {
		if _, err := h.repo.MarkExpired(ctx, id, time.Now()); err != nil {
			h.log.Error("sweep: mark expired",
				zap.String("contract_id", id.String()), zap.Error(err))
		}
	}
}
