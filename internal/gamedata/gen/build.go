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
		// Deprecated hull pieces that survive only inside shipwrecks; their en
		// names are tagged "[DEPRECATED …]" and they carry no localization.
		"ShipHull_WreckOnly": true,
	}

	// excludedSpaceObjectIDs drops space objects by EXACT id — placeholders and
	// test entities that are not real stations (the "Empty" sentinel, the pirate
	// spawn test object). Exact match only, never substring/regex, so a real
	// future id is never caught by accident.
	excludedSpaceObjectIDs = map[string]bool{
		"Empty":        true,
		"Test_Pirates": true,
	}

	// excludedContractIDs drops stale contracts by EXACT id — the "Deprecated"
	// bucket and the bare "Tuto" tutorial template. Exact match only (so real
	// contracts like "Tuto_Material_Iron" are kept), never substring/regex.
	excludedContractIDs = map[string]bool{
		"Deprecated": true,
		"Tuto":       true,
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
	ContractNames map[i18n.Language]map[schema.GDID]string // contract id -> localized title
	FactionNames  map[i18n.Language]map[schema.GDID]string // faction code -> localized name
	SpaceObjNames map[i18n.Language]map[schema.GDID]string // space-object id -> localized name
}

// buildResult is the dataset plus a report of what happened.
type buildResult struct {
	data                dataset
	kept                []string          // kept item ids, sorted
	dropped             map[string]string // dropped item id -> reason
	icons               map[string]string // emoji name -> source icon filename (to copy)
	iconMissing         []string          // kept items that have an icon block but no alias
	droppedSpaceObjects []string          // space-object ids excluded by id, sorted
	droppedContracts    []string          // contract ids excluded by id, sorted
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
	for id, c := range s.contracts {
		if excludedContractIDs[id] {
			continue
		}
		for _, e := range c.Items {
			add(e.Item)
		}
		for _, e := range c.Rewards {
			add(e.Item)
		}
	}
	for id, so := range s.spaceObjects {
		if excludedSpaceObjectIDs[id] {
			continue
		}
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
			ContractNames: map[i18n.Language]map[schema.GDID]string{},
			FactionNames:  map[i18n.Language]map[schema.GDID]string{},
			SpaceObjNames: map[i18n.Language]map[schema.GDID]string{},
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
		if excludedContractIDs[id] {
			res.droppedContracts = append(res.droppedContracts, id)
			continue
		}
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
	sort.Strings(res.droppedContracts)
	for id, so := range s.spaceObjects {
		if excludedSpaceObjectIDs[id] {
			res.droppedSpaceObjects = append(res.droppedSpaceObjects, id)
			continue
		}
		res.data.SpaceObjects[schema.GDID(id)] = schema.SpaceObject{
			ID:       schema.GDID(id),
			Owner:    so.Owner,
			Building: so.Building,
		}
	}
	sort.Strings(res.droppedSpaceObjects)

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

		// Name-only tables (the data carries no desc for these). Contracts and
		// space objects are scoped to kept ids; factions have no entity of their
		// own, so every translated faction code is kept.
		contractNames := map[schema.GDID]string{}
		for id := range res.data.Contracts {
			if v, ok := tr.Contract[string(id)]; ok && v.Name != "" {
				contractNames[id] = v.Name
			}
		}
		spaceObjNames := map[schema.GDID]string{}
		for id := range res.data.SpaceObjects {
			if v, ok := tr.SpaceObject[string(id)]; ok && v.Name != "" {
				spaceObjNames[id] = v.Name
			}
		}
		factionNames := map[schema.GDID]string{}
		for code, v := range tr.Faction {
			if v.Name != "" {
				factionNames[schema.GDID(code)] = v.Name
			}
		}
		res.data.ContractNames[lang] = contractNames
		res.data.SpaceObjNames[lang] = spaceObjNames
		res.data.FactionNames[lang] = factionNames
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
