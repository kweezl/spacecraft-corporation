package settings

import (
	"context"
	"fmt"

	"github.com/bwmarrin/discordgo"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// NewCommand builds the /settings command (theme / language / show). It is
// DefaultDeny, so it is owner/admin-only by default (and delegatable via
// /permissions, like any gated command). The theme and language options offer
// the available values as Discord choices, sourced from the Translator catalog.
func NewCommand(store *Store, tr *i18n.Translator, loc *i18n.Localizer) *registry.Command {
	return &registry.Command{
		DefaultDeny: true,
		Def: &discordgo.ApplicationCommand{
			Name:        "settings",
			Description: "Configure this server's bot theme and language",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "theme",
					Description: "Set the wording theme for this server",
					Options: []*discordgo.ApplicationCommandOption{{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "name",
						Description: "Theme name",
						Required:    true,
						Choices:     choices(tr.Themes()),
					}},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "language",
					Description: "Set the language for this server",
					Options: []*discordgo.ApplicationCommandOption{{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "code",
						Description: "Language code",
						Required:    true,
						Choices:     choices(tr.Languages()),
					}},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "show",
					Description: "Show this server's current theme and language",
				},
			},
		},
		Handler: handle(store, loc),
	}
}

func handle(store *Store, loc *i18n.Localizer) registry.Handler {
	return func(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate) error {
		data := i.ApplicationCommandData()
		if len(data.Options) == 0 {
			return r.RespondEphemeral(i.Interaction, "Specify a subcommand: theme, language, or show.")
		}
		sub := data.Options[0]
		serverID := i.GuildID

		switch sub.Name {
		case "theme":
			theme := optString(sub.Options, "name")
			if err := store.SetTheme(ctx, serverID, theme); err != nil {
				return fmt.Errorf("set theme: %w", err)
			}
			// Rendered after the write, so the confirmation already uses the new theme.
			return r.RespondEphemeral(i.Interaction,
				loc.Render(ctx, serverID, "settings.theme_set", map[string]any{"Theme": theme}))
		case "language":
			lang := optString(sub.Options, "code")
			if err := store.SetLanguage(ctx, serverID, lang); err != nil {
				return fmt.Errorf("set language: %w", err)
			}
			return r.RespondEphemeral(i.Interaction,
				loc.Render(ctx, serverID, "settings.language_set", map[string]any{"Language": lang}))
		case "show":
			theme, lang := store.Resolve(ctx, serverID)
			return r.RespondEphemeral(i.Interaction,
				loc.Render(ctx, serverID, "settings.current", map[string]any{"Theme": theme, "Language": lang}))
		default:
			return r.RespondEphemeral(i.Interaction, "Unknown subcommand.")
		}
	}
}

// choices maps values to Discord option choices (display name == value).
func choices(values []string) []*discordgo.ApplicationCommandOptionChoice {
	out := make([]*discordgo.ApplicationCommandOptionChoice, len(values))
	for i, v := range values {
		out[i] = &discordgo.ApplicationCommandOptionChoice{Name: v, Value: v}
	}
	return out
}

// optString returns a string-valued option by name, or "" when absent.
func optString(opts []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, o := range opts {
		if o.Name == name {
			if s, ok := o.Value.(string); ok {
				return s
			}
		}
	}
	return ""
}
