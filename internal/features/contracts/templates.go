package contracts

import (
	"context"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// A contract template is a server-owned set of DEFAULT VALUES for contracts:
// title, description, rewards, a default deadline duration, a delivery location,
// and a required-item list. Instantiating a template copies those values onto a
// new (fully editable) contract — later template edits or deletion never touch
// existing contracts; the contract's TemplateID is a stats-only provenance link.
//
// Templates are managed from the /contracts console (the Templates library,
// gated by the "contracts.templates" key) and consumed by the "New from
// template" flow (gated by the "contracts.template" key).

// Template is one contract template with its required items.
type Template struct {
	ID          uuid.UUID
	ServerID    uuid.UUID
	Title       string
	Description string
	// RewardCredits is the corpo-credits reward default (zero = no credit
	// reward; NUMERIC in the DB, carried as decimal.Decimal — never a float).
	RewardCredits       decimal.Decimal
	RewardReputation    int
	RewardLicencePoints int
	// DeadlineMinutes is the default deadline as a duration in whole minutes
	// (templates are reusable, so they store how long a contract runs, not a
	// fixed timestamp); 0 = no default deadline.
	DeadlineMinutes int
	// LocationGDID is the delivery location as a gamedata space-object GDID plus
	// the catalog version it was picked from; both empty = not set.
	LocationGDID      string
	LocationGDVersion string
	CreatedByUserID   string
	Items             []TemplateItem
}

// TemplateItem is one required line item on a template, keyed by gamedata id
// (no free-text path — templates postdate the gamedata integration).
type TemplateItem struct {
	ID        uuid.UUID
	GDID      string
	GDVersion string
	Qty       int
}

// TemplateListEntry is one template in the paginated library / picker listing.
type TemplateListEntry struct {
	ID        uuid.UUID
	Title     string
	ItemCount int
}

// TemplateRepository persists contract templates. Kept separate from Repository:
// template CRUD is a distinct lifecycle with no outbox side effects (there is no
// forum post to refresh). Every method is scoped to a server and resolves the
// target by a persistent id, so cross-server and forged-id access is impossible;
// serverID is the resolved servers.id. Implemented by the same pgRepository.
type TemplateRepository interface {
	// CreateTemplate inserts a template with the given title/description and
	// zero-value defaults for everything else (edited afterward on the template
	// page); a case-insensitive title collision yields ErrTemplateExists.
	CreateTemplate(ctx context.Context, serverID uuid.UUID, title, description, actor string) (uuid.UUID, error)
	// TemplateByID returns a template with its items, or ErrTemplateNotFound.
	TemplateByID(ctx context.Context, serverID, templateID uuid.UUID) (Template, error)
	// ListTemplates pages a server's templates filtered by a case-insensitive
	// title substring ("" = all), ordered by title, plus the total match count.
	// ILIKE metacharacters in the query match literally.
	ListTemplates(ctx context.Context, serverID uuid.UUID, titleQuery string, limit, offset int) (page []TemplateListEntry, total int, err error)
	// UpdateTemplateDetails edits title, description, and the default deadline
	// duration (whole minutes, 0 = none). A title collision with another template
	// yields ErrTemplateExists.
	UpdateTemplateDetails(ctx context.Context, serverID, templateID uuid.UUID, title, description string, deadlineMinutes int, actor string) error
	// UpdateTemplateRewards sets the three reward defaults (zero = none; the
	// columns are NOT NULL).
	UpdateTemplateRewards(ctx context.Context, serverID, templateID uuid.UUID, credits decimal.Decimal, reputation, licencePoints int, actor string) error
	// SetTemplateLocation sets (or clears, with empty gdid) the delivery
	// location default; gdid/gdVersion are set or cleared together.
	SetTemplateLocation(ctx context.Context, serverID, templateID uuid.UUID, gdid, gdVersion, actor string) error
	// AddTemplateItem adds a required item; a duplicate gdid yields
	// ErrTemplateItemExists, the maxItems cap ErrMaxItems.
	AddTemplateItem(ctx context.Context, serverID, templateID uuid.UUID, gdid, gdVersion string, qty, maxItems int, actor string) error
	// UpdateTemplateItemQty sets an item's quantity, returning the parent
	// template id (for the re-render).
	UpdateTemplateItemQty(ctx context.Context, serverID, templateItemID uuid.UUID, qty int, actor string) (templateID uuid.UUID, err error)
	// RemoveTemplateItem deletes an item, returning the parent template id.
	RemoveTemplateItem(ctx context.Context, serverID, templateItemID uuid.UUID, actor string) (templateID uuid.UUID, err error)
	// DeleteTemplate deletes a template and its items in one transaction.
	// Contracts created from it survive with their provenance link nulled
	// (ON DELETE SET NULL).
	DeleteTemplate(ctx context.Context, serverID, templateID uuid.UUID, actor string) error
}
