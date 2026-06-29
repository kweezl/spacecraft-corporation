package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/kweezl/spacecraft-corporation/internal/gamedata/schema"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// snapshot is the full effective state of one version, committed alongside its
// generated Go as the canonical basis for the next generation's diff.
type snapshot struct {
	Version       string                                   `json:"version"`
	Parent        string                                   `json:"parent"`
	Items         map[schema.GDID]schema.Item              `json:"items"`
	Categories    map[schema.GDID]schema.Category          `json:"categories"`
	Contracts     map[schema.GDID]schema.Contract          `json:"contracts"`
	SpaceObjects  map[schema.GDID]schema.SpaceObject       `json:"spaceObjects"`
	Names         map[i18n.Language]map[schema.GDID]string `json:"names"`
	Descs         map[i18n.Language]map[schema.GDID]string `json:"descs"`
	CategoryNames map[i18n.Language]map[schema.GDID]string `json:"categoryNames"`
}

func snapshotOf(version, parent string, d dataset) snapshot {
	return snapshot{
		Version:       version,
		Parent:        parent,
		Items:         d.Items,
		Categories:    d.Categories,
		Contracts:     d.Contracts,
		SpaceObjects:  d.SpaceObjects,
		Names:         d.Names,
		Descs:         d.Descs,
		CategoryNames: d.CategoryNames,
	}
}

func loadSnapshot(path string) (*snapshot, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read snapshot %s: %w", path, err)
	}
	var s snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("decode snapshot %s: %w", path, err)
	}
	return &s, nil
}

func writeSnapshot(path string, s snapshot) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// change is the per-entity diff between two effective states.
type change struct {
	Added   []string
	Removed []string
	Changed []string
}

// report is the full diff used to decide compatible-update vs new-version.
type report struct {
	Items     change
	Contracts change
	SpaceObjs change
}

// breaking is true when the change could orphan a stored link: an item or
// contract that existed before is gone. Pure additions and field tweaks are
// backward-compatible (safe to overwrite the current version in place).
func (r report) breaking() bool {
	return len(r.Items.Removed) > 0 || len(r.Contracts.Removed) > 0
}

func diffMap[K ~string, V any](prev, cur map[K]V) change {
	var c change
	for id, cv := range cur {
		pv, ok := prev[id]
		if !ok {
			c.Added = append(c.Added, string(id))
			continue
		}
		if !jsonEqual(pv, cv) {
			c.Changed = append(c.Changed, string(id))
		}
	}
	for id := range prev {
		if _, ok := cur[id]; !ok {
			c.Removed = append(c.Removed, string(id))
		}
	}
	sort.Strings(c.Added)
	sort.Strings(c.Removed)
	sort.Strings(c.Changed)
	return c
}

func diffSnapshots(prev *snapshot, cur dataset) report {
	var p snapshot
	if prev != nil {
		p = *prev
	}
	return report{
		Items:     diffMap(p.Items, cur.Items),
		Contracts: diffMap(p.Contracts, cur.Contracts),
		SpaceObjs: diffMap(p.SpaceObjects, cur.SpaceObjects),
	}
}

// deltaDataset reduces a full effective dataset to only what differs from the
// parent: added/changed entries per entity, plus the item ids removed vs the
// parent. The runtime Catalog overlays this on the parent and applies removals.
func deltaDataset(parent snapshot, eff dataset) (dataset, []schema.GDID) {
	d := dataset{
		Items:         map[schema.GDID]schema.Item{},
		Categories:    map[schema.GDID]schema.Category{},
		Contracts:     map[schema.GDID]schema.Contract{},
		SpaceObjects:  map[schema.GDID]schema.SpaceObject{},
		Names:         map[i18n.Language]map[schema.GDID]string{},
		Descs:         map[i18n.Language]map[schema.GDID]string{},
		CategoryNames: map[i18n.Language]map[schema.GDID]string{},
	}
	for id, v := range eff.Items {
		if pv, ok := parent.Items[id]; !ok || !jsonEqual(pv, v) {
			d.Items[id] = v
		}
	}
	for id, v := range eff.Categories {
		if pv, ok := parent.Categories[id]; !ok || !jsonEqual(pv, v) {
			d.Categories[id] = v
		}
	}
	for id, v := range eff.Contracts {
		if pv, ok := parent.Contracts[id]; !ok || !jsonEqual(pv, v) {
			d.Contracts[id] = v
		}
	}
	for id, v := range eff.SpaceObjects {
		if pv, ok := parent.SpaceObjects[id]; !ok || !jsonEqual(pv, v) {
			d.SpaceObjects[id] = v
		}
	}
	deltaStrings(parent.Names, eff.Names, d.Names)
	deltaStrings(parent.Descs, eff.Descs, d.Descs)
	deltaStrings(parent.CategoryNames, eff.CategoryNames, d.CategoryNames)

	var removed []schema.GDID
	for id := range parent.Items {
		if _, ok := eff.Items[id]; !ok {
			removed = append(removed, id)
		}
	}
	sort.Slice(removed, func(i, j int) bool { return removed[i] < removed[j] })
	return d, removed
}

func deltaStrings(parent, cur, out map[i18n.Language]map[schema.GDID]string) {
	for lang, m := range cur {
		dm := map[schema.GDID]string{}
		for id, v := range m {
			if parent[lang][id] != v {
				dm[id] = v
			}
		}
		out[lang] = dm
	}
}

func jsonEqual(a, b any) bool {
	ab, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return bytes.Equal(ab, bb)
}
