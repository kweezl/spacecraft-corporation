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
// contracts; keyTemplate covers instantiating contracts from templates and
// editing the result (a template is defaults only, so the contract stays fully
// editable under this key). keyRepublish is independent and reposts either kind.
const (
	keyCustom    = "contracts.custom"
	keyTemplate  = "contracts.template"
	keyRepublish = "contracts.republish"
	// keyManage gates participant management (the "contracts manager" role):
	// updating a reservation, delivering on behalf, cancelling a reservation or a
	// participation. Independent of the custom/template create-and-edit keys.
	keyManage = "contracts.manage"
	// keyTemplates gates the template LIBRARY (creating/editing/deleting the
	// server's templates) — a stronger grant than keyTemplate, which only lets a
	// member consume the library.
	keyTemplates = "contracts.templates"
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
// when set, is the permission required regardless of the target's kind (create
// actions, republish, and the template-library segments). Otherwise the key is
// derived from the contract's kind (keyCustom / keyTemplate) — a template is
// defaults only, so BOTH kinds allow the full action set under their key.
type mutationGate struct {
	src      idSource
	fixedKey string
}

// mutationGates maps every gated console segment (both the component button and
// its modal-submit twin) to its authorization rule. Read-only navigation segments
// are deliberately absent — gateMutation lets them through on the coarse gate.
// The shared gamedata pick select (segPick) is also absent: its destination (and
// so its key) rides in the CustomID, so handlePickSelect re-checks itself.
var mutationGates = map[string]mutationGate{
	// Create / republish: a fixed key regardless of kind.
	segCreate:  {src: srcNone, fixedKey: keyCustom},
	segMCreate: {src: srcNone, fixedKey: keyCustom},
	segRepub:   {src: srcContract, fixedKey: keyRepublish},

	// Contract edits (details/deadline, cancel, items, rewards, location): keyed
	// by the contract's kind.
	segCEdit:   {src: srcContract},
	segMCEdit:  {src: srcContract},
	segCancel:  {src: srcContract},
	segMCancel: {src: srcContract},
	segCAdd:    {src: srcContract},
	segMCAdd:   {src: srcContract},
	segCRew:    {src: srcContract},
	segMCRew:   {src: srcContract},
	segCLoc:    {src: srcContract},
	segIDel:    {src: srcItem},
	segMIDel:   {src: srcItem},
	segIEdit:   {src: srcItem},
	segMIEdit:  {src: srcItem},
	segILink:   {src: srcItem},
	segMILink:  {src: srcItem},

	// Participant management: the "contracts manager" key, on either modal step.
	segPEdit:  {src: srcItem, fixedKey: keyManage},
	segMPEdit: {src: srcItem, fixedKey: keyManage},

	// Instantiating from a template (the pick list's Use button + its confirm
	// modal). segTemplate/segTPick (opening the pick list) stay ungated nav — the
	// home button is hidden without the key and the mutation re-checks here.
	segTUse:  {src: srcNone, fixedKey: keyTemplate},
	segMTUse: {src: srcNone, fixedKey: keyTemplate},

	// Template library management: every mutation under the stronger keyTemplates.
	// Targets are template/template-item UUIDs (server-scoped in SQL), so kind
	// resolution never applies.
	segTNew:    {src: srcNone, fixedKey: keyTemplates},
	segMTNew:   {src: srcNone, fixedKey: keyTemplates},
	segTEdit:   {src: srcNone, fixedKey: keyTemplates},
	segMTEdit:  {src: srcNone, fixedKey: keyTemplates},
	segTRew:    {src: srcNone, fixedKey: keyTemplates},
	segMTRew:   {src: srcNone, fixedKey: keyTemplates},
	segTLoc:    {src: srcNone, fixedKey: keyTemplates},
	segTAdd:    {src: srcNone, fixedKey: keyTemplates},
	segMTAdd:   {src: srcNone, fixedKey: keyTemplates},
	segTIEdit:  {src: srcNone, fixedKey: keyTemplates},
	segMTIEdit: {src: srcNone, fixedKey: keyTemplates},
	segTIDel:   {src: srcNone, fixedKey: keyTemplates},
	segTDel:    {src: srcNone, fixedKey: keyTemplates},
	segMTDel:   {src: srcNone, fixedKey: keyTemplates},
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
