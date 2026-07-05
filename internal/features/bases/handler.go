package bases

import (
	"context"
	"errors"
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
	"github.com/kweezl/spacecraft-corporation/internal/roman"
)

// Feature bundles the dependencies the /base command and its pagination
// component share (notably one page store), so both are built from a single
// instance. Constructed via New; the fx module exposes Command() and Component().
type Feature struct {
	repo  Repository
	loc   *i18n.Localizer
	cfg   Config
	pages *pageStore
}

// New builds the bases Feature. It returns an error only if the page store can't
// be created.
func New(repo Repository, loc *i18n.Localizer, cfg Config) (*Feature, error) {
	pages, err := newPageStore()
	if err != nil {
		return nil, err
	}
	return &Feature{repo: repo, loc: loc, cfg: cfg, pages: pages}, nil
}

// Command builds the /base registry command: SubcommandGated (so each tier path
// is granted independently) and DefaultDeny (locked to owner/admins until a role
// is granted).
func (h *Feature) Command() *registry.Command {
	return &registry.Command{
		Def:             buildDef(),
		Handler:         h.handle,
		Autocomplete:    h.autocomplete,
		DefaultDeny:     true,
		SubcommandGated: true,
	}
}

// Component builds the pagination component handler, routed by its CustomID
// namespace (see componentPrefix).
func (h *Feature) Component() *registry.Component {
	return &registry.Component{Prefix: componentPrefix, Handler: h.handleComponent}
}

// parsePath unpacks the interaction into its tier (own/corp/member, empty for
// list), operation (the leaf subcommand), and that leaf's options.
func parsePath(i *discordgo.InteractionCreate) (tier, op string, opts []*discordgo.ApplicationCommandInteractionDataOption) {
	data := i.ApplicationCommandData()
	if len(data.Options) == 0 {
		return "", "", nil
	}
	top := data.Options[0]
	if top.Name == opList {
		return "", opList, top.Options
	}
	tier = top.Name
	if len(top.Options) == 0 {
		return tier, "", nil
	}
	leaf := top.Options[0]
	return tier, leaf.Name, leaf.Options
}

// ownership derives the SQL ownership scope from the tier. The member tier reads
// the required member user option; ok is false when it is missing.
func ownership(tier string, i *discordgo.InteractionCreate, serverID uuid.UUID, opts []*discordgo.ApplicationCommandInteractionDataOption) (o Ownership, ok bool) {
	switch tier {
	case tierOwn:
		return MemberOwnership(serverID, invokerID(i)), true
	case tierCorp:
		return CorpOwnership(serverID), true
	case tierMember:
		target := optString(opts, optMember)
		if target == "" {
			return Ownership{}, false
		}
		return MemberOwnership(serverID, target), true
	}
	return Ownership{}, false
}

func (h *Feature) handle(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	tier, op, opts := parsePath(i)
	if op == opList {
		return h.handleList(ctx, r, i, serverID, opts)
	}
	o, ok := ownership(tier, i, serverID, opts)
	if !ok {
		return h.reply(ctx, r, i, serverID, "bases.error.no_member", nil)
	}
	switch op {
	case opRegister:
		return h.handleRegister(ctx, r, i, serverID, o, opts)
	case opUnregister:
		return h.handleUnregister(ctx, r, i, serverID, o, opts)
	case opAddExtractor:
		return h.handleAddExtractor(ctx, r, i, serverID, o, opts)
	case opAddProduction:
		return h.handleAddProduction(ctx, r, i, serverID, o, opts)
	case opRemoveExtractor:
		return h.handleRemoveExtractor(ctx, r, i, serverID, o, opts)
	case opRemoveProduction:
		return h.handleRemoveProduction(ctx, r, i, serverID, o, opts)
	default:
		return h.reply(ctx, r, i, serverID, "bases.error.unknown", nil)
	}
}

// reply renders a key and sends it ephemerally (mutation confirmations and
// errors don't clutter the channel; the public surface is /base list).
func (h *Feature) reply(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, key string, data map[string]any) error {
	return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, key, data))
}

func (h *Feature) handleRegister(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, o Ownership, opts []*discordgo.ApplicationCommandInteractionDataOption) error {
	in := RegisterInput{
		ServerID:        serverID,
		Kind:            o.Kind,
		OwnerUserID:     o.OwnerUserID,
		CreatedByUserID: invokerID(i),
		Name:            optString(opts, optName),
		SectorName:      optString(opts, optSector),
		SystemCode:      optString(opts, optSystem),
		PlanetNumber:    optInt(opts, optPlanet),
	}
	limit := h.cfg.MemberLimit
	if o.Kind == KindCorp {
		limit = h.cfg.CorpLimit
	}
	_, err := h.repo.Register(ctx, in, limit)
	switch {
	case errors.Is(err, ErrLimitReached):
		return h.reply(ctx, r, i, serverID, "bases.register.limit", map[string]any{"Limit": limit})
	case err != nil:
		return fmt.Errorf("register base: %w", err)
	}
	return h.reply(ctx, r, i, serverID, "bases.register.ok", map[string]any{
		"Name": in.Name, "Sector": in.SectorName, "System": in.SystemCode, "Planet": roman.Numeral(in.PlanetNumber),
	})
}

func (h *Feature) handleUnregister(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, o Ownership, opts []*discordgo.ApplicationCommandInteractionDataOption) error {
	base := optString(opts, optBase)
	if base == allBasesValue {
		n, err := h.repo.DeleteAll(ctx, o)
		if err != nil {
			return fmt.Errorf("unregister all: %w", err)
		}
		return h.reply(ctx, r, i, serverID, "bases.unregister.all", map[string]any{"Count": n})
	}
	id, err := uuid.Parse(base)
	if err != nil {
		return h.reply(ctx, r, i, serverID, "bases.error.bad_base", nil)
	}
	n, err := h.repo.DeleteOne(ctx, o, id)
	if err != nil {
		return fmt.Errorf("unregister base: %w", err)
	}
	if n == 0 {
		return h.reply(ctx, r, i, serverID, "bases.error.base_not_found", nil)
	}
	return h.reply(ctx, r, i, serverID, "bases.unregister.ok", nil)
}

func (h *Feature) handleAddExtractor(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, o Ownership, opts []*discordgo.ApplicationCommandInteractionDataOption) error {
	id, err := uuid.Parse(optString(opts, optBase))
	if err != nil {
		return h.reply(ctx, r, i, serverID, "bases.error.bad_base", nil)
	}
	resource := optString(opts, optResource)
	err = h.repo.AddExtractor(ctx, o, id, resource, h.cfg.ExtractorLimit)
	switch {
	case errors.Is(err, ErrBaseNotFound):
		return h.reply(ctx, r, i, serverID, "bases.error.base_not_found", nil)
	case errors.Is(err, ErrLimitReached):
		return h.reply(ctx, r, i, serverID, "bases.extractor.limit", map[string]any{"Limit": h.cfg.ExtractorLimit})
	case err != nil:
		return fmt.Errorf("add extractor: %w", err)
	}
	return h.reply(ctx, r, i, serverID, "bases.extractor.added", map[string]any{"Resource": resource})
}

func (h *Feature) handleAddProduction(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, o Ownership, opts []*discordgo.ApplicationCommandInteractionDataOption) error {
	id, err := uuid.Parse(optString(opts, optBase))
	if err != nil {
		return h.reply(ctx, r, i, serverID, "bases.error.bad_base", nil)
	}
	item := optString(opts, optItem)
	err = h.repo.AddProduction(ctx, o, id, item, h.cfg.ProductionLimit)
	switch {
	case errors.Is(err, ErrBaseNotFound):
		return h.reply(ctx, r, i, serverID, "bases.error.base_not_found", nil)
	case errors.Is(err, ErrLimitReached):
		return h.reply(ctx, r, i, serverID, "bases.production.limit", map[string]any{"Limit": h.cfg.ProductionLimit})
	case err != nil:
		return fmt.Errorf("add production: %w", err)
	}
	return h.reply(ctx, r, i, serverID, "bases.production.added", map[string]any{"Item": item})
}

func (h *Feature) handleRemoveExtractor(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, o Ownership, opts []*discordgo.ApplicationCommandInteractionDataOption) error {
	id, err := uuid.Parse(optString(opts, optExtractor))
	if err != nil {
		return h.reply(ctx, r, i, serverID, "bases.error.bad_equipment", nil)
	}
	n, err := h.repo.RemoveExtractor(ctx, o, id)
	if err != nil {
		return fmt.Errorf("remove extractor: %w", err)
	}
	if n == 0 {
		return h.reply(ctx, r, i, serverID, "bases.error.equipment_not_found", nil)
	}
	return h.reply(ctx, r, i, serverID, "bases.extractor.removed", nil)
}

func (h *Feature) handleRemoveProduction(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, o Ownership, opts []*discordgo.ApplicationCommandInteractionDataOption) error {
	id, err := uuid.Parse(optString(opts, optProduction))
	if err != nil {
		return h.reply(ctx, r, i, serverID, "bases.error.bad_equipment", nil)
	}
	n, err := h.repo.RemoveProduction(ctx, o, id)
	if err != nil {
		return fmt.Errorf("remove production: %w", err)
	}
	if n == 0 {
		return h.reply(ctx, r, i, serverID, "bases.error.equipment_not_found", nil)
	}
	return h.reply(ctx, r, i, serverID, "bases.production.removed", nil)
}
