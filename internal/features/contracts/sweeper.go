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
	feature      *Feature
	interval     time.Duration
	notifyWithin time.Duration
	log          *zap.Logger

	cancel context.CancelFunc
	done   chan struct{}
}

func newSweeper(f *Feature, cfg Config, log *zap.Logger) *Sweeper {
	return &Sweeper{
		feature:      f,
		interval:     cfg.SweepInterval,
		notifyWithin: cfg.ExpiresNotify,
		log:          log,
	}
}

// Start launches the ticker goroutine. The fx OnStart context is intentionally
// not used: the loop owns a session-lifetime context (cancelled on Stop), not the
// start context, which is done once Start returns — so it is wired via
// fx.StartHook, which accepts a context-less func.
func (s *Sweeper) Start() error {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})
	go s.loop(ctx)
	s.log.Info("contract sweeper started",
		zap.Duration("interval", s.interval),
		zap.Duration("notify_within", s.notifyWithin))
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
			s.feature.sweep(sctx, s.notifyWithin)
			cancel()
		}
	}
}

// Stop cancels the ticker and waits for an in-flight sweep to unwind. Wired via
// fx.StopHook (no context needed — the wait is bounded by the sweep timeout).
func (s *Sweeper) Stop() error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.done != nil {
		<-s.done
	}
	return nil
}

// sweep runs the two time-driven passes for open contracts, each via an
// idempotent, collapsing repository transition so a double-run or second instance
// can't duplicate a side effect:
//
//   - expire — past-deadline contracts flip to expired and enqueue their close.
//   - notice — contracts within notifyWithin of their deadline (and not yet
//     notified) post the one-shot "closing soon" participant ping.
//
// There is no keep-warm re-render pass: the embed renders the deadline as Discord
// timestamp markdown (<t:…:f> / <t:…:R>), which the client keeps current on its
// own, so an open contract never needs a periodic server-side re-render.
//
// The passes partition cleanly: the notice scan requires deadline > now, so an
// already-due contract expires rather than getting a closing-soon ping.
func (h *Feature) sweep(ctx context.Context, notifyWithin time.Duration) {
	now := time.Now()

	due, err := h.repo.DueContracts(ctx, now, sweepBatch)
	if err != nil {
		h.log.Error("sweep: list due contracts", zap.Error(err))
	}
	for _, id := range due {
		if _, err := h.repo.MarkExpired(ctx, id, time.Now()); err != nil {
			h.log.Error("sweep: mark expired",
				zap.String("contract_id", id.String()), zap.Error(err))
		}
	}

	soon, err := h.repo.NotifyDue(ctx, now, notifyWithin, sweepBatch)
	if err != nil {
		h.log.Error("sweep: list notify-due contracts", zap.Error(err))
	}
	for _, id := range soon {
		if _, err := h.repo.MarkNotified(ctx, id, time.Now()); err != nil {
			h.log.Error("sweep: mark notified",
				zap.String("contract_id", id.String()), zap.Error(err))
		}
	}
}
