package settings

import (
	"context"
	"fmt"

	lru "github.com/hashicorp/golang-lru/v2"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// defaultCacheSize bounds how many servers' resolved settings are held in
// memory; the LRU evicts the least-recently-used beyond this.
const defaultCacheSize = 1000

// resolved is a server's effective theme/language with defaults already applied.
type resolved struct {
	theme string
	lang  string
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
	cache *lru.Cache[string, resolved]
}

// NewStore wraps a Repository with the resolution cache. The Translator supplies
// the app defaults and validates stored values still exist.
func NewStore(repo Repository, tr *i18n.Translator, log *zap.Logger) (*Store, error) {
	c, err := lru.New[string, resolved](defaultCacheSize)
	if err != nil {
		return nil, fmt.Errorf("settings: new cache: %w", err)
	}
	return &Store{repo: repo, tr: tr, log: log, cache: c}, nil
}

// Resolve returns the server's effective theme and language, applying app
// defaults for unset or no-longer-valid values. It never fails; a lookup error
// falls back to defaults (and is logged) without caching.
func (s *Store) Resolve(ctx context.Context, serverID string) (string, string) {
	if r, ok := s.cache.Get(serverID); ok {
		return r.theme, r.lang
	}
	defTheme, defLang := s.tr.Defaults()
	st, err := s.repo.Get(ctx, serverID)
	if err != nil {
		s.log.Error("resolve settings", zap.String("server_id", serverID), zap.Error(err))
		return defTheme, defLang
	}
	theme := st.Theme
	if theme == "" || !s.tr.HasTheme(theme) {
		theme = defTheme
	}
	lang := st.Language
	if lang == "" || !s.tr.HasLanguage(lang) {
		lang = defLang
	}
	s.cache.Add(serverID, resolved{theme: theme, lang: lang})
	return theme, lang
}

// Get returns the raw stored settings (uncached, for display).
func (s *Store) Get(ctx context.Context, serverID string) (Settings, error) {
	return s.repo.Get(ctx, serverID)
}

// SetTheme persists the theme and invalidates the server's cached resolution.
func (s *Store) SetTheme(ctx context.Context, serverID, theme string) error {
	if err := s.repo.SetTheme(ctx, serverID, theme); err != nil {
		return err
	}
	s.cache.Remove(serverID)
	return nil
}

// SetLanguage persists the language and invalidates the server's cached resolution.
func (s *Store) SetLanguage(ctx context.Context, serverID, language string) error {
	if err := s.repo.SetLanguage(ctx, serverID, language); err != nil {
		return err
	}
	s.cache.Remove(serverID)
	return nil
}
