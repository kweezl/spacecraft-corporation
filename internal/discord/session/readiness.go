package session

import (
	"context"
	"errors"

	"github.com/kweezl/spacecraft-corporation/internal/instrumentation"
)

// errNotConnected is the readiness failure reported while the gateway is down.
var errNotConnected = errors.New("discord gateway not connected")

// newReadinessCheck contributes a "discord" probe to the instrumentation
// readiness group: /readyz stays red until the gateway has connected and
// finished its READY handshake, and goes red again on a disconnect. Open()
// returns before READY, so a startup-ordering flag could not capture this.
func newReadinessCheck(m *Manager) instrumentation.ReadinessCheck {
	return instrumentation.ReadinessCheck{
		Name: "discord",
		Probe: func(context.Context) error {
			if !m.Connected() {
				return errNotConnected
			}
			return nil
		},
	}
}
