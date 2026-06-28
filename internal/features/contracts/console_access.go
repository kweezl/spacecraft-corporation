package contracts

import (
	"context"
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/discord/session"
)

// Fine-grained console permission keys. Who may open /contracts (and view) is
// governed by Discord's native command permissions (the command is DiscordManaged);
// these keys gate what a viewer may then create or edit. They are declared in the
// command's ExtraAccessKeys so /permissions can grant each to roles; every check
// is DefaultDeny (admins bypass). Authoring
// is split by contract kind — keyCustom covers creating and fully editing custom
// contracts; keyTemplate covers creating template contracts and their limited
// edits (deadline + cancel). keyRepublish is independent and reposts either kind.
const (
	keyCustom    = "contracts.custom"
	keyTemplate  = "contracts.template"
	keyRepublish = "contracts.republish"
	// keyManage gates participant management (the "contracts manager" role):
	// updating a reservation, delivering on behalf, cancelling a reservation or a
	// participation. Independent of the custom/template create-and-edit keys.
	keyManage = "contracts.manage"
)

// keyForKind maps a contract kind to the permission key that governs editing it.
func keyForKind(k Kind) string {
	if k == KindTemplate {
		return keyTemplate
	}
	return keyCustom
}

// idSource says where a gated mutation's target id sits in the CustomID parts, so
// the gate can resolve the owning contract's kind to pick the permission key.
type idSource int

const (
	srcNone     idSource = iota // create actions: no contract exists yet
	srcContract                 // parts[0] is a contract id
	srcItem                     // parts[0] is an item id (resolve its contract)
)

// mutationGate describes how to authorize one console mutation segment. fixedKey,
// when set, is the permission required regardless of the target's kind (the two
// create actions and republish). Otherwise the key is derived from the contract's
// kind; templateAllowed then says whether the action is permitted on a template
// contract at all — custom-only actions (add item, item edit/remove) set it
// false and are refused outright on a template.
type mutationGate struct {
	src             idSource
	fixedKey        string
	templateAllowed bool
}

// mutationGates maps every gated console segment (both the component button and
// its modal-submit twin) to its authorization rule. Read-only navigation segments
// are deliberately absent — gateMutation lets them through on the coarse gate.
var mutationGates = map[string]mutationGate{
	// Create / republish: a fixed key regardless of kind.
	segCreate:   {src: srcNone, fixedKey: keyCustom},
	segMCreate:  {src: srcNone, fixedKey: keyCustom},
	segTemplate: {src: srcNone, fixedKey: keyTemplate},
	segRepub:    {src: srcContract, fixedKey: keyRepublish},

	// Edit (name/description/deadline for custom; deadline only for template) and
	// cancel: allowed on both kinds, keyed by the contract's kind.
	segCEdit:   {src: srcContract, templateAllowed: true},
	segMCEdit:  {src: srcContract, templateAllowed: true},
	segCancel:  {src: srcContract, templateAllowed: true},
	segMCancel: {src: srcContract, templateAllowed: true},

	// Add item: custom-only (refused on a template contract).
	segCAdd:  {src: srcContract, templateAllowed: false},
	segMCAdd: {src: srcContract, templateAllowed: false},

	// Item edit / remove: custom-only (item-keyed; refused on a template).
	segIDel:   {src: srcItem, templateAllowed: false},
	segMIDel:  {src: srcItem, templateAllowed: false},
	segIEdit:  {src: srcItem, templateAllowed: false},
	segMIEdit: {src: srcItem, templateAllowed: false},

	// Participant management: the "contracts manager" key, on either modal step.
	segPEdit:  {src: srcItem, fixedKey: keyManage},
	segMPEdit: {src: srcItem, fixedKey: keyManage},
}

// gateMutation authorizes a console mutation before its handler runs — the
// server-side enforcement behind the buttons the views already hide. It returns
// proceed=false once it has responded (a denial or a load error); err is non-nil
// only for an unexpected failure to surface to the dispatcher. Non-mutation
// segments (navigation) are absent from mutationGates and always proceed — the
// ephemeral console is only reachable by whoever Discord let run /contracts.
func (h *Feature) gateMutation(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, seg string, parts []string) (proceed bool, err error) {
	g, gated := mutationGates[seg]
	if !gated {
		return true, nil
	}
	key := g.fixedKey
	if key == "" {
		kind, kerr := h.resolveKind(ctx, serverID, g.src, parts)
		if kerr != nil {
			return false, h.consoleErr(ctx, r, i, serverID, kerr)
		}
		if kind == KindTemplate && !g.templateAllowed {
			return false, h.reply(ctx, r, i, serverID, "contracts.console.template_locked", nil)
		}
		key = keyForKind(kind)
	}
	ok, aerr := h.authorizedKey(ctx, i, serverID, key)
	if aerr != nil {
		return false, fmt.Errorf("contracts: authorize %s: %w", key, aerr)
	}
	if !ok {
		return false, h.reply(ctx, r, i, serverID, "contracts.console.denied", nil)
	}
	return true, nil
}

// resolveKind loads the kind of the contract a gated mutation targets.
func (h *Feature) resolveKind(ctx context.Context, serverID uuid.UUID, src idSource, parts []string) (Kind, error) {
	switch src {
	case srcContract:
		cid, ok := argUUID(parts, 0)
		if !ok {
			return "", ErrNotFound
		}
		return h.repo.KindByID(ctx, serverID, cid)
	case srcItem:
		itemID, ok := argUUID(parts, 0)
		if !ok {
			return "", ErrItemNotFound
		}
		return h.repo.KindByItem(ctx, serverID, itemID)
	default:
		return KindCustom, nil
	}
}

// authorizedKey re-checks the interacting member against a specific grantable key
// (DefaultDeny): administrators bypass; with the permissions feature absent
// (access nil) gating is off entirely, like the session's own gate.
func (h *Feature) authorizedKey(ctx context.Context, i *discordgo.InteractionCreate, serverID uuid.UUID, key string) (bool, error) {
	if i.Member != nil && i.Member.Permissions&discordgo.PermissionAdministrator != 0 {
		return true, nil
	}
	if h.access == nil {
		return true, nil
	}
	var roles []string
	if i.Member != nil {
		roles = i.Member.Roles
	}
	return h.access.IsAllowed(ctx, session.AccessRequest{
		ServerID:    serverID,
		Command:     key,
		UserRoles:   roles,
		DefaultDeny: true,
	})
}

// may is the rendering-time counterpart of authorizedKey: it reports whether the
// member holds key, used to decide which buttons to show. A check error hides the
// button (and is logged) — defense in depth, since gateMutation re-authorizes the
// action server-side regardless of what the view rendered.
func (h *Feature) may(ctx context.Context, i *discordgo.InteractionCreate, serverID uuid.UUID, key string) bool {
	ok, err := h.authorizedKey(ctx, i, serverID, key)
	if err != nil {
		h.log.Error("contracts: permission check failed", zap.String("key", key), zap.Error(err))
		return false
	}
	return ok
}
