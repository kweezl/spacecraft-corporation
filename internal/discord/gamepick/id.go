package gamepick

import (
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
)

// divider is a visual separator between picker rows inside a Container.
func divider() discordgo.Separator { return discordgo.Separator{Divider: boolPtr(true)} }

// Picker component segments. These literals are part of the CustomID contract:
// they match the pre-extraction contracts console segments byte-for-byte, so a
// Picker built with Prefix "contract" emits the exact ids live messages carry.
const (
	segPick         = "pick"   // the shared gamedata pick select
	segBrowse       = "brw"    // category list; select choice drills into a category
	segBrowseItems  = "brwi"   // one category page; select choice → quantity modal
	segBrowseSearch = "brws"   // open the type-first query modal from the browser
	segMBrowseQty   = "m_bqty" // quantity modal submit → apply the picked item
	segBrowseSub    = "brwsub" // subcategory filter on the item page
	segLocBrowse    = "lbrw"   // location picker; select choice applies immediately
	segLocClear     = "lclr"   // clear the delivery location
)

// subAll is the subcategory select's "no filter" option value (an option value
// may not be empty).
const subAll = "-"

// qtyField is the quantity modal's text-input CustomID (matches the contracts
// "qty" field so an in-flight modal round-trips).
const qtyField = "qty"

// buildID assembles a picker CustomID: "<Prefix>:<seg>[:<part>...]".
func (p *Picker) buildID(seg string, parts ...string) string {
	if len(parts) == 0 {
		return p.cfg.Prefix + ":" + seg
	}
	return p.cfg.Prefix + ":" + seg + ":" + strings.Join(parts, ":")
}

// argUUID parses parts[idx] as a UUID.
func argUUID(parts []string, idx int) (uuid.UUID, bool) {
	if idx >= len(parts) {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(parts[idx])
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// argInt parses parts[idx] as a non-negative int, defaulting to 0 when absent.
func argInt(parts []string, idx int) int {
	if idx >= len(parts) {
		return 0
	}
	n, err := strconv.Atoi(parts[idx])
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// intStr renders an int for a CustomID part.
func intStr(n int) string { return strconv.Itoa(n) }

// truncate clamps s to at most n runes (Discord label/title/option limits).
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// boolPtr is the pointer-to-bool discordgo requires for optional flags.
func boolPtr(b bool) *bool { return &b }
