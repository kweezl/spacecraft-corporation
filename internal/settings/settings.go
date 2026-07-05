// Package settings stores each server's localization choice — the theme
// (wording skin) and language the bot renders messages in — and exposes it as
// the i18n.Resolver. Unset fields fall back to the app defaults (APP_THEME /
// APP_LANGUAGE). It also provides the /settings command to change them.
package settings

import (
	"context"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// Settings is a server's stored choice. An empty field means "unset" (use the
// app default, where one exists).
type Settings struct {
	Theme    string
	Language i18n.Language
	// ContractsForumChannelID is the Discord forum channel the contracts feature
	// posts contract threads to; empty = unset. Owned by the contracts feature
	// conceptually, stored here per the chosen "extend settings" approach.
	ContractsForumChannelID string
	// ContractsReportsChannelID is the Discord text channel the contracts feature
	// posts completed contracts' payout reports to; empty = unset. Owned by the
	// contracts feature, stored here like the forum channel.
	ContractsReportsChannelID string
	// ContractsRewardFactor is the server's default participant reward factor
	// (percent, 0–100) prefilled onto new contract templates and custom
	// contracts. Unlike the fields above, zero IS the default (participants get
	// nothing), so an unset column reads as 0 and the field stays pointer-free.
	// Owned by the contracts feature, stored here like the forum channel.
	ContractsRewardFactor decimal.Decimal
}

// Repository persists per-server settings. serverID is the resolved servers.id.
type Repository interface {
	// Get returns a server's settings; an unknown server yields the zero value.
	Get(ctx context.Context, serverID uuid.UUID) (Settings, error)
	// SetTheme upserts the server's theme, leaving other columns untouched.
	SetTheme(ctx context.Context, serverID uuid.UUID, theme string) error
	// SetLanguage upserts the server's language, leaving other columns untouched.
	SetLanguage(ctx context.Context, serverID uuid.UUID, language i18n.Language) error
	// SetContractsForumChannelID upserts the server's contracts forum channel,
	// leaving other columns untouched.
	SetContractsForumChannelID(ctx context.Context, serverID uuid.UUID, channelID string) error
	// SetContractsReportsChannelID upserts the server's contracts reports channel,
	// leaving other columns untouched.
	SetContractsReportsChannelID(ctx context.Context, serverID uuid.UUID, channelID string) error
	// SetContractsRewardFactor upserts the server's default participant reward
	// factor, leaving other columns untouched.
	SetContractsRewardFactor(ctx context.Context, serverID uuid.UUID, factor decimal.Decimal) error
}
