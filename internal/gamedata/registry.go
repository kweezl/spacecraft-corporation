package gamedata

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"go.uber.org/zap"

	"github.com/kweezl/spacecraft-corporation/internal/gamedata/schema"
	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

// source is the generated wiring for one defined version: its package vars,
// collected by registry_gen.go into definedSources. The Registry turns the
// requested subset of these into linked Catalogs.
type source struct {
	version          string
	parent           string
	items            map[schema.GDID]schema.Item
	removedItems     []schema.GDID
	categories       map[schema.GDID]schema.Category
	contracts        map[schema.GDID]schema.Contract
	spaceObjects     map[schema.GDID]schema.SpaceObject
	names            map[i18n.Language]map[schema.GDID]string
	descs            map[i18n.Language]map[schema.GDID]string
	categoryNames    map[i18n.Language]map[schema.GDID]string
	contractNames    map[i18n.Language]map[schema.GDID]string
	factionNames     map[i18n.Language]map[schema.GDID]string
	spaceObjectNames map[i18n.Language]map[schema.GDID]string
}

// Registry holds the loaded game-data versions and resolves a stored
// gamedata_version to its Catalog. New links are stamped with Latest().
type Registry struct {
	versions map[string]*Catalog
	latest   *Catalog
}

// newRegistry is the fx constructor. It loads the versions named by
// GAMEDATA_VERSIONS (plus their ancestors); when the var is unset it loads every
// defined version. Unknown listed versions are warned and skipped.
func newRegistry(log *zap.Logger) (*Registry, error) {
	requested, present := loadRequestedVersions()
	if !present {
		requested = definedVersionNames()
	}
	return buildRegistry(requested, definedSources, log)
}

func buildRegistry(requested []string, defined map[string]source, log *zap.Logger) (*Registry, error) {
	needed := map[string]bool{}
	for _, name := range requested {
		if _, ok := defined[name]; !ok {
			warn(log, "requested gamedata version is not defined; skipping", zap.String("version", name))
			continue
		}
		// Pull in the whole ancestor chain — a delta layer is meaningless
		// without the parents it overlays.
		for cur := name; cur != ""; {
			s, ok := defined[cur]
			if !ok {
				return nil, fmt.Errorf("gamedata version %q references undefined parent %q", name, cur)
			}
			needed[cur] = true
			cur = s.parent
		}
	}

	cats := map[string]*Catalog{}
	var build func(name string) *Catalog
	build = func(name string) *Catalog {
		if c, ok := cats[name]; ok {
			return c
		}
		s := defined[name]
		var parent *Catalog
		if s.parent != "" {
			parent = build(s.parent)
		}
		c := newCatalog(s, parent)
		cats[name] = c
		return c
	}

	versions := map[string]*Catalog{}
	var latest *Catalog
	for name := range needed {
		c := build(name)
		versions[name] = c
		if latest == nil || versionNumber(name) > versionNumber(latest.version) {
			latest = c
		}
	}

	loaded := make([]string, 0, len(versions))
	for name := range versions {
		loaded = append(loaded, name)
	}
	sort.Slice(loaded, func(i, j int) bool { return versionNumber(loaded[i]) < versionNumber(loaded[j]) })
	latestName := ""
	if latest != nil {
		latestName = latest.version
	}
	info(log, "gamedata loaded", zap.Strings("versions", loaded), zap.String("latest", latestName))

	return &Registry{versions: versions, latest: latest}, nil
}

// Load builds a Registry outside the fx graph (tests, tools): the named
// versions plus their ancestors, or every defined version when names is empty.
// Pure compiled-in data — no I/O. A nil logger is fine.
func Load(names []string, log *zap.Logger) (*Registry, error) {
	if len(names) == 0 {
		names = definedVersionNames()
	}
	return buildRegistry(names, definedSources, log)
}

// Version returns the Catalog for a version name (as stored on a link).
func (r *Registry) Version(name string) (*Catalog, bool) {
	c, ok := r.versions[name]
	return c, ok
}

// Latest returns the newest loaded version, used to stamp new links. It is nil
// only when no versions are loaded (GAMEDATA_VERSIONS empty).
func (r *Registry) Latest() *Catalog { return r.latest }

// Loaded lists the loaded version names, oldest first.
func (r *Registry) Loaded() []string {
	out := make([]string, 0, len(r.versions))
	for name := range r.versions {
		out = append(out, name)
	}
	sort.Slice(out, func(i, j int) bool { return versionNumber(out[i]) < versionNumber(out[j]) })
	return out
}

func definedVersionNames() []string {
	out := make([]string, 0, len(definedSources))
	for name := range definedSources {
		out = append(out, name)
	}
	sort.Slice(out, func(i, j int) bool { return versionNumber(out[i]) < versionNumber(out[j]) })
	return out
}

func versionNumber(name string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(name, "v"))
	return n
}

func info(log *zap.Logger, msg string, fields ...zap.Field) {
	if log != nil {
		log.Info(msg, fields...)
	}
}

func warn(log *zap.Logger, msg string, fields ...zap.Field) {
	if log != nil {
		log.Warn(msg, fields...)
	}
}
