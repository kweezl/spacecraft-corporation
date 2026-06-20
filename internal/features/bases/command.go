package bases

import "github.com/bwmarrin/discordgo"

// commandName is the single top-level command. It is SubcommandGated, so each
// leaf path (e.g. "base own register") is granted to roles independently.
const commandName = "base"

// Tier groups: the second path segment. Tier is what the access gate and the SQL
// ownership predicate both key on, so own/corp/member are separate subcommand
// groups rather than an option.
const (
	tierOwn    = "own"
	tierCorp   = "corp"
	tierMember = "member"
)

// Operations: the leaf subcommand within a tier group.
const (
	opRegister         = "register"
	opUnregister       = "unregister"
	opAddExtractor     = "add-extractor"
	opRemoveExtractor  = "remove-extractor"
	opAddProduction    = "add-production"
	opRemoveProduction = "remove-production"
	opList             = "list"
)

// Option names.
const (
	optName       = "name"
	optSector     = "sector"
	optSystem     = "system"
	optPlanet     = "planet"
	optMember     = "member"
	optBase       = "base"
	optResource   = "resource"
	optItem       = "item"
	optExtractor  = "extractor"
	optProduction = "production"
)

// allBasesValue is the sentinel the unregister "base" picker offers to mean
// "every base in scope" (vs a specific base id).
const allBasesValue = "all"

// buildDef assembles the /base command tree: three tier groups (each with the
// same six operations) plus a top-level list subcommand. The member group adds a
// required member user option to every operation.
func buildDef() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        commandName,
		Description: "Register and browse member and corporation bases",
		Options: []*discordgo.ApplicationCommandOption{
			tierGroup(tierOwn, "your own bases", false),
			tierGroup(tierCorp, "corporation-owned bases", false),
			tierGroup(tierMember, "another member's bases", true),
			listSubcommand(),
		},
	}
}

func tierGroup(tier, desc string, withMember bool) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
		Name:        tier,
		Description: "Manage " + desc,
		Options: []*discordgo.ApplicationCommandOption{
			sub(opRegister, "Register a base", withMemberOpt(withMember,
				strOpt(optName, "Base name", true),
				strOpt(optSector, "Sector name", true),
				strOpt(optSystem, "System code", true),
				planetOpt(),
			)),
			sub(opUnregister, "Unregister a base (soft delete)", withMemberOpt(withMember,
				autoOpt(optBase, "Which base (or All)"),
			)),
			sub(opAddExtractor, "Install an extractor on a base", withMemberOpt(withMember,
				autoOpt(optBase, "Which base"),
				strOpt(optResource, "Resource the extractor pulls", true),
			)),
			sub(opRemoveExtractor, "Remove an extractor from a base", withMemberOpt(withMember,
				autoOpt(optBase, "Which base"),
				autoOpt(optExtractor, "Which extractor to remove"),
			)),
			sub(opAddProduction, "Install a production facility on a base", withMemberOpt(withMember,
				autoOpt(optBase, "Which base"),
				strOpt(optItem, "Item the facility produces", true),
			)),
			sub(opRemoveProduction, "Remove a production facility from a base", withMemberOpt(withMember,
				autoOpt(optBase, "Which base"),
				autoOpt(optProduction, "Which production to remove"),
			)),
		},
	}
}

// listSubcommand is the cross-tier listing with optional filters.
func listSubcommand() *discordgo.ApplicationCommandOption {
	return sub(opList, "List bases, with optional filters", []*discordgo.ApplicationCommandOption{
		strOpt(optSector, "Filter by sector name", false),
		strOpt(optSystem, "Filter by system code", false),
		strOpt(optName, "Filter by base name", false),
		strOpt(optResource, "Filter by extracted resource", false),
		strOpt(optItem, "Filter by produced item", false),
		userOpt(optMember, "Filter by owning member", false),
	})
}

func sub(name, desc string, opts []*discordgo.ApplicationCommandOption) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionSubCommand,
		Name:        name,
		Description: desc,
		Options:     opts,
	}
}

// withMemberOpt prepends the required member user option for the member tier, so
// it is filled before the base picker (whose suggestions are scoped to it).
func withMemberOpt(withMember bool, opts ...*discordgo.ApplicationCommandOption) []*discordgo.ApplicationCommandOption {
	if !withMember {
		return opts
	}
	member := userOpt(optMember, "The member whose base this is", true)
	return append([]*discordgo.ApplicationCommandOption{member}, opts...)
}

func strOpt(name, desc string, required bool) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionString,
		Name:        name,
		Description: desc,
		Required:    required,
	}
}

// autoOpt is a required string option whose values come from autocomplete.
func autoOpt(name, desc string) *discordgo.ApplicationCommandOption {
	o := strOpt(name, desc, true)
	o.Autocomplete = true
	return o
}

func userOpt(name, desc string, required ...bool) *discordgo.ApplicationCommandOption {
	req := false
	if len(required) > 0 {
		req = required[0]
	}
	return &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionUser,
		Name:        name,
		Description: desc,
		Required:    req,
	}
}

func planetOpt() *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionInteger,
		Name:        optPlanet,
		Description: "Planet number (I–X)",
		Required:    true,
		Choices:     planetChoices(),
	}
}

func planetChoices() []*discordgo.ApplicationCommandOptionChoice {
	out := make([]*discordgo.ApplicationCommandOptionChoice, 0, planetMax)
	for n := planetMin; n <= planetMax; n++ {
		out = append(out, &discordgo.ApplicationCommandOptionChoice{Name: toRoman(n), Value: n})
	}
	return out
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

// optString returns a string-valued option (string and user options both carry a
// string value — a snowflake for users), or "" when absent.
func optString(opts []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	if o, ok := findOpt(opts, name); ok {
		if s, ok := o.Value.(string); ok {
			return s
		}
	}
	return ""
}

// optInt returns an integer-valued option (Discord delivers numbers as float64),
// or 0 when absent.
func optInt(opts []*discordgo.ApplicationCommandInteractionDataOption, name string) int {
	if o, ok := findOpt(opts, name); ok {
		if f, ok := o.Value.(float64); ok {
			return int(f)
		}
	}
	return 0
}

// focusedOption returns the option the user is currently typing (for
// autocomplete), or nil.
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
