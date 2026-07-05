// Package supply is the member-supply-requests feature: a member posts a
// personal "I need these items" request via /supply; it is published as a forum
// thread in the server's supply forum where any member can reserve / deliver /
// release the requested items via buttons. Deliberately simpler than contracts —
// no deadlines/sweeper, no rewards/payouts, no templates, no officer participant
// management, no bot permission keys. The /supply console is strictly
// self-scoped (a member sees and manages only their own requests); access to
// running /supply is Discord-managed, so this feature needs no permissions gate.
package supply

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/gamedata"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// Tunables. Unlike contracts, supply has no env Config: the only per-server knob
// (the open-request limit) is a /settings value; the rest are fixed.
const (
	// maxItems caps the distinct items per request. Discord's string-select holds
	// 25 options, so the panel's item picker is the real bound; a per-server
	// setting could follow the contracts pattern later if wanted.
	maxItems = 25
	// DefaultRequestLimit is the per-member open-request cap when a server has not
	// set its own (LimitConfig ok=false).
	DefaultRequestLimit = 10
	// consolePageSize is how many requests (and item rows) one console page shows.
	consolePageSize = 3
	// CurrentPostVersion is the format version of the forum-post card the bot
	// writes today; a lower stored value marks a stale post to migrate.
	CurrentPostVersion = 1
)

// Outbox task kinds. Every Discord REST side effect is enqueued under one of
// these in the same transaction as the domain write.
const (
	taskCreate  = "supply.thread.create"
	taskRefresh = "supply.thread.refresh"
	taskClose   = "supply.thread.close"
)

// taskPayload is the JSON payload for every supply outbox task. AppID/Token are
// only set for the create task, so the worker can edit the original interaction
// reply with the outcome (the thread link, or an error).
type taskPayload struct {
	RequestID uuid.UUID `json:"request_id"`
	AppID     string    `json:"app_id,omitempty"`
	Token     string    `json:"token,omitempty"`
}

// Status is a request's lifecycle state. Only "open" accepts mutations; the
// rest are terminal end states.
type Status string

// Request lifecycle states. Only StatusOpen accepts mutations.
const (
	StatusOpen      Status = "open"
	StatusCompleted Status = "completed"
	StatusCancelled Status = "cancelled"
)

// Sentinel errors the repository returns so handlers can render the right
// user-facing message. They are not user-facing themselves.
var (
	// ErrNotFound: no request for the (server, owner, thread/id). Also what a
	// forged/cross-owner id yields — the ownership predicate matches zero rows.
	ErrNotFound = errors.New("supply: request not found")
	// ErrClosed: the request is not open (completed/cancelled).
	ErrClosed = errors.New("supply: request is closed")
	// ErrItemNotFound: no such item on the request.
	ErrItemNotFound = errors.New("supply: item not found")
	// ErrItemExists: the request already lists that item (same gdid).
	ErrItemExists = errors.New("supply: item already exists")
	// ErrMaxItems: the request already has the maximum number of items.
	ErrMaxItems = errors.New("supply: item limit reached")
	// ErrLimit: the member is at their open-request limit.
	ErrLimit = errors.New("supply: open-request limit reached")
	// ErrOverCap: a reservation would exceed the item's remaining unreserved qty.
	ErrOverCap = errors.New("supply: reservation exceeds remaining")
	// ErrOverReserved: a delivery would exceed the member's own reservation.
	ErrOverReserved = errors.New("supply: delivery exceeds reservation")
	// ErrNoReservation: the member has no reservation on the item.
	ErrNoReservation = errors.New("supply: no reservation")
	// ErrBelowDelivered: a release would drop the reservation below delivered.
	ErrBelowDelivered = errors.New("supply: release below delivered")
	// ErrQtyBelowReserved: an item's new required qty is below what is reserved.
	ErrQtyBelowReserved = errors.New("supply: required below reserved")
)

// MessageRef is an optional Discord reference-message link, stored as its three
// identifiers (never the URL). The guild always equals the request's server
// snowflake (validated at input).
type MessageRef struct {
	GuildID   string
	ChannelID string
	MessageID string
}

// Link reconstructs the canonical Discord message link from the identifiers.
func (m MessageRef) Link() string {
	return fmt.Sprintf("https://discord.com/channels/%s/%s/%s", m.GuildID, m.ChannelID, m.MessageID)
}

// Request is one supply request.
type Request struct {
	ID          uuid.UUID
	ServerID    uuid.UUID
	OwnerUserID string
	ThreadID    string
	Title       string
	Description string
	Status      Status
	PostVersion int
	// Optional destination.
	LocationGDID      string
	LocationGDVersion string
	SystemName        string
	SystemCode        string
	PlanetNumber      *int
	// Optional Discord reference-message link (nil = unset).
	RefMessage *MessageRef
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Item is a requested line item with its progress aggregates (summed across all
// members). Supply items are gamedata-native: GDID/GDVersion are always set.
type Item struct {
	ID           uuid.UUID
	GDID         string
	GDVersion    string
	RequiredQty  int
	ReservedQty  int
	DeliveredQty int
	Participants []Participant
}

// Remaining is how much of the item is not yet reserved by anyone.
func (it Item) Remaining() int {
	if r := it.RequiredQty - it.ReservedQty; r > 0 {
		return r
	}
	return 0
}

// OutstandingReserved is the still-pending reserved amount across all members.
func (it Item) OutstandingReserved() int {
	if r := it.ReservedQty - it.DeliveredQty; r > 0 {
		return r
	}
	return 0
}

// Participant is one member's contribution to an item.
type Participant struct {
	UserID    string
	Reserved  int
	Delivered int
}

// Outstanding is how much this member still owes (reserved − delivered).
func (p Participant) Outstanding() int { return p.Reserved - p.Delivered }

// Progress is a request plus its items with progress aggregates — the shape the
// card and the console view render.
type Progress struct {
	Request
	Items []Item
}

// MemberItem is one item a member has a reservation on, for the deliver/release
// panel: the member's own reserved/delivered on it.
type MemberItem struct {
	GDID      string
	GDVersion string
	Reserved  int
	Delivered int
}

// Outstanding is reserved − delivered for this member on the item.
func (m MemberItem) Outstanding() int { return m.Reserved - m.Delivered }

// ListEntry is one row of the owner's request list.
type ListEntry struct {
	ID     uuid.UUID
	Title  string
	Status Status
}

// CreateInput is the payload for creating a request. OpenLimit is the resolved
// per-member cap enforced under lock; AppID/Token let the create task edit the
// original reply with the outcome.
type CreateInput struct {
	ServerID    uuid.UUID
	OwnerUserID string
	Title       string
	Description string
	OpenLimit   int
	AppID       string
	Token       string
}

// Repository persists supply requests. Every owner-scoped mutation carries
// owner_user_id in its SQL WHERE (bases-style), so a forged id affects zero rows
// (→ ErrNotFound). serverID is the resolved servers.id.
type Repository interface {
	Create(ctx context.Context, in CreateInput) (uuid.UUID, error)

	// Progress loads a request + items by thread (the public panel), by id (the
	// worker), or by id scoped to its owner (the console).
	Progress(ctx context.Context, serverID uuid.UUID, threadID string) (Progress, error)
	ProgressByID(ctx context.Context, requestID uuid.UUID) (Progress, error)
	ProgressByIDOwned(ctx context.Context, serverID uuid.UUID, ownerUserID string, requestID uuid.UUID) (Progress, error)
	ProgressByItemOwned(ctx context.Context, serverID uuid.UUID, ownerUserID string, itemID uuid.UUID) (Progress, error)

	SetThreadID(ctx context.Context, requestID uuid.UUID, threadID string) error
	RecreatePost(ctx context.Context, requestID uuid.UUID) error
	Republish(ctx context.Context, serverID uuid.UUID, ownerUserID string, requestID uuid.UUID) error

	UpdateDetails(ctx context.Context, serverID uuid.UUID, ownerUserID string, requestID uuid.UUID, title, description string) error
	SetDeliveryLocation(ctx context.Context, serverID uuid.UUID, ownerUserID string, requestID uuid.UUID, gdid, gdVersion string) error
	SetSystemInfo(ctx context.Context, serverID uuid.UUID, ownerUserID string, requestID uuid.UUID, systemName, systemCode string, planet *int) error
	SetMessageRef(ctx context.Context, serverID uuid.UUID, ownerUserID string, requestID uuid.UUID, guildID, channelID, messageID string) error

	AddItem(ctx context.Context, serverID uuid.UUID, ownerUserID string, requestID uuid.UUID, gdid, gdVersion string, qty, maxItems int) error
	UpdateItemQty(ctx context.Context, serverID uuid.UUID, ownerUserID string, itemID uuid.UUID, qty int) error
	RemoveItem(ctx context.Context, serverID uuid.UUID, ownerUserID string, itemID uuid.UUID) (int, error)
	Cancel(ctx context.Context, serverID uuid.UUID, ownerUserID string, requestID uuid.UUID) error

	// Panel mutations (public; self-scoped by userID), keyed by thread + item gdid.
	Reserve(ctx context.Context, serverID uuid.UUID, threadID, gdid, userID string, qty int) error
	Deliver(ctx context.Context, serverID uuid.UUID, threadID, gdid, userID string, qty int) (complete bool, err error)
	Release(ctx context.Context, serverID uuid.UUID, threadID, gdid, userID string, qty int) error
	MemberOutstanding(ctx context.Context, serverID uuid.UUID, threadID, userID string) ([]MemberItem, error)

	ListByOwner(ctx context.Context, serverID uuid.UUID, ownerUserID string, statuses []Status, limit, offset int) ([]ListEntry, int, error)
}

// Gateway is the narrow Discord surface the outbox worker drives (create the
// forum thread, edit/close the card, delete a stale post, edit the original
// reply). Implemented by session.Live.
type Gateway interface {
	CreateForumPost(channelID, name string, components []discordgo.MessageComponent) (threadID string, err error)
	EditPost(threadID string, components []discordgo.MessageComponent) error
	ClosePost(threadID string, components []discordgo.MessageComponent) error
	DeletePost(threadID string) error
	EditOriginalResponse(appID, token, content string) error
}

// ForumConfig resolves a server's designated supply forum channel. Implemented
// by settings.Store. Read-only (the /settings section owns the write path).
type ForumConfig interface {
	SupplyForumChannelID(ctx context.Context, serverID uuid.UUID) (string, bool)
}

// LimitConfig resolves a server's per-member open-request limit (ok=false → use
// DefaultRequestLimit). Implemented by settings.Store.
type LimitConfig interface {
	SupplyRequestLimit(ctx context.Context, serverID uuid.UUID) (int, bool)
}

// GameSearch is the gamedata autocomplete search the picker runs. Implemented by
// *gamedata.Searcher; a supply-local type so fx doesn't collide with contracts
// providing the same shared type (it converts implicitly to gamepick.GameSearch).
type GameSearch interface {
	Search(kind gamedata.Kind, lang i18n.Language, query string, limit int) ([]gamedata.Hit, error)
}

// LangResolver resolves the server's wording theme + language. Implemented by
// *settings.Store; supply-local for the same fx reason as GameSearch.
type LangResolver interface {
	Resolve(ctx context.Context, serverID uuid.UUID) (theme string, lang i18n.Language)
}
