package permissions

import (
	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// NewPanelCommand and NewPanelComponent build the panel over a fixed set of valid
// command paths, so external tests can exercise it without standing up the full
// registry/fx graph. They return the exported registry types; the panel and
// catalog stay internal. A nil valid set leaves the catalog unbound (paths()
// returns nil).
func NewPanelCommand(store *Store, loc *i18n.Localizer, valid []string) *registry.Command {
	return testPanel(store, loc, valid).command()
}

func NewPanelComponent(store *Store, loc *i18n.Localizer, valid []string) *registry.Component {
	return testPanel(store, loc, valid).component()
}

func testPanel(store *Store, loc *i18n.Localizer, valid []string) *panel {
	c := &catalog{}
	if valid != nil {
		c.list = func() []string { return valid }
	}
	return newPanel(store, NewGate(store), loc, c)
}
