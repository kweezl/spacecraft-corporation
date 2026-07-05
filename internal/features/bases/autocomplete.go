package bases

import (
	"context"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/roman"
)

// maxChoices is Discord's hard cap on autocomplete suggestions.
const maxChoices = 25

// autocomplete suggests values for the option the user is typing. Suggestions
// are scoped to bases the caller may target, but that is a convenience only —
// the submitted value is re-validated by the ownership-scoped query when the
// command runs, so a forged value cannot reach another owner's base.
func (h *Feature) autocomplete(ctx context.Context, i *discordgo.InteractionCreate, serverID uuid.UUID) ([]*discordgo.ApplicationCommandOptionChoice, error) {
	tier, op, opts := parsePath(i)
	if op == opList {
		return nil, nil
	}
	o, ok := ownership(tier, i, serverID, opts)
	if !ok {
		return nil, nil // member tier: nothing to suggest until a member is chosen
	}
	focused := focusedOption(opts)
	if focused == nil {
		return nil, nil
	}
	typed := strings.ToLower(stringValue(focused))
	switch focused.Name {
	case optBase:
		return h.suggestBases(ctx, o, op, typed)
	case optExtractor:
		return h.suggestExtractors(ctx, o, opts, typed)
	case optProduction:
		return h.suggestProductions(ctx, o, opts, typed)
	}
	return nil, nil
}

func (h *Feature) suggestBases(ctx context.Context, o Ownership, op, typed string) ([]*discordgo.ApplicationCommandOptionChoice, error) {
	bases, err := h.repo.ListOwned(ctx, o, maxChoices)
	if err != nil {
		return nil, err
	}
	var out []*discordgo.ApplicationCommandOptionChoice
	// The unregister picker offers an "All" entry to remove every base in scope.
	if op == opUnregister {
		out = append(out, &discordgo.ApplicationCommandOptionChoice{Name: "✦ All bases", Value: allBasesValue})
	}
	for _, b := range bases {
		label := baseLabel(b)
		if typed != "" && !strings.Contains(strings.ToLower(label), typed) {
			continue
		}
		out = append(out, &discordgo.ApplicationCommandOptionChoice{Name: truncate(label, 100), Value: b.ID.String()})
		if len(out) >= maxChoices {
			break
		}
	}
	return out, nil
}

func (h *Feature) suggestExtractors(ctx context.Context, o Ownership, opts []*discordgo.ApplicationCommandInteractionDataOption, typed string) ([]*discordgo.ApplicationCommandOptionChoice, error) {
	baseID, err := uuid.Parse(optString(opts, optBase))
	if err != nil {
		return nil, nil // base not picked yet
	}
	ex, err := h.repo.ListExtractors(ctx, o, baseID)
	if err != nil {
		return nil, err
	}
	var out []*discordgo.ApplicationCommandOptionChoice
	for _, e := range ex {
		if typed != "" && !strings.Contains(strings.ToLower(e.ResourceName), typed) {
			continue
		}
		out = append(out, &discordgo.ApplicationCommandOptionChoice{Name: truncate(e.ResourceName, 100), Value: e.ID.String()})
		if len(out) >= maxChoices {
			break
		}
	}
	return out, nil
}

func (h *Feature) suggestProductions(ctx context.Context, o Ownership, opts []*discordgo.ApplicationCommandInteractionDataOption, typed string) ([]*discordgo.ApplicationCommandOptionChoice, error) {
	baseID, err := uuid.Parse(optString(opts, optBase))
	if err != nil {
		return nil, nil
	}
	prod, err := h.repo.ListProductions(ctx, o, baseID)
	if err != nil {
		return nil, err
	}
	var out []*discordgo.ApplicationCommandOptionChoice
	for _, p := range prod {
		if typed != "" && !strings.Contains(strings.ToLower(p.ItemName), typed) {
			continue
		}
		out = append(out, &discordgo.ApplicationCommandOptionChoice{Name: truncate(p.ItemName, 100), Value: p.ID.String()})
		if len(out) >= maxChoices {
			break
		}
	}
	return out, nil
}

// baseLabel is the human-readable label for a base in a picker.
func baseLabel(b Base) string {
	return fmt.Sprintf("%s — %s / %s %s", b.Name, b.SectorName, b.SystemCode, roman.Numeral(b.PlanetNumber))
}

// stringValue reads an option's current value as a string (autocomplete delivers
// the in-progress text this way).
func stringValue(o *discordgo.ApplicationCommandInteractionDataOption) string {
	if s, ok := o.Value.(string); ok {
		return s
	}
	return ""
}

// truncate clips s to at most n runes (Discord counts characters, not bytes).
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
