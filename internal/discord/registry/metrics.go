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

// newCommandDuration declares the slash-command handler latency histogram,
// discord_command_duration_seconds{command="..."}. Default buckets span the
// region that matters here: Discord invalidates an interaction after ~3s, which
// falls between the 2.5s and 5s buckets, so a rising p99 there is an early
// warning of "Unknown interaction" (10062) errors.
//
// Naming convention: Namespace = module area (discord), Subsystem = the
// contextual feature (command), Name = what we collect (duration_seconds).
func newCommandDuration(reg *prometheus.Registry) *prometheus.HistogramVec {
	h := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "discord",
		Subsystem: "command",
		Name:      "duration_seconds",
		Help:      "Slash-command handler execution time in seconds, by command name.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"command"})
	reg.MustRegister(h)
	return h
}
