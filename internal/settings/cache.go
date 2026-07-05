package settings

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// defaultCacheSize bounds how many servers' resolved settings are held in
// memory; the LRU evicts the least-recently-used beyond this.
const defaultCacheSize = 1000

// resolved is a server's effective theme/language with defaults already applied,
// plus the raw contracts forum channel id (no default — empty means unset) and
// the default participant reward factor (zero IS the default).
type resolved struct {
	theme   string
	lang    i18n.Language
	forum   string
	reports string
	factor  decimal.Decimal
}

// Store fronts the Repository with an in-memory LRU cache and implements
// i18n.Resolver. Resolve runs on every rendered message, so it is cached; writes
// (SetTheme/SetLanguage) invalidate the server. It is coherent within this single
// process (the bot runs as one process), the same caching model as
// permissions.Store.
type Store struct {
	repo  Repository
	tr    *i18n.Translator
	log   *zap.Logger
	cache *lru.Cache[uuid.UUID, resolved]
}

// NewStore wraps a Repository with the resolution cache. The Translator supplies
// the app defaults and validates stored values still exist.
func NewStore(repo Repository, tr *i18n.Translator, log *zap.Logger) (*Store, error) {
	c, err := lru.New[uuid.UUID, resolved](defaultCacheSize)
	if err != nil {
		return nil, fmt.Errorf("settings: new cache: %w", err)
	}
	return &Store{repo: repo, tr: tr, log: log, cache: c}, nil
}

// resolve returns the server's effective settings (theme/language with defaults
// applied, plus the raw forum channel), caching the result. It never fails; a
// lookup error falls back to defaults (and is logged) without caching, so a
// transient DB error is retried next call. ok reports whether the value came
// from a successful load (so callers needn't re-check the error path).
func (s *Store) resolve(ctx context.Context, serverID uuid.UUID) (resolved, bool) {
	if r, ok := s.cache.Get(serverID); ok {
		return r, true
	}
	defTheme, defLang := s.tr.Defaults()
	st, err := s.repo.Get(ctx, serverID)
	if err != nil {
		s.log.Error("resolve settings", zap.String("server_id", serverID.String()), zap.Error(err))
		return resolved{theme: defTheme, lang: defLang}, false
	}
	theme := st.Theme
	if theme == "" || !s.tr.HasTheme(theme) {
		theme = defTheme
	}
	lang := st.Language
	if lang == "" || !s.tr.HasLanguage(lang) {
		lang = defLang
	}
	r := resolved{theme: theme, lang: lang, forum: st.ContractsForumChannelID, reports: st.ContractsReportsChannelID, factor: st.ContractsRewardFactor}
	s.cache.Add(serverID, r)
	return r, true
}

// Resolve returns the server's effective theme and language, applying app
// defaults for unset or no-longer-valid values. It never fails.
func (s *Store) Resolve(ctx context.Context, serverID uuid.UUID) (string, i18n.Language) {
	r, _ := s.resolve(ctx, serverID)
	return r.theme, r.lang
}

// ContractsForumChannelID returns the server's configured contracts forum
// channel and whether one is set. Cached on the same resolution as Resolve.
func (s *Store) ContractsForumChannelID(ctx context.Context, serverID uuid.UUID) (string, bool) {
	r, _ := s.resolve(ctx, serverID)
	return r.forum, r.forum != ""
}

// ContractsReportsChannelID returns the server's configured contract reports
// channel and whether one is set. Cached on the same resolution as Resolve.
func (s *Store) ContractsReportsChannelID(ctx context.Context, serverID uuid.UUID) (string, bool) {
	r, _ := s.resolve(ctx, serverID)
	return r.reports, r.reports != ""
}

// ContractsRewardFactor returns the server's default participant reward factor
// (percent, 0–100; zero when unset). Cached on the same resolution as Resolve.
func (s *Store) ContractsRewardFactor(ctx context.Context, serverID uuid.UUID) decimal.Decimal {
	r, _ := s.resolve(ctx, serverID)
	return r.factor
}

// Get returns the raw stored settings (uncached, for display).
func (s *Store) Get(ctx context.Context, serverID uuid.UUID) (Settings, error) {
	return s.repo.Get(ctx, serverID)
}

// SetTheme persists the theme and invalidates the server's cached resolution.
func (s *Store) SetTheme(ctx context.Context, serverID uuid.UUID, theme string) error {
	if err := s.repo.SetTheme(ctx, serverID, theme); err != nil {
		return err
	}
	s.cache.Remove(serverID)
	return nil
}

// SetLanguage persists the language and invalidates the server's cached resolution.
func (s *Store) SetLanguage(ctx context.Context, serverID uuid.UUID, language i18n.Language) error {
	if err := s.repo.SetLanguage(ctx, serverID, language); err != nil {
		return err
	}
	s.cache.Remove(serverID)
	return nil
}

// SetContractsForumChannelID persists the contracts forum channel and
// invalidates the server's cached resolution.
func (s *Store) SetContractsForumChannelID(ctx context.Context, serverID uuid.UUID, channelID string) error {
	if err := s.repo.SetContractsForumChannelID(ctx, serverID, channelID); err != nil {
		return err
	}
	s.cache.Remove(serverID)
	return nil
}

// SetContractsReportsChannelID persists the contracts reports channel and
// invalidates the server's cached resolution.
func (s *Store) SetContractsReportsChannelID(ctx context.Context, serverID uuid.UUID, channelID string) error {
	if err := s.repo.SetContractsReportsChannelID(ctx, serverID, channelID); err != nil {
		return err
	}
	s.cache.Remove(serverID)
	return nil
}

// SetContractsRewardFactor persists the default participant reward factor and
// invalidates the server's cached resolution.
func (s *Store) SetContractsRewardFactor(ctx context.Context, serverID uuid.UUID, factor decimal.Decimal) error {
	if err := s.repo.SetContractsRewardFactor(ctx, serverID, factor); err != nil {
		return err
	}
	s.cache.Remove(serverID)
	return nil
}
