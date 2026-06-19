// Package settings stores each server's localization choice — the theme
// (wording skin) and language the bot renders messages in — and exposes it as
// the i18n.Resolver. Unset fields fall back to the app defaults (APP_THEME /
// APP_LANGUAGE). It also provides the /settings command to change them.
package settings

import "context"

// Settings is a server's stored choice. An empty field means "unset" (use the
// app default).
type Settings struct {
	Theme    string
	Language string
}

// Repository persists per-server settings.
type Repository interface {
	// Get returns a server's settings; an unknown server yields the zero value.
	Get(ctx context.Context, serverID string) (Settings, error)
	// SetTheme upserts the server's theme, leaving language untouched.
	SetTheme(ctx context.Context, serverID, theme string) error
	// SetLanguage upserts the server's language, leaving theme untouched.
	SetLanguage(ctx context.Context, serverID, language string) error
}
