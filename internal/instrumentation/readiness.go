package instrumentation

import (
	"context"
	"fmt"

	"go.uber.org/fx"
)

// ReadinessCheck is a named health probe contributed by a subsystem (the
// database pool, the Discord session, ...). Probe reports the subsystem's
// CURRENT health: a nil error means ready. Subsystems contribute checks into the
// "readiness_checks" fx group; /readyz reports ready only when every probe
// passes, so readiness reflects live dependency health rather than a one-shot
// "startup finished" flag.
type ReadinessCheck struct {
	Name  string
	Probe func(ctx context.Context) error
}

// readinessParams collects the readiness checks contributed by other modules.
type readinessParams struct {
	fx.In
	Checks []ReadinessCheck `group:"readiness_checks"`
}

// Readiness aggregates the readiness probes contributed across the app.
type Readiness struct {
	checks []ReadinessCheck
}

func newReadiness(p readinessParams) *Readiness {
	return &Readiness{checks: p.Checks}
}

// Check runs every probe and returns the first failure (prefixed with the
// failing check's name), or nil when all pass. Probes share the caller's
// context so a slow dependency can't hang the readiness endpoint.
func (r *Readiness) Check(ctx context.Context) error {
	for _, c := range r.checks {
		if err := c.Probe(ctx); err != nil {
			return fmt.Errorf("%s: %w", c.Name, err)
		}
	}
	return nil
}
