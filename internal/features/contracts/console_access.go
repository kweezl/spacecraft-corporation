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

// Console permission model. Who may OPEN /contracts — and thus view and navigate
// it read-only — is governed by Discord's native command permissions (the
// command is DiscordManaged); navigation needs no bot grant. Every MODIFICATION,
// plus the public payout report's buttons, requires the single "contract
// manager" key keyManage (DefaultDeny; administrators bypass). The public
// reserve/deliver/release panel is separate: it accepts the participant key
// contracts.use OR the manager key (see panel.go).
const (
	// keyManage is the one grantable "contract manager" key gating every console
	// mutation and the payout-report actions. Declared in the command's
	// ExtraAccessKeys so /permissions can grant it.
	keyManage = "contracts.manage"
)

// gatedSegments is the set of console component/modal segments that mutate (or
// are the public payout-report actions) and so require keyManage. Navigation
// segments are deliberately absent — gateMutation lets them through. The shared
// gamedata pick/browse apply paths carry their destination in the CustomID, so
// they gate themselves (authorizePick / submitBrowseQty / handlePickSelect), all
// keyed on keyManage.
var gatedSegments = map[string]bool{
	// Create / republish.
	segCreate: true, segMCreate: true,
	segRepub: true,

	// Contract edits (details/deadline, cancel, items, rewards, location) and the
	// post-completion payout controls.
	segCEdit: true, segMCEdit: true,
	segCancel: true, segMCancel: true,
	segCAdd: true, segMCAdd: true,
	segCRew: true, segMCRew: true,
	segCLoc:   true,
	segPayRep: true, segPayPaid: true,
	segIDel: true, segMIDel: true,
	segIEdit: true, segMIEdit: true,
	segILink: true, segMILink: true,

	// Participant management.
	segPEdit: true, segMPEdit: true,

	// Instantiating from a template.
	segTUse: true, segMTUse: true,

	// Template library management.
	segTNew: true, segMTNew: true,
	segTEdit: true, segMTEdit: true,
	segTRew: true, segMTRew: true,
	segTLoc: true,
	segTAdd: true, segMTAdd: true,
	segTIEdit: true, segMTIEdit: true,
	segTIDel: true,
	segTDel:  true, segMTDel: true,

	// Public payout-report buttons (posted outside the ephemeral console).
	segRepView: true, segRepPaid: true,
}

// gateMutation authorizes a console mutation before its handler runs — the
// server-side enforcement behind the buttons the views already hide. Gated
// segments require keyManage (administrators bypass); navigation segments are
// absent from gatedSegments and always proceed (the ephemeral console is only
// reachable by whoever Discord let run /contracts). It returns proceed=false once
// it has responded (a denial); err is non-nil only for an unexpected failure to
// surface to the dispatcher.
func (h *Feature) gateMutation(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, seg string, _ []string) (proceed bool, err error) {
	if !gatedSegments[seg] {
		return true, nil
	}
	ok, aerr := h.authorizedKey(ctx, i, serverID, keyManage)
	if aerr != nil {
		return false, fmt.Errorf("contracts: authorize %s: %w", keyManage, aerr)
	}
	if !ok {
		return false, h.reply(ctx, r, i, serverID, "contracts.console.denied", nil)
	}
	return true, nil
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
