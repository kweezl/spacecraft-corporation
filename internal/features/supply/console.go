package supply

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
)

// The /supply console is a single ephemeral, in-place control that navigates
// List → Request by editing one message (UpdateComponentsV2, including from modal
// submits). It is strictly SELF-SCOPED: a member sees and manages only their own
// requests, enforced in SQL (every owner-scoped mutation carries owner_user_id in
// its WHERE), so there is no console-side access gate. Who can run /supply is
// governed by Discord (DiscordManaged). Members self-serve reserve/deliver/
// release from a request's public forum-post panel (panel.go).
//
// It is STATELESS: every CustomID carries only persistent ids (request/item UUID)
// plus two transient list params (a status-filter bitmask + page). Mutations are
// keyed by UUID and owner, so a click acts on exactly that object (or zero rows).
//
// CustomID grammar (namespace "supply:", routed to handleComponent):
//
//	supply:list:<mask>:<page>   list prev/next
//	supply:sfilter              list status filter (multi-select)
//	supply:new | m_new          create a request (→ modal, then submit)
//	supply:view:<rid>           open a request
//	supply:back:<mask>:<page>   request → list
//	supply:redit:<rid> | m_redit   edit title/description (→ modal, submit)
//	supply:rclose:<rid> | m_rclose typed-confirm cancel (→ modal, submit)
//	supply:rsys:<rid> | m_rsys     system name/code/planet modal (3 optional inputs)
//	supply:rref:<rid> | m_rref     reference message-link modal (1 optional input)
//	supply:rloc:<rid>              open the delivery-location browser (dest sl)
//	supply:radd:<rid>              open the item browser (dest si)
//	supply:m_iadd:<rid>            item search submit → RunPick("si", rid, query)
//	supply:repub:<rid>             republish the forum post
//	supply:ipage:<rid>:<page>      item-row pagination
//	supply:iedit:<itemid> | m_iedit  item qty edit (→ modal, submit)
//	supply:idel:<itemid> | m_idel    remove item (→ confirm modal, submit)
//
// Shared gamepick segments (prefix "supply"): pick/brw/brwi/brwsub/brws/m_bqty
// (dest si, item+qty) and lbrw/lclr (dest sl, location). Public panel: supply:
// panel:<op> and supply:qty:<op> (op = reserve|deliver|release).
const (
	commandName     = "supply"
	componentPrefix = "supply"
)

// Console component + modal segments.
const (
	segList    = "list"
	segFilter  = "sfilter"
	segNew     = "new"
	segMNew    = "m_new"
	segView    = "view"
	segBack    = "back"
	segREdit   = "redit"
	segMREdit  = "m_redit"
	segRClose  = "rclose"
	segMRClose = "m_rclose"
	segRSys    = "rsys"
	segMRSys   = "m_rsys"
	segRRef    = "rref"
	segMRRef   = "m_rref"
	segRLoc    = "rloc"
	segRAdd    = "radd"
	segMIAdd   = "m_iadd"
	segRepub   = "repub"
	segIPage   = "ipage"
	segIRow    = "irow"
	segIEdit   = "iedit"
	segMIEdit  = "m_iedit"
	segIDel    = "idel"
	segMIDel   = "m_idel"
)

// Shared gamepick segments (same literals the picker emits with prefix "supply").
const (
	segPick         = "pick"
	segBrowse       = "brw"
	segBrowseItems  = "brwi"
	segBrowseSub    = "brwsub"
	segBrowseSearch = "brws"
	segMBrowseQty   = "m_bqty"
	segLocBrowse    = "lbrw"
	segLocClear     = "lclr"
)

// Public panel segments.
const (
	segPanel = "panel"
	segQty   = "qty"
)

// Gamepick destination codes.
const (
	destItem = "si" // item + quantity
	destLoc  = "sl" // delivery location (space object)
)

// Status-filter bitmask (mirrors the contracts list filter, minus expired).
const (
	maskOpen = 1 << iota
	maskCompleted
	maskCancelled

	defaultMask = maskOpen
)

var statusBits = []struct {
	bit    int
	status Status
	value  string
}{
	{maskOpen, StatusOpen, "open"},
	{maskCompleted, StatusCompleted, "completed"},
	{maskCancelled, StatusCancelled, "cancelled"},
}

// statusesFromMask expands a bitmask to the Status set; empty defaults to open.
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

// maskFromValues folds a multi-select's chosen values back into a bitmask.
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

// --- CustomID helpers ---

func buildID(seg string, parts ...string) string {
	if len(parts) == 0 {
		return componentPrefix + ":" + seg
	}
	return componentPrefix + ":" + seg + ":" + strings.Join(parts, ":")
}

func parseID(id string) (seg string, parts []string, ok bool) {
	fields := strings.Split(id, ":")
	if len(fields) < 2 || fields[0] != componentPrefix {
		return "", nil, false
	}
	return fields[1], fields[2:], true
}

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

func intStr(n int) string { return strconv.Itoa(n) }

// listCtx decodes the (mask, page) return context carried through the request
// view so Back and item pagination restore the list filter.
func listCtx(parts []string, maskIdx int) (mask, page int) {
	mask = defaultMask
	if m := argInt(parts, maskIdx); m != 0 {
		mask = m
	}
	return mask, argInt(parts, maskIdx+1)
}

// Command builds the /supply console command. Who may run it is governed by
// Discord's native command permissions (DiscordManaged); the console is
// ephemeral and self-scoped, so there is no bot-side gate and no ExtraAccessKeys.
func (h *Feature) Command() *registry.Command {
	return &registry.Command{
		Def: &discordgo.ApplicationCommand{
			Name:        commandName,
			Description: "Post and manage your personal supply requests",
		},
		Handler:        h.handleConsole,
		DiscordManaged: true,
	}
}

// Component builds the handler for every "supply:" component and modal (the
// console and the public panel share the namespace).
func (h *Feature) Component() *registry.Component {
	return &registry.Component{Prefix: componentPrefix, Handler: h.handleComponent}
}

// handleConsole opens the console straight at the owner's request list (no home
// dashboard — supply has a single self-scoped surface).
func (h *Feature) handleConsole(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	return h.renderListView(ctx, r, i, serverID, defaultMask, 0, false)
}

// handleComponent routes every "supply:" interaction: modal submits first, then
// the public panel buttons, then the console.
func (h *Feature) handleComponent(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	if i.Type == discordgo.InteractionModalSubmit {
		return h.handleModalSubmit(ctx, r, i, serverID)
	}
	seg, parts, ok := parseID(i.MessageComponentData().CustomID)
	if !ok {
		return fmt.Errorf("supply: bad component id %q", i.MessageComponentData().CustomID)
	}
	if seg == segPanel {
		return h.handlePanelButton(ctx, r, i, serverID)
	}
	return h.routeComponent(ctx, r, i, serverID, seg, parts)
}

func (h *Feature) handleModalSubmit(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	seg, parts, ok := parseID(i.ModalSubmitData().CustomID)
	if !ok {
		return fmt.Errorf("supply: bad modal id %q", i.ModalSubmitData().CustomID)
	}
	if seg == segQty {
		return h.handleQtyModal(ctx, r, i, serverID)
	}
	return h.routeModal(ctx, r, i, serverID, seg, parts)
}

func (h *Feature) routeComponent(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, seg string, parts []string) error {
	switch seg {
	case segList:
		mask, page := listCtx(parts, 0)
		return h.renderListView(ctx, r, i, serverID, mask, page, true)
	case segFilter:
		return h.handleFilter(ctx, r, i, serverID)
	case segNew:
		return h.openCreateModal(ctx, r, i, serverID)
	case segView:
		return h.handleOpenRequest(ctx, r, i, serverID, parts)
	case segBack:
		mask, page := listCtx(parts, 0)
		return h.renderListView(ctx, r, i, serverID, mask, page, true)
	case segREdit:
		return h.openEditModal(ctx, r, i, serverID, parts)
	case segRClose:
		return h.openCloseModal(ctx, r, i, serverID, parts)
	case segRSys:
		return h.openSystemModal(ctx, r, i, serverID, parts)
	case segRRef:
		return h.openRefModal(ctx, r, i, serverID, parts)
	case segRLoc:
		return h.handleOpenLocation(ctx, r, i, serverID, parts)
	case segRAdd:
		return h.handleOpenAddItem(ctx, r, i, serverID, parts)
	case segRepub:
		return h.handleRepublish(ctx, r, i, serverID, parts)
	case segIPage:
		return h.handleItemPage(ctx, r, i, serverID, parts)
	case segIRow:
		return h.handleOpenItem(ctx, r, i, serverID, parts)
	case segIEdit:
		return h.openItemQtyModal(ctx, r, i, serverID, parts)
	case segIDel:
		return h.openRemoveItemModal(ctx, r, i, serverID, parts)
	// Shared gamepick component segments.
	case segPick:
		return h.pick.HandlePick(ctx, r, i, serverID, parts)
	case segBrowse:
		return h.pick.HandleBrowse(ctx, r, i, serverID, parts)
	case segBrowseItems:
		return h.pick.HandleBrowseItems(ctx, r, i, serverID, parts)
	case segBrowseSub:
		return h.pick.HandleBrowseSub(ctx, r, i, serverID, parts)
	case segBrowseSearch:
		return h.pick.HandleBrowseSearch(ctx, r, i, serverID, parts)
	case segLocBrowse:
		return h.pick.HandleLocBrowse(ctx, r, i, serverID, parts)
	case segLocClear:
		return h.pick.HandleLocClear(ctx, r, i, serverID, parts)
	default:
		return fmt.Errorf("supply: unknown console component seg %q", seg)
	}
}

func (h *Feature) routeModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, seg string, parts []string) error {
	switch seg {
	case segMNew:
		return h.submitCreate(ctx, r, i, serverID)
	case segMREdit:
		return h.submitEdit(ctx, r, i, serverID, parts)
	case segMRClose:
		return h.submitClose(ctx, r, i, serverID, parts)
	case segMRSys:
		return h.submitSystem(ctx, r, i, serverID, parts)
	case segMRRef:
		return h.submitRef(ctx, r, i, serverID, parts)
	case segMIAdd:
		return h.submitAddItemSearch(ctx, r, i, serverID, parts)
	case segMIEdit:
		return h.submitItemQty(ctx, r, i, serverID, parts)
	case segMIDel:
		return h.submitRemoveItem(ctx, r, i, serverID, parts)
	case segMBrowseQty:
		return h.pick.HandleQtySubmit(ctx, r, i, serverID, parts)
	default:
		return fmt.Errorf("supply: unknown console modal seg %q", seg)
	}
}

// respondView renders a console view as a Components V2 message: the first
// response creates the ephemeral message; later ones edit it in place.
func (h *Feature) respondView(i *discordgo.InteractionCreate, r registry.Responder, components []discordgo.MessageComponent, update bool) error {
	if update {
		return r.UpdateComponentsV2(i.Interaction, components)
	}
	return r.RespondComponentsV2Ephemeral(i.Interaction, components)
}

// divider is a visual separator between console rows inside a Container.
func divider() discordgo.Separator { return discordgo.Separator{Divider: boolPtr(true)} }

func boolPtr(b bool) *bool { return &b }
func intPtr(n int) *int    { return &n }
