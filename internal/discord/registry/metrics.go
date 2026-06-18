package registry

import "github.com/prometheus/client_golang/prometheus"

// newCommandCounter declares the shared slash-command call counter,
// discord_command_total{command="..."}.
//
// Naming convention: Namespace = module area (discord), Subsystem = the
// contextual feature (command), Name = what we collect (total).
func newCommandCounter(reg *prometheus.Registry) *prometheus.CounterVec {
	c := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "discord",
		Subsystem: "command",
		Name:      "total",
		Help:      "Total number of slash commands dispatched, by command name.",
	}, []string{"command"})
	reg.MustRegister(c)
	return c
}
