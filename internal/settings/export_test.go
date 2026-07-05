package settings

import (
	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/discord/session"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// NewPanelCommand and NewPanelComponent build the panel so external tests can
// exercise it without standing up the full registry/fx graph. They return the
// exported registry types; the panel stays internal. access may be nil (the
// permissions feature off), in which case panel mutations are admin-only.
func NewPanelCommand(store *Store, tr *i18n.Translator, loc *i18n.Localizer) *registry.Command {
	return newPanel(store, tr, loc, nil, nil).command()
}

func NewPanelComponent(store *Store, tr *i18n.Translator, loc *i18n.Localizer, access session.CommandAccess, sections ...Section) *registry.Component {
	return newPanel(store, tr, loc, access, sections).component()
}
