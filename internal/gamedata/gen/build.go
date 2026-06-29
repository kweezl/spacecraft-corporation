package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kweezl/spacecraft-corporation/internal/gamedata/schema"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// Exclusion rules, applied at generation time. An item matching any rule is
// dropped from the catalog (and so gets no icon emoji) — UNLESS it is referenced
// by a contract or space object, which always wins (see referencedIDs). Items
// with no displayCategory at all (the heterogeneous "None" bucket: Empty*,
// *_Virtual, Instance*, loot/spawn markers, decals) are dropped too, again
// except when referenced (which rescues currencies like CorpoCredits).
var (
	excludedDisplayCategories = map[string]bool{
		"Knowledge": true,
		"QuestItem": true,
	}
	excludedSubcategories = map[string]bool{
		"Scrap":             true,
		"ShipDecorative":    true,
		"BaseBuilding_Deco": true,
	}
)

// dataset is the effective, kept game data for one generation.
type dataset struct {
	Items         map[schema.GDID]schema.Item
	Categories    map[schema.GDID]schema.Category
	Contracts     map[schema.GDID]schema.Contract
	SpaceObjects  map[schema.GDID]schema.SpaceObject
	Names         map[i18n.Language]map[schema.GDID]string // item id -> localized name
	Descs         map[i18n.Language]map[schema.GDID]string // item id -> localized description
	CategoryNames map[i18n.Language]map[schema.GDID]string // itemType id -> localized name
}

// buildResult is the dataset plus a report of what happened.
type buildResult struct {
	data        dataset
	kept        []string          // kept item ids, sorted
	dropped     map[string]string // dropped item id -> reason
	icons       map[string]string // emoji name -> source icon filename (to copy)
	iconMissing []string          // kept items that have an icon block but no alias
}

// referencedIDs collects every item id named by a contract (requirement or
// reward) or a space-object buyout. These are kept regardless of category so
// link resolution never misses an item a contract points at.
func referencedIDs(s *source) map[string]bool {
	r := map[string]bool{}
	add := func(id string) {
		if id != "" {
			r[id] = true
		}
	}
	for _, c := range s.contracts {
		for _, e := range c.Items {
			add(e.Item)
		}
		for _, e := range c.Rewards {
			add(e.Item)
		}
	}
	for _, so := range s.spaceObjects {
		for _, e := range so.Props.Buyout {
			add(e.Item)
		}
	}
	return r
}

// excluded reports whether an item is dropped, with a human reason for the
// report. A referenced item is never excluded.
func excluded(it rawItem, referenced map[string]bool) (bool, string) {
	if referenced[it.ID] {
		return false, ""
	}
	if it.DisplayCategory == nil {
		return true, "no-category"
	}
	if excludedDisplayCategories[*it.DisplayCategory] {
		return true, "category:" + *it.DisplayCategory
	}
	if excludedSubcategories[it.Subcategory] {
		return true, "subcategory:" + it.Subcategory
	}
	return false, ""
}

// buildDataset applies the rules and assembles the effective kept data.
func buildDataset(s *source) (*buildResult, error) {
	ref := referencedIDs(s)
	res := &buildResult{
		data: dataset{
			Items:         map[schema.GDID]schema.Item{},
			Categories:    map[schema.GDID]schema.Category{},
			Contracts:     map[schema.GDID]schema.Contract{},
			SpaceObjects:  map[schema.GDID]schema.SpaceObject{},
			Names:         map[i18n.Language]map[schema.GDID]string{},
			Descs:         map[i18n.Language]map[schema.GDID]string{},
			CategoryNames: map[i18n.Language]map[schema.GDID]string{},
		},
		dropped: map[string]string{},
		icons:   map[string]string{},
	}

	for id, it := range s.items {
		if drop, reason := excluded(it, ref); drop {
			res.dropped[id] = reason
			continue
		}
		iconName := ""
		if file := s.aliases[id]; file != "" {
			iconName = strings.TrimSuffix(file, filepath.Ext(file))
			if !validEmojiName(iconName) {
				return nil, fmt.Errorf("item %q: icon %q is not a valid emoji name (2-32 of [A-Za-z0-9_])", id, iconName)
			}
			res.icons[iconName] = file
		} else if it.Icon != nil {
			res.iconMissing = append(res.iconMissing, id)
		}
		res.data.Items[schema.GDID(id)] = schema.Item{
			ID:               schema.GDID(id),
			Type:             it.Type,
			Price:            it.Price,
			Storage:          it.Storage,
			LootLevel:        it.LootLevel,
			RefDesc:          it.RefDesc,
			DisplayCategory:  derefStr(it.DisplayCategory),
			Subcategory:      it.Subcategory,
			Tags:             it.Tags,
			Skills:           it.Skills,
			CompatibleSkills: it.CompatibleSkills,
			LootMaterial:     it.LootMaterial,
			Attributes:       convAttrs(it.Attributes),
			IconName:         iconName,
		}
		res.kept = append(res.kept, id)
	}
	sort.Strings(res.kept)

	for id, c := range s.categories {
		res.data.Categories[schema.GDID(id)] = schema.Category{ID: schema.GDID(id), Parent: schema.GDID(c.Parent)}
	}
	for id, c := range s.contracts {
		res.data.Contracts[schema.GDID(id)] = schema.Contract{
			ID:            schema.GDID(id),
			Client:        c.Client,
			NPC:           c.NPC,
			Level:         c.Level,
			Duration:      c.Duration,
			CreditFormula: c.CreditFormula,
			Items:         convRequest(c.Items),
			Rewards:       convReward(c.Rewards),
		}
	}
	for id, so := range s.spaceObjects {
		res.data.SpaceObjects[schema.GDID(id)] = schema.SpaceObject{
			ID:       schema.GDID(id),
			Owner:    so.Owner,
			Building: so.Building,
		}
	}

	for lang, tr := range s.translations {
		names, descs, catNames := map[schema.GDID]string{}, map[schema.GDID]string{}, map[schema.GDID]string{}
		for id := range res.data.Items {
			if v, ok := tr.Item[string(id)]; ok {
				if v.Name != "" {
					names[id] = v.Name
				}
				if v.Desc != "" {
					descs[id] = v.Desc
				}
			}
		}
		for id := range res.data.Categories {
			if v, ok := tr.ItemType[string(id)]; ok && v.Name != "" {
				catNames[id] = v.Name
			}
		}
		res.data.Names[lang] = names
		res.data.Descs[lang] = descs
		res.data.CategoryNames[lang] = catNames
	}

	return res, nil
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func convAttrs(in []rawAttr) []schema.Attribute {
	if len(in) == 0 {
		return nil
	}
	out := make([]schema.Attribute, len(in))
	for i, a := range in {
		out[i] = schema.Attribute{Attr: a.Attr, Value: a.Value}
	}
	return out
}

func convRequest(in []rawItemQty) []schema.RequestItem {
	if len(in) == 0 {
		return nil
	}
	out := make([]schema.RequestItem, len(in))
	for i, e := range in {
		out[i] = schema.RequestItem{Item: schema.GDID(e.Item), Qty: e.Qty}
	}
	return out
}

func convReward(in []rawItemCount) []schema.RewardItem {
	if len(in) == 0 {
		return nil
	}
	out := make([]schema.RewardItem, len(in))
	for i, e := range in {
		out[i] = schema.RewardItem{Item: schema.GDID(e.Item), Count: e.Count}
	}
	return out
}

// validEmojiName mirrors Discord's emoji-name rule (2-32 chars of [A-Za-z0-9_]).
// It is duplicated from internal/emoji (whose copy is unexported) so the
// generator can reject a bad icon name before writing an unusable asset.
func validEmojiName(name string) bool {
	if len(name) < 2 || len(name) > 32 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
}
