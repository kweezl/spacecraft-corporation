// Package registry collects slash commands contributed by feature modules and
// dispatches incoming interactions to the right handler.
package registry

import (
	"context"
	"fmt"
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
// mentioned roles.
type Responder interface {
	Respond(i *discordgo.Interaction, content string) error
	RespondEphemeral(i *discordgo.Interaction, content string) error
}

// Handler runs the logic for one slash command. serverID is the resolved
// servers.id (the UUID primary key), looked up once from the Discord snowflake in
// the session before dispatch; handlers pass it straight to their repositories
// (which key on servers_id) and to the Localizer, so the snowflake never has to be
// re-resolved per query.
type Handler func(ctx context.Context, r Responder, i *discordgo.InteractionCreate, serverID uuid.UUID) error

// Command is what a feature module contributes: a definition + a handler.
type Command struct {
	Def     *discordgo.ApplicationCommand
	Handler Handler
	// DefaultDeny is the command's access policy when a server has no role
	// mapping for it: false (the zero value) means open to everyone ("optional"
	// gating — an admin may restrict it), true means locked to the server
	// owner/admins until a role is granted ("required" gating). It is only
	// consulted when the permissions feature is enabled; otherwise every command
	// is open. See internal/features/permissions.
	DefaultDeny bool
}

// Registry maps command names to handlers and exposes the definitions to
// register with Discord.
type Registry struct {
	handlers map[string]Handler
	policies map[string]bool // command name -> DefaultDeny
	defs     []*discordgo.ApplicationCommand
	counter  *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

// Params collects all *Command values from the "commands" fx group. A nil entry
// means a disabled feature module and is skipped.
type Params struct {
	fx.In
	Commands []*Command `group:"commands"`
	Counter  *prometheus.CounterVec
	Duration *prometheus.HistogramVec
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
		handlers: make(map[string]Handler),
		policies: make(map[string]bool),
		counter:  counter,
		duration: duration,
	}
	for _, c := range p.Commands {
		if c == nil {
			continue
		}
		r.handlers[c.Def.Name] = c.Handler
		r.policies[c.Def.Name] = c.DefaultDeny
		r.defs = append(r.defs, c.Def)
	}
	return r
}

// Policy reports a command's default-deny policy (see Command.DefaultDeny) and
// whether the command is known. The access gate uses it to decide what happens
// when a server has no role mapping for the command.
func (r *Registry) Policy(name string) (defaultDeny, known bool) {
	d, ok := r.policies[name]
	return d, ok
}

// Commands returns the definitions to register with Discord.
func (r *Registry) Commands() []*discordgo.ApplicationCommand { return r.defs }

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
	defer func(start time.Time) {
		r.duration.WithLabelValues(name).Observe(time.Since(start).Seconds())
	}(time.Now())
	return h(ctx, resp, i, serverID)
}
