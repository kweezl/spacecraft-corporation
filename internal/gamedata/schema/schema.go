// Package schema holds the dependency-free game-data value types (the entity
// shapes — Item, Category, Contract, SpaceObject). Both the build-time generator
// (internal/gamedata/gen) and the runtime registry (internal/gamedata) import it.
// The language code itself is the app-wide i18n.Language, not defined here, so
// the per-version packages key their localized tables by that shared type.
package schema

// GDID is a game-data object id — the `id` every parsed game object carries
// (items, categories, contracts, space objects) and the key the files join on.
// References between objects (a category's parent, a contract's line items) are
// GDIDs too, and the Catalog is keyed by it.
type GDID string

// Attribute is one raw item stat: a code plus its value. The human label for
// the code is localized separately via the attribute/itemType string tables.
type Attribute struct {
	Attr  string
	Value float64
}

// Item is one game item. Its display name and description are deliberately not
// stored here — they live in the per-language string tables and are resolved
// through Catalog.Name / Catalog.Desc, keyed by ID.
type Item struct {
	ID               GDID
	Type             string
	Price            float64
	Storage          float64
	LootLevel        int
	RefDesc          string
	DisplayCategory  string
	Subcategory      string
	Tags             []string
	Skills           []string
	CompatibleSkills []string
	LootMaterial     []string
	Attributes       []Attribute
	// IconName is the canonical emoji name (the icon filename without its
	// extension) for this item, or "" when the item has no icon. Resolve it to
	// a sendable token via the emoji Store: store.Format(item.IconName).
	IconName string
}

// Category is an itemType node. Categories form a tree via Parent ("" at a
// root). Localize a category's display name with Catalog.CategoryName.
type Category struct {
	ID     GDID
	Parent GDID
}

// RequestItem is an item id paired with a quantity (a contract requirement).
type RequestItem struct {
	Item GDID
	Qty  int
}

// RewardItem is an item id paired with a count (a contract reward).
type RewardItem struct {
	Item  GDID
	Count int
}

// Contract is a contract template: the items a player delivers in exchange for
// the rewards. Item / reward ids resolve against the same version's Catalog.
type Contract struct {
	ID            GDID
	Client        string
	NPC           string
	Level         int
	Duration      int
	CreditFormula float64
	Items         []RequestItem
	Rewards       []RewardItem
}

// SpaceObject is a station or other space entity.
type SpaceObject struct {
	ID       GDID
	Owner    string
	Building string
}
