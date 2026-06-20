package contracts

import "github.com/bwmarrin/discordgo"

// commandName is the single top-level command. It is SubcommandGated, so each
// leaf path (e.g. "contract participate") is granted to roles independently.
const commandName = "contract"

// Subcommand group and leaf names (the path segments the access gate keys on).
const (
	grpItem = "item"

	opCreate        = "create"
	opItemAdd       = "add"
	opItemRemove    = "remove"
	opParticipate   = "participate"
	opDeliver       = "deliver"
	opRelease       = "release"
	opReleaseMember = "release-member"
	opCancel        = "cancel"
	opList          = "list"
	opShow          = "show"
	opForum         = "forum"
)

// Option names.
const (
	optTitle       = "title"
	optDuration    = "duration"
	optDescription = "description"
	optName        = "name"
	optItem        = "item"
	optQty         = "qty"
	optMember      = "member"
	optStatus      = "status"
	optChannel     = "channel"
)

// statusAll is the /contract list filter value that includes every status.
const statusAll = "all"

// Input length caps. Title is bounded to the forum-thread-name limit (100); the
// description is kept well under Discord's 4096 embed-description limit, leaving
// room for the status line. Enforced client-side via the option MaxLength, and
// defensively clamped again when the embed is built (see embed.go).
const (
	titleMaxLen       = 100
	descriptionMaxLen = 2000
)

// buildDef assembles the /contract command tree. Every leaf is gated
// independently (SubcommandGated); the in-thread leaves resolve their contract
// from the channel the command runs in.
func buildDef() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        commandName,
		Description: "Post and fulfil corporation supply contracts",
		Options: []*discordgo.ApplicationCommandOption{
			sub(opCreate, "Create a contract (posts a forum thread)", []*discordgo.ApplicationCommandOption{
				strOptMax(optTitle, "Contract title", true, titleMaxLen),
				strOpt(optDuration, "Time to complete, e.g. \"3d 11h 10m\"", true),
				strOptMax(optDescription, "Optional details", false, descriptionMaxLen),
			}),
			{
				Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
				Name:        grpItem,
				Description: "Manage a contract's required items",
				Options: []*discordgo.ApplicationCommandOption{
					sub(opItemAdd, "Add a required item", []*discordgo.ApplicationCommandOption{
						strOpt(optName, "Item name", true),
						qtyOpt("How many are required"),
					}),
					sub(opItemRemove, "Remove a required item", []*discordgo.ApplicationCommandOption{
						autoOpt(optName, "Which item to remove"),
					}),
				},
			},
			sub(opParticipate, "Reserve an amount of an item you'll bring", []*discordgo.ApplicationCommandOption{
				autoOpt(optItem, "Which item"),
				qtyOpt("How much you'll reserve"),
			}),
			sub(opDeliver, "Deliver an amount you reserved", []*discordgo.ApplicationCommandOption{
				autoOpt(optItem, "Which item"),
				qtyOpt("How much you're delivering"),
			}),
			sub(opRelease, "Release part of your own reservation", []*discordgo.ApplicationCommandOption{
				autoOpt(optItem, "Which item"),
				qtyOpt("How much to release"),
			}),
			sub(opReleaseMember, "Release another member's reservation", []*discordgo.ApplicationCommandOption{
				userOpt(optMember, "The member whose reservation to release", true),
				autoOpt(optItem, "Which item"),
				qtyOpt("How much to release"),
			}),
			sub(opCancel, "Cancel this contract", nil),
			sub(opList, "List contracts", []*discordgo.ApplicationCommandOption{
				statusOpt(),
			}),
			sub(opShow, "Re-post this contract's progress", nil),
			sub(opForum, "Set the forum channel new contracts are posted to", []*discordgo.ApplicationCommandOption{
				forumChannelOpt(),
			}),
		},
	}
}

func sub(name, desc string, opts []*discordgo.ApplicationCommandOption) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionSubCommand,
		Name:        name,
		Description: desc,
		Options:     opts,
	}
}

func strOpt(name, desc string, required bool) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionString,
		Name:        name,
		Description: desc,
		Required:    required,
	}
}

// strOptMax is a string option with a Discord-enforced maximum length, so
// over-long input is rejected client-side before it can break a downstream embed.
func strOptMax(name, desc string, required bool, maxLen int) *discordgo.ApplicationCommandOption {
	o := strOpt(name, desc, required)
	o.MaxLength = maxLen
	return o
}

// autoOpt is a required string option whose values come from autocomplete.
func autoOpt(name, desc string) *discordgo.ApplicationCommandOption {
	o := strOpt(name, desc, true)
	o.Autocomplete = true
	return o
}

// qtyOpt is a required positive-integer quantity option.
func qtyOpt(desc string) *discordgo.ApplicationCommandOption {
	minQty := 1.0
	return &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionInteger,
		Name:        optQty,
		Description: desc,
		Required:    true,
		MinValue:    &minQty,
	}
}

func userOpt(name, desc string, required bool) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionUser,
		Name:        name,
		Description: desc,
		Required:    required,
	}
}

// forumChannelOpt is the required forum-channel option for /contract forum.
func forumChannelOpt() *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{
		Type:         discordgo.ApplicationCommandOptionChannel,
		Name:         optChannel,
		Description:  "The forum channel for contracts",
		Required:     true,
		ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildForum},
	}
}

func statusOpt() *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionString,
		Name:        optStatus,
		Description: "Filter by status (default: open)",
		Required:    false,
		Choices: []*discordgo.ApplicationCommandOptionChoice{
			{Name: "open", Value: string(StatusOpen)},
			{Name: "completed", Value: string(StatusCompleted)},
			{Name: "expired", Value: string(StatusExpired)},
			{Name: "cancelled", Value: string(StatusCancelled)},
			{Name: "all", Value: statusAll},
		},
	}
}

// --- interaction-option accessors ---

func findOpt(opts []*discordgo.ApplicationCommandInteractionDataOption, name string) (*discordgo.ApplicationCommandInteractionDataOption, bool) {
	for _, o := range opts {
		if o.Name == name {
			return o, true
		}
	}
	return nil, false
}

func optString(opts []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	if o, ok := findOpt(opts, name); ok {
		if s, ok := o.Value.(string); ok {
			return s
		}
	}
	return ""
}

func optInt(opts []*discordgo.ApplicationCommandInteractionDataOption, name string) int {
	if o, ok := findOpt(opts, name); ok {
		if f, ok := o.Value.(float64); ok {
			return int(f)
		}
	}
	return 0
}

func focusedOption(opts []*discordgo.ApplicationCommandInteractionDataOption) *discordgo.ApplicationCommandInteractionDataOption {
	for _, o := range opts {
		if o.Focused {
			return o
		}
	}
	return nil
}

// invokerID returns the Discord user ID of the member who ran the command.
// Commands are guild-only (the session ignores DMs), so Member is set.
func invokerID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	return ""
}
