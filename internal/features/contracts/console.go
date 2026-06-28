package contracts

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/discord/session"
)

// The /contracts console is a single ephemeral, in-place control that navigates
// List → Contract → Item by editing one message (UpdateMessage, including from
// modal submits). It is the officer/management surface; members self-serve via
// the public forum-post panel (panel.go).
//
// It is STATELESS: there is no server-side nav store. Every CustomID carries only
// persistent ids (contract/item UUID, Discord user id) plus two transient view
// params that never decide what to mutate — a status-filter bitmask and a page
// number. Mutations are keyed by UUID, so a click acts on exactly that object (or
// zero rows if it was deleted); parent ids are derived in SQL. Because there is
// no state, the console never expires — Discord issues a fresh token per click.
//
// CustomID grammar (namespace "contract:", routed to handleComponent):
//
//	contract:cfilter                  list status filter (multi-select)
//	contract:list:<mask>:<page>       list prev/next
//	contract:view:<cid>               open a contract
//	contract:cancel:<cid>             cancel a contract (→ confirm modal)
//	contract:create                   create a contract (→ modal)
//	contract:cname|cdead|cadd|crepub:<cid>  contract-view actions
//	contract:cback                    back to list
//	contract:irow|idel|iname:<itemid> item actions
//	contract:ipage:<cid>:<page>       item-row pagination
//	contract:iback:<cid>              item view → contract view
//	contract:prel|prem:<itemid>:<userid>  participant release/remove
//	contract:ppage:<itemid>:<page>    participant pagination
//
// Modal submits reuse the prefix with an "m_"-prefixed segment carrying the same
// ids (e.g. contract:m_cname:<cid>, contract:m_prel:<itemid>:<userid>).
const (
	// commandName is the single console command. componentPrefix is deliberately a
	// separate literal: the command renamed from "contract" to "contracts", but the
	// component namespace stays "contract" so live forum-panel buttons keep routing.
	commandName     = "contracts"
	componentPrefix = "contract"

	// consolePageSize is the per-page count for the list, item rows, and
	// participant rows — bounded by Discord's 5-action-row limit (a filter/top row,
	// up to 3 item/contract/participant rows, and a pager row).
	consolePageSize = 3
)

// Console component segments.
const (
	segFilter   = "cfilter"
	segListPage = "list"
	segView     = "view"
	segCancel   = "cancel"
	segCreate   = "create"
	segCName    = "cname"
	segCDead    = "cdead"
	segCAdd     = "cadd"
	segRepub    = "crepub"
	segCBack    = "cback"
	segIRow     = "irow"
	segIDel     = "idel"
	segIPage    = "ipage"
	segIName    = "iname"
	segIBack    = "iback"
	segPRel     = "prel"
	segPRem     = "prem"
	segPPage    = "ppage"
)

// Console modal-submit segments (carry the same ids as the opening button).
const (
	segMCreate = "m_create"
	segMCName  = "m_cname"
	segMCDead  = "m_cdead"
	segMCAdd   = "m_cadd"
	segMIDel   = "m_idel"
	segMIName  = "m_iname"
	segMPRel   = "m_prel"
	segMPRem   = "m_prem"
	segMCancel = "m_cancel"
)

// Status-filter bitmask: the list filter is a multi-select over these, encoded as
// a small int carried in the prev/next CustomIDs.
const (
	maskOpen = 1 << iota
	maskCompleted
	maskExpired
	maskCancelled

	defaultMask = maskOpen // the default filter: active (open) only
)

// statusBits maps each filter bit to its Status and select-option value.
var statusBits = []struct {
	bit    int
	status Status
	value  string
}{
	{maskOpen, StatusOpen, "open"},
	{maskCompleted, StatusCompleted, "completed"},
	{maskExpired, StatusExpired, "expired"},
	{maskCancelled, StatusCancelled, "cancelled"},
}

// statusesFromMask expands a bitmask to the Status set the repo filters on; an
// empty mask defaults to open.
func statusesFromMask(mask int) []Status {
	if mask == 0 {
		mask = defaultMask
	}
	out := make([]Status, 0, len(statusBits))
	for _, b := range statusBits {
		if mask&b.bit != 0 {
			out = append(out, b.status)
		}
	}
	return out
}

// maskFromValues folds a multi-select's chosen option values back into a bitmask;
// an empty/unknown selection defaults to open.
func maskFromValues(values []string) int {
	m := 0
	for _, v := range values {
		for _, b := range statusBits {
			if b.value == v {
				m |= b.bit
			}
		}
	}
	if m == 0 {
		m = defaultMask
	}
	return m
}

// buildID assembles a console CustomID: "contract:<seg>[:<part>...]".
func buildID(seg string, parts ...string) string {
	if len(parts) == 0 {
		return componentPrefix + ":" + seg
	}
	return componentPrefix + ":" + seg + ":" + strings.Join(parts, ":")
}

// parseID splits a CustomID into its segment and trailing parts. ok is false for
// a foreign or malformed id.
func parseID(id string) (seg string, parts []string, ok bool) {
	fields := strings.Split(id, ":")
	if len(fields) < 2 || fields[0] != componentPrefix {
		return "", nil, false
	}
	return fields[1], fields[2:], true
}

// argUUID parses parts[idx] as a UUID.
func argUUID(parts []string, idx int) (uuid.UUID, bool) {
	if idx >= len(parts) {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(parts[idx])
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// intStr renders an int for a CustomID part.
func intStr(n int) string { return strconv.Itoa(n) }

// argInt parses parts[idx] as a non-negative int, defaulting to 0 when absent.
func argInt(parts []string, idx int) int {
	if idx >= len(parts) {
		return 0
	}
	n, err := strconv.Atoi(parts[idx])
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// Command builds the /contracts console command: a single DefaultDeny command
// (admins bypass; one "contracts" grant unlocks the whole console). It also
// declares the public panel's grantable key so /permissions can grant it.
func (h *Feature) Command() *registry.Command {
	return &registry.Command{
		Def: &discordgo.ApplicationCommand{
			Name:        commandName,
			Description: "Manage corporation supply contracts",
		},
		Handler:         h.handleConsole,
		DefaultDeny:     true,
		ExtraAccessKeys: []string{panelAccessKey},
	}
}

// Component builds the handler for every "contract:" component and modal
// interaction (the console and the public panel share the namespace).
func (h *Feature) Component() *registry.Component {
	return &registry.Component{Prefix: componentPrefix, Handler: h.handleComponent}
}

// handleConsole opens the console at the list view (default active filter). The
// command dispatch was already access-gated by the registry (DefaultDeny).
func (h *Feature) handleConsole(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	return h.renderListView(ctx, r, i, serverID, defaultMask, 0, false)
}

// handleComponent routes every "contract:" interaction: modal submits first, then
// the public panel buttons, then the console (coarse-gated).
func (h *Feature) handleComponent(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	if i.Type == discordgo.InteractionModalSubmit {
		return h.handleModalSubmit(ctx, r, i, serverID)
	}
	seg, parts, ok := parseID(i.MessageComponentData().CustomID)
	if !ok {
		return fmt.Errorf("contracts: bad component id %q", i.MessageComponentData().CustomID)
	}
	if seg == segPanel {
		return h.handlePanelButton(ctx, r, i, serverID)
	}
	if ok, err := h.consoleAuthorized(ctx, i, serverID); err != nil {
		return fmt.Errorf("contracts: authorize console: %w", err)
	} else if !ok {
		return h.denyConsole(ctx, r, i, serverID)
	}
	return h.routeConsoleComponent(ctx, r, i, serverID, seg, parts)
}

// handleModalSubmit routes a "contract:" modal submit: the public panel's qty
// modal, else a coarse-gated console modal.
func (h *Feature) handleModalSubmit(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	seg, parts, ok := parseID(i.ModalSubmitData().CustomID)
	if !ok {
		return fmt.Errorf("contracts: bad modal id %q", i.ModalSubmitData().CustomID)
	}
	if seg == segQty {
		return h.handleQtyModal(ctx, r, i, serverID)
	}
	if ok, err := h.consoleAuthorized(ctx, i, serverID); err != nil {
		return fmt.Errorf("contracts: authorize console: %w", err)
	} else if !ok {
		return h.denyConsole(ctx, r, i, serverID)
	}
	return h.routeConsoleModal(ctx, r, i, serverID, seg, parts)
}

func (h *Feature) routeConsoleComponent(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, seg string, parts []string) error {
	switch seg {
	case segFilter:
		return h.handleFilter(ctx, r, i, serverID)
	case segListPage:
		return h.handleListPage(ctx, r, i, serverID, parts)
	case segView, segIBack:
		return h.handleOpenContract(ctx, r, i, serverID, parts)
	case segCreate:
		return h.openCreateModal(ctx, r, i, serverID)
	case segCancel:
		return h.openCancelModal(ctx, r, i, serverID, parts)
	case segCName:
		return h.openRenameModal(ctx, r, i, serverID, parts)
	case segCDead:
		return h.openDeadlineModal(ctx, r, i, serverID, parts)
	case segCAdd:
		return h.openAddItemModal(ctx, r, i, serverID, parts)
	case segRepub:
		return h.handleRepublish(ctx, r, i, serverID, parts)
	case segCBack:
		return h.renderListView(ctx, r, i, serverID, defaultMask, 0, true)
	case segIRow:
		return h.handleOpenItem(ctx, r, i, serverID, parts)
	case segIDel:
		return h.openRemoveItemModal(ctx, r, i, serverID, parts)
	case segIPage:
		return h.handleItemPage(ctx, r, i, serverID, parts)
	case segIName:
		return h.openItemRenameModal(ctx, r, i, serverID, parts)
	case segPRel:
		return h.openReleaseModal(ctx, r, i, serverID, parts)
	case segPRem:
		return h.openRemoveParticipantModal(ctx, r, i, serverID, parts)
	case segPPage:
		return h.handleParticipantPage(ctx, r, i, serverID, parts)
	default:
		return fmt.Errorf("contracts: unknown console component seg %q", seg)
	}
}

func (h *Feature) routeConsoleModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, seg string, parts []string) error {
	switch seg {
	case segMCreate:
		return h.submitCreate(ctx, r, i, serverID)
	case segMCName:
		return h.submitRename(ctx, r, i, serverID, parts)
	case segMCDead:
		return h.submitDeadline(ctx, r, i, serverID, parts)
	case segMCAdd:
		return h.submitAddItem(ctx, r, i, serverID, parts)
	case segMIDel:
		return h.submitRemoveItem(ctx, r, i, serverID, parts)
	case segMIName:
		return h.submitItemRename(ctx, r, i, serverID, parts)
	case segMPRel:
		return h.submitRelease(ctx, r, i, serverID, parts)
	case segMPRem:
		return h.submitRemoveParticipant(ctx, r, i, serverID, parts)
	case segMCancel:
		return h.submitCancel(ctx, r, i, serverID, parts)
	default:
		return fmt.Errorf("contracts: unknown console modal seg %q", seg)
	}
}

// consoleAuthorized re-checks the interacting member against the coarse
// "contracts" key: administrators bypass; otherwise a role must be granted it
// (DefaultDeny). With the permissions feature absent (access nil) gating is off.
func (h *Feature) consoleAuthorized(ctx context.Context, i *discordgo.InteractionCreate, serverID uuid.UUID) (bool, error) {
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
		Command:     commandName,
		UserRoles:   roles,
		DefaultDeny: true,
	})
}

// denyConsole replies that the member may not use the console.
func (h *Feature) denyConsole(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "session.denied",
		map[string]any{"Command": commandName}))
}

// respondView renders a console view as a Components V2 message: the first
// response (from the slash command) creates the ephemeral message; every later
// one edits it in place (works for both component clicks and modal submits).
func (h *Feature) respondView(i *discordgo.InteractionCreate, r registry.Responder, components []discordgo.MessageComponent, update bool) error {
	if update {
		return r.UpdateComponentsV2(i.Interaction, components)
	}
	return r.RespondComponentsV2Ephemeral(i.Interaction, components)
}

// divider is a visual separator between console rows inside a Container.
func divider() discordgo.Separator { return discordgo.Separator{Divider: boolPtr(true)} }

// consoleErr maps a repository sentinel to an ephemeral console message (leaving
// the console message as-is). Unknown errors get a generic notice and are
// surfaced to the dispatcher for logging. Every path responds, so the interaction
// is always acknowledged.
func (h *Feature) consoleErr(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, err error) error {
	if key, ok := consoleErrorKey(err); ok {
		return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, key, nil))
	}
	_ = r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.error", nil))
	return err
}

// consoleErrorKey maps a known repository sentinel to its console message key.
// ErrMaxItems is handled by its caller (it needs the limit in template data).
func consoleErrorKey(err error) (string, bool) {
	switch {
	case errors.Is(err, ErrNotFound):
		return "contracts.console.not_found", true
	case errors.Is(err, ErrClosed):
		return "contracts.console.closed", true
	case errors.Is(err, ErrExpired):
		return "contracts.console.expired", true
	case errors.Is(err, ErrItemNotFound):
		return "contracts.console.item_not_found", true
	case errors.Is(err, ErrItemExists):
		return "contracts.console.item_exists", true
	case errors.Is(err, ErrNoReservation):
		return "contracts.console.no_reservation", true
	case errors.Is(err, ErrBelowDelivered):
		return "contracts.console.below_delivered", true
	default:
		return "", false
	}
}
