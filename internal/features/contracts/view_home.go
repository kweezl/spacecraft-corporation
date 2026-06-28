package contracts

import (
	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
)

// renderHomeView renders (or updates) the console dashboard as a Components V2
// message: a Container with the contract stats (active / unpublished / finished
// / declined counts), then a row of creation buttons (New from template · New
// custom contract) and a row to open the list view. It is the entry surface of
// the /contracts console; the list lives one click away behind "List contracts".
func (h *Feature) renderHomeView(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID, update bool) error {
	counts, err := h.repo.Counts(ctx, serverID)
	if err != nil {
		return h.consoleErr(ctx, r, i, serverID, err)
	}

	inner := []discordgo.MessageComponent{
		discordgo.TextDisplay{Content: "## " + h.loc.Render(ctx, serverID, "contracts.console.home_title", nil)},
		discordgo.TextDisplay{Content: h.loc.Render(ctx, serverID, "contracts.console.home_stats", map[string]any{
			"Active":      counts.Active,
			"Unpublished": counts.Unpublished,
			"Completed":   counts.Completed,
			"Cancelled":   counts.Cancelled,
		})},
	}
	// Only surface the "why unpublished" hint when there is something to explain.
	if counts.Unpublished > 0 {
		inner = append(inner, divider(), discordgo.TextDisplay{
			Content: h.loc.Render(ctx, serverID, "contracts.console.home_unpublished_hint", nil),
		})
	}

	// List first, then the create buttons — all inside the card. Create buttons
	// only appear for members who hold the matching permission; the handlers
	// re-check regardless (gateMutation). An empty creation row is omitted (Discord
	// rejects an empty row).
	inner = append(inner, divider(), discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_list", nil),
			Style:    discordgo.PrimaryButton,
			CustomID: buildID(segList),
		},
	}})

	var create []discordgo.MessageComponent
	if h.may(ctx, i, serverID, keyTemplate) {
		create = append(create, discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_new_template", nil),
			Style:    discordgo.SecondaryButton,
			CustomID: buildID(segTemplate),
		})
	}
	if h.may(ctx, i, serverID, keyCustom) {
		create = append(create, discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_new_custom", nil),
			Style:    discordgo.SuccessButton,
			CustomID: buildID(segCreate),
		})
	}
	if len(create) > 0 {
		inner = append(inner, discordgo.ActionsRow{Components: create})
	}

	components := []discordgo.MessageComponent{discordgo.Container{Components: inner}}
	return h.respondView(i, r, components, update)
}

// handleTemplateWIP replies that template-based contracts are not built yet,
// without disturbing the dashboard message (ephemeral notice).
func (h *Feature) handleTemplateWIP(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	return r.RespondEphemeral(i.Interaction, h.loc.Render(ctx, serverID, "contracts.console.template_wip", nil))
}
