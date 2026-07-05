package contracts

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
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
//	contract:home                     open the dashboard (stats + new/list buttons)
//	contract:golist                   dashboard → list view (default filter)
//	contract:tmpl                     new-from-template (→ the pick list)
//	contract:cfilter                  list status filter (multi-select)
//	contract:list:<mask>:<page>       list prev/next
//	contract:view:<cid>               open a contract
//	contract:cancel:<cid>             cancel a contract (→ confirm modal)
//	contract:create                   create a custom contract (→ modal)
//	contract:cedit|cadd|crepub|cancel:<cid>  contract-view actions
//	contract:crew|cloc:<cid>          contract rewards modal / location search modal
//	contract:cback                    back to list
//	contract:irow|idel|iedit:<itemid> item actions
//	contract:ilink:<itemid>           link a legacy item to gamedata (→ search modal)
//	contract:ipage:<cid>:<page>       item-row pagination
//	contract:iback:<cid>              item view → contract view
//	contract:pedit:<itemid>:<userid>  participant manage (→ modal: action + qty)
//	contract:ppage:<itemid>:<page>    participant pagination
//
// Template library + picker (the <q> part is a url-escaped search query, see
// encQuery; it always rides LAST so it can never shift the ids before it):
//
//	contract:tlist:<page>:<q>         manage list (the Templates library)
//	contract:tpick:<page>:<q>         pick list ("New from template")
//	contract:tsearch:<mode>           open the search modal (mode m=manage, p=pick)
//	contract:tnew                     create template (→ modal: title + description)
//	contract:tview:<tid>:<page>       template edit page (page = item page)
//	contract:tedit|trew|tloc|tadd:<tid>  edit page modals (details/rewards/location/add item)
//	contract:tiedit:<tplitemid>       item qty modal
//	contract:tidels:<tid>:<page>      remove-item select (option value = item id)
//	contract:tdel:<tid>               delete template (→ typed-confirm modal)
//	contract:tuse:<tid>               instantiate (→ confirm modal: title + D/H/M)
//	contract:pick:<dst>:<uuid>        gamedata pick select (picker.go)
//	contract:brw:<dst>:<uuid>         category browser (select choice = category)
//	contract:brwi:<dst>:<uuid>:<cat>:<page>:<sub>  category page (select choice = item → qty modal)
//	contract:brwsub:<dst>:<uuid>:<cat>       subcategory filter (choice re-renders page 0)
//	contract:brws:<dst>:<uuid>        open the query modal from the browser
//	contract:m_bqty:<dst>:<uuid>:<gdid>      quantity modal submit (applies the pick)
//	contract:lbrw:<dst>:<uuid>        location picker (select choice applies)
//	contract:lclr:<dst>:<uuid>        clear the delivery location
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
	segHome     = "home"
	segList     = "golist"
	segTemplate = "tmpl"
	segFilter   = "cfilter"
	segListPage = "list"
	segView     = "view"
	segCancel   = "cancel"
	segCreate   = "create"
	segCEdit    = "cedit" // contract edit (name + description + deadline)
	segCAdd     = "cadd"
	segRepub    = "crepub"
	segCBack    = "cback"
	segIRow     = "irow"
	segIDel     = "idel"
	segIPage    = "ipage"
	segIEdit    = "iedit" // item edit (name + quantity)
	segILink    = "ilink" // link a legacy free-text item to gamedata (search modal → picker)
	segIBack    = "iback"
	segPEdit    = "pedit" // participant manage (modal: action + quantity)
	segPPage    = "ppage"
	segCRew     = "crew"    // contract rewards (modal: credits + reputation + licence + factor)
	segCLoc     = "cloc"    // contract delivery location (search modal → picker)
	segPayRep   = "payrep"  // re-post the payout report from the persisted rows (completed only)
	segPayPaid  = "paypaid" // mark the payouts as handed out in game (completed only, once)
	// segRepView / segRepPaid are the buttons on the PUBLIC payout report message
	// (posted to the reports channel). Distinct from segPayRep/segPayPaid so their
	// handlers don't UpdateMessage the shared report. Both require keyManage.
	segRepView = "repview" // open the (ephemeral) console contract view from the report
	segRepPaid = "reppaid" // mark payouts paid from the report + edit it in place
)

// Template library / picker component segments.
const (
	segTList   = "tlist" // manage list (the Templates library)
	segTPick   = "tpick" // pick list ("New from template")
	segTSearch = "tsearch"
	segTNew    = "tnew"
	segTView   = "tview"
	segTEdit   = "tedit" // template details (title + description + D/H/M)
	segTRew    = "trew"
	segTLoc    = "tloc"
	segTAdd    = "tadd"
	segTIEdit  = "tiedit"
	segTIDel   = "tidels" // remove-item select
	segTDel    = "tdel"
	segTUse    = "tuse"
	segPick    = "pick" // the shared gamedata pick select (picker.go)
)

// Category-browser segments (browse.go): the zero-typing item picker behind
// "Add item". Every id carries the pick destination + target, so the views are
// as stateless as the rest of the console.
const (
	segBrowse       = "brw"    // category list; select choice drills into a category
	segBrowseItems  = "brwi"   // one category page; select choice → quantity modal
	segBrowseSearch = "brws"   // open the type-first query modal from the browser
	segMBrowseQty   = "m_bqty" // quantity modal submit → apply the picked item
	segBrowseSub    = "brwsub" // subcategory filter on the item page
	segLocBrowse    = "lbrw"   // location picker; select choice applies immediately
	segLocClear     = "lclr"   // clear the delivery location
)

// tsearch/m_tsearch mode part: which template list the search re-renders.
const (
	tplModeManage = "m"
	tplModePick   = "p"
)

// Console modal-submit segments (carry the same ids as the opening button).
const (
	segMCreate = "m_create"
	segMCEdit  = "m_cedit"
	segMCAdd   = "m_cadd"
	segMIDel   = "m_idel"
	segMIEdit  = "m_iedit"
	segMILink  = "m_ilink"
	segMPEdit  = "m_pedit"
	segMCancel = "m_cancel"
	segMCRew   = "m_crew"

	segMTSearch = "m_tsearch"
	segMTNew    = "m_tnew"
	segMTEdit   = "m_tedit"
	segMTRew    = "m_trew"
	segMTAdd    = "m_tadd"
	segMTIEdit  = "m_tiedit"
	segMTDel    = "m_tdel"
	segMTUse    = "m_tuse"
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

// queryTokenMax caps the ENCODED search-query token inside a CustomID: Discord
// caps CustomIDs at 100 chars, and the longest carrier
// ("contract:tpick:<page>:<q>") leaves comfortable room at 48.
const queryTokenMax = 48

// encQuery makes a free-text search query safe as a CustomID part:
// url.QueryEscape encodes ":" (the part separator) and any non-ASCII, then the
// token is truncated to queryTokenMax WITHOUT splitting a %XX escape. Truncating
// a long query is fine — it is only a substring filter. The empty query encodes
// as "" (a trailing empty part round-trips through parseID).
func encQuery(q string) string {
	enc := url.QueryEscape(strings.TrimSpace(q))
	if len(enc) <= queryTokenMax {
		return enc
	}
	cut := queryTokenMax
	// Back up over a straddled %XX escape (at most two bytes).
	for i := cut - 2; i < cut && i >= 0; i++ {
		if enc[i] == '%' {
			cut = i
			break
		}
	}
	return enc[:cut]
}

// argQuery decodes the query token at parts[idx]; "" when absent or corrupt.
func argQuery(parts []string, idx int) string {
	if idx >= len(parts) {
		return ""
	}
	q, err := url.QueryUnescape(parts[idx])
	if err != nil {
		return ""
	}
	return q
}

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

// listCtx decodes the list-filter return context (status mask + page) carried
// through the contract/item views so Back and item pagination restore the filter
// the user drilled in from. An absent or zero mask falls back to the default
// (active) filter — the case for entry points with no list origin.
func listCtx(parts []string, maskIdx int) (mask, page int) {
	mask = defaultMask
	if m := argInt(parts, maskIdx); m != 0 {
		mask = m
	}
	return mask, argInt(parts, maskIdx+1)
}

// Command builds the /contracts console command. Who may run it (and thus open
// and view the console) is governed by Discord's native command permissions
// (DiscordManaged) — not a bot grant. What a member may then CREATE or EDIT is
// gated by the per-kind/republish keys (gateMutation). It also declares the
// public reserve/deliver panel's key so /permissions can grant it.
func (h *Feature) Command() *registry.Command {
	return &registry.Command{
		Def: &discordgo.ApplicationCommand{
			Name:        commandName,
			Description: "Manage corporation supply contracts",
		},
		Handler:         h.handleConsole,
		DiscordManaged:  true,
		ExtraAccessKeys: []string{panelAccessKey, keyManage},
	}
}

// Component builds the handler for every "contract:" component and modal
// interaction (the console and the public panel share the namespace).
func (h *Feature) Component() *registry.Component {
	return &registry.Component{Prefix: componentPrefix, Handler: h.handleComponent}
}

// handleConsole opens the console at the dashboard (stats + new/list buttons).
// Who can run /contracts is governed by Discord (DiscordManaged); the console is
// ephemeral, so only the invoker drives it. Create/edit actions are re-checked
// against the bot keys in gateMutation.
func (h *Feature) handleConsole(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	return h.renderHomeView(ctx, r, i, serverID, false)
}

// handleComponent routes every "contract:" interaction: modal submits first, then
// the public panel buttons, then the console. The console itself isn't coarse-
// gated (Discord controls who opened it; the message is ephemeral) — only its
// mutations are, per-action, inside routeConsoleComponent (gateMutation).
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
	return h.routeConsoleComponent(ctx, r, i, serverID, seg, parts)
}

// handleModalSubmit routes a "contract:" modal submit: the public panel's qty
// modal, else a console modal (mutations re-checked in routeConsoleModal).
func (h *Feature) handleModalSubmit(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	seg, parts, ok := parseID(i.ModalSubmitData().CustomID)
	if !ok {
		return fmt.Errorf("contracts: bad modal id %q", i.ModalSubmitData().CustomID)
	}
	if seg == segQty {
		return h.handleQtyModal(ctx, r, i, serverID)
	}
	return h.routeConsoleModal(ctx, r, i, serverID, seg, parts)
}

func (h *Feature) routeConsoleComponent(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, seg string, parts []string) error {
	if proceed, err := h.gateMutation(ctx, r, i, serverID, seg, parts); !proceed {
		return err
	}
	switch seg {
	case segHome:
		return h.renderHomeView(ctx, r, i, serverID, true)
	case segList:
		return h.renderListView(ctx, r, i, serverID, defaultMask, 0, true)
	case segTemplate:
		return h.renderTemplatesView(ctx, r, i, serverID, tplModePick, 0, "", true)
	case segTList:
		return h.renderTemplatesView(ctx, r, i, serverID, tplModeManage, argInt(parts, 0), argQuery(parts, 1), true)
	case segTPick:
		return h.renderTemplatesView(ctx, r, i, serverID, tplModePick, argInt(parts, 0), argQuery(parts, 1), true)
	case segTSearch:
		return h.openTemplateSearchModal(ctx, r, i, serverID, parts)
	case segTNew:
		return h.openTemplateNewModal(ctx, r, i, serverID)
	case segTView:
		return h.handleOpenTemplate(ctx, r, i, serverID, parts)
	case segTEdit:
		return h.openTemplateDetailsModal(ctx, r, i, serverID, parts)
	case segTRew:
		return h.openTemplateRewardsModal(ctx, r, i, serverID, parts)
	case segTLoc:
		return h.handleTemplateLocation(ctx, r, i, serverID, parts)
	case segTAdd:
		return h.handleTemplateAddItem(ctx, r, i, serverID, parts)
	case segBrowse:
		return h.pick.HandleBrowse(ctx, r, i, serverID, parts)
	case segBrowseItems:
		return h.pick.HandleBrowseItems(ctx, r, i, serverID, parts)
	case segBrowseSub:
		return h.pick.HandleBrowseSub(ctx, r, i, serverID, parts)
	case segBrowseSearch:
		return h.pick.HandleBrowseSearch(ctx, r, i, serverID, parts)
	case segTIEdit:
		return h.openTemplateItemQtyModal(ctx, r, i, serverID, parts)
	case segTIDel:
		return h.handleTemplateItemRemove(ctx, r, i, serverID, parts)
	case segTDel:
		return h.openTemplateDeleteModal(ctx, r, i, serverID, parts)
	case segTUse:
		return h.openUseTemplateModal(ctx, r, i, serverID, parts)
	case segPick:
		return h.pick.HandlePick(ctx, r, i, serverID, parts)
	case segCRew:
		return h.openContractRewardsModal(ctx, r, i, serverID, parts)
	case segCLoc:
		return h.handleContractLocation(ctx, r, i, serverID, parts)
	case segLocBrowse:
		return h.pick.HandleLocBrowse(ctx, r, i, serverID, parts)
	case segLocClear:
		return h.pick.HandleLocClear(ctx, r, i, serverID, parts)
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
	case segCEdit:
		return h.openEditModal(ctx, r, i, serverID, parts)
	case segCAdd:
		return h.handleAddItem(ctx, r, i, serverID, parts)
	case segRepub:
		return h.handleRepublish(ctx, r, i, serverID, parts)
	case segPayRep:
		return h.handlePayoutReprint(ctx, r, i, serverID, parts)
	case segPayPaid:
		return h.handlePayoutPaid(ctx, r, i, serverID, parts)
	case segRepView:
		return h.handleReportView(ctx, r, i, serverID, parts)
	case segRepPaid:
		return h.handleReportPaid(ctx, r, i, serverID, parts)
	case segCBack:
		mask, page := listCtx(parts, 0)
		return h.renderListView(ctx, r, i, serverID, mask, page, true)
	case segIRow:
		return h.handleOpenItem(ctx, r, i, serverID, parts)
	case segIDel:
		return h.openRemoveItemModal(ctx, r, i, serverID, parts)
	case segIPage:
		return h.handleItemPage(ctx, r, i, serverID, parts)
	case segIEdit:
		return h.openItemEditModal(ctx, r, i, serverID, parts)
	case segILink:
		return h.openLinkItemModal(ctx, r, i, serverID, parts)
	case segPEdit:
		return h.openParticipantModal(ctx, r, i, serverID, parts)
	case segPPage:
		return h.handleParticipantPage(ctx, r, i, serverID, parts)
	default:
		return fmt.Errorf("contracts: unknown console component seg %q", seg)
	}
}

func (h *Feature) routeConsoleModal(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, seg string, parts []string) error {
	if proceed, err := h.gateMutation(ctx, r, i, serverID, seg, parts); !proceed {
		return err
	}
	switch seg {
	case segMCreate:
		return h.submitCreate(ctx, r, i, serverID)
	case segMCEdit:
		return h.submitEdit(ctx, r, i, serverID, parts)
	case segMCAdd:
		return h.submitAddItem(ctx, r, i, serverID, parts)
	case segMIDel:
		return h.submitRemoveItem(ctx, r, i, serverID, parts)
	case segMIEdit:
		return h.submitItemEdit(ctx, r, i, serverID, parts)
	case segMILink:
		return h.submitLinkItem(ctx, r, i, serverID, parts)
	case segMBrowseQty:
		return h.pick.HandleQtySubmit(ctx, r, i, serverID, parts)
	case segMPEdit:
		return h.submitParticipant(ctx, r, i, serverID, parts)
	case segMCancel:
		return h.submitCancel(ctx, r, i, serverID, parts)
	case segMCRew:
		return h.submitContractRewards(ctx, r, i, serverID, parts)
	case segMTSearch:
		return h.submitTemplateSearch(ctx, r, i, serverID, parts)
	case segMTNew:
		return h.submitTemplateNew(ctx, r, i, serverID)
	case segMTEdit:
		return h.submitTemplateDetails(ctx, r, i, serverID, parts)
	case segMTRew:
		return h.submitTemplateRewards(ctx, r, i, serverID, parts)
	case segMTAdd:
		return h.submitTemplateAddItem(ctx, r, i, serverID, parts)
	case segMTIEdit:
		return h.submitTemplateItemQty(ctx, r, i, serverID, parts)
	case segMTDel:
		return h.submitTemplateDelete(ctx, r, i, serverID, parts)
	case segMTUse:
		return h.submitUseTemplate(ctx, r, i, serverID, parts)
	default:
		return fmt.Errorf("contracts: unknown console modal seg %q", seg)
	}
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
	case errors.Is(err, ErrOverReserved):
		return "contracts.console.over_reserved", true
	case errors.Is(err, ErrOverCap):
		return "contracts.console.over_cap", true
	case errors.Is(err, ErrQtyBelowReserved):
		return "contracts.console.qty_below_reserved", true
	case errors.Is(err, ErrBadReward):
		return "contracts.console.bad_reward", true
	case errors.Is(err, ErrTemplateNotFound):
		return "contracts.console.tpl_not_found", true
	case errors.Is(err, ErrTemplateExists):
		return "contracts.console.tpl_exists", true
	case errors.Is(err, ErrTemplateItemNotFound):
		return "contracts.console.tpl_item_not_found", true
	case errors.Is(err, ErrTemplateItemExists):
		return "contracts.console.tpl_item_exists", true
	default:
		return "", false
	}
}
