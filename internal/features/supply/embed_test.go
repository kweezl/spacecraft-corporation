package supply

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/gamedata"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

func TestSystemText(t *testing.T) {
	assert.Equal(t, "Muvalis(QR-439F)", systemText("Muvalis", "QR-439F"))
	assert.Equal(t, "Muvalis", systemText("Muvalis", ""))
	assert.Equal(t, "QR-439F", systemText("", "QR-439F"))
	assert.Equal(t, "", systemText("", ""))
}

// bareFeature builds a Feature with only the deps destinationBlock/embed touch
// (localizer + gamedata picker); the repo/gateway are unused here.
func bareFeature(t *testing.T) *Feature {
	t.Helper()
	reg, err := gamedata.Load(nil, nil)
	require.NoError(t, err)
	tr, err := i18n.New(i18n.Config{DefaultLanguage: "en", DefaultTheme: "standard"})
	require.NoError(t, err)
	loc := i18n.NewLocalizer(tr, i18n.StaticResolver{Theme: "standard", Lang: "en"})
	return New(nil, loc, nil, nil, nil, nil, i18n.StaticResolver{Theme: "standard", Lang: "en"}, reg, nil, zap.NewNop())
}

func TestDestinationBlock(t *testing.T) {
	h := bareFeature(t)
	ctx := context.Background()
	gid := uuid.New()

	// Nothing set → empty block.
	assert.Empty(t, h.destinationBlock(ctx, gid, Request{}))

	planet := 15
	req := Request{
		LocationGDID:      "Station_Cairn",
		LocationGDVersion: "v1",
		SystemName:        "Muvalis",
		SystemCode:        "QR-439F",
		PlanetNumber:      &planet,
		RefMessage:        &MessageRef{GuildID: "111", ChannelID: "222", MessageID: "333"},
	}
	block := h.destinationBlock(ctx, gid, req)
	assert.Contains(t, block, "Syracuse", "location resolves the space-object name")
	assert.Contains(t, block, "Muvalis(QR-439F)")
	assert.Contains(t, block, "XV", "planet 15 renders as a Roman numeral")
	assert.Contains(t, block, "https://discord.com/channels/111/222/333", "reference link is reconstructed")

	// Only a system code set → degrades to just the code.
	block = h.destinationBlock(ctx, gid, Request{SystemCode: "QR-439F"})
	assert.Contains(t, block, "QR-439F")
	assert.NotContains(t, block, "(")
}
