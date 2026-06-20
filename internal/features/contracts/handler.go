package contracts

import (
	"context"
	"errors"
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// Feature bundles the dependencies the /contract command, its autocomplete, its
// list-pagination component, and the expiry sweeper share. Constructed via New.
type Feature struct {
	repo  Repository
	loc   *i18n.Localizer
	cfg   Config
	gw    Gateway
	forum ForumConfig
	log   *zap.Logger
	pages *pageStore
}

// New builds the contracts Feature.
func New(repo Repository, loc *i18n.Localizer, cfg Config, gw Gateway, forum ForumConfig, log *zap.Logger) (*Feature, error) {
	pages, err := newPageStore()
	if err != nil {
		return nil, err
	}
	return &Feature{repo: repo, loc: loc, cfg: cfg, gw: gw, forum: forum, log: log, pages: pages}, nil
}

// Command builds the /contract registry command: SubcommandGated and DefaultDeny,
// like /base — each leaf is granted to roles independently and admins bypass.
func (h *Feature) Command() *registry.Command {
	return &registry.Command{
		Def:             buildDef(),
		Handler:         h.handle,
		Autocomplete:    h.autocomplete,
		DefaultDeny:     true,
		SubcommandGated: true,
	}
}

// Component builds the list-pagination component handler.
func (h *Feature) Component() *registry.Component {
	return &registry.Component{Prefix: componentPrefix, Handler: h.handleComponent}
}

func (h *Feature) handle(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	data := i.ApplicationCommandData()
	if len(data.Options) == 0 {
		return h.reply(ctx, r, i, serverID, "contracts.error.unknown", nil)
	}
	top := data.Options[0]
	switch top.Name {
	case opCreate:
		return h.handleCreate(ctx, r, i, serverID, top.Options)
	case grpItem:
		if len(top.Options) == 0 {
			return h.reply(ctx, r, i, serverID, "contracts.error.unknown", nil)
		}
		leaf := top.Options[0]
		switch leaf.Name {
		case opItemAdd:
			return h.handleItemAdd(ctx, r, i, serverID, leaf.Options)
		case opItemRemove:
			return h.handleItemRemove(ctx, r, i, serverID, leaf.Options)
		}
	case opParticipate:
		return h.handleParticipate(ctx, r, i, serverID, top.Options)
	case opDeliver:
		return h.handleDeliver(ctx, r, i, serverID, top.Options)
	case opRelease:
		return h.handleRelease(ctx, r, i, serverID, top.Options, invokerID(i), false)
	case opReleaseMember:
		return h.handleRelease(ctx, r, i, serverID, top.Options, optString(top.Options, optMember), true)
	case opCancel:
		return h.handleCancel(ctx, r, i, serverID)
	case opList:
		return h.handleList(ctx, r, i, serverID, top.Options)
	case opShow:
		return h.handleShow(ctx, r, i, serverID)
	case opForum:
		return h.handleSetForum(ctx, r, i, serverID, top.Options)
	}
	return h.reply(ctx, r, i, serverID, "contracts.error.unknown", nil)
}

// reply renders a key and sends it ephemerally — mutation confirmations and
// errors don't clutter the thread; the public surface is the progress embed.
func (h *Feature) reply(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, key string, data map[string]any) error {
	return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, key, data))
}

// replyMapped renders the user-facing message for a known repository sentinel.
// It returns handled=false for an unrecognized error so the caller can wrap and
// surface it to the dispatcher. Errors needing template data (ErrMaxItems) are
// handled by the caller before falling through here.
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
		key = "contracts.participate.over_cap"
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

func (h *Feature) handleCreate(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, opts []*discordgo.ApplicationCommandInteractionDataOption) error {
	title := normalizeItem(optString(opts, optTitle))
	if title == "" {
		return h.reply(ctx, r, i, serverID, "contracts.create.bad_title", nil)
	}
	dur, err := parseDuration(optString(opts, optDuration))
	if err != nil {
		return h.reply(ctx, r, i, serverID, "contracts.create.bad_duration", nil)
	}
	// Fail fast on the cheap, common misconfiguration synchronously; the worker
	// handles the rest (and re-checks the forum) asynchronously.
	if _, ok := h.forum.ContractsForumChannelID(ctx, serverID); !ok {
		return h.reply(ctx, r, i, serverID, "contracts.create.no_forum", nil)
	}

	// Persist the contract + a create-thread task atomically, then ack. The worker
	// creates the forum thread and edits this reply with the outcome (token + app
	// id travel on the task).
	if _, err := h.repo.Create(ctx, CreateInput{
		ServerID: serverID, Title: title, Description: optString(opts, optDescription),
		Deadline: nowAdd(dur), CreatedByUserID: invokerID(i),
		AppID: i.AppID, Token: i.Token,
	}); err != nil {
		return fmt.Errorf("create contract: %w", err)
	}
	return h.reply(ctx, r, i, serverID, "contracts.create.accepted", nil)
}

func (h *Feature) handleItemAdd(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, opts []*discordgo.ApplicationCommandInteractionDataOption) error {
	name := normalizeItem(optString(opts, optName))
	qty := optInt(opts, optQty)
	if name == "" || qty <= 0 {
		return h.reply(ctx, r, i, serverID, "contracts.item.bad_input", nil)
	}
	err := h.repo.AddItem(ctx, serverID, threadOf(i), name, qty, h.cfg.MaxItems, invokerID(i))
	if errors.Is(err, ErrMaxItems) {
		return h.reply(ctx, r, i, serverID, "contracts.item.max_items", map[string]any{"Limit": h.cfg.MaxItems})
	}
	if err != nil {
		if handled, rerr := h.replyMapped(ctx, r, i, serverID, err); handled {
			return rerr
		}
		return fmt.Errorf("add item: %w", err)
	}
	return h.reply(ctx, r, i, serverID, "contracts.item.added", map[string]any{"Name": name, "Qty": qty})
}

func (h *Feature) handleItemRemove(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, opts []*discordgo.ApplicationCommandInteractionDataOption) error {
	name := normalizeItem(optString(opts, optName))
	cleared, err := h.repo.RemoveItem(ctx, serverID, threadOf(i), name, invokerID(i))
	if err != nil {
		if handled, rerr := h.replyMapped(ctx, r, i, serverID, err); handled {
			return rerr
		}
		return fmt.Errorf("remove item: %w", err)
	}
	return h.reply(ctx, r, i, serverID, "contracts.item.removed", map[string]any{"Name": name, "Cleared": cleared})
}

func (h *Feature) handleParticipate(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, opts []*discordgo.ApplicationCommandInteractionDataOption) error {
	item := normalizeItem(optString(opts, optItem))
	qty := optInt(opts, optQty)
	err := h.repo.Participate(ctx, serverID, threadOf(i), item, invokerID(i), qty)
	if err != nil {
		if handled, rerr := h.replyMapped(ctx, r, i, serverID, err); handled {
			return rerr
		}
		return fmt.Errorf("participate: %w", err)
	}
	return h.reply(ctx, r, i, serverID, "contracts.participate.ok", map[string]any{"Item": item, "Qty": qty})
}

func (h *Feature) handleDeliver(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, opts []*discordgo.ApplicationCommandInteractionDataOption) error {
	item := normalizeItem(optString(opts, optItem))
	qty := optInt(opts, optQty)
	complete, err := h.repo.Deliver(ctx, serverID, threadOf(i), item, invokerID(i), qty)
	if err != nil {
		if handled, rerr := h.replyMapped(ctx, r, i, serverID, err); handled {
			return rerr
		}
		return fmt.Errorf("deliver: %w", err)
	}
	if complete {
		return h.reply(ctx, r, i, serverID, "contracts.deliver.completed", map[string]any{"Item": item, "Qty": qty})
	}
	return h.reply(ctx, r, i, serverID, "contracts.deliver.ok", map[string]any{"Item": item, "Qty": qty})
}

func (h *Feature) handleRelease(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, opts []*discordgo.ApplicationCommandInteractionDataOption, target string, forMember bool) error {
	if target == "" {
		return h.reply(ctx, r, i, serverID, "contracts.release.no_target", nil)
	}
	item := normalizeItem(optString(opts, optItem))
	qty := optInt(opts, optQty)
	err := h.repo.Release(ctx, serverID, threadOf(i), item, target, qty, invokerID(i))
	if err != nil {
		if handled, rerr := h.replyMapped(ctx, r, i, serverID, err); handled {
			return rerr
		}
		return fmt.Errorf("release: %w", err)
	}
	if forMember {
		return h.reply(ctx, r, i, serverID, "contracts.release.ok_member", map[string]any{"Member": target, "Item": item, "Qty": qty})
	}
	return h.reply(ctx, r, i, serverID, "contracts.release.ok", map[string]any{"Item": item, "Qty": qty})
}

func (h *Feature) handleCancel(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	err := h.repo.Cancel(ctx, serverID, threadOf(i), invokerID(i))
	if err != nil {
		if handled, rerr := h.replyMapped(ctx, r, i, serverID, err); handled {
			return rerr
		}
		return fmt.Errorf("cancel: %w", err)
	}
	return h.reply(ctx, r, i, serverID, "contracts.cancel.ok", nil)
}

func (h *Feature) handleShow(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	p, err := h.repo.Progress(ctx, serverID, threadOf(i))
	if errors.Is(err, ErrNotFound) {
		return h.reply(ctx, r, i, serverID, "contracts.error.not_in_thread", nil)
	}
	if err != nil {
		return fmt.Errorf("show contract: %w", err)
	}
	return r.RespondEmbed(i.Interaction, h.renderEmbed(ctx, serverID, p))
}

// handleSetForum designates the server's contracts forum channel. The value is
// stored in the core settings store, but this command lives in the contracts
// feature so it is only registered when the feature is enabled.
func (h *Feature) handleSetForum(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, opts []*discordgo.ApplicationCommandInteractionDataOption) error {
	channelID := optString(opts, optChannel)
	if channelID == "" {
		return h.reply(ctx, r, i, serverID, "contracts.forum.bad_channel", nil)
	}
	if err := h.forum.SetContractsForumChannelID(ctx, serverID, channelID); err != nil {
		return fmt.Errorf("set contracts forum: %w", err)
	}
	return h.reply(ctx, r, i, serverID, "contracts.forum.set", map[string]any{"Channel": channelID})
}

// threadOf is the channel the command ran in — for in-thread leaves this is the
// contract's forum thread, the key the repository resolves the contract by.
func threadOf(i *discordgo.InteractionCreate) string { return i.ChannelID }
