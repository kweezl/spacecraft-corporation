// Package contracts is the corporation-contracts feature: a corporation posts a
// supply contract requiring large quantities of items, and any member can chip
// in toward fulfilling it before a deadline. Each contract is a Discord forum
// thread whose starter message carries a live progress embed; members run the
// /contract subcommands inside the thread, which the bot maps to the contract by
// the thread id.
//
// The delivery model is reserve-then-deliver: a member participates (reserves an
// item + amount, capped at what the contract still needs), then later delivers up
// to what they reserved; an outstanding reservation can be released (by the
// member, or by an officer on their behalf). There is no game API, so deliveries
// are self-reported on a corp-trust basis.
//
// Authorization is two-layered, like bases: the /contract subcommands are
// SubcommandGated, so the role gate decides per leaf who may invoke them; on top
// of that every mutation is scoped in SQL to the contract's server and resolved
// by thread id, so a forged thread/item id simply affects zero rows.
package contracts

import (
	"context"
	"errors"
	"fmt"
	"regexp"
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
)

// Contract is one supply contract.
type Contract struct {
	ID              uuid.UUID
	ServerID        uuid.UUID
	ThreadID        string
	Title           string
	Description     string
	Status          Status
	Deadline        time.Time
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
// items (count + overall delivered/required) but not the per-item detail.
type ListEntry struct {
	Contract
	ItemCount      int
	TotalRequired  int
	TotalDelivered int
}

// CreateInput is a new contract to persist (no thread or items yet). AppID/Token
// are the invoking interaction's identifiers, stored on the create-thread task so
// the worker can edit the original reply with the result; they are opaque to the
// repository.
type CreateInput struct {
	ServerID        uuid.UUID
	Title           string
	Description     string
	Deadline        time.Time
	CreatedByUserID string
	AppID           string
	Token           string
}

// Repository persists contracts and their items/reservations. Every method is
// scoped to a server and resolves the contract by thread id, so cross-server and
// forged-id access is impossible. serverID is the resolved servers.id.
type Repository interface {
	// Create inserts a new open contract and returns its id.
	Create(ctx context.Context, in CreateInput) (uuid.UUID, error)

	// Progress returns a contract (any status) and its items with aggregates.
	Progress(ctx context.Context, serverID uuid.UUID, threadID string) (Progress, error)
	// ProgressByID is Progress keyed by contract id (used by the outbox worker,
	// which only carries the id; the contract's thread may still be unset).
	ProgressByID(ctx context.Context, contractID uuid.UUID) (Progress, error)
	// SetThreadID records the forum thread the worker created for a contract.
	SetThreadID(ctx context.Context, contractID uuid.UUID, threadID string) error

	// AddItem adds a required item to an open contract (actor = invoker).
	AddItem(ctx context.Context, serverID uuid.UUID, threadID, itemName string, qty, maxItems int, actor string) error
	// RemoveItem deletes an item and cascades its reservations, returning how many
	// reservations were cleared.
	RemoveItem(ctx context.Context, serverID uuid.UUID, threadID, itemName, actor string) (cleared int, err error)

	// Participate additively reserves qty for a member, capped at the item's
	// remaining unreserved quantity.
	Participate(ctx context.Context, serverID uuid.UUID, threadID, itemName, userID string, qty int) error
	// Deliver adds qty to a member's delivered total (bounded by their
	// reservation) and reports whether the contract is now fully delivered; when
	// it is, the contract is flipped to completed within the same transaction.
	Deliver(ctx context.Context, serverID uuid.UUID, threadID, itemName, userID string, qty int) (complete bool, err error)
	// Release reduces a member's reservation by qty, floored at what they already
	// delivered. actor is the invoker (may differ from targetUserID for the
	// officer release-member path).
	Release(ctx context.Context, serverID uuid.UUID, threadID, itemName, targetUserID string, qty int, actor string) error

	// Cancel flips an open contract to cancelled.
	Cancel(ctx context.Context, serverID uuid.UUID, threadID, actor string) error

	// MemberOutstanding returns the items a member still has reserved-but-not-
	// delivered on a contract (deliver/release autocomplete).
	MemberOutstanding(ctx context.Context, serverID uuid.UUID, threadID, userID string) ([]MemberItem, error)

	// List returns one page of a server's contracts filtered by status (empty =
	// open), plus the total match count.
	List(ctx context.Context, serverID uuid.UUID, status string, limit, offset int) (page []ListEntry, total int, err error)

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
	CreateForumPost(channelID, name string, embed *discordgo.MessageEmbed, components []discordgo.MessageComponent) (threadID string, err error)
	EditPost(threadID string, embed *discordgo.MessageEmbed, components []discordgo.MessageComponent) error
	ClosePost(threadID string, embed *discordgo.MessageEmbed) error
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

// durationRe matches a relative duration in "Nd Nh Nm" form, each unit optional
// (but at least one required, enforced separately). Units must appear in d, h, m
// order; whitespace between them is optional.
var durationRe = regexp.MustCompile(`(?i)^\s*(?:(\d+)\s*d)?\s*(?:(\d+)\s*h)?\s*(?:(\d+)\s*m)?\s*$`)

// parseDuration turns a "Nd Nh Nm" string into a positive Duration. It rejects
// the empty match (all units absent) and a non-positive total.
func parseDuration(s string) (time.Duration, error) {
	m := durationRe.FindStringSubmatch(s)
	if m == nil || (m[1] == "" && m[2] == "" && m[3] == "") {
		return 0, ErrBadDuration
	}
	atoi := func(v string) int {
		n, _ := strconv.Atoi(v)
		return n
	}
	d := time.Duration(atoi(m[1]))*24*time.Hour +
		time.Duration(atoi(m[2]))*time.Hour +
		time.Duration(atoi(m[3]))*time.Minute
	if d <= 0 {
		return 0, ErrBadDuration
	}
	return d, nil
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

// nowAdd returns the deadline for a duration entered at create time. time.Now()
// is in the configured local zone (see CLAUDE.md), matching how deadlines are
// stored and compared.
func nowAdd(d time.Duration) time.Time { return time.Now().Add(d) }
