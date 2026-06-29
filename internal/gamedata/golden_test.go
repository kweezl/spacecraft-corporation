package gamedata

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/gamedata/schema"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// goldenV1 builds a registry over the real generated data.
func goldenV1(t *testing.T) *Catalog {
	t.Helper()
	reg, err := buildRegistry(definedVersionNames(), definedSources, zap.NewNop())
	require.NoError(t, err)
	c, ok := reg.Version("v1")
	require.True(t, ok, "v1 must be defined")
	return c
}

func TestV1KnownItem(t *testing.T) {
	c := goldenV1(t)

	ore, ok := c.Item("IronOre")
	require.True(t, ok)
	assert.NotEmpty(t, ore.IconName, "IronOre should have an icon")
	assert.Equal(t, "Iron Ore", c.Name("IronOre", i18n.LanguageEN))
	assert.NotEmpty(t, c.Name("IronOre", i18n.LanguageRU), "IronOre should have a Russian name")
}

func TestV1SafetyNetRescuedCurrencies(t *testing.T) {
	c := goldenV1(t)
	for _, id := range []schema.GDID{"CorpoCredits", "LicensePoints", "CorpoReputation"} {
		_, ok := c.Item(id)
		assert.Truef(t, ok, "%s must survive exclusion (referenced by a contract)", id)
	}
}

func TestV1LatestIsV1(t *testing.T) {
	reg, err := buildRegistry(definedVersionNames(), definedSources, zap.NewNop())
	require.NoError(t, err)
	require.NotNil(t, reg.Latest())
	assert.Equal(t, "v1", reg.Latest().Version())
}

// TestV1IconAssetsExist guards against drift between the catalog and the emoji
// assets the generator copies: every item that claims an icon must have a
// committed asset file the emoji module can upload.
func TestV1IconAssetsExist(t *testing.T) {
	c := goldenV1(t)
	assetsDir := filepath.Join("..", "emoji", "assets")

	missing := 0
	for _, it := range c.Items() {
		if it.IconName == "" {
			continue
		}
		path := filepath.Join(assetsDir, it.IconName+".webp")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("item %s: icon asset %s missing", it.ID, path)
			missing++
		}
	}
	assert.Zero(t, missing)
}
