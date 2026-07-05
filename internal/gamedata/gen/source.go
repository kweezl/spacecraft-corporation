package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// The raw JSON shapes as emitted by the spacecraft-resources pipeline
// (https://github.com/kweezl/spacecraft-resources, the generated/ folder).
// Only the fields the bot consumes are decoded; everything else is ignored.

type rawIcon struct {
	File string `json:"file"`
}

type rawAttr struct {
	Attr  string  `json:"attr"`
	Value float64 `json:"value"`
}

type rawItem struct {
	ID               string    `json:"id"`
	Type             string    `json:"type"`
	Price            float64   `json:"price"`
	Storage          float64   `json:"storage"`
	LootLevel        int       `json:"lootLevel"`
	RefDesc          string    `json:"refDesc"`
	DisplayCategory  *string   `json:"displayCategory"`
	Subcategory      string    `json:"subcategory"`
	Tags             []string  `json:"tags"`
	Skills           []string  `json:"skills"`
	CompatibleSkills []string  `json:"compatibleSkills"`
	LootMaterial     []string  `json:"lootMaterial"`
	Attributes       []rawAttr `json:"attributes"`
	Icon             *rawIcon  `json:"icon"`
	// InGame is the resources pipeline's best-effort "ships in the game" marker
	// (referenced/placed/named in a scene, minus dev placeholders). A pointer so
	// an absent field means "show" (default in game), per the resources contract.
	InGame *bool `json:"inGame"`
}

type rawCategory struct {
	ID     string `json:"id"`
	Parent string `json:"parent"`
}

type rawItemQty struct {
	Item string `json:"item"`
	Qty  int    `json:"qty"`
}

type rawItemCount struct {
	Item  string `json:"item"`
	Count int    `json:"count"`
}

type rawContract struct {
	ID            string         `json:"id"`
	Client        string         `json:"client"`
	NPC           string         `json:"npc"`
	Level         int            `json:"level"`
	Duration      int            `json:"duration"`
	CreditFormula float64        `json:"creditFormula"`
	Items         []rawItemQty   `json:"items"`
	Rewards       []rawItemCount `json:"rewards"`
	InGame        *bool          `json:"inGame"`
}

type rawSpaceObject struct {
	ID       string `json:"id"`
	Owner    string `json:"owner"`
	Building string `json:"building"`
	Props    struct {
		Buyout []struct {
			Item string `json:"item"`
		} `json:"buyout"`
	} `json:"props"`
	InGame *bool `json:"inGame"`
}

type rawTranslation struct {
	Item        map[string]rawString `json:"item"`
	ItemType    map[string]rawString `json:"itemType"`
	Contract    map[string]rawString `json:"contract"`
	Faction     map[string]rawString `json:"faction"`
	SpaceObject map[string]rawString `json:"spaceObject"`
}

type rawString struct {
	Name string `json:"name"`
	Desc string `json:"desc"`
}

// source is everything parsed from one GAMEDATA_SOURCE directory.
type source struct {
	items        map[string]rawItem
	categories   map[string]rawCategory
	contracts    map[string]rawContract
	spaceObjects map[string]rawSpaceObject
	aliases      map[string]string // item id -> canonical icon filename
	translations map[i18n.Language]rawTranslation
}

// loadSource reads and decodes every file the generator needs from dir.
func loadSource(dir string) (*source, error) {
	s := &source{translations: map[i18n.Language]rawTranslation{}}

	if err := readJSON(filepath.Join(dir, "items.json"), &struct {
		Items *map[string]rawItem `json:"items"`
	}{Items: &s.items}); err != nil {
		return nil, err
	}
	if err := readJSON(filepath.Join(dir, "item_categories.json"), &struct {
		Categories *map[string]rawCategory `json:"categories"`
	}{Categories: &s.categories}); err != nil {
		return nil, err
	}
	if err := readJSON(filepath.Join(dir, "contracts.json"), &struct {
		Contracts *map[string]rawContract `json:"contracts"`
	}{Contracts: &s.contracts}); err != nil {
		return nil, err
	}
	if err := readJSON(filepath.Join(dir, "space_objects.json"), &struct {
		SpaceObjects *map[string]rawSpaceObject `json:"spaceObjects"`
	}{SpaceObjects: &s.spaceObjects}); err != nil {
		return nil, err
	}
	if err := readJSON(filepath.Join(dir, "aliases.json"), &struct {
		Icons *map[string]string `json:"icons"`
	}{Icons: &s.aliases}); err != nil {
		return nil, err
	}
	for _, lang := range i18n.KnownLanguages() {
		var tr rawTranslation
		path := filepath.Join(dir, "i18n", "translation."+string(lang)+".json")
		if err := readJSON(path, &tr); err != nil {
			return nil, err
		}
		s.translations[lang] = tr
	}
	return s, nil
}

func readJSON(path string, dst any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}
