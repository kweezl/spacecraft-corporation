//go:generate go run ./gen -version v1

// Package gamedata is the core module exposing the compiled-in SpaceCraft game
// reference data (items, categories, contract templates, space objects) as a
// versioned, read-only Registry of Catalogs.
//
// The data is generated from the public spacecraft-resources pipeline
// (https://github.com/kweezl/spacecraft-resources) into per-version packages
// under db/v*, with item icons emitted into the emoji module's assets. Nothing
// is parsed at runtime. Regenerate with `go generate ./internal/gamedata/...`
// (needs GAMEDATA_SOURCE pointing at the resources' generated/ folder).
//
// Versions are immutable once a newer one is cut: a backward-compatible game
// update overwrites the current top version in place, while a breaking one (an
// item or contract removed) is cut as a new layer that overlays the previous,
// so links stamped with an older gamedata_version keep resolving.
package gamedata

import (
	"github.com/kweezl/spacecraft-corporation/internal/gamedata/schema"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// The value types are re-exported so consumers only import this package. The
// language code is the app-wide i18n.Language (validate a value with its Valid
// method).
type (
	// Item is a game item; see schema.Item.
	Item = schema.Item
	// Category is an itemType node; see schema.Category.
	Category = schema.Category
	// Contract is a contract template; see schema.Contract.
	Contract = schema.Contract
	// SpaceObject is a station/space entity; see schema.SpaceObject.
	SpaceObject = schema.SpaceObject
	// RequestItem is a contract requirement (item id + qty); see schema.RequestItem.
	RequestItem = schema.RequestItem
	// RewardItem is a contract reward (item id + count); see schema.RewardItem.
	RewardItem = schema.RewardItem
	// Attribute is a raw item stat; see schema.Attribute.
	Attribute = schema.Attribute
	// GDID is a game-data object id; see schema.GDID.
	GDID = schema.GDID
	// Language is the app-wide language code; see i18n.Language.
	Language = i18n.Language
)

// DefaultLang is the base game-data language; missing translations fall back to
// it.
const DefaultLang = i18n.LanguageEN
