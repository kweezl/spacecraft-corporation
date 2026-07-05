package i18n_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

func newTranslator(t *testing.T) *i18n.Translator {
	t.Helper()
	tr, err := i18n.New(i18n.Config{DefaultLanguage: "en", DefaultTheme: "standard"})
	require.NoError(t, err)
	return tr
}

func TestNew_RejectsMissingDefaultTheme(t *testing.T) {
	_, err := i18n.New(i18n.Config{DefaultLanguage: "en", DefaultTheme: "nope"})
	require.Error(t, err)
}

func TestRender_ThemeAndLanguage(t *testing.T) {
	tr := newTranslator(t)

	assert.Equal(t, "3 ms",
		tr.Render("standard", "en", "ping.latency_ms", map[string]any{"Value": 3}))
	assert.Equal(t, "3 мс",
		tr.Render("standard", "ru", "ping.latency_ms", map[string]any{"Value": 3}))
	assert.Contains(t,
		tr.Render("lore", "en", "ping.title", nil), "Telemetry")
}

func TestRender_ConditionalTemplate(t *testing.T) {
	tr := newTranslator(t)

	withOwner := tr.Render("standard", "en", "session.unapproved", map[string]any{"Owner": "12345"})
	assert.Contains(t, withOwner, "<@12345>")

	without := tr.Render("standard", "en", "session.unapproved", map[string]any{})
	assert.NotContains(t, without, "<@")
	assert.Contains(t, without, "isn't approved")
}

func TestRender_FallsBackToDefaultThemeAndLang(t *testing.T) {
	tr := newTranslator(t)

	// Unknown theme/language fall back to the default (standard/en).
	got := tr.Render("ghost", "xx", "ping.latency_ms", map[string]any{"Value": 1})
	assert.Equal(t, "1 ms", got)
}

func TestRender_UnknownKeyReturnsKey(t *testing.T) {
	tr := newTranslator(t)
	assert.Equal(t, "nope.missing", tr.Render("standard", "en", "nope.missing", nil))
}

func TestCatalog(t *testing.T) {
	tr := newTranslator(t)
	assert.Equal(t, []string{"lore", "standard"}, tr.Themes())
	assert.Equal(t, []i18n.Language{"en", "ru"}, tr.Languages())
	assert.True(t, tr.HasTheme("lore"))
	assert.False(t, tr.HasTheme("ghost"))
	assert.True(t, tr.HasLanguage("ru"))
	assert.False(t, tr.HasLanguage("xx"))
	theme, lang := tr.Defaults()
	assert.Equal(t, "standard", theme)
	assert.Equal(t, i18n.LanguageEN, lang)
}

func TestLocalizer_RendersForResolvedServer(t *testing.T) {
	tr := newTranslator(t)
	loc := i18n.NewLocalizer(tr, i18n.StaticResolver{Theme: "standard", Lang: "ru"})

	got := loc.Render(context.Background(), uuid.New(), "ping.latency_ms", map[string]any{"Value": 7})
	assert.Equal(t, "7 мс", got)
}
