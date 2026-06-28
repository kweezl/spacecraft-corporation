package permissions

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/discord/session"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

const (
	// componentPrefix namespaces the panel's component CustomIDs so the registry
	// routes them here (it keys on the text before the first ':').
	componentPrefix = commandName // "permissions"
	// panelPageSize bounds commands shown per page. Each is a role-select in its
	// own action row; with the Prev/Next row that is ≤5 action rows per message.
	panelPageSize = 4
	// maxRolesPerCommand caps a role-select; Discord's hard limit is 25.
	maxRolesPerCommand = 25
)

// panel renders and drives the /permissions management UI: an ephemeral,
// paginated Components V2 message with one native role-picker per command,
// prefilled with the roles currently granted that command. Editing a picker
// applies the change immediately; Prev/Next page through the command catalog.
type panel struct {
	store *Store
	gate  *Gate
	loc   *i18n.Localizer
	cat   *catalog
}

func newPanel(store *Store, gate *Gate, loc *i18n.Localizer, cat *catalog) *panel {
	return &panel{store: store, gate: gate, loc: loc, cat: cat}
}

// command is the /permissions slash command: it just opens the panel. It is
// DefaultDeny, so only the owner/admins (or a role granted access to it) can run
// it — and since the panel is ephemeral, only that invoker can click it.
func (p *panel) command() *registry.Command {
	return &registry.Command{
		DefaultDeny: true,
		Def: &discordgo.ApplicationCommand{
			Name:        commandName,
			Description: "Open the role-permissions panel for this server",
		},
		Handler: p.handle,
	}
}

// component handles the panel's role-select edits and page buttons.
func (p *panel) component() *registry.Component {
	return &registry.Component{Prefix: componentPrefix, Handler: p.handleComponent}
}

func (p *panel) handle(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	comps, err := p.view(ctx, serverID, 0)
	if err != nil {
		return fmt.Errorf("permissions panel: %w", err)
	}
	return r.RespondComponentsV2Ephemeral(i.Interaction, comps)
}

func (p *panel) handleComponent(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	// The session gates component interactions only on server approval, trusting
	// each mutating handler to re-authorize itself (see session.go). The panel is
	// ephemeral, but a persisted ephemeral message can outlive the invoker's
	// access, so re-check the same boundary the /permissions command enforces
	// rather than relying on ephemerality alone.
	ok, err := p.authorized(ctx, i, serverID)
	if err != nil {
		return fmt.Errorf("permissions: authorize panel: %w", err)
	}
	if !ok {
		return r.UpdateComponentsV2(i.Interaction, []discordgo.MessageComponent{
			discordgo.TextDisplay{Content: p.loc.Render(ctx, serverID, "session.denied",
				map[string]any{"Command": commandName})},
		})
	}

	id := i.MessageComponentData().CustomID
	kind, page, command, ok := parseCustomID(id)
	if !ok {
		return fmt.Errorf("permissions: bad component id %q", id)
	}
	if kind == "set" {
		// Don't trust the CustomID: only act on a real command path. A crafted or
		// stale id is ignored (we still re-render so the panel stays responsive).
		if p.cat.has(command) {
			roles := selectedRoleIDs(i.MessageComponentData())
			if err := p.store.SetRoles(ctx, serverID, command, roles, invokerID(i)); err != nil {
				return fmt.Errorf("permissions: set roles for %q: %w", command, err)
			}
		}
	}
	comps, err := p.view(ctx, serverID, page)
	if err != nil {
		return fmt.Errorf("permissions panel: %w", err)
	}
	return r.UpdateComponentsV2(i.Interaction, comps)
}

// authorized re-checks that the interacting member may manage permissions — the
// same boundary the session applies to the /permissions command: administrators
// (and the guild owner, whose computed permissions include Administrator) always
// pass; otherwise the member must hold a role granted the "permissions" command
// (which is DefaultDeny).
func (p *panel) authorized(ctx context.Context, i *discordgo.InteractionCreate, serverID uuid.UUID) (bool, error) {
	if i.Member != nil && i.Member.Permissions&discordgo.PermissionAdministrator != 0 {
		return true, nil
	}
	var roles []string
	if i.Member != nil {
		roles = i.Member.Roles
	}
	return p.gate.IsAllowed(ctx, session.AccessRequest{
		ServerID:    serverID,
		Command:     commandName,
		UserRoles:   roles,
		DefaultDeny: true,
	})
}

// view builds the Components V2 view for a page: a header, then for each command
// a label and a role-picker prefilled with its current roles, then Prev/Next.
func (p *panel) view(ctx context.Context, serverID uuid.UUID, page int) ([]discordgo.MessageComponent, error) {
	paths := p.cat.paths()
	total := len(paths)
	pages := (total + panelPageSize - 1) / panelPageSize
	if pages < 1 {
		pages = 1
	}
	page = clampPage(page, pages)

	comps := []discordgo.MessageComponent{discordgo.TextDisplay{
		Content: p.loc.Render(ctx, serverID, "permissions.panel.header",
			map[string]any{"Page": page + 1, "Pages": pages}),
	}}
	if total == 0 {
		return append(comps, discordgo.TextDisplay{
			Content: p.loc.Render(ctx, serverID, "permissions.panel.empty", nil),
		}), nil
	}

	// One query for the whole server, indexed by command, rather than a
	// cache-bypassing RolesFor per command (≤4 round-trips per render).
	all, err := p.store.List(ctx, serverID)
	if err != nil {
		return nil, err
	}
	rolesByCommand := make(map[string][]string, len(all))
	for _, m := range all {
		rolesByCommand[m.Command] = append(rolesByCommand[m.Command], m.RoleID)
	}

	start := page * panelPageSize
	end := min(start+panelPageSize, total)
	placeholder := p.loc.Render(ctx, serverID, "permissions.panel.placeholder", nil)
	for _, command := range paths[start:end] {
		roles := rolesByCommand[command]
		comps = append(comps,
			discordgo.TextDisplay{Content: p.describe(ctx, serverID, command)},
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				roleSelect(command, page, roles, placeholder),
			}},
		)
	}
	if pages > 1 {
		comps = append(comps, p.navRow(ctx, serverID, page, pages))
	}
	return comps, nil
}

// describe renders a permission's localized description. Each grantable key may
// have a specific entry "permissions.command.<key>" (prose that names the real
// command, e.g. "Create & edit custom contracts (`/contracts`)"); when absent it
// falls back to the localized generic line "permissions.panel.command", which
// just shows the command path. The Translator returns the requested key verbatim
// when it has no entry, so an unchanged result signals "no specific description".
func (p *panel) describe(ctx context.Context, serverID uuid.UUID, command string) string {
	key := "permissions.command." + command
	if desc := p.loc.Render(ctx, serverID, key, map[string]any{"Command": command}); desc != key {
		return desc
	}
	return p.loc.Render(ctx, serverID, "permissions.panel.command", map[string]any{"Command": command})
}

// roleSelect builds a native role-picker for a command, prefilled with the roles
// currently granted it.
func roleSelect(command string, page int, roleIDs []string, placeholder string) discordgo.SelectMenu {
	defaults := make([]discordgo.SelectMenuDefaultValue, len(roleIDs))
	for i, id := range roleIDs {
		defaults[i] = discordgo.SelectMenuDefaultValue{ID: id, Type: discordgo.SelectMenuDefaultValueRole}
	}
	minValues := 0
	return discordgo.SelectMenu{
		MenuType:      discordgo.RoleSelectMenu,
		CustomID:      setCustomID(page, command),
		Placeholder:   placeholder,
		MinValues:     &minValues,
		MaxValues:     maxRolesPerCommand,
		DefaultValues: defaults,
	}
}

func (p *panel) navRow(ctx context.Context, serverID uuid.UUID, page, pages int) discordgo.MessageComponent {
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{
			Label:    p.loc.Render(ctx, serverID, "permissions.panel.prev", nil),
			Style:    discordgo.SecondaryButton,
			CustomID: pageCustomID(page - 1),
			Disabled: page <= 0,
		},
		discordgo.Button{
			Label:    p.loc.Render(ctx, serverID, "permissions.panel.next", nil),
			Style:    discordgo.SecondaryButton,
			CustomID: pageCustomID(page + 1),
			Disabled: page >= pages-1,
		},
	}}
}

// setCustomID encodes a role-select: "permissions:set:<page>:<command>". The
// command path may contain spaces but never ':', so it is safe as the trailing
// segment.
func setCustomID(page int, command string) string {
	return fmt.Sprintf("%s:set:%d:%s", componentPrefix, page, command)
}

func pageCustomID(page int) string {
	return fmt.Sprintf("%s:page:%d", componentPrefix, page)
}

// parseCustomID is the inverse of setCustomID/pageCustomID. kind is "set" or
// "page"; command is set only for "set".
func parseCustomID(id string) (kind string, page int, command string, ok bool) {
	parts := strings.SplitN(id, ":", 4)
	if len(parts) < 3 || parts[0] != componentPrefix {
		return "", 0, "", false
	}
	page, err := strconv.Atoi(parts[2])
	if err != nil || page < 0 {
		return "", 0, "", false
	}
	switch parts[1] {
	case "page":
		return "page", page, "", true
	case "set":
		if len(parts) != 4 || parts[3] == "" {
			return "", 0, "", false
		}
		return "set", page, parts[3], true
	default:
		return "", 0, "", false
	}
}

// selectedRoleIDs reads the role IDs chosen in a role-select. Discord delivers
// them in Values; some library/payload versions only populate Resolved, so fall
// back to that. An empty result is legitimate (all roles removed → clear).
func selectedRoleIDs(d discordgo.MessageComponentInteractionData) []string {
	if len(d.Values) > 0 {
		return d.Values
	}
	if len(d.Resolved.Roles) == 0 {
		return nil
	}
	ids := make([]string, 0, len(d.Resolved.Roles))
	for id := range d.Resolved.Roles {
		ids = append(ids, id)
	}
	return ids
}

func clampPage(page, pages int) int {
	if page < 0 {
		return 0
	}
	if page >= pages {
		return pages - 1
	}
	return page
}

// invokerID returns the ID of the member who triggered the interaction. Panel
// interactions are guild-only (the session ignores DMs), so Member is set.
func invokerID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	return ""
}
