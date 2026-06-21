// Package registry collects slash commands contributed by feature modules and
// dispatches incoming interactions to the right handler.
package registry

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/fx"
)

// Responder sends a reply to an interaction. Handlers use this instead of
// touching *discordgo.Session directly, so they stay testable. RespondEphemeral
// replies privately to the invoking user only (Discord ephemeral message) —
// used for admin/config replies that shouldn't clutter the channel or notify
// mentioned roles. RespondEmbed replies with a rich embed (e.g. /ping's latency
// breakdown).
type Responder interface {
	Respond(i *discordgo.Interaction, content string) error
	RespondEphemeral(i *discordgo.Interaction, content string) error
	RespondEmbed(i *discordgo.Interaction, embed *discordgo.MessageEmbed) error
	// RespondAutocomplete answers an autocomplete interaction with suggestion
	// choices (Discord renders at most 25).
	RespondAutocomplete(i *discordgo.Interaction, choices []*discordgo.ApplicationCommandOptionChoice) error
	// RespondEmbedComponents replies with an embed plus attached message
	// components (e.g. pagination buttons).
	RespondEmbedComponents(i *discordgo.Interaction, embed *discordgo.MessageEmbed, components []discordgo.MessageComponent) error
	// UpdateMessage edits in place the message a component interaction is
	// attached to (e.g. flipping a pagination page without posting anew).
	UpdateMessage(i *discordgo.Interaction, embed *discordgo.MessageEmbed, components []discordgo.MessageComponent) error
	// RespondComponentsV2Ephemeral replies with an ephemeral message built
	// entirely from Components V2 (no content/embeds), e.g. the permissions panel.
	RespondComponentsV2Ephemeral(i *discordgo.Interaction, components []discordgo.MessageComponent) error
	// RespondModal opens a modal (popup form) in response to a command or
	// component interaction. customID routes the eventual modal-submit back to a
	// component handler (by its namespace prefix, like any CustomID); components
	// are the modal's action rows, each wrapping one input (e.g. a TextInput).
	// A modal may not be opened in response to another modal submit.
	RespondModal(i *discordgo.Interaction, customID, title string, components []discordgo.MessageComponent) error
	// UpdateComponentsV2 edits a Components V2 message in place (panel paging /
	// applying a role-picker change).
	UpdateComponentsV2(i *discordgo.Interaction, components []discordgo.MessageComponent) error
}

// Handler runs the logic for one slash command. serverID is the resolved
// servers.id (the UUID primary key), looked up once from the Discord snowflake in
// the session before dispatch; handlers pass it straight to their repositories
// (which key on servers_id) and to the Localizer, so the snowflake never has to be
// re-resolved per query.
type Handler func(ctx context.Context, r Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error

// AutocompleteHandler produces suggestion choices for the option the user is
// currently typing on a command. It is dispatched on the autocomplete
// interaction by the command's name (see DispatchAutocomplete); the handler
// inspects the focused option via i.ApplicationCommandData() and returns up to
// 25 choices. It must scope its suggestions by ownership itself — autocomplete
// is a convenience, never an authorization boundary (the submitted value is
// re-validated when the command runs).
type AutocompleteHandler func(ctx context.Context, i *discordgo.InteractionCreate, serverID uuid.UUID) ([]*discordgo.ApplicationCommandOptionChoice, error)

// ComponentHandler runs when a user interacts with a message component (e.g. a
// pagination button). It is routed by the namespace prefix of the component's
// CustomID (the text before the first ':'); see DispatchComponent.
type ComponentHandler func(ctx context.Context, r Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error

// Component is what a feature module contributes to handle its message
// components: a CustomID namespace prefix and the handler for it. CustomIDs are
// formatted "<prefix>:<rest>" so the registry can route them without knowing
// each feature's internal encoding.
type Component struct {
	Prefix  string
	Handler ComponentHandler
}

// Command is what a feature module contributes: a definition + a handler.
type Command struct {
	Def     *discordgo.ApplicationCommand
	Handler Handler
	// Autocomplete, when set, answers autocomplete interactions for this
	// command's options. Optional.
	Autocomplete AutocompleteHandler
	// DefaultDeny is the command's access policy when a server has no role
	// mapping for it: false (the zero value) means open to everyone ("optional"
	// gating — an admin may restrict it), true means locked to the server
	// owner/admins until a role is granted ("required" gating). It is only
	// consulted when the permissions feature is enabled; otherwise every command
	// is open. See internal/features/permissions.
	DefaultDeny bool
	// SubcommandGated makes the access gate key on the full subcommand path
	// (e.g. "base own register") instead of just the top-level command name, so
	// each subcommand can be granted to different roles. The DefaultDeny policy
	// still applies to the whole command. Off by default: top-level-only gating,
	// unchanged for existing commands.
	SubcommandGated bool
}

// Registry maps command names to handlers and exposes the definitions to
// register with Discord.
type Registry struct {
	handlers      map[string]Handler
	autocompletes map[string]AutocompleteHandler
	components    map[string]ComponentHandler // CustomID prefix -> handler
	policies      map[string]bool             // command name -> DefaultDeny
	subGated      map[string]bool             // command name -> SubcommandGated
	defs          []*discordgo.ApplicationCommand
	counter       *prometheus.CounterVec
	duration      *prometheus.HistogramVec
}

// Params collects all *Command and *Component values from the fx groups. A nil
// entry means a disabled feature module and is skipped.
type Params struct {
	fx.In
	Commands   []*Command   `group:"commands"`
	Components []*Component `group:"components"`
	Counter    *prometheus.CounterVec
	Duration   *prometheus.HistogramVec
}

// New builds a Registry, ignoring nil (disabled) commands.
func New(p Params) *Registry {
	counter := p.Counter
	duration := p.Duration
	if counter == nil || duration == nil {
		// Direct (non-fx) construction in tests: a throwaway registry backs any
		// missing metric so Dispatch needs no nil checks.
		reg := prometheus.NewRegistry()
		if counter == nil {
			counter = newCommandCounter(reg)
		}
		if duration == nil {
			duration = newCommandDuration(reg)
		}
	}
	r := &Registry{
		handlers:      make(map[string]Handler),
		autocompletes: make(map[string]AutocompleteHandler),
		components:    make(map[string]ComponentHandler),
		policies:      make(map[string]bool),
		subGated:      make(map[string]bool),
		counter:       counter,
		duration:      duration,
	}
	for _, c := range p.Commands {
		if c == nil {
			continue
		}
		r.handlers[c.Def.Name] = c.Handler
		r.policies[c.Def.Name] = c.DefaultDeny
		r.subGated[c.Def.Name] = c.SubcommandGated
		if c.Autocomplete != nil {
			r.autocompletes[c.Def.Name] = c.Autocomplete
		}
		applyDefaultMemberPermissions(c)
		r.defs = append(r.defs, c.Def)
	}
	for _, c := range p.Components {
		if c == nil {
			continue
		}
		r.components[c.Prefix] = c.Handler
	}
	return r
}

// adminOnly is the default_member_permissions value that hides a command from
// every non-administrator in the Discord client. Discord serialises the field as
// a permission bitfield string; the empty bitfield ("0") means "no permission is
// enough", so only members with Administrator (who bypass the check) see it.
const adminOnly int64 = 0

// applyDefaultMemberPermissions mirrors a command's DefaultDeny policy onto
// Discord's native default_member_permissions as a client-side, defence-in-depth
// layer: an owner/admin command (DefaultDeny) is also hidden from non-admins in
// the Discord UI, so they never get as far as invoking it. The custom permissions
// gate remains the real authorization boundary (it is also the only thing that
// can grant a non-admin role access per subcommand path).
//
// Caveat for delegation: because Discord enforces this BEFORE the interaction
// reaches the bot, a non-admin who was granted a role via /permissions still
// won't see a DefaultDeny command unless a server admin also unhides it for that
// role in Server Settings → Integrations. We only set the field when a command
// hasn't already declared one, so a feature can opt into a looser default (e.g.
// Manage Guild) to keep delegation reachable.
func applyDefaultMemberPermissions(c *Command) {
	if !c.DefaultDeny || c.Def.DefaultMemberPermissions != nil {
		return
	}
	perm := adminOnly
	c.Def.DefaultMemberPermissions = &perm
}

// Policy reports a command's default-deny policy (see Command.DefaultDeny) and
// whether the command is known. The access gate uses it to decide what happens
// when a server has no role mapping for the command.
func (r *Registry) Policy(name string) (defaultDeny, known bool) {
	d, ok := r.policies[name]
	return d, ok
}

// AccessKey returns the key the access gate should authorize for an interaction.
// For a SubcommandGated command it is the full subcommand path (e.g.
// "base own register"); otherwise it is the top-level command name. Unknown
// commands return their bare name. This is the value stored against role grants,
// so granting "base own register" is independent of "base corp register".
func (r *Registry) AccessKey(i *discordgo.InteractionCreate) string {
	data := i.ApplicationCommandData()
	if !r.subGated[data.Name] {
		return data.Name
	}
	parts := []string{data.Name}
	for opts := data.Options; len(opts) > 0; {
		o := opts[0]
		if o.Type != discordgo.ApplicationCommandOptionSubCommand &&
			o.Type != discordgo.ApplicationCommandOptionSubCommandGroup {
			break
		}
		parts = append(parts, o.Name)
		opts = o.Options
	}
	return strings.Join(parts, " ")
}

// CommandPaths returns every gateable key across all registered commands: the
// bare name for top-level-gated commands, and one entry per leaf subcommand path
// for SubcommandGated commands. Used to offer valid grant targets (e.g. as
// /permissions autocomplete). Sorted for stable output.
func (r *Registry) CommandPaths() []string {
	var out []string
	for _, def := range r.defs {
		if !r.subGated[def.Name] {
			out = append(out, def.Name)
			continue
		}
		out = append(out, leafPaths(def.Name, def.Options)...)
	}
	sort.Strings(out)
	return out
}

// leafPaths walks an option tree, emitting "<prefix> <name>" for each leaf
// subcommand and recursing through subcommand groups.
func leafPaths(prefix string, opts []*discordgo.ApplicationCommandOption) []string {
	var out []string
	for _, o := range opts {
		switch o.Type {
		case discordgo.ApplicationCommandOptionSubCommandGroup:
			out = append(out, leafPaths(prefix+" "+o.Name, o.Options)...)
		case discordgo.ApplicationCommandOptionSubCommand:
			out = append(out, prefix+" "+o.Name)
		}
	}
	return out
}

// Commands returns the definitions to register with Discord.
func (r *Registry) Commands() []*discordgo.ApplicationCommand { return r.defs }

// DispatchAutocomplete routes an autocomplete interaction to the command's
// AutocompleteHandler and answers with its choices. Commands without an
// autocomplete handler answer with an empty list (Discord shows nothing).
func (r *Registry) DispatchAutocomplete(ctx context.Context, resp Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	h, ok := r.autocompletes[i.ApplicationCommandData().Name]
	if !ok {
		return resp.RespondAutocomplete(i.Interaction, nil)
	}
	choices, err := h(ctx, i, serverID)
	if err != nil {
		// Don't leave the box spinning: answer empty, surface the error upstream.
		_ = resp.RespondAutocomplete(i.Interaction, nil)
		return err
	}
	return resp.RespondAutocomplete(i.Interaction, choices)
}

// DispatchComponent routes a message-component interaction to the handler
// registered for its CustomID namespace (the text before the first ':').
func (r *Registry) DispatchComponent(ctx context.Context, resp Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	customID := i.MessageComponentData().CustomID
	prefix := customID
	if idx := strings.IndexByte(customID, ':'); idx >= 0 {
		prefix = customID[:idx]
	}
	h, ok := r.components[prefix]
	if !ok {
		return fmt.Errorf("no component handler for prefix %q", prefix)
	}
	return h(ctx, resp, i, serverID)
}

// DispatchModal routes a modal-submit interaction to the handler registered for
// its CustomID namespace (the text before the first ':') — the same component
// handler map as DispatchComponent, since a feature handles its modal submits
// alongside its component clicks. The handler tells the two apart by i.Type.
func (r *Registry) DispatchModal(ctx context.Context, resp Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	customID := i.ModalSubmitData().CustomID
	prefix := customID
	if idx := strings.IndexByte(customID, ':'); idx >= 0 {
		prefix = customID[:idx]
	}
	h, ok := r.components[prefix]
	if !ok {
		return fmt.Errorf("no component handler for modal prefix %q", prefix)
	}
	return h(ctx, resp, i, serverID)
}

// Dispatch routes an interaction to its handler. serverID is the resolved
// servers.id for the interaction's guild (see Handler).
func (r *Registry) Dispatch(ctx context.Context, resp Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error {
	name := i.ApplicationCommandData().Name
	h, ok := r.handlers[name]
	if !ok {
		return fmt.Errorf("no handler for command %q", name)
	}
	r.counter.WithLabelValues(name).Inc()
	// Observe in a defer so the latency is recorded even if the handler panics;
	// time.Since covers the full handler run (including the reply round-trip).
	// The same start instant is stashed in the context so a handler can report
	// its own handle latency (see Elapsed) from the metric's reference point.
	start := time.Now()
	ctx = context.WithValue(ctx, startKey, start)
	defer func() {
		r.duration.WithLabelValues(name).Observe(time.Since(start).Seconds())
	}()
	return h(ctx, resp, i, serverID)
}

// ctxKey is the private type for context keys owned by this package.
type ctxKey int

const startKey ctxKey = iota

// Elapsed reports how long the current command has been running, measured from
// the dispatcher's start instant — the same reference point as the
// discord_command_duration_seconds histogram. It returns (0, false) when called
// outside Dispatch (e.g. a unit test invoking a handler directly), so callers
// can fall back gracefully.
func Elapsed(ctx context.Context) (time.Duration, bool) {
	start, ok := ctx.Value(startKey).(time.Time)
	if !ok {
		return 0, false
	}
	return time.Since(start), true
}
