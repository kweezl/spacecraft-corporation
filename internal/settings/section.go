package settings

import (
	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
)

// Section is a pluggable part of the /settings panel contributed by a feature
// module via the "settings_sections" fx group, so it only appears when that
// feature is enabled. The contracts feature uses one to expose the forum-channel
// setting (which lives in the settings store but is contracts-specific).
//
// A Section renders its own component rows (appended to the panel after
// theme/language) and handles its own component interactions, namespaced under
// the "settings:" prefix so the registry routes them to the settings panel, which
// then claims them via Owns. The panel re-authorizes the same settings gate
// before dispatching, so a Section needn't re-check access itself.
type Section interface {
	// Rows returns this section's ActionsRow(s), prefilled from current state.
	Rows(ctx context.Context, serverID uuid.UUID) []discordgo.MessageComponent
	// Owns reports whether a component CustomID belongs to this section.
	Owns(customID string) bool
	// Handle processes the section's own component interaction and re-renders the
	// panel. rerender rebuilds the full panel view (including this section's freshly
	// prefilled rows), so the handler persists its change then calls it.
	Handle(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, rerender func() []discordgo.MessageComponent) error
}
