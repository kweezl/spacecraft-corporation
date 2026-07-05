package emoji

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/bwmarrin/discordgo"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/discord/session"
)

// Config is this module's env config.
//
// Upload is the master switch for managing application emojis from the repo: when
// false the bot is read-only (it just lists the application's emojis to populate
// the Store, including any an admin uploaded via the web). When true it uploads
// repo-bundled assets that are missing, and the two sub-toggles below apply.
//
// Prune (default on) deletes application emojis whose name the repo no longer
// defines. Replace (default off) force-replaces every embedded emoji on each
// start — an emoji's image can't be edited, so this deletes and recreates it
// (minting a new id) without any change detection. Leave Replace off for normal
// runs (it re-uploads unconditionally on every boot); flip it on for a one-shot
// deploy after changing an image, like Upload. Prune and Replace only act when
// Upload is on.
type Config struct {
	Upload  bool `env:"EMOJI_UPLOAD"  envDefault:"false"`
	Prune   bool `env:"EMOJI_PRUNE"   envDefault:"true"`
	Replace bool `env:"EMOJI_REPLACE" envDefault:"false"`
}

const (
	// connectPoll is how often the sync goroutine checks for the gateway becoming
	// ready before it runs (the application id comes from the READY handshake).
	connectPoll = 500 * time.Millisecond
	// retryDelay backs off between sync attempts when listing/uploading fails, so
	// a transient Discord API error self-heals instead of leaving the bot unready.
	retryDelay = 5 * time.Second
)

// emojiAPI is the slice of the Discord session the Syncer needs. *session.Live
// satisfies it; tests use a fake.
type emojiAPI interface {
	Connected() bool
	ApplicationEmojis() ([]*discordgo.Emoji, error)
	ApplicationEmojiCreate(name, image string) (*discordgo.Emoji, error)
	ApplicationEmojiDelete(id string) error
}

var _ emojiAPI = (*session.Live)(nil)

// Syncer populates the Store at startup from the bot's application emojis,
// optionally uploading repo-bundled images first. It runs in the background
// (so it never blocks fx startup) and reports completion through Ready, which
// backs the "emoji" readiness probe.
type Syncer struct {
	api     emojiAPI
	store   *Store
	assets  map[string]string // name → data URI; nil unless Upload is enabled
	upload  bool
	prune   bool
	replace bool
	log     *zap.Logger

	ready   atomic.Bool
	baseCtx context.Context
	cancel  context.CancelFunc
}

func newSyncer(cfg Config, store *Store, live *session.Live, log *zap.Logger) (*Syncer, error) {
	var assets map[string]string
	if cfg.Upload {
		a, err := loadAssets(assetsFS, assetsRoot)
		if err != nil {
			return nil, err
		}
		assets = a
	}
	return &Syncer{
		api:     live,
		store:   store,
		assets:  assets,
		upload:  cfg.Upload,
		prune:   cfg.Prune,
		replace: cfg.Replace,
		log:     log,
	}, nil
}

// Start launches the background sync. It returns immediately: the goroutine waits
// for the gateway, syncs, and flips Ready; the OnStart context is not retained
// (it is done once Start returns), so the goroutine uses a session-lifetime
// context cancelled on Stop.
func (s *Syncer) Start(context.Context) error {
	s.baseCtx, s.cancel = context.WithCancel(context.Background())
	go s.run(s.baseCtx)
	return nil
}

// Stop cancels the sync goroutine.
func (s *Syncer) Stop(context.Context) error {
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}

// Ready reports whether the initial sync has completed. It backs the readiness
// probe; once true it stays true (emoji are a one-time startup sync, not a live
// dependency that can degrade).
func (s *Syncer) Ready() bool { return s.ready.Load() }

// run waits for the gateway, then syncs with retry until it succeeds or the
// context is cancelled.
func (s *Syncer) run(ctx context.Context) {
	if err := s.waitConnected(ctx); err != nil {
		return // cancelled before the gateway connected
	}
	for {
		if err := s.sync(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			s.log.Error("emoji sync failed; retrying", zap.Error(err))
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryDelay):
				continue
			}
		}
		s.ready.Store(true)
		return
	}
}

// waitConnected blocks until the gateway is past its READY handshake (so the
// application id is known) or the context is cancelled.
func (s *Syncer) waitConnected(ctx context.Context) error {
	t := time.NewTicker(connectPoll)
	defer t.Stop()
	for {
		if s.api.Connected() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

// sync lists the application's emojis and rebuilds the Store from them. With
// Upload off it is read-only (it just exposes whatever is on the application,
// including admin-uploaded emojis). With Upload on it reconciles the application
// to the embedded set: create missing, optionally force-replace existing
// (Replace), and optionally prune emojis the repo no longer defines (Prune).
//
// A listing failure is returned for retry; per-emoji create/delete failures are
// logged and skipped (best effort) so one bad emoji does not keep the bot
// unready, and a failed delete keeps the existing emoji in the Store.
func (s *Syncer) sync(ctx context.Context) error {
	existing, err := s.api.ApplicationEmojis()
	if err != nil {
		return fmt.Errorf("list application emojis: %w", err)
	}
	byName := make(map[string]string, len(existing)+len(s.assets))

	if !s.upload {
		for _, e := range existing {
			byName[e.Name] = e.MessageFormat()
		}
		s.store.replace(byName)
		s.log.Info("emoji sync complete (read-only)", zap.Int("count", len(byName)))
		return nil
	}

	existingByName := make(map[string]*discordgo.Emoji, len(existing))
	for _, e := range existing {
		existingByName[e.Name] = e
	}

	// Reconcile the embedded set: create what's missing; with Replace on,
	// delete-and-recreate what's already there (no change detection).
	for name, image := range s.assets {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		cur, exists := existingByName[name]
		if exists && !s.replace {
			byName[name] = cur.MessageFormat() // already present; leave as-is
			continue
		}
		if exists {
			if err := s.api.ApplicationEmojiDelete(cur.ID); err != nil {
				s.log.Error("replace application emoji: delete",
					zap.String("name", name), zap.Error(err))
				byName[name] = cur.MessageFormat() // delete failed; keep the old one
				continue
			}
		}
		created, err := s.api.ApplicationEmojiCreate(name, image)
		if err != nil {
			s.log.Error("upload application emoji", zap.String("name", name), zap.Error(err))
			continue
		}
		byName[name] = created.MessageFormat()
		if exists {
			s.log.Info("replaced application emoji", zap.String("name", name))
		} else {
			s.log.Info("uploaded application emoji", zap.String("name", name))
		}
	}

	// Handle emojis on the application that the embedded set does not define:
	// prune them, or keep them available in the Store when Prune is off.
	for name, e := range existingByName {
		if _, defined := s.assets[name]; defined {
			continue // already reconciled above
		}
		if !s.prune {
			byName[name] = e.MessageFormat()
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.api.ApplicationEmojiDelete(e.ID); err != nil {
			s.log.Error("prune application emoji", zap.String("name", name), zap.Error(err))
			byName[name] = e.MessageFormat() // delete failed; keep it
			continue
		}
		s.log.Info("pruned application emoji", zap.String("name", name))
	}

	s.store.replace(byName)
	s.log.Info("emoji sync complete", zap.Int("count", len(byName)))
	return nil
}
