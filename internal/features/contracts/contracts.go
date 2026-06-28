// Package contracts is the corporation-contracts feature: a corporation posts a
// supply contract requiring large quantities of items, and any member can chip
// in toward fulfilling it (optionally before a deadline). Each contract is a
// Discord forum thread whose starter message carries a live progress embed.
//
// There are two interaction surfaces:
//
//   - The PUBLIC panel — the Reserve/Deliver/Release buttons on the forum post,
//     which members use to self-serve. Reserve-then-deliver: a member reserves an
//     item + amount (capped at what the contract still needs), then delivers up to
//     what they reserved; an outstanding reservation can be released. Deliveries
//     are self-reported (no game API). The panel re-authorizes against the
//     grantable "contracts.use" key.
//   - The OFFICER console — the single ephemeral /contracts command, an in-place
//     control that navigates List → Contract → Item. Officers create/cancel
//     contracts, edit name/deadline, add/remove items, and release/remove other
//     members' reservations, plus Republish (re-post / refresh / recreate the
//     forum post). The console re-authorizes against the coarse "contracts" key.
//
// Authorization is layered like bases: the role gate decides who may act, and on
// top of that every mutation is scoped in SQL to the contract's server and
// resolved by a persistent UUID (contract/item) — so a forged id affects zero
// rows. Deadlines are optional: a contract with no deadline never auto-expires.
package contracts

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
)

// Config is this module's env config. Only read when the contracts feature is
// enabled.
type Config struct {
	// SweepInterval is how often the background ticker runs (expiry + pre-expiry
	// notice). It is the scheduling granularity, so keep it no longer than
	// ExpiresNotify.
	SweepInterval time.Duration `env:"CONTRACTS_SWEEP_INTERVAL" envDefault:"1m"`
	// ExpiresNotify is how long before the deadline the sweeper posts the one-shot
	// "closing soon" comment that pings every participant.
	ExpiresNotify time.Duration `env:"CONTRACTS_EXPIRES_NOTIFY" envDefault:"1h"`
	// PageSize is how many contracts one /contract list page shows.
	PageSize int `env:"CONTRACTS_LIST_PAGE_SIZE" envDefault:"8"`
	// MaxItems caps the distinct required items per contract.
	MaxItems int `env:"CONTRACTS_MAX_ITEMS" envDefault:"25"`
}

// Status is a contract's lifecycle state. Only "open" accepts mutations; the rest
// are terminal end states.
type Status string

// Contract lifecycle states. Only StatusOpen accepts mutations.
const (
	StatusOpen      Status = "open"
	StatusCompleted Status = "completed"
	StatusExpired   Status = "expired"
	StatusCancelled Status = "cancelled"
)

// Kind is how a contract was authored, which governs its allowed console actions
// and the permission key that gates them. A KindCustom contract was filled in
// field-by-field and allows the full action set (under the custom permission); a
// KindTemplate contract was instantiated from a server template and allows only
// deadline changes and cancellation (under the template permission) — its items
// are fixed.
type Kind string

// Contract kinds. KindCustom is the only kind today (template creation is WIP);
// every pre-templates row is backfilled to it.
const (
	KindCustom   Kind = "custom"
	KindTemplate Kind = "template"
)

// Outbox task kinds (registered with the outbox worker). Every Discord REST side
// effect is enqueued under one of these in the same transaction as the domain
// write, then performed asynchronously by the worker.
const (
	taskCreateThread = "contracts.thread.create"
	taskRefresh      = "contracts.thread.refresh"
	taskClose        = "contracts.thread.close"
	// taskNotify posts the one-shot "closing soon" comment pinging participants.
	taskNotify = "contracts.thread.notify"
)

// taskPayload is the JSON payload for every contracts outbox task. AppID/Token
// are only set for the create task, so the worker can edit the original
// interaction reply with the outcome (the thread link, or a permission error).
type taskPayload struct {
	ContractID uuid.UUID `json:"contract_id"`
	AppID      string    `json:"app_id,omitempty"`
	Token      string    `json:"token,omitempty"`
}

// Sentinel errors the repository returns so handlers can render the right
// user-facing message. They are not user-facing themselves.
var (
	// ErrNotFound: no contract for the thread in this server.
	ErrNotFound = errors.New("contracts: contract not found")
	// ErrClosed: the contract is not open (completed/expired/cancelled).
	ErrClosed = errors.New("contracts: contract is closed")
	// ErrExpired: the contract is still marked open but its deadline has passed
	// (the lazy deadline guard; the sweeper will flip the status shortly).
	ErrExpired = errors.New("contracts: contract deadline passed")
	// ErrItemNotFound: no such required item on the contract.
	ErrItemNotFound = errors.New("contracts: item not found")
	// ErrItemExists: an item with that (case-insensitive) name already exists.
	ErrItemExists = errors.New("contracts: item already exists")
	// ErrMaxItems: the contract already has the maximum number of items.
	ErrMaxItems = errors.New("contracts: item limit reached")
	// ErrOverCap: a reservation would exceed the item's remaining unreserved qty.
	ErrOverCap = errors.New("contracts: reservation exceeds remaining")
	// ErrOverReserved: a delivery would exceed the member's own reservation.
	ErrOverReserved = errors.New("contracts: delivery exceeds reservation")
	// ErrNoReservation: the member has no reservation on the item.
	ErrNoReservation = errors.New("contracts: no reservation")
	// ErrBelowDelivered: a release would drop the reservation below what was
	// already delivered.
	ErrBelowDelivered = errors.New("contracts: release below delivered")
	// ErrBadDuration: the duration string could not be parsed.
	ErrBadDuration = errors.New("contracts: bad duration")
	// ErrQtyBelowReserved: an item's new required quantity is below what members
	// have already reserved on it.
	ErrQtyBelowReserved = errors.New("contracts: required below reserved")
)

// Contract is one supply contract.
type Contract struct {
	ID          uuid.UUID
	ServerID    uuid.UUID
	ThreadID    string
	Title       string
	Description string
	Status      Status
	// Kind is whether the contract is custom or template (defaults to custom for
	// every pre-templates row via the backfill migration).
	Kind Kind
	// Deadline is when the contract auto-expires, or nil for a deadline-less
	// contract (never auto-expires, never gets a closing-soon notice).
	Deadline        *time.Time
	CreatedByUserID string
	// LastRefreshedAt is when the contract was last mutated (and its embed
	// re-rendered); surfaced in the embed footer ("last updated"). Advanced by
	// every mutation in lockstep with the refresh task it enqueues.
	LastRefreshedAt time.Time
}

// Item is a required line item with its progress aggregates (summed across all
// members). ReservedQty/DeliveredQty are populated by Progress, as are the
// per-member Participants (ordered by user).
type Item struct {
	ID           uuid.UUID
	Name         string
	RequiredQty  int
	ReservedQty  int
	DeliveredQty int
	Participants []Participant
}

// Participant is one member's contribution to an item: how much they have
// reserved and how much of that they have already delivered. The breakdown
// behind the embed's per-item contributor lines.
type Participant struct {
	UserID    string
	Reserved  int
	Delivered int
}

// Outstanding is how much this member still owes: reserved minus already
// delivered. Zero once they have delivered everything they reserved.
func (p Participant) Outstanding() int { return p.Reserved - p.Delivered }

// Remaining is how much of the item is not yet reserved by anyone.
func (it Item) Remaining() int {
	r := it.RequiredQty - it.ReservedQty
	if r < 0 {
		return 0
	}
	return r
}

// OutstandingReserved is the still-pending reserved amount across all members:
// reserved minus already delivered, floored at zero. The display figure for
// "reserved" — once everything reserved has been delivered it is zero and the
// reserved part of the line is dropped.
func (it Item) OutstandingReserved() int {
	r := it.ReservedQty - it.DeliveredQty
	if r < 0 {
		return 0
	}
	return r
}

// Progress is a contract plus its items with aggregates — the data behind the
// thread's progress embed.
type Progress struct {
	Contract
	Items []Item
}

// MemberItem is one item a member has an outstanding reservation on (for the
// deliver/release autocomplete pickers).
type MemberItem struct {
	Name      string
	Reserved  int
	Delivered int
}

// Outstanding is reserved minus delivered (what the member can still deliver or
// release).
func (m MemberItem) Outstanding() int { return m.Reserved - m.Delivered }

// ListEntry is one contract in the paginated listing, with a roll-up of its
// items (count + overall reserved/delivered/required, plus the distinct
// participant count) but not the per-item detail.
type ListEntry struct {
	Contract
	ItemCount        int
	TotalRequired    int
	TotalReserved    int
	TotalDelivered   int
	ParticipantCount int
}

// Counts is a server's contract tally by lifecycle state, for the console
// dashboard. Unpublished and Active are both open contracts, partitioned by
// whether their forum thread exists yet: an Unpublished one has not been posted
// (often because a Discord access error blocked the create-thread task — tap
// Republish to retry), an Active one is live in the forum. Completed and
// Cancelled are terminal ("finished" / "declined").
type Counts struct {
	Unpublished int
	Active      int
	Completed   int
	Cancelled   int
}

// CreateInput is a new contract to persist (no thread or items yet). AppID/Token
// are the invoking interaction's identifiers, stored on the create-thread task so
// the worker can edit the original reply with the result; they are opaque to the
// repository.
type CreateInput struct {
	ServerID uuid.UUID
	// Kind is the contract kind to persist (custom or template).
	Kind        Kind
	Title       string
	Description string
	// Deadline is the contract's expiry, or nil for a deadline-less contract.
	Deadline        *time.Time
	CreatedByUserID string
	AppID           string
	Token           string
}

// RepublishAction reports what Republish enqueued, so the console can render the
// right ephemeral feedback.
type RepublishAction string

const (
	// RepublishCreating means the forum post did not exist (or was lost) and a
	// create-thread task was enqueued.
	RepublishCreating RepublishAction = "creating"
	// RepublishRefreshing means the forum post exists and a refresh/close was enqueued.
	RepublishRefreshing RepublishAction = "refreshing"
)

// Repository persists contracts and their items/reservations. Every method is
// scoped to a server and resolves the target by a persistent id, so cross-server
// and forged-id access is impossible. serverID is the resolved servers.id.
//
// Methods split by surface: the public panel keys by thread id + item name
// (Progress/Participate/Deliver/Release/MemberOutstanding); the officer console
// keys by contract/item UUID (the *ByID/*Scoped methods); the worker and sweeper
// key by contract id.
type Repository interface {
	// Create inserts a new open contract and returns its id.
	Create(ctx context.Context, in CreateInput) (uuid.UUID, error)

	// Progress returns a contract (any status) and its items with aggregates,
	// resolved by thread id (the public panel).
	Progress(ctx context.Context, serverID uuid.UUID, threadID string) (Progress, error)
	// ProgressByID is Progress keyed by contract id (the outbox worker, which only
	// carries the id; the contract's thread may still be unset).
	ProgressByID(ctx context.Context, contractID uuid.UUID) (Progress, error)
	// ProgressByIDScoped is Progress keyed by contract id AND server (the console
	// Contract view); a forged/cross-server id yields ErrNotFound.
	ProgressByIDScoped(ctx context.Context, serverID, contractID uuid.UUID) (Progress, error)
	// ProgressByItemScoped loads the whole contract from one of its item ids,
	// scoped to the server (the console Item view, and re-render after item /
	// participant actions). A forged/cross-server item yields ErrNotFound.
	ProgressByItemScoped(ctx context.Context, serverID, itemID uuid.UUID) (Progress, error)

	// SetThreadID records the forum thread the worker created for a contract.
	SetThreadID(ctx context.Context, contractID uuid.UUID, threadID string) error
	// ClearThreadID unsets a contract's thread id (the post was deleted; Republish
	// will recreate it).
	ClearThreadID(ctx context.Context, contractID uuid.UUID) error
	// RequeueCreate enqueues a fresh create-thread task for a contract (used to
	// recreate a deleted post). No interaction token travels with it.
	RequeueCreate(ctx context.Context, contractID uuid.UUID) error
	// Republish enqueues the appropriate repair task (create if no thread,
	// refresh/close if it exists) in its own tx and reports which it did.
	Republish(ctx context.Context, serverID, contractID uuid.UUID) (RepublishAction, error)

	// AddItemByID adds a required item to an open contract resolved by id (console).
	AddItemByID(ctx context.Context, serverID, contractID uuid.UUID, itemName string, qty, maxItems int, actor string) error
	// RemoveItemByID deletes an item (resolved by id) and cascades its
	// reservations, returning the parent contract id and how many were cleared.
	RemoveItemByID(ctx context.Context, serverID, itemID uuid.UUID, actor string) (cid uuid.UUID, cleared int, err error)
	// UpdateItem renames an item (resolved by id) and sets its required quantity,
	// enforcing case-insensitive name uniqueness within the contract; the new
	// quantity must be at least what is already reserved (else ErrQtyBelowReserved).
	// Returns the parent contract id.
	UpdateItem(ctx context.Context, serverID, itemID uuid.UUID, newName string, newQty int, actor string) (cid uuid.UUID, err error)

	// UpdateDetails edits an open contract's title and description (console).
	UpdateDetails(ctx context.Context, serverID, contractID uuid.UUID, title, description, actor string) error
	// SetDeadline sets (or clears, with nil) an open contract's deadline and
	// re-arms its closing-soon latch (console).
	SetDeadline(ctx context.Context, serverID, contractID uuid.UUID, deadline *time.Time, actor string) error
	// CancelByID flips an open contract (resolved by id) to cancelled (console).
	CancelByID(ctx context.Context, serverID, contractID uuid.UUID, actor string) error

	// Participate additively reserves qty for a member, capped at the item's
	// remaining unreserved quantity (public panel).
	Participate(ctx context.Context, serverID uuid.UUID, threadID, itemName, userID string, qty int) error
	// Deliver adds qty to a member's delivered total (bounded by their
	// reservation) and reports whether the contract is now fully delivered; when
	// it is, the contract is flipped to completed within the same transaction.
	Deliver(ctx context.Context, serverID uuid.UUID, threadID, itemName, userID string, qty int) (complete bool, err error)
	// Release reduces a member's reservation by qty, floored at what they already
	// delivered, resolved by thread + item name (public panel, self-scoped).
	Release(ctx context.Context, serverID uuid.UUID, threadID, itemName, targetUserID string, qty int, actor string) error
	// DeliverByItem records qty delivered by a participant on an item (officer,
	// keyed by item id + target user), bounded by their outstanding
	// (reserved−delivered); it flips the contract to completed in the same
	// transaction when every item is fully delivered. Returns the parent contract
	// id and whether it is now complete.
	DeliverByItem(ctx context.Context, serverID, itemID uuid.UUID, targetUserID string, qty int, actor string) (cid uuid.UUID, complete bool, err error)
	// SetReservationByItem sets a participant's reservation on an item (officer) to
	// an absolute quantity, floored at what they have already delivered
	// (ErrBelowDelivered) and capped at the item's remaining capacity (ErrOverCap);
	// a value that leaves the row 0 reserved / 0 delivered deletes it. Returns the
	// parent contract id.
	SetReservationByItem(ctx context.Context, serverID, itemID uuid.UUID, targetUserID string, newReserved int, actor string) (cid uuid.UUID, err error)
	// RemoveReservation hard-deletes a participant's reservation on an item
	// (reserved + delivered both gone); returns the parent contract id (console).
	RemoveReservation(ctx context.Context, serverID, itemID uuid.UUID, targetUserID, actor string) (cid uuid.UUID, err error)

	// MemberOutstanding returns the items a member still has reserved-but-not-
	// delivered on a contract (the public deliver/release pickers).
	MemberOutstanding(ctx context.Context, serverID uuid.UUID, threadID, userID string) ([]MemberItem, error)

	// List returns one page of a server's contracts filtered to a set of statuses
	// (empty = open), plus the total match count.
	List(ctx context.Context, serverID uuid.UUID, statuses []Status, limit, offset int) (page []ListEntry, total int, err error)

	// Counts tallies a server's contracts by lifecycle state for the console
	// dashboard (open split into unpublished/active by forum-thread presence).
	Counts(ctx context.Context, serverID uuid.UUID) (Counts, error)

	// KindByID returns a contract's kind, scoped to the server (the console gate
	// resolves which permission a contract-keyed mutation needs). A forged or
	// cross-server id yields ErrNotFound.
	KindByID(ctx context.Context, serverID, contractID uuid.UUID) (Kind, error)
	// KindByItem returns the kind of the contract owning an item, scoped to the
	// server (the gate for item-keyed mutations). A forged/cross-server item
	// yields ErrNotFound.
	KindByItem(ctx context.Context, serverID, itemID uuid.UUID) (Kind, error)

	// DueContracts returns the ids of open contracts whose deadline is at or
	// before now, across all servers (for the global sweeper, which only needs the
	// id to MarkExpired).
	DueContracts(ctx context.Context, now time.Time, limit int) ([]uuid.UUID, error)
	// MarkExpired idempotently flips one open, past-deadline contract to expired
	// and enqueues its close task in the same tx; reports whether it actually
	// transitioned (so only the winner enqueues the close).
	MarkExpired(ctx context.Context, id uuid.UUID, now time.Time) (bool, error)

	// NotifyDue returns the ids of open, not-yet-notified contracts whose deadline
	// falls within [now, now+within] — the pre-expiry notice scan.
	NotifyDue(ctx context.Context, now time.Time, within time.Duration, limit int) ([]uuid.UUID, error)
	// MarkNotified latches one open contract's expiry_notified_at and enqueues the
	// notify task in the same tx; reports whether it transitioned, so the ping
	// fires exactly once.
	MarkNotified(ctx context.Context, id uuid.UUID, now time.Time) (bool, error)
	// OutstandingParticipantUserIDs returns the distinct user ids who still owe
	// delivery on the contract (reserved_qty > delivered_qty) — whom the expiry
	// notice pings. A member who has delivered everything they reserved has nothing
	// left to do and is excluded.
	OutstandingParticipantUserIDs(ctx context.Context, contractID uuid.UUID) ([]string, error)
}

// Gateway performs the proactive Discord operations the feature needs (create the
// forum thread, edit the live progress embed, close/lock the thread). Implemented
// by session.Live (split out from the session Manager so the contracts wiring,
// which the registry depends on, doesn't form a dependency cycle through it).
type Gateway interface {
	// CreateForumPost opens a forum thread whose starter message is a Components V2
	// card (no embed); components is the single Container built by postComponents.
	CreateForumPost(channelID, name string, components []discordgo.MessageComponent) (threadID string, err error)
	// EditPost replaces the starter message's Components V2 card in place.
	EditPost(threadID string, components []discordgo.MessageComponent) error
	// ClosePost writes the final card (no action buttons) then archives + locks.
	ClosePost(threadID string, components []discordgo.MessageComponent) error
	// DeletePost deletes a contract's forum thread (and its starter message). Used
	// to remove a stale pre-V2 post before recreating it as a Components V2 card,
	// so the migration doesn't leave a duplicate.
	DeletePost(threadID string) error
	// PostIsComponentsV2 reports whether the thread's starter message was created
	// with the Components V2 flag (immutable after creation). A post made before
	// the V2 migration returns false; the migration path keys on this so it never
	// deletes a genuine V2 post that was merely rejected for another reason.
	PostIsComponentsV2(threadID string) (bool, error)
	// CommentPost posts a plain message in the contract thread, mentioning
	// mentionUserIDs (passed through AllowedMentions so they actually ping). Used
	// for the pre-expiry "closing soon" notice.
	CommentPost(threadID, content string, mentionUserIDs []string) error
	// EditOriginalResponse edits the original reply of an interaction (by app id +
	// token), used to deliver the async create outcome. Fails once the token has
	// expired (~15 min); callers treat that as best-effort.
	EditOriginalResponse(appID, token, content string) error
}

// ForumConfig resolves and sets a server's designated contracts forum channel.
// The value lives in the core settings store (alongside theme/language), but the
// command that sets it belongs to this feature so it only exists when contracts
// is enabled. Implemented by settings.Store.
type ForumConfig interface {
	ContractsForumChannelID(ctx context.Context, serverID uuid.UUID) (string, bool)
	SetContractsForumChannelID(ctx context.Context, serverID uuid.UUID, channelID string) error
}

// parseDHM builds a deadline from the console's three-field (days/hours/minutes)
// modal. Each field is an optional non-negative integer; blank counts as zero.
// When the total is zero (all blank/zero) the result is nil — the contract has
// no deadline. A positive total returns now+total in the configured local zone.
// Negative or non-numeric input is rejected with ErrBadDuration.
func parseDHM(days, hours, mins string) (*time.Time, error) {
	d, err := atoiField(days)
	if err != nil {
		return nil, err
	}
	h, err := atoiField(hours)
	if err != nil {
		return nil, err
	}
	m, err := atoiField(mins)
	if err != nil {
		return nil, err
	}
	total := time.Duration(d)*24*time.Hour + time.Duration(h)*time.Hour + time.Duration(m)*time.Minute
	if total <= 0 {
		return nil, nil
	}
	t := time.Now().Add(total)
	return &t, nil
}

// atoiField parses one deadline-modal field: blank is zero; otherwise a
// non-negative integer (negatives and non-numbers are rejected).
func atoiField(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, ErrBadDuration
	}
	return n, nil
}

// splitDHM breaks a duration into whole days/hours/minutes for prefilling the
// deadline modal. A past/zero duration yields all zeros.
func splitDHM(d time.Duration) (days, hours, mins int) {
	if d < 0 {
		d = 0
	}
	days = int(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour
	hours = int(d / time.Hour)
	d -= time.Duration(hours) * time.Hour
	mins = int(d / time.Minute)
	return days, hours, mins
}

// formatTimeLeft renders the remaining time as "Nd Nh Nm", omitting zero units
// (e.g. "2d 5h", "3h 20m", "45m"). A past/zero duration renders as "0m". The
// unit letters are a compact numeric format (like bases' Roman numerals), built
// in Go and wrapped by the i18n template.
func formatTimeLeft(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	days := int(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour
	hours := int(d / time.Hour)
	d -= time.Duration(hours) * time.Hour
	mins := int(d / time.Minute)

	var parts []string
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if mins > 0 {
		parts = append(parts, fmt.Sprintf("%dm", mins))
	}
	if len(parts) == 0 {
		return "0m"
	}
	return strings.Join(parts, " ")
}

// normalizeItem trims surrounding whitespace from a free-text item name; matching
// is case-insensitive in SQL (lower(item_name)).
func normalizeItem(s string) string { return strings.TrimSpace(s) }
