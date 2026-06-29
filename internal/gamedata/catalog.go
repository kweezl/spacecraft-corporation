package gamedata

import (
	"sort"

	"github.com/kweezl/spacecraft-corporation/internal/gamedata/schema"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// Catalog is one version's read-only view of the game data. Versions form a
// parent chain — a base layer plus delta layers — so a lookup checks this layer,
// honors this layer's item removals, then falls back to the parent. The maps are
// never mutated after construction, so every method is safe for concurrent use.
type Catalog struct {
	version string
	parent  *Catalog

	items   map[schema.GDID]schema.Item
	removed map[schema.GDID]bool // item ids removed by this layer vs its parent

	categories   map[schema.GDID]schema.Category
	contracts    map[schema.GDID]schema.Contract
	spaceObjects map[schema.GDID]schema.SpaceObject

	names         map[i18n.Language]map[schema.GDID]string
	descs         map[i18n.Language]map[schema.GDID]string
	categoryNames map[i18n.Language]map[schema.GDID]string
}

func newCatalog(s source, parent *Catalog) *Catalog {
	removed := make(map[schema.GDID]bool, len(s.removedItems))
	for _, id := range s.removedItems {
		removed[id] = true
	}
	return &Catalog{
		version:       s.version,
		parent:        parent,
		items:         s.items,
		removed:       removed,
		categories:    s.categories,
		contracts:     s.contracts,
		spaceObjects:  s.spaceObjects,
		names:         s.names,
		descs:         s.descs,
		categoryNames: s.categoryNames,
	}
}

// Version is this catalog's version name (e.g. "v1").
func (c *Catalog) Version() string { return c.version }

// Item resolves an item id, walking the parent chain. A removed id reports
// false even if a parent still defines it.
func (c *Catalog) Item(id schema.GDID) (schema.Item, bool) {
	if c.removed[id] {
		return schema.Item{}, false
	}
	if it, ok := c.items[id]; ok {
		return it, true
	}
	if c.parent != nil {
		return c.parent.Item(id)
	}
	return schema.Item{}, false
}

// Category resolves an itemType id, walking the parent chain.
func (c *Catalog) Category(id schema.GDID) (schema.Category, bool) {
	if v, ok := c.categories[id]; ok {
		return v, true
	}
	if c.parent != nil {
		return c.parent.Category(id)
	}
	return schema.Category{}, false
}

// Contract resolves a contract template id, walking the parent chain.
func (c *Catalog) Contract(id schema.GDID) (schema.Contract, bool) {
	if v, ok := c.contracts[id]; ok {
		return v, true
	}
	if c.parent != nil {
		return c.parent.Contract(id)
	}
	return schema.Contract{}, false
}

// SpaceObject resolves a space-object id, walking the parent chain.
func (c *Catalog) SpaceObject(id schema.GDID) (schema.SpaceObject, bool) {
	if v, ok := c.spaceObjects[id]; ok {
		return v, true
	}
	if c.parent != nil {
		return c.parent.SpaceObject(id)
	}
	return schema.SpaceObject{}, false
}

// Name returns an item's localized display name, or "" if the item is unknown
// or has no name. A missing translation falls back to the default language.
func (c *Catalog) Name(id schema.GDID, lang i18n.Language) string {
	if c.removed[id] {
		return ""
	}
	if n, ok := localStr(c.names, id, lang); ok {
		return n
	}
	if c.parent != nil {
		return c.parent.Name(id, lang)
	}
	return ""
}

// Desc returns an item's localized description, or "" if none.
func (c *Catalog) Desc(id schema.GDID, lang i18n.Language) string {
	if c.removed[id] {
		return ""
	}
	if d, ok := localStr(c.descs, id, lang); ok {
		return d
	}
	if c.parent != nil {
		return c.parent.Desc(id, lang)
	}
	return ""
}

// CategoryName returns an itemType's localized name, or "" if none.
func (c *Catalog) CategoryName(id schema.GDID, lang i18n.Language) string {
	if n, ok := localStr(c.categoryNames, id, lang); ok {
		return n
	}
	if c.parent != nil {
		return c.parent.CategoryName(id, lang)
	}
	return ""
}

// IconName returns the canonical emoji name for an item's icon, or "" if the
// item is unknown or has no icon. Resolve it via the emoji Store.
func (c *Catalog) IconName(id schema.GDID) string {
	it, ok := c.Item(id)
	if !ok {
		return ""
	}
	return it.IconName
}

// Items returns every effective item (the flattened chain, removals applied),
// sorted by id. Allocates on each call — intended for listing, not hot paths.
func (c *Catalog) Items() []schema.Item {
	acc := map[schema.GDID]schema.Item{}
	c.collectItems(acc)
	out := make([]schema.Item, 0, len(acc))
	for _, it := range acc {
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (c *Catalog) collectItems(acc map[schema.GDID]schema.Item) {
	if c.parent != nil {
		c.parent.collectItems(acc)
	}
	for id := range c.removed {
		delete(acc, id)
	}
	for id, it := range c.items {
		acc[id] = it
	}
}

// localStr looks an id up in a language table with default-language fallback.
func localStr(m map[i18n.Language]map[schema.GDID]string, id schema.GDID, lang i18n.Language) (string, bool) {
	if t := m[lang]; t != nil {
		if v, ok := t[id]; ok {
			return v, true
		}
	}
	if lang != i18n.LanguageEN {
		if t := m[i18n.LanguageEN]; t != nil {
			if v, ok := t[id]; ok {
				return v, true
			}
		}
	}
	return "", false
}
