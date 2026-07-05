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
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
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
}

// DefaultMaxItems caps the distinct required items per contract when a server has
// not set its own limit (the former CONTRACTS_MAX_ITEMS default, now a per-server
// /settings value resolved via ItemCap).
const DefaultMaxItems = 25

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

// Kind is how a contract was authored, which selects the permission key that
// gates its console actions. A KindCustom contract was filled in field-by-field;
// a KindTemplate contract was instantiated from a server template with the
// template's values copied in as defaults. Both kinds allow the full action set
// — a template is defaults only, so the resulting contract stays fully editable
// (its provenance is the stats-only TemplateID link).
type Kind string

// Contract kinds. Every pre-templates row is backfilled to KindCustom.
const (
	KindCustom   Kind = "custom"
	KindTemplate Kind = "template"
)

// CurrentPostVersion is the format version of the forum-post starter message the
// bot writes today. A post whose recorded post_version is below this can't be
// edited into the current format (e.g. the Components V2 flag is immutable), so
// it is migrated by deleting and recreating it. Bump this when the post format
// changes again (v3, v4, …); existing posts then re-render on their next update.
//
//	1 = embed post (pre-V2)
//	2 = Components V2 card
const CurrentPostVersion = 2

// Outbox task kinds (registered with the outbox worker). Every Discord REST side
// effect is enqueued under one of these in the same transaction as the domain
// write, then performed asynchronously by the worker.
const (
	taskCreateThread = "contracts.thread.create"
	taskRefresh      = "contracts.thread.refresh"
	taskClose        = "contracts.thread.close"
	// taskNotify posts the one-shot "closing soon" comment pinging participants.
	taskNotify = "contracts.thread.notify"
	// taskPayout computes, persists, and posts the participant reward payouts of
	// a completed contract. Its own kind so chronometric collapsing never merges
	// it with a refresh/close of the same contract.
	taskPayout = "contracts.reward.payout"
)

// taskPayload is the JSON payload for every contracts outbox task. AppID/Token
// are only set for the create task, so the worker can edit the original
// interaction reply with the outcome (the thread link, or a permission error).
// Repost is only set for a payout task re-enqueued from the console's Reprint
// button: it skips the posted-at latch and re-posts from the persisted rows.
type taskPayload struct {
	ContractID uuid.UUID `json:"contract_id"`
	AppID      string    `json:"app_id,omitempty"`
	Token      string    `json:"token,omitempty"`
	Repost     bool      `json:"repost,omitempty"`
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
	// ErrBadReward: a reward field could not be parsed (credits must be a
	// non-negative decimal with at most two fraction digits; reputation and
	// licence points non-negative integers).
	ErrBadReward = errors.New("contracts: bad reward")
	// ErrTemplateNotFound: no such template in this server.
	ErrTemplateNotFound = errors.New("contracts: template not found")
	// ErrTemplateExists: a template with that (case-insensitive) title already
	// exists in this server.
	ErrTemplateExists = errors.New("contracts: template already exists")
	// ErrTemplateItemNotFound: no such required item on the template.
	ErrTemplateItemNotFound = errors.New("contracts: template item not found")
	// ErrTemplateItemExists: the template already requires that item (same gdid).
	ErrTemplateItemExists = errors.New("contracts: template item already exists")
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
	// PostVersion is the format version of the live forum-post starter message
	// (see CurrentPostVersion). Stamped when the post is (re)created; a value below
	// CurrentPostVersion marks a stale-format post to migrate.
	PostVersion int
	// Deadline is when the contract auto-expires, or nil for a deadline-less
	// contract (never auto-expires, never gets a closing-soon notice).
	Deadline *time.Time
	// RewardCredits is the corpo-credits reward (NUMERIC in the DB, carried as
	// the app-wide decimal type — money never touches a float); nil = not set.
	// The int rewards are nil when unset. All copied from the template at
	// instantiation and editable afterward.
	RewardCredits       *decimal.Decimal
	RewardReputation    *int
	RewardLicencePoints *int
	// ParticipantRewardFactor is the percent (0–100, up to two fraction digits)
	// of RewardCredits distributed to participants when the contract completes,
	// proportional to the value each delivered. Copied from the template (or the
	// server default for custom contracts) at creation and editable while open —
	// never re-resolved. Zero = participants get nothing (NOT NULL in the DB).
	ParticipantRewardFactor decimal.Decimal
	// PayoutPostedAt is when the payout report was successfully posted to the
	// reports channel — the payout task's idempotency latch for its Discord side
	// effect; nil = not posted (or nothing to post).
	PayoutPostedAt *time.Time
	// PayoutsPaidAt / PayoutsPaidByUserID record an officer pressing "mark paid"
	// on a completed contract: when the computed payouts were handed out in game
	// and by whom. Both set together; nil/"" = not paid yet.
	PayoutsPaidAt       *time.Time
	PayoutsPaidByUserID string
	// PayoutReportChannelID / PayoutReportMessageID locate the already-posted
	// payout report, so Reprint and Mark-paid edit that one message in place
	// instead of posting a duplicate. Both empty until the report is first posted.
	PayoutReportChannelID string
	PayoutReportMessageID string
	// LocationGDID is the delivery location as a gamedata space-object GDID plus
	// the catalog version it was picked from; both empty = not set.
	LocationGDID      string
	LocationGDVersion string
	// ServerDiscordID is the owning server's Discord snowflake (discordgo's
	// guild id), resolved from the servers row — the payout task needs it to
	// look up member display names. Read-only, never written by this feature.
	ServerDiscordID string
	// TemplateID is the stats-only provenance link to the template this contract
	// was instantiated from; nil for custom contracts and after the template is
	// deleted (ON DELETE SET NULL). Never consulted for behavior. A template
	// belongs to exactly one server: the link is a composite FK on
	// (contract_templates_id, servers_id), so a cross-server template id is
	// rejected by the database itself.
	TemplateID      *uuid.UUID
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
	ID   uuid.UUID
	Name string
	// GDID/GDVersion link the item to the gamedata catalog (the id plus the
	// catalog version it was picked from). Both empty for a legacy free-text
	// item. Name is always set — for gdid items it is the localized-name
	// snapshot taken at add time, which the public panel resolves by.
	GDID         string
	GDVersion    string
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
// deliver/release autocomplete pickers). GDID/GDVersion carry the gamedata link
// (empty for a free-text item) so the op modal can show the catalog icon.
type MemberItem struct {
	Name      string
	GDID      string
	GDVersion string
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
	Deadline *time.Time
	// Rewards and delivery location, copied from the template when instantiating
	// (see the Contract fields of the same names); zero values = not set.
	RewardCredits       *decimal.Decimal
	RewardReputation    *int
	RewardLicencePoints *int
	// ParticipantRewardFactor is the concrete factor to stamp on the contract:
	// the template's value when instantiating, the server default for custom
	// contracts (prefill + copy — the contract never re-resolves it).
	ParticipantRewardFactor decimal.Decimal
	LocationGDID            string
	LocationGDVersion       string
	// TemplateID is the stats-only provenance link when instantiating from a
	// template; nil for custom contracts.
	TemplateID *uuid.UUID
	// Items are the initial required items, inserted with the contract and its
	// create-thread task in one transaction (how create-from-template stays
	// atomic). The custom create path passes none.
	Items           []CreateItemInput
	CreatedByUserID string
	AppID           string
	Token           string
}

// CreateItemInput is one initial required item for Create. Name is the localized
// display snapshot; GDID/GDVersion link it to the gamedata catalog (empty for a
// legacy free-text item).
type CreateItemInput struct {
	Name      string
	GDID      string
	GDVersion string
	Qty       int
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
	// RecreatePost atomically clears a contract's thread id and enqueues a fresh
	// create-thread task in one transaction — recovering a deleted post or
	// migrating a stale-format one. The single transaction is the safety property:
	// a crash can never leave the contract with its thread cleared but no queued
	// create (which would orphan it with no post). No interaction token travels
	// with the create.
	RecreatePost(ctx context.Context, contractID uuid.UUID) error
	// Republish enqueues the appropriate repair task (create if no thread,
	// refresh/close if it exists) in its own tx and reports which it did.
	Republish(ctx context.Context, serverID, contractID uuid.UUID) (RepublishAction, error)

	// AddItemByID adds a required item to an open contract resolved by id
	// (console). itemName is the display name (for gdid items the localized
	// snapshot); gdid/gdVersion link it to the gamedata catalog and are empty for
	// a legacy free-text item. aliases are additional names the item is known by
	// (the caller passes the catalog names in every game language), so a
	// pre-gamedata free-text item can't be duplicated under a different-language
	// snapshot. Duplicates by any name (case-insensitive) or by gdid yield
	// ErrItemExists.
	AddItemByID(ctx context.Context, serverID, contractID uuid.UUID, itemName, gdid, gdVersion string, aliases []string, qty, maxItems int, actor string) error
	// LinkItemGDID stamps a gamedata link onto an existing item (resolved by id)
	// — the assisted migration for pre-gamedata free-text items. The stored
	// item_name is untouched (it is the public panel's identity, and live
	// reservations resolve by it); display switches to the catalog name once
	// linked. Relinking an already-linked item is allowed (fixes a wrong link).
	// Another item on the contract already carrying the gdid, or whose name
	// matches one of the aliases, yields ErrItemExists. Returns the parent
	// contract id.
	LinkItemGDID(ctx context.Context, serverID, itemID uuid.UUID, gdid, gdVersion string, aliases []string, actor string) (cid uuid.UUID, err error)
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
	// UpdateRewards sets an open contract's reward fields (console). nil values
	// clear the matching column; factor is NOT NULL (zero = no participant
	// rewards), edited in the same modal.
	UpdateRewards(ctx context.Context, serverID, contractID uuid.UUID, credits *decimal.Decimal, factor decimal.Decimal, reputation, licencePoints *int, actor string) error
	// SetDeliveryLocation sets (or clears, with empty gdid) an open contract's
	// delivery location (console). gdid/gdVersion are set or cleared together.
	SetDeliveryLocation(ctx context.Context, serverID, contractID uuid.UUID, gdid, gdVersion, actor string) error
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

	// Payouts returns a contract's persisted participant payouts, ordered by user
	// id; empty = never computed (the payout worker keys idempotency on this).
	Payouts(ctx context.Context, contractID uuid.UUID) ([]Payout, error)
	// SavePayouts inserts a contract's computed payouts in one transaction.
	// Conflicting rows (a crashed earlier attempt) are left untouched, so a retry
	// can never double-insert or alter posted figures.
	SavePayouts(ctx context.Context, contractID uuid.UUID, rows []Payout) error
	// MarkPayoutPosted latches that the payout report was posted (the worker's
	// Discord-side idempotency marker) and records where — the channel + message
	// id, so a later Reprint/Mark-paid edits that message in place.
	MarkPayoutPosted(ctx context.Context, contractID uuid.UUID, channelID, messageID string, now time.Time) error
	// MarkPayoutsPaid records an officer marking a completed contract's payouts
	// as handed out in game (who + when). Guarded in SQL: reports false without
	// writing when the contract is not completed or was already marked, so a
	// concurrent double-press has exactly one winner.
	MarkPayoutsPaid(ctx context.Context, serverID, contractID uuid.UUID, actor string, now time.Time) (bool, error)
	// RequestPayoutRepost enqueues the payout task with its repost flag for a
	// completed contract (the console's Reprint button): the worker re-posts the
	// report from the persisted rows. A non-completed or forged/cross-server id
	// yields ErrNotFound.
	RequestPayoutRepost(ctx context.Context, serverID, contractID uuid.UUID) error
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
	// to remove a stale-format post before recreating it in the current format, so
	// the migration doesn't leave a duplicate.
	DeletePost(threadID string) error
	// CommentPost posts a plain message in the contract thread, mentioning
	// mentionUserIDs (passed through AllowedMentions so they actually ping). Used
	// for the pre-expiry "closing soon" notice.
	CommentPost(threadID, content string, mentionUserIDs []string) error
	// PostChannelMessage posts the payout report to the server's reports channel:
	// content + participant mentions (AllowedMentions), the CSV export as a file,
	// and an ActionsRow of buttons. Returns the new message id so a later
	// Reprint/Mark-paid edits it in place.
	PostChannelMessage(channelID, content string, mentionUserIDs []string, files []*discordgo.File, components []discordgo.MessageComponent) (messageID string, err error)
	// EditChannelMessage edits an already-posted payout report's content +
	// components in place. When files is non-nil the existing attachments are
	// replaced by them (so a Reprint after a language change refreshes the CSV);
	// nil files leaves the existing attachment untouched.
	EditChannelMessage(channelID, messageID, content string, files []*discordgo.File, components []discordgo.MessageComponent) error
	// MemberDisplayName resolves a member's display name (nick > global name >
	// username) for the payout report's name snapshots; ok is false when the
	// member can't be resolved and the caller falls back to the raw user id.
	MemberDisplayName(guildID, userID string) (name string, ok bool)
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

// ReportsConfig resolves and sets a server's designated contract reports
// channel — where completed contracts post their payout report. Like ForumConfig,
// the value lives in the core settings store but the control belongs to this
// feature. Implemented by settings.Store.
type ReportsConfig interface {
	ContractsReportsChannelID(ctx context.Context, serverID uuid.UUID) (string, bool)
	SetContractsReportsChannelID(ctx context.Context, serverID uuid.UUID, channelID string) error
}

// RewardDefaults resolves and sets a server's default participant reward factor
// (percent, 0–100; zero = participants get nothing) — the prefill for new
// templates and custom contracts. Like ForumConfig, the value lives in the core
// settings store but the control belongs to this feature. Implemented by
// settings.Store.
type RewardDefaults interface {
	ContractsRewardFactor(ctx context.Context, serverID uuid.UUID) decimal.Decimal
	SetContractsRewardFactor(ctx context.Context, serverID uuid.UUID, factor decimal.Decimal) error
}

// ItemCap resolves and sets a server's per-contract distinct-item cap (the
// former CONTRACTS_MAX_ITEMS env var). ok=false means unset — callers fall back
// to DefaultMaxItems. Like ForumConfig, the value lives in the core settings
// store but the control belongs to this feature. Implemented by settings.Store.
type ItemCap interface {
	ContractsMaxItems(ctx context.Context, serverID uuid.UUID) (int, bool)
	SetContractsMaxItems(ctx context.Context, serverID uuid.UUID, limit int) error
}

// parseDHMMinutes totals the console's three-field (days/hours/minutes) modal
// into whole minutes. Each field is an optional non-negative integer; blank
// counts as zero, so an all-blank modal totals 0 (= no deadline / no default).
// Negative or non-numeric input is rejected with ErrBadDuration.
func parseDHMMinutes(days, hours, mins string) (int, error) {
	d, err := atoiField(days)
	if err != nil {
		return 0, err
	}
	h, err := atoiField(hours)
	if err != nil {
		return 0, err
	}
	m, err := atoiField(mins)
	if err != nil {
		return 0, err
	}
	return d*24*60 + h*60 + m, nil
}

// parseDHM builds a deadline from the console's three-field modal: nil when the
// total is zero (no deadline), otherwise now+total in the configured local zone.
func parseDHM(days, hours, mins string) (*time.Time, error) {
	total, err := parseDHMMinutes(days, hours, mins)
	if err != nil || total == 0 {
		return nil, err
	}
	t := time.Now().Add(time.Duration(total) * time.Minute)
	return &t, nil
}

// dhmStrings renders a minute total as the three modal prefill strings, all
// blank for zero (a blank modal reads as "none" better than three zeros).
func dhmStrings(minutes int) (days, hours, mins string) {
	if minutes <= 0 {
		return "", "", ""
	}
	d, h, m := splitDHM(time.Duration(minutes) * time.Minute)
	return strconv.Itoa(d), strconv.Itoa(h), strconv.Itoa(m)
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

// creditsPattern is the accepted shape of a corpo-credits reward: a non-negative
// decimal with at most two fraction digits (NUMERIC(14,2) in the DB), comma
// accepted as the decimal separator.
var creditsPattern = regexp.MustCompile(`^\d{1,10}([.,]\d{1,2})?$`)

// parseCredits validates a credits modal field and returns the decimal to
// persist (comma accepted as the separator). Blank clears the reward (nil).
// Credits are NUMERIC in the DB, bound and scanned as decimal.Decimal by the
// registered pgx codec — never a float. Bad input is rejected with ErrBadReward.
func parseCredits(s string) (*decimal.Decimal, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if !creditsPattern.MatchString(s) {
		return nil, ErrBadReward
	}
	d, err := decimal.NewFromString(strings.ReplaceAll(s, ",", "."))
	if err != nil {
		return nil, ErrBadReward
	}
	return &d, nil
}

// creditsSet reports whether a credits value carries an actual reward: NULL
// (nil) and zero both count as "no reward" for display and copying.
func creditsSet(d *decimal.Decimal) bool { return d != nil && d.IsPositive() }

// factorPattern is the accepted shape of a participant reward factor: up to
// three integer digits (the range check below rejects >100) with at most two
// fraction digits, comma accepted as the decimal separator.
var factorPattern = regexp.MustCompile(`^\d{1,3}([.,]\d{1,2})?$`)

// oneHundred is the factor range cap (and the percent divisor in the payout
// pool computation).
var oneHundred = decimal.NewFromInt(100)

// parseFactor validates a participant-reward-factor modal field: blank means
// zero (no participant rewards — the column is NOT NULL, so there is no "unset");
// otherwise a decimal in [0, 100] with at most two fraction digits, comma
// accepted as the separator. Bad input is rejected with ErrBadReward.
func parseFactor(s string) (decimal.Decimal, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return decimal.Decimal{}, nil
	}
	if !factorPattern.MatchString(s) {
		return decimal.Decimal{}, ErrBadReward
	}
	d, err := decimal.NewFromString(strings.ReplaceAll(s, ",", "."))
	if err != nil || d.GreaterThan(oneHundred) {
		return decimal.Decimal{}, ErrBadReward
	}
	return d, nil
}

// parseRewardInt parses an int reward modal field: blank clears (nil); otherwise
// a non-negative integer. Bad input is rejected with ErrBadReward.
func parseRewardInt(s string) (*int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return nil, ErrBadReward
	}
	return &n, nil
}
