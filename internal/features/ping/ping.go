// Package ping implements the /ping slash command. It is a pure latency probe:
// it reports the bot's handle latency and the full request round-trip, and
// persists nothing.
package ping

import (
	"context"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// NewCommand builds the /ping command. It replies with an embed showing two
// latencies:
//   - Handle latency: time spent inside the bot, measured from the dispatcher's
//     start instant (the same reference as discord_command_duration_seconds).
//   - Round-trip latency: now minus the moment Discord created the interaction,
//     decoded from the interaction ID snowflake — an approximation of the full
//     request→response round trip.
//
// (Command-call counts are recorded centrally by the registry as
// discord_command_total.)
func NewCommand(loc *i18n.Localizer) *registry.Command {
	return &registry.Command{
		Def: &discordgo.ApplicationCommand{
			Name:        "ping",
			Description: "Replies with the bot's handle and round-trip latency",
		},
		Handler: func(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
			handle, _ := registry.Elapsed(ctx)
			roundTrip := roundTripLatency(i)

			// The handle path is sub-millisecond, so render it in whatever unit
			// keeps it meaningful (µs under 1ms, ms above) rather than truncating
			// to "0 ms".
			latency := func(d time.Duration) string {
				key, value := "ping.latency_ms", d.Milliseconds()
				if d < time.Millisecond {
					key, value = "ping.latency_us", d.Microseconds()
				}
				return loc.Render(ctx, serverID, key, map[string]any{"Value": value})
			}

			embed := &discordgo.MessageEmbed{
				Title: loc.Render(ctx, serverID, "ping.title", nil),
				Fields: []*discordgo.MessageEmbedField{
					{
						Name:   loc.Render(ctx, serverID, "ping.handle_field", nil),
						Value:  latency(handle),
						Inline: true,
					},
					{
						Name:   loc.Render(ctx, serverID, "ping.roundtrip_field", nil),
						Value:  latency(roundTrip),
						Inline: true,
					},
				},
			}
			return r.RespondEmbed(i.Interaction, embed)
		},
	}
}

// roundTripLatency approximates the user-perceived round trip: the time since
// Discord created the interaction, decoded from the interaction ID snowflake. A
// malformed/empty ID (e.g. in tests) or a clock skew that would yield a negative
// value collapses to 0.
func roundTripLatency(i *discordgo.InteractionCreate) time.Duration {
	created, err := discordgo.SnowflakeTimestamp(i.ID)
	if err != nil {
		return 0
	}
	if d := time.Since(created); d > 0 {
		return d
	}
	return 0
}
