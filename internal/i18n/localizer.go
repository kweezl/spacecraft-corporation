package i18n

import "context"

// Resolver returns the theme and language a server should use. Implemented by
// the settings module (per-server choice with env defaults); the returned values
// are always concrete (defaults already applied).
type Resolver interface {
	Resolve(ctx context.Context, serverID string) (theme, lang string)
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

// Render resolves the server's theme/language and renders the message.
func (l *Localizer) Render(ctx context.Context, serverID, key string, data any) string {
	theme, lang := l.res.Resolve(ctx, serverID)
	return l.tr.Render(theme, lang, key, data)
}

// StaticResolver always resolves to a fixed theme/language. Useful as a
// degenerate resolver and in tests.
type StaticResolver struct {
	Theme string
	Lang  string
}

// Resolve returns the fixed theme and language.
func (s StaticResolver) Resolve(context.Context, string) (string, string) {
	return s.Theme, s.Lang
}
