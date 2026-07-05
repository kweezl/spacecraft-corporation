package i18n

import (
	"context"

	"github.com/google/uuid"
)

// Resolver returns the theme and language a server should use, keyed by the
// resolved servers.id. Implemented by the settings module (per-server choice with
// env defaults); the returned values are always concrete (defaults already
// applied). The zero UUID (uuid.Nil, used when a server could not be resolved)
// has no settings row and so resolves to the app defaults.
type Resolver interface {
	Resolve(ctx context.Context, serverID uuid.UUID) (theme string, lang Language)
}

// Localizer is the handler-facing facade: it resolves a server's theme/language
// and renders a message key. It never fails — rendering falls back to defaults
// and ultimately to the key itself.
type Localizer struct {
	tr  *Translator
	res Resolver
}

// NewLocalizer builds a Localizer over a Translator and a Resolver.
func NewLocalizer(tr *Translator, res Resolver) *Localizer {
	return &Localizer{tr: tr, res: res}
}

// Render resolves the server's theme/language and renders the message. serverID
// is the resolved servers.id (uuid.Nil falls back to app defaults).
func (l *Localizer) Render(ctx context.Context, serverID uuid.UUID, key string, data any) string {
	theme, lang := l.res.Resolve(ctx, serverID)
	return l.tr.Render(theme, lang, key, data)
}

// StaticResolver always resolves to a fixed theme/language. Useful as a
// degenerate resolver and in tests.
type StaticResolver struct {
	Theme string
	Lang  Language
}

// Resolve returns the fixed theme and language.
func (s StaticResolver) Resolve(context.Context, uuid.UUID) (string, Language) {
	return s.Theme, s.Lang
}
