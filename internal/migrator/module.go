package migrator

import "go.uber.org/fx"

// Module runs migrations as an fx invoke. Invokes execute during fx
// construction, before any lifecycle OnStart hook (e.g. the session manager),
// guaranteeing the schema exists before sessions load tokens.
func Module() fx.Option {
	return fx.Module("migrator", fx.Invoke(Run))
}
