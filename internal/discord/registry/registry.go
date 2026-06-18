// Package registry collects slash commands contributed by feature modules and
// dispatches incoming interactions to the right handler.
package registry

import (
	"context"
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/fx"
)

// Responder sends a reply to an interaction. Handlers use this instead of
// touching *discordgo.Session directly, so they stay testable.
type Responder interface {
	Respond(i *discordgo.Interaction, content string) error
}

// Handler runs the logic for one slash command.
type Handler func(ctx context.Context, r Responder, i *discordgo.InteractionCreate) error

// Command is what a feature module contributes: a definition + a handler.
type Command struct {
	Def     *discordgo.ApplicationCommand
	Handler Handler
}

// Registry maps command names to handlers and exposes the definitions to
// register with Discord.
type Registry struct {
	handlers map[string]Handler
	defs     []*discordgo.ApplicationCommand
	counter  *prometheus.CounterVec
}

// Params collects all *Command values from the "commands" fx group. A nil entry
// means a disabled feature module and is skipped.
type Params struct {
	fx.In
	Commands []*Command `group:"commands"`
	Counter  *prometheus.CounterVec
}

// New builds a Registry, ignoring nil (disabled) commands.
func New(p Params) *Registry {
	counter := p.Counter
	if counter == nil {
		// Direct (non-fx) construction in tests: a throwaway registry keeps the
		// counter usable so Dispatch needs no nil check.
		counter = newCommandCounter(prometheus.NewRegistry())
	}
	r := &Registry{handlers: make(map[string]Handler), counter: counter}
	for _, c := range p.Commands {
		if c == nil {
			continue
		}
		r.handlers[c.Def.Name] = c.Handler
		r.defs = append(r.defs, c.Def)
	}
	return r
}

// Commands returns the definitions to register with Discord.
func (r *Registry) Commands() []*discordgo.ApplicationCommand { return r.defs }

// Dispatch routes an interaction to its handler.
func (r *Registry) Dispatch(ctx context.Context, resp Responder, i *discordgo.InteractionCreate) error {
	name := i.ApplicationCommandData().Name
	h, ok := r.handlers[name]
	if !ok {
		return fmt.Errorf("no handler for command %q", name)
	}
	r.counter.WithLabelValues(name).Inc()
	return h(ctx, resp, i)
}

// Module provides the Registry built from the fx command group. Core module.
func Module() fx.Option {
	return fx.Module("registry",
		fx.Provide(newCommandCounter),
		fx.Provide(New),
	)
}
