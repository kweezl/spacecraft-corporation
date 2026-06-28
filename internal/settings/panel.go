package settings

import (
	"context"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"

	"github.com/kweezl/spacecraft-corporation/internal/discord/registry"
	"github.com/kweezl/spacecraft-corporation/internal/discord/session"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

const (
	// commandName is the /settings command and the namespace prefix for its
	// component CustomIDs (the registry routes components by the text before the
	// first ':').
	commandName     = "settings"
	componentPrefix = commandName
)

// panel renders and drives the /settings UI: an ephemeral Components V2 message
// showing the server's current theme and language, each editable through a
// string-select. Choosing a value applies it immediately and re-renders the
// panel in the (possibly new) theme/language — one command instead of the old
// theme/language/show subcommand trio.
type panel struct {
	store *Store
	tr    *i18n.Translator
	loc   *i18n.Localizer
	// access gates panel mutations; it is provided by the permissions feature and
	// is nil when that feature is disabled (role control off entirely).
	access session.CommandAccess
	// sections are feature-contributed extra settings (e.g. the contracts forum
	// channel), appended after theme/language. Empty when no feature contributes.
	sections []Section
}

func newPanel(store *Store, tr *i18n.Translator, loc *i18n.Localizer, access session.CommandAccess, sections []Section) *panel {
	return &panel{store: store, tr: tr, loc: loc, access: access, sections: sections}
}

// command is the /settings slash command: it just opens the panel. It is
// DefaultDeny, so only the owner/admins (or a role granted access to it) can run
// it — and since the panel is ephemeral, only that invoker can click it.
func (p *panel) command() *registry.Command {
	return &registry.Command{
		DefaultDeny: true,
		Def: &discordgo.ApplicationCommand{
			Name:        commandName,
			Description: "Open this server's theme and language settings",
		},
		Handler: p.handle,
	}
}

// component handles the panel's theme/language select edits.
func (p *panel) component() *registry.Component {
	return &registry.Component{Prefix: componentPrefix, Handler: p.handleComponent}
}

func (p *panel) handle(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	return r.RespondComponentsV2Ephemeral(i.Interaction, p.view(ctx, serverID))
}

func (p *panel) handleComponent(ctx context.Context, r registry.Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	// The session gates component interactions only on server approval, trusting
	// each mutating handler to re-authorize itself (see session.go). The panel is
	// ephemeral, but a persisted ephemeral message can outlive the invoker's
	// access, so re-check the same boundary the /settings command enforces.
	ok, err := p.authorized(ctx, i, serverID)
	if err != nil {
		return fmt.Errorf("settings: authorize panel: %w", err)
	}
	if !ok {
		return r.UpdateComponentsV2(i.Interaction, []discordgo.MessageComponent{
			discordgo.TextDisplay{Content: p.loc.Render(ctx, serverID, "session.denied",
				map[string]any{"Command": commandName})},
		})
	}

	id := i.MessageComponentData().CustomID
	// Feature-contributed sections claim their own CustomIDs (e.g. "settings:forum").
	// They run behind the same authorize gate above; each persists its change and
	// re-renders the whole panel.
	for _, s := range p.sections {
		if s.Owns(id) {
			return s.Handle(ctx, r, i, serverID, func() []discordgo.MessageComponent { return p.view(ctx, serverID) })
		}
	}
	kind, ok := parseCustomID(id)
	if !ok {
		return fmt.Errorf("settings: bad component id %q", id)
	}
	// Resolve current values up front: they gate redundant writes below and feed
	// the re-render at the end (one Resolve, served from cache).
	theme, lang := p.store.Resolve(ctx, serverID)
	// Don't trust the CustomID/value: act only on a value that is a real theme or
	// language and actually differs from the current one — re-picking the
	// already-selected value is a no-op, not a DB write + cache invalidation. A
	// crafted or stale selection is ignored (we still re-render so the panel stays
	// responsive).
	value := selectedValue(i.MessageComponentData())
	switch kind {
	case "theme":
		if value != "" && value != theme && p.tr.HasTheme(value) {
			if err := p.store.SetTheme(ctx, serverID, value); err != nil {
				return fmt.Errorf("settings: set theme %q: %w", value, err)
			}
			theme = value
		}
	case "language":
		if value != "" && value != lang && p.tr.HasLanguage(value) {
			if err := p.store.SetLanguage(ctx, serverID, value); err != nil {
				return fmt.Errorf("settings: set language %q: %w", value, err)
			}
			lang = value
		}
	}
	return r.UpdateComponentsV2(i.Interaction, p.viewFrom(ctx, serverID, theme, lang))
}

// authorized re-checks that the interacting member may manage settings — the
// same boundary the session applies to the /settings command: administrators
// (and the guild owner, whose computed permissions include Administrator) always
// pass. When the permissions feature is enabled the member must otherwise hold a
// role granted the "settings" command (which is DefaultDeny); when it is disabled
// the access gate is nil and the command is admin-only via Discord's default
// member permissions, so reaching here without admin is allowed the same way the
// command itself is — mirroring session.allowed's nil-gate behavior.
func (p *panel) authorized(ctx context.Context, i *discordgo.InteractionCreate, serverID uuid.UUID) (bool, error) {
	if i.Member != nil && i.Member.Permissions&discordgo.PermissionAdministrator != 0 {
		return true, nil
	}
	if p.access == nil {
		return true, nil
	}
	var roles []string
	if i.Member != nil {
		roles = i.Member.Roles
	}
	return p.access.IsAllowed(ctx, session.AccessRequest{
		ServerID:    serverID,
		Command:     commandName,
		UserRoles:   roles,
		DefaultDeny: true,
	})
}

// view builds the Components V2 view from the server's current settings. The
// command handler uses it (resolving fresh); the component handler resolves once
// for its no-op guard and calls viewFrom directly to avoid a second Resolve.
func (p *panel) view(ctx context.Context, serverID uuid.UUID) []discordgo.MessageComponent {
	theme, lang := p.store.Resolve(ctx, serverID)
	return p.viewFrom(ctx, serverID, theme, lang)
}

// viewFrom builds the Components V2 view for the given theme/language: a header
// showing them, then a theme select and a language select, each prefilled.
func (p *panel) viewFrom(ctx context.Context, serverID uuid.UUID, theme, lang string) []discordgo.MessageComponent {
	out := []discordgo.MessageComponent{
		discordgo.TextDisplay{Content: p.loc.Render(ctx, serverID, "settings.panel.header",
			map[string]any{"Theme": theme, "Language": lang})},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			valueSelect("theme", p.tr.Themes(), theme,
				p.loc.Render(ctx, serverID, "settings.panel.theme_placeholder", nil)),
		}},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			valueSelect("language", p.tr.Languages(), lang,
				p.loc.Render(ctx, serverID, "settings.panel.language_placeholder", nil)),
		}},
	}
	// Feature-contributed sections (e.g. the contracts forum channel) render after
	// the built-in theme/language rows.
	for _, s := range p.sections {
		out = append(out, s.Rows(ctx, serverID)...)
	}
	return out
}

// valueSelect builds a single-choice string-select over values, with current
// marked as the default selection.
func valueSelect(kind string, values []string, current, placeholder string) discordgo.SelectMenu {
	opts := make([]discordgo.SelectMenuOption, len(values))
	for i, v := range values {
		opts[i] = discordgo.SelectMenuOption{Label: v, Value: v, Default: v == current}
	}
	one := 1
	return discordgo.SelectMenu{
		MenuType:    discordgo.StringSelectMenu,
		CustomID:    customID(kind),
		Placeholder: placeholder,
		MinValues:   &one,
		MaxValues:   1,
		Options:     opts,
	}
}

// customID encodes a select: "settings:theme" / "settings:language".
func customID(kind string) string { return componentPrefix + ":" + kind }

// parseCustomID is the inverse of customID; kind is "theme" or "language".
func parseCustomID(id string) (kind string, ok bool) {
	parts := strings.SplitN(id, ":", 2)
	if len(parts) != 2 || parts[0] != componentPrefix {
		return "", false
	}
	switch parts[1] {
	case "theme", "language":
		return parts[1], true
	default:
		return "", false
	}
}

// selectedValue reads the single chosen value of a string-select. An empty
// result (nothing selected) is ignored by the caller.
func selectedValue(d discordgo.MessageComponentInteractionData) string {
	if len(d.Values) > 0 {
		return d.Values[0]
	}
	return ""
}
