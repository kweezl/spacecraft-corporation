package contracts

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/gamedata"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// pickGid is the server id the display-helper tests resolve against (the static
// resolver ignores it).
var pickGid = uuid.New()

func TestEncQueryArgQuery(t *testing.T) {
	// Round-trips, including the CustomID separator and Cyrillic.
	for _, q := range []string{"", "steel", "steel ingot", "a:b:c", "стальной", "100% steel_x"} {
		enc := encQuery(q)
		assert.NotContainsf(t, enc, ":", "encQuery(%q) must not leak the part separator", q)
		got := argQuery([]string{enc}, 0)
		assert.Equalf(t, strings.TrimSpace(q), got, "round-trip of %q", q)
	}

	// Truncation never splits a %XX escape: 47 ASCII chars + one Cyrillic rune
	// encodes to 53 bytes; the 48-byte cut would land inside the escape, so it
	// backs up to 47 and the token stays decodable.
	long := strings.Repeat("a", 47) + "д"
	enc := encQuery(long)
	assert.LessOrEqual(t, len(enc), queryTokenMax)
	assert.Equal(t, strings.Repeat("a", 47), argQuery([]string{enc}, 0))

	// A pure-Cyrillic overflow truncates on an escape boundary and still decodes.
	enc = encQuery(strings.Repeat("щ", 20))
	assert.LessOrEqual(t, len(enc), queryTokenMax)
	assert.Equal(t, strings.Repeat("щ", queryTokenMax/6), argQuery([]string{enc}, 0))

	// Corrupt tokens and absent parts decode to "".
	assert.Empty(t, argQuery([]string{"%zz"}, 0))
	assert.Empty(t, argQuery(nil, 0))
}

// pickerFeature is a Feature with just enough wiring for the display helpers:
// the real compiled-in gamedata, English, no emoji store.
func pickerFeature(t *testing.T) *Feature {
	t.Helper()
	reg, err := gamedata.Load(nil, nil)
	require.NoError(t, err)
	return New(nil, nil, testLoc(t), Config{}, nil, nil, nil, nil, nil, nil,
		i18n.StaticResolver{Theme: "standard", Lang: i18n.LanguageEN}, reg, nil, zap.NewNop())
}

func TestItemAndLocationDisplay(t *testing.T) {
	h := pickerFeature(t)
	ctx := context.Background()

	// Known catalog entries resolve to their localized names; without an emoji
	// store the icon token degrades to nothing.
	assert.Equal(t, "Hydraulic Actuator", h.itemDisplay(ctx, pickGid, "Actuator", h.reg.Latest().Version()))
	assert.Equal(t, "Syracuse", h.spaceObjectDisplay(ctx, pickGid, "Station_Cairn", "v1"))

	// An unknown stored version falls back to the latest catalog; an unknown gdid
	// falls back to the raw id.
	assert.Equal(t, "Hydraulic Actuator", h.itemDisplay(ctx, pickGid, "Actuator", "v999"))
	assert.Equal(t, "NoSuchThing", h.itemDisplay(ctx, pickGid, "NoSuchThing", "v1"))
}

func TestContractFactsAndCardItems(t *testing.T) {
	h := pickerFeature(t)
	ctx := context.Background()

	rep := 5
	c := Contract{
		RewardCredits:    decPtr("1250.50"),
		RewardReputation: &rep,
		LocationGDID:     "Station_Cairn",
	}
	facts := h.contractFacts(ctx, pickGid, c)
	assert.Contains(t, facts, "1250.5")
	assert.Contains(t, facts, "Syracuse")
	assert.NotContains(t, facts, "licence", "unset rewards are omitted")

	assert.Empty(t, h.contractFacts(ctx, pickGid, Contract{}), "no facts block when nothing is set")
	assert.Empty(t, h.contractFacts(ctx, pickGid, Contract{RewardCredits: decPtr("0.00")}), "zero credits count as unset")

	// The forum card renders gamedata items via the catalog (live-localized) and
	// legacy free-text items verbatim.
	p := Progress{
		Contract: Contract{Title: "Mixed", Status: StatusOpen, LastRefreshedAt: time.Now()},
		Items: []Item{
			{Name: "snapshot name", GDID: "Actuator", GDVersion: h.reg.Latest().Version(), RequiredQty: 10},
			{Name: "Handwritten Part", RequiredQty: 5},
		},
	}
	comps := h.postComponents(ctx, pickGid, p, false)
	text := componentsText(comps)
	assert.Contains(t, text, "Hydraulic Actuator", "gdid item renders its catalog name")
	assert.NotContains(t, text, "snapshot name", "the stored snapshot is identity, not display")
	assert.Contains(t, text, "Handwritten Part", "legacy item renders its stored name")
}

// componentsText flattens every TextDisplay in a component tree.
func componentsText(comps []discordgo.MessageComponent) string {
	var b strings.Builder
	for _, c := range comps {
		switch v := c.(type) {
		case discordgo.Container:
			b.WriteString(componentsText(v.Components))
		case discordgo.Section:
			b.WriteString(componentsText(v.Components))
		case discordgo.TextDisplay:
			b.WriteString(v.Content)
			b.WriteString("\n")
		}
	}
	return b.String()
}
