package contracts

import (
	"context"
	"errors"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/discord/session"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// Feature bundles the dependencies the /contracts officer console, the public
// reserve/deliver/release panel, and the expiry sweeper share. Constructed via
// New.
type Feature struct {
	repo   Repository
	loc    *i18n.Localizer
	cfg    Config
	gw     Gateway
	forum  ForumConfig
	access session.CommandAccess
	log    *zap.Logger
}

// New builds the contracts Feature. access is the permissions gate (contracts
// requires the permissions feature), used to re-authorize the console actions
// (against the "contracts" key) and the public panel buttons (against
// "contracts.use").
func New(repo Repository, loc *i18n.Localizer, cfg Config, gw Gateway, forum ForumConfig, access session.CommandAccess, log *zap.Logger) *Feature {
	return &Feature{repo: repo, loc: loc, cfg: cfg, gw: gw, forum: forum, access: access, log: log}
}

// reply renders a key and sends it ephemerally — confirmations and errors don't
// clutter the channel; the durable surface is the contract's forum post.
func (h *Feature) reply(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, key string, data map[string]any) error {
	return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, key, data))
}

// replyMapped renders the user-facing message for a known repository sentinel as
// seen from the PUBLIC panel (its phrasing — "you are not in a contract thread",
// etc.). It returns handled=false for an unrecognized error so the caller can
// wrap and surface it to the dispatcher. The console maps the same sentinels to
// its own phrasing via consoleErrorKey.
func (h *Feature) replyMapped(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, err error) (bool, error) {
	var key string
	switch {
	case errors.Is(err, ErrNotFound):
		key = "contracts.error.not_in_thread"
	case errors.Is(err, ErrClosed):
		key = "contracts.error.closed"
	case errors.Is(err, ErrExpired):
		key = "contracts.error.expired"
	case errors.Is(err, ErrItemNotFound):
		key = "contracts.error.item_not_found"
	case errors.Is(err, ErrItemExists):
		key = "contracts.item.exists"
	case errors.Is(err, ErrOverCap):
		key = "contracts.reserve.over_cap"
	case errors.Is(err, ErrOverReserved):
		key = "contracts.deliver.over_reserved"
	case errors.Is(err, ErrNoReservation):
		key = "contracts.release.no_reservation"
	case errors.Is(err, ErrBelowDelivered):
		key = "contracts.release.below_delivered"
	default:
		return false, nil
	}
	return true, h.reply(ctx, r, i, serverID, key, nil)
}
