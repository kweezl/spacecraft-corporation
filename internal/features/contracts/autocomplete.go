package contracts

import (
	"context"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
)

// maxChoices is Discord's hard cap on autocomplete suggestions.
const maxChoices = 25

// autocomplete suggests item names for the option being typed, resolved from the
// contract thread the command runs in. Suggestions are a convenience only — the
// submitted value is re-validated by the scoped query when the command runs.
func (h *Feature) autocomplete(ctx context.Context, i *discordgo.InteractionCreate, serverID uuid.UUID) ([]*discordgo.ApplicationCommandOptionChoice, error) {
	op, sub, opts := acPath(i)
	focused := focusedOption(opts)
	if focused == nil {
		return nil, nil
	}
	typed := strings.ToLower(stringValue(focused))
	thread := threadOf(i)

	switch {
	case op == opParticipate && focused.Name == optItem:
		return h.suggestRemaining(ctx, serverID, thread, typed)
	case op == opDeliver && focused.Name == optItem:
		return h.suggestMemberItems(ctx, serverID, thread, invokerID(i), typed)
	case op == opRelease && focused.Name == optItem:
		return h.suggestMemberItems(ctx, serverID, thread, invokerID(i), typed)
	case op == opReleaseMember && focused.Name == optItem:
		member := optString(opts, optMember)
		if member == "" {
			return nil, nil // nothing to suggest until the member is chosen
		}
		return h.suggestMemberItems(ctx, serverID, thread, member, typed)
	case op == grpItem && sub == opItemRemove && focused.Name == optName:
		return h.suggestAllItems(ctx, serverID, thread, typed)
	}
	return nil, nil
}

// suggestRemaining suggests items that still have unreserved capacity (participate).
func (h *Feature) suggestRemaining(ctx context.Context, serverID uuid.UUID, thread, typed string) ([]*discordgo.ApplicationCommandOptionChoice, error) {
	p, err := h.repo.Progress(ctx, serverID, thread)
	if err != nil {
		return nil, nil
	}
	var out []*discordgo.ApplicationCommandOptionChoice
	for _, it := range p.Items {
		if it.Remaining() <= 0 || !matches(it.Name, typed) {
			continue
		}
		out = appendChoice(out, it.Name)
		if len(out) >= maxChoices {
			break
		}
	}
	return out, nil
}

// suggestAllItems suggests every required item on the contract (item remove).
func (h *Feature) suggestAllItems(ctx context.Context, serverID uuid.UUID, thread, typed string) ([]*discordgo.ApplicationCommandOptionChoice, error) {
	p, err := h.repo.Progress(ctx, serverID, thread)
	if err != nil {
		return nil, nil
	}
	var out []*discordgo.ApplicationCommandOptionChoice
	for _, it := range p.Items {
		if !matches(it.Name, typed) {
			continue
		}
		out = appendChoice(out, it.Name)
		if len(out) >= maxChoices {
			break
		}
	}
	return out, nil
}

// suggestMemberItems suggests items the given member still has reserved but not
// delivered (deliver / release / release-member).
func (h *Feature) suggestMemberItems(ctx context.Context, serverID uuid.UUID, thread, userID, typed string) ([]*discordgo.ApplicationCommandOptionChoice, error) {
	items, err := h.repo.MemberOutstanding(ctx, serverID, thread, userID)
	if err != nil {
		return nil, nil
	}
	var out []*discordgo.ApplicationCommandOptionChoice
	for _, m := range items {
		if !matches(m.Name, typed) {
			continue
		}
		out = appendChoice(out, m.Name)
		if len(out) >= maxChoices {
			break
		}
	}
	return out, nil
}

func matches(name, typed string) bool {
	return typed == "" || strings.Contains(strings.ToLower(name), typed)
}

func appendChoice(out []*discordgo.ApplicationCommandOptionChoice, name string) []*discordgo.ApplicationCommandOptionChoice {
	return append(out, &discordgo.ApplicationCommandOptionChoice{Name: truncate(name, 100), Value: name})
}

// acPath unpacks the focused command path: (op, sub, leaf-options). For the item
// group op is "item" and sub is the leaf ("add"/"remove"); otherwise op is the
// leaf and sub is empty.
func acPath(i *discordgo.InteractionCreate) (op, sub string, opts []*discordgo.ApplicationCommandInteractionDataOption) {
	data := i.ApplicationCommandData()
	if len(data.Options) == 0 {
		return "", "", nil
	}
	top := data.Options[0]
	if top.Type == discordgo.ApplicationCommandOptionSubCommandGroup {
		if len(top.Options) == 0 {
			return top.Name, "", nil
		}
		leaf := top.Options[0]
		return top.Name, leaf.Name, leaf.Options
	}
	return top.Name, "", top.Options
}

// stringValue reads an option's current value as a string (autocomplete delivers
// the in-progress text this way).
func stringValue(o *discordgo.ApplicationCommandInteractionDataOption) string {
	if s, ok := o.Value.(string); ok {
		return s
	}
	return ""
}
