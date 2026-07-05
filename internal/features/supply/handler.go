package supply

import (
	"context"
	"errors"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/discord/gamepick"
	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/emoji"
	"github.com/kweezl/spacecraft-corporation/internal/gamedata"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// Feature bundles the dependencies the /supply self-scoped console, the public
// reserve/deliver/release panel, and the outbox task handlers share. Constructed
// via New.
type Feature struct {
	repo  Repository
	loc   *i18n.Localizer
	gw    Gateway
	forum ForumConfig
	limit LimitConfig
	reg   *gamedata.Registry
	emo   *emoji.Store
	log   *zap.Logger
	pick  *gamepick.Picker
}

// New builds the supply Feature. search/langs/reg/emo back the shared gamedata
// picker (item + location choices from the catalog in the server's language);
// forum/limit resolve the per-server supply forum channel and open-request cap.
func New(repo Repository, loc *i18n.Localizer, gw Gateway, forum ForumConfig, limit LimitConfig, search GameSearch, langs LangResolver, reg *gamedata.Registry, emo *emoji.Store, log *zap.Logger) *Feature {
	h := &Feature{repo: repo, loc: loc, gw: gw, forum: forum, limit: limit, reg: reg, emo: emo, log: log}
	h.pick = gamepick.New(gamepick.Config{
		Prefix:   componentPrefix,
		Keys:     "supply.console",
		Loc:      loc,
		Reg:      reg,
		Emo:      emo,
		Search:   search,
		Langs:    langs,
		Log:      log,
		OnError:  h.consoleErr,
		NotFound: ErrNotFound,
	}, h.pickDestinations()...)
	return h
}

// requestLimit resolves the server's per-member open-request limit, falling back
// to DefaultRequestLimit when unset.
func (h *Feature) requestLimit(ctx context.Context, serverID uuid.UUID) int {
	if n, ok := h.limit.SupplyRequestLimit(ctx, serverID); ok {
		return n
	}
	return DefaultRequestLimit
}

// consoleErr maps a repository sentinel to an ephemeral console message (leaving
// the console message as-is). Unknown errors get a generic notice and are
// surfaced to the dispatcher for logging. Every path responds.
func (h *Feature) consoleErr(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, err error) error {
	if key, ok := consoleErrorKey(err); ok {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, key, nil))
	}
	_ = r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "supply.console.error", nil))
	return err
}

// consoleErrorKey maps a known repository sentinel to its console message key.
// ErrMaxItems / ErrLimit are handled by their callers (they need the limit in
// template data).
func consoleErrorKey(err error) (string, bool) {
	switch {
	case errors.Is(err, ErrNotFound):
		return "supply.console.not_found", true
	case errors.Is(err, ErrClosed):
		return "supply.console.closed", true
	case errors.Is(err, ErrItemNotFound):
		return "supply.console.item_not_found", true
	case errors.Is(err, ErrItemExists):
		return "supply.console.item_exists", true
	case errors.Is(err, ErrNoReservation):
		return "supply.console.no_reservation", true
	case errors.Is(err, ErrBelowDelivered):
		return "supply.console.below_delivered", true
	case errors.Is(err, ErrOverReserved):
		return "supply.console.over_reserved", true
	case errors.Is(err, ErrOverCap):
		return "supply.console.over_cap", true
	case errors.Is(err, ErrQtyBelowReserved):
		return "supply.console.qty_below_reserved", true
	default:
		return "", false
	}
}
