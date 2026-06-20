package permissions

import "github.com/kweezl/spacecraft-corporation/internal/discord/registry"

// catalog supplies the set of gateable command paths to the /permissions panel
// (one role-picker per command). It holds a lazily-bound accessor rather than the
// registry itself: the registry can't be injected into the panel directly, since
// the /permissions command is a member of the registry's command group and that
// would form an fx construction cycle. The catalog is provided dependency-free
// and its accessor is bound to registry.CommandPaths via fx.Invoke once both
// exist (see Module). It is only read at interaction time, long after binding, so
// a plain field is safe.
type catalog struct {
	list func() []string
}

func newCatalog() *catalog { return &catalog{} }

// paths returns the gateable command paths, or nil before the catalog is bound.
func (c *catalog) paths() []string {
	if c == nil || c.list == nil {
		return nil
	}
	return c.list()
}

// has reports whether command is a known gateable path. The panel uses it to
// ignore a component CustomID that doesn't name a real command (don't trust
// input). An unbound catalog can't tell, so it reports false.
func (c *catalog) has(command string) bool {
	for _, p := range c.paths() {
		if p == command {
			return true
		}
	}
	return false
}

// catalogBinding is the fx.Invoke target that binds the catalog to the registry
// after both are constructed. Declared here so Module stays terse.
func bindCatalog(c *catalog, r *registry.Registry) { c.list = r.CommandPaths }
