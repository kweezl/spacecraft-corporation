// Package permissions implements role-based command access control. A server
// admin maps Discord roles to commands; a member may run a command if they hold
// any mapped role (any-of). Commands with no mapping fall back to their policy:
// a "required" command (registry.Command.DefaultDeny) is denied, an "optional"
// one is allowed. The server owner and administrators always pass — the gate is
// bypassed for them in the session before it is consulted.
//
// The feature contributes the access Gate (the session's CommandAccess) and the
// /permissions command used to manage the mapping. When the feature is disabled
// no gate is provided, so the session allows every command (role control off).
// Mappings are isolated per server: every query is keyed by the server ID, and
// Discord role IDs are themselves per-guild.
package permissions

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/discord/session"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// commandName is the management command's name. It is itself access-controlled
// (DefaultDeny), so it is owner/admin-only until the owner grants a role access
// to it — that is how the owner delegates permission management.
const commandName = "permissions"

// Mapping is one (command, role) grant for a server.
type Mapping struct {
	Command string
	RoleID  string
}

// Repository persists the per-server command→role mapping. serverID is the
// resolved servers.id.
type Repository interface {
	// RolesFor returns the role IDs granted access to a command on a server.
	RolesFor(ctx context.Context, serverID uuid.UUID, command string) ([]string, error)
	// Grant maps a role to a command (idempotent), recording the granting user.
	Grant(ctx context.Context, serverID uuid.UUID, command, roleID, createdByUserID string) error
	// Revoke removes a single role mapping for a command.
	Revoke(ctx context.Context, serverID uuid.UUID, command, roleID string) error
	// Clear removes every role mapping for a command.
	Clear(ctx context.Context, serverID uuid.UUID, command string) error
	// List returns every mapping on a server, for the management command.
	List(ctx context.Context, serverID uuid.UUID) ([]Mapping, error)
}

// Gate answers the session's CommandAccess check from the role mapping, reading
// through the Store's per-server cache.
type Gate struct {
	store *Store
}

// NewGate builds the access gate over the cached store.
func NewGate(store *Store) *Gate { return &Gate{store: store} }

// IsAllowed grants access when the user holds any mapped role; with no mapping
// it honours the command's default policy (deny for required, allow for
// optional). Owner/admin bypass happens in the session before this is called.
// Membership is a direct lookup against the cached role set — O(user roles),
// no per-call allocation.
func (g *Gate) IsAllowed(ctx context.Context, req session.AccessRequest) (bool, error) {
	mapped, err := g.store.roleSet(ctx, req.ServerID, req.Command)
	if err != nil {
		return false, fmt.Errorf("permissions: roles for %q: %w", req.Command, err)
	}
	if len(mapped) == 0 {
		return !req.DefaultDeny, nil
	}
	for _, r := range req.UserRoles {
		if _, ok := mapped[r]; ok {
			return true, nil
		}
	}
	return false, nil
}

// NewCommand builds the /permissions management command. It is DefaultDeny so
// only the owner/admins (and any role the owner grants it to) can run it. It
// takes the Store (not the raw Repository) so its writes invalidate the cache
// the gate reads.
func NewCommand(store *Store, loc *i18n.Localizer) *registry.Command {
	return &registry.Command{
		DefaultDeny: true,
		Def: &discordgo.ApplicationCommand{
			Name:        commandName,
			Description: "Manage which roles may use each command on this server",
			Options: []*discordgo.ApplicationCommandOption{
				subCommand("grant", "Grant a role access to a command", true, true),
				subCommand("revoke", "Revoke a role's access to a command", true, true),
				subCommand("list", "List role access (a command, or all commands)", false, false),
				subCommand("clear", "Remove every role's access to a command", false, true),
			},
		},
		Handler: handle(store, loc),
	}
}

// subCommand builds one /permissions subcommand. withRole adds the role option;
// requireCommand makes the command-name option mandatory (only "list" omits it).
func subCommand(name, desc string, withRole, requireCommand bool) *discordgo.ApplicationCommandOption {
	opts := []*discordgo.ApplicationCommandOption{{
		Type:        discordgo.ApplicationCommandOptionString,
		Name:        "command",
		Description: "The slash command name (without the leading slash)",
		Required:    requireCommand,
	}}
	if withRole {
		opts = append(opts, &discordgo.ApplicationCommandOption{
			Type:        discordgo.ApplicationCommandOptionRole,
			Name:        "role",
			Description: "The Discord role",
			Required:    true,
		})
	}
	return &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionSubCommand,
		Name:        name,
		Description: desc,
		Options:     opts,
	}
}

func handle(store *Store, loc *i18n.Localizer) registry.Handler {
	return func(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
		data := i.ApplicationCommandData()
		if len(data.Options) == 0 {
			return r.RespondEphemeral(i.Interaction, loc.Render(ctx, serverID, "permissions.no_subcommand", nil))
		}
		sub := data.Options[0]
		command := optString(sub.Options, "command")
		roleID := optString(sub.Options, "role")

		switch sub.Name {
		case "grant":
			if err := store.Grant(ctx, serverID, command, roleID, invokerID(i)); err != nil {
				return fmt.Errorf("grant: %w", err)
			}
			return r.RespondEphemeral(i.Interaction, loc.Render(ctx, serverID, "permissions.granted",
				map[string]any{"RoleID": roleID, "Command": command}))
		case "revoke":
			if err := store.Revoke(ctx, serverID, command, roleID); err != nil {
				return fmt.Errorf("revoke: %w", err)
			}
			return r.RespondEphemeral(i.Interaction, loc.Render(ctx, serverID, "permissions.revoked",
				map[string]any{"RoleID": roleID, "Command": command}))
		case "clear":
			if err := store.Clear(ctx, serverID, command); err != nil {
				return fmt.Errorf("clear: %w", err)
			}
			return r.RespondEphemeral(i.Interaction, loc.Render(ctx, serverID, "permissions.cleared",
				map[string]any{"Command": command}))
		case "list":
			msg, err := listMessage(ctx, store, loc, serverID, command)
			if err != nil {
				return fmt.Errorf("list: %w", err)
			}
			return r.RespondEphemeral(i.Interaction, msg)
		default:
			return r.RespondEphemeral(i.Interaction, loc.Render(ctx, serverID, "permissions.unknown_subcommand", nil))
		}
	}
}

// listMessage renders the mapping for one command, or for the whole server when
// command is empty.
func listMessage(ctx context.Context, store *Store, loc *i18n.Localizer, serverID uuid.UUID, command string) (string, error) {
	if command != "" {
		roles, err := store.RolesFor(ctx, serverID, command)
		if err != nil {
			return "", err
		}
		if len(roles) == 0 {
			return loc.Render(ctx, serverID, "permissions.list_command_empty",
				map[string]any{"Command": command}), nil
		}
		return loc.Render(ctx, serverID, "permissions.list_command",
			map[string]any{"Command": command, "Roles": mentionList(roles)}), nil
	}

	all, err := store.List(ctx, serverID)
	if err != nil {
		return "", err
	}
	if len(all) == 0 {
		return loc.Render(ctx, serverID, "permissions.list_all_empty", nil), nil
	}
	byCommand := make(map[string][]string)
	for _, m := range all {
		byCommand[m.Command] = append(byCommand[m.Command], m.RoleID)
	}
	commands := make([]string, 0, len(byCommand))
	for c := range byCommand {
		commands = append(commands, c)
	}
	sort.Strings(commands)
	var b strings.Builder
	b.WriteString(loc.Render(ctx, serverID, "permissions.list_all_header", nil))
	for _, c := range commands {
		b.WriteString("\n")
		b.WriteString(loc.Render(ctx, serverID, "permissions.list_all_line",
			map[string]any{"Command": c, "Roles": mentionList(byCommand[c])}))
	}
	return b.String(), nil
}

// optString returns a string-valued option (string and role options both carry
// the value as a snowflake string), or "" when absent.
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

// invokerID returns the ID of the member who ran the command. Commands are
// guild-only (the session ignores DMs), so Member is set.
func invokerID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	return ""
}

// roleMention renders a role as a Discord mention. Replies are ephemeral, so the
// mention renders as @rolename without notifying the role.
func roleMention(roleID string) string { return "<@&" + roleID + ">" }

func mentionList(roleIDs []string) string {
	out := make([]string, len(roleIDs))
	for i, id := range roleIDs {
		out[i] = roleMention(id)
	}
	return strings.Join(out, " ")
}
