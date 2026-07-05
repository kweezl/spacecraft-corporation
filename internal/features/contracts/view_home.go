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

	// List first, then the create buttons — all inside the card. The create row
	// only appears for contract managers; the handlers re-check regardless
	// (gateMutation).
	inner = append(inner, divider(), discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{
			Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_list", nil),
			Style:    discordgo.PrimaryButton,
			CustomID: buildID(segList),
		},
	}})

	// Create / library buttons show only to contract managers (the single manager
	// key now governs every authoring action); everyone else sees list-only.
	if h.may(ctx, i, serverID, keyManage) {
		inner = append(inner, discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_new_template", nil),
				Style:    discordgo.SecondaryButton,
				CustomID: buildID(segTemplate),
			},
			discordgo.Button{
				Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_new_custom", nil),
				Style:    discordgo.SuccessButton,
				CustomID: buildID(segCreate),
			},
			discordgo.Button{
				Label:    h.loc.Render(ctx, serverID, "contracts.console.btn_templates", nil),
				Style:    discordgo.SecondaryButton,
				CustomID: buildID(segTList, "0", ""),
			},
		}})
	}

	components := []discordgo.MessageComponent{discordgo.Container{Components: inner}}
	return h.respondView(i, r, components, update)
}
