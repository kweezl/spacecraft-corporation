// Package bases is the member-bases feature: players register their planetary
// bases (and the bases their corporation owns) so other members can find and
// visit them to exchange resources. It contributes the /base command — register,
// unregister, equipment management, and a paginated, filterable listing.
//
// Authorization is two-layered. The /base subcommands are SubcommandGated, so
// the access gate decides per tier (own/corp/member) which roles may invoke them
// (see internal/features/permissions). On top of that, every mutation is
// ownership-scoped in SQL: the row is matched by a WHERE predicate keyed on the
// tier and owner, so a forged/mismatched base id simply affects zero rows. The
// autocomplete pickers only suggest bases the caller may target — convenience,
// never the authorization boundary.
package bases

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// Config is this module's env config: the in-game limits (mirrored here, gated
// so they can be tuned as the game changes) and the list page size.
type Config struct {
	// MemberLimit caps how many live bases one member may own (the game's
	// current per-player restriction).
	MemberLimit int `env:"BASES_MEMBER_LIMIT" envDefault:"3"`
	// CorpLimit caps how many live bases a corporation (server) may own.
	CorpLimit int `env:"BASES_CORP_LIMIT" envDefault:"6"`
	// ExtractorLimit caps extractors per base.
	ExtractorLimit int `env:"BASES_EXTRACTOR_LIMIT" envDefault:"4"`
	// ProductionLimit caps production facilities per base.
	ProductionLimit int `env:"BASES_PRODUCTION_LIMIT" envDefault:"30"`
	// PageSize is how many bases one /base list page shows.
	PageSize int `env:"BASES_LIST_PAGE_SIZE" envDefault:"8"`
}

// Kind is who owns a base: an individual member, or the corporation (server).
type Kind string

const (
	// KindMember is a base owned by an individual member (OwnerUserID set).
	KindMember Kind = "member"
	// KindCorp is a base owned by the corporation/server (no member owner).
	KindCorp Kind = "corp"
)

// Sentinel errors the repository returns so handlers can render the right
// user-facing message. They are not user-facing themselves.
var (
	// ErrBaseNotFound means no live base matched the ownership-scoped predicate:
	// the base does not exist, is deleted, or is not the caller's to act on.
	ErrBaseNotFound = errors.New("bases: base not found or not owned")
	// ErrLimitReached means an applicable limit (member/corp/extractor/
	// production) is already met, so the insert was refused.
	ErrLimitReached = errors.New("bases: limit reached")
)

// Ownership identifies the set of bases an action may touch: the server, the
// tier (member vs corp), and — for member tiers — whose bases. It is the SQL
// ownership predicate in struct form, built by the session-resolved server id
// plus the command tier, and threaded into every scoped query.
type Ownership struct {
	ServerID    uuid.UUID
	Kind        Kind
	OwnerUserID string // the owning member for KindMember; empty for KindCorp
}

// MemberOwnership scopes to one member's bases (the "own" and "member" tiers).
func MemberOwnership(serverID uuid.UUID, ownerUserID string) Ownership {
	return Ownership{ServerID: serverID, Kind: KindMember, OwnerUserID: ownerUserID}
}

// CorpOwnership scopes to the server's corp-owned bases (the "corp" tier).
func CorpOwnership(serverID uuid.UUID) Ownership {
	return Ownership{ServerID: serverID, Kind: KindCorp}
}

// Base is a registered base. Extractors/Productions are populated only where a
// query asks for them (the listing and the equipment pickers).
type Base struct {
	ID              uuid.UUID
	Kind            Kind
	OwnerUserID     string
	Name            string
	SectorName      string
	SystemCode      string
	PlanetNumber    int
	CreatedByUserID string
	Extractors      []Extractor
	Productions     []Production
}

// Extractor is one resource extractor installed on a base.
type Extractor struct {
	ID           uuid.UUID
	ResourceName string
}

// Production is one production facility installed on a base.
type Production struct {
	ID       uuid.UUID
	ItemName string
}

// RegisterInput is a new base to persist. OwnerUserID must be set for KindMember
// and empty for KindCorp (enforced by a DB check constraint too).
type RegisterInput struct {
	ServerID        uuid.UUID
	Kind            Kind
	OwnerUserID     string
	Name            string
	SectorName      string
	SystemCode      string
	PlanetNumber    int
	CreatedByUserID string
}

// Filter narrows a base listing. Every field is optional; empty fields don't
// constrain. Text fields match case-insensitively as substrings; Resource/Item
// match a base that has any extractor/production with that name.
type Filter struct {
	SectorName  string
	SystemCode  string
	BaseName    string
	Resource    string
	Item        string
	OwnerUserID string
}

// Repository persists bases and their equipment. Every method is scoped to a
// server (and, for mutations, an Ownership), so cross-server and cross-owner
// access is impossible regardless of the ids a caller supplies. serverID is the
// resolved servers.id.
type Repository interface {
	// Register inserts a base and returns its id. It refuses with
	// ErrLimitReached when the owner/corp is already at limit (checked
	// atomically against the live-base count).
	Register(ctx context.Context, in RegisterInput, limit int) (uuid.UUID, error)

	// DeleteOne soft-deletes a single base in the ownership scope, returning the
	// number of rows affected (0 = ErrBaseNotFound territory; the caller decides).
	DeleteOne(ctx context.Context, o Ownership, baseID uuid.UUID) (int, error)
	// DeleteAll soft-deletes every live base in the ownership scope, returning
	// how many were deleted.
	DeleteAll(ctx context.Context, o Ownership) (int, error)

	// ListOwned returns the live bases in the ownership scope (for the unregister
	// and equipment pickers), capped for autocomplete.
	ListOwned(ctx context.Context, o Ownership, limit int) ([]Base, error)

	// AddExtractor / AddProduction install equipment on a base the caller owns,
	// refusing with ErrBaseNotFound (not owned) or ErrLimitReached (at cap).
	AddExtractor(ctx context.Context, o Ownership, baseID uuid.UUID, resourceName string, limit int) error
	AddProduction(ctx context.Context, o Ownership, baseID uuid.UUID, itemName string, limit int) error
	// RemoveExtractor / RemoveProduction delete equipment from a base the caller
	// owns, returning rows affected (0 = not found / not owned).
	RemoveExtractor(ctx context.Context, o Ownership, extractorID uuid.UUID) (int, error)
	RemoveProduction(ctx context.Context, o Ownership, productionID uuid.UUID) (int, error)
	// ListExtractors / ListProductions return a base's equipment (for the remove
	// pickers), scoped so only an owner sees them.
	ListExtractors(ctx context.Context, o Ownership, baseID uuid.UUID) ([]Extractor, error)
	ListProductions(ctx context.Context, o Ownership, baseID uuid.UUID) ([]Production, error)

	// List returns one page of live bases on a server matching the filter, plus
	// the total match count for pagination. Returned bases carry their
	// extractors and productions.
	List(ctx context.Context, serverID uuid.UUID, f Filter, limit, offset int) (page []Base, total int, err error)
}
