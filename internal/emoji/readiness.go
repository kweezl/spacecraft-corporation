package emoji

import (
	"context"
	"errors"

	"github.com/kweezl/spacecraft-corporation/internal/instrumentation"
)

// errNotSynced is the readiness failure reported until the startup sync finishes.
var errNotSynced = errors.New("emoji not synced")

// newReadinessCheck contributes an "emoji" probe to the instrumentation
// readiness group: /readyz stays red until the startup sync has populated the
// Store, so the bot is not reported ready before its emojis are available.
func newReadinessCheck(s *Syncer) instrumentation.ReadinessCheck {
	return instrumentation.ReadinessCheck{
		Name: "emoji",
		Probe: func(context.Context) error {
			if !s.Ready() {
				return errNotSynced
			}
			return nil
		},
	}
}
