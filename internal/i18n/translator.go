// Package i18n renders user-facing messages from embedded template bundles,
// keyed by theme (a wording "skin") then language then message key. Templates
// are plain text/template strings loaded from locales/<theme>/<lang>.json at
// startup. A server picks its theme and language (see internal/settings); the
// Localizer resolves those per server and renders.
package i18n

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"text/template"

	"github.com/caarlos0/env/v11"
)

//go:embed all:locales
var localesFS embed.FS

// Config holds the app-wide fallback theme and language, used for servers that
// have not chosen their own and as the last resort in the render fallback chain.
type Config struct {
	DefaultLanguage Language `env:"APP_LANGUAGE" envDefault:"en"`
	DefaultTheme    string   `env:"APP_THEME"    envDefault:"standard"`
}

func loadConfig() (Config, error) { return env.ParseAs[Config]() }

// Translator holds the compiled templates for every theme/language and renders
// messages. It is read-only after construction, so it is safe for concurrent
// use.
type Translator struct {
	// themes[theme][lang][key] -> compiled template (lang keyed by string, as it
	// comes from the bundle filenames).
	themes       map[string]map[string]map[string]*template.Template
	themeList    []string
	langSet      map[Language]struct{}
	langList     []Language
	defaultTheme string
	defaultLang  Language
}

// New loads the embedded bundles and validates that the configured default
// theme/language exist.
func New(cfg Config) (*Translator, error) {
	t, err := load(localesFS, "locales")
	if err != nil {
		return nil, err
	}
	t.defaultTheme = cfg.DefaultTheme
	t.defaultLang = cfg.DefaultLanguage
	if _, ok := t.themes[cfg.DefaultTheme]; !ok {
		return nil, fmt.Errorf("i18n: default theme %q not found in bundles", cfg.DefaultTheme)
	}
	if _, ok := t.themes[cfg.DefaultTheme][string(cfg.DefaultLanguage)]; !ok {
		return nil, fmt.Errorf("i18n: default language %q not found in theme %q",
			cfg.DefaultLanguage, cfg.DefaultTheme)
	}
	return t, nil
}

// load reads locales/<theme>/<lang>.json into compiled templates.
func load(fsys fs.FS, root string) (*Translator, error) {
	themeDirs, err := fs.ReadDir(fsys, root)
	if err != nil {
		return nil, fmt.Errorf("i18n: read locales: %w", err)
	}
	t := &Translator{
		themes:  make(map[string]map[string]map[string]*template.Template),
		langSet: make(map[Language]struct{}),
	}
	for _, td := range themeDirs {
		if !td.IsDir() {
			continue
		}
		theme := td.Name()
		langFiles, err := fs.ReadDir(fsys, path.Join(root, theme))
		if err != nil {
			return nil, fmt.Errorf("i18n: read theme %q: %w", theme, err)
		}
		t.themes[theme] = make(map[string]map[string]*template.Template)
		for _, lf := range langFiles {
			lang := strings.TrimSuffix(lf.Name(), ".json")
			if lang == lf.Name() {
				continue // not a .json file
			}
			tmpls, err := loadFile(fsys, path.Join(root, theme, lf.Name()), theme, lang)
			if err != nil {
				return nil, err
			}
			t.themes[theme][lang] = tmpls
			t.langSet[Language(lang)] = struct{}{}
		}
	}
	for theme := range t.themes {
		t.themeList = append(t.themeList, theme)
	}
	for lang := range t.langSet {
		t.langList = append(t.langList, lang)
	}
	sort.Strings(t.themeList)
	sort.Slice(t.langList, func(i, j int) bool { return t.langList[i] < t.langList[j] })
	return t, nil
}

func loadFile(fsys fs.FS, name, theme, lang string) (map[string]*template.Template, error) {
	raw, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, fmt.Errorf("i18n: read %s: %w", name, err)
	}
	var msgs map[string]string
	if err := json.Unmarshal(raw, &msgs); err != nil {
		return nil, fmt.Errorf("i18n: parse %s: %w", name, err)
	}
	out := make(map[string]*template.Template, len(msgs))
	for key, str := range msgs {
		tmpl, err := template.New(key).Parse(str)
		if err != nil {
			return nil, fmt.Errorf("i18n: template %q in %s/%s: %w", key, theme, lang, err)
		}
		out[key] = tmpl
	}
	return out, nil
}

// Render renders a message for the given theme and language. Unknown theme or
// language fall back to the configured defaults; a key missing in the chosen
// (theme, language) falls back to the same theme's default language, then the
// default theme's default language. If the key is unknown everywhere, the key
// itself is returned so a missing translation is visible but never fatal.
func (t *Translator) Render(theme string, lang Language, key string, data any) string {
	if tmpl := t.lookup(theme, lang, key); tmpl != nil {
		return exec(tmpl, data, key)
	}
	if tmpl := t.lookup(theme, t.defaultLang, key); tmpl != nil {
		return exec(tmpl, data, key)
	}
	if tmpl := t.lookup(t.defaultTheme, t.defaultLang, key); tmpl != nil {
		return exec(tmpl, data, key)
	}
	return key
}

func (t *Translator) lookup(theme string, lang Language, key string) *template.Template {
	langs, ok := t.themes[theme]
	if !ok {
		return nil
	}
	keys, ok := langs[string(lang)]
	if !ok {
		return nil
	}
	return keys[key]
}

func exec(tmpl *template.Template, data any, key string) string {
	var b bytes.Buffer
	if err := tmpl.Execute(&b, data); err != nil {
		return key
	}
	return b.String()
}

// Themes returns the available theme names, sorted.
func (t *Translator) Themes() []string { return append([]string(nil), t.themeList...) }

// Languages returns the renderable language codes (those with a bundle), sorted.
// This is the subset a server may choose, not the full known set
// (see KnownLanguages).
func (t *Translator) Languages() []Language { return append([]Language(nil), t.langList...) }

// HasTheme reports whether a theme exists.
func (t *Translator) HasTheme(theme string) bool { _, ok := t.themes[theme]; return ok }

// HasLanguage reports whether a language is renderable (has a bundle).
func (t *Translator) HasLanguage(lang Language) bool { _, ok := t.langSet[lang]; return ok }

// Defaults returns the configured default theme and language.
func (t *Translator) Defaults() (theme string, lang Language) { return t.defaultTheme, t.defaultLang }
