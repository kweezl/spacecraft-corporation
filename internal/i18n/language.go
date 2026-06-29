package i18n

// Language is a canonical language code used across the app — for the bot UI
// (this package), per-server settings, and game-data lookups (internal/gamedata).
// It is a named string (not a closed enum at the type level), but the constants
// below are the known/allowed values: the union of every language the app deals
// with. Note these two sets differ and both are valid uses of the type:
//
//   - RENDERABLE: the languages the UI can actually show, i.e. those with an
//     embedded locales/<theme>/<lang>.json bundle (see (*Translator).Languages
//     and HasLanguage). Today: en, ru.
//   - GAME-DATA: every language the generated game data carries names for. All
//     eight constants below.
//
// A server's stored language is always a renderable one; game data may be
// queried with any known language (it already has the strings).
type Language string

// The known languages (the game ships translations for all of these).
const (
	LanguageEN   Language = "en"
	LanguageDE   Language = "de"
	LanguageES   Language = "es"
	LanguageFR   Language = "fr"
	LanguagePL   Language = "pl"
	LanguagePTBR Language = "pt-BR"
	LanguageRU   Language = "ru"
	LanguageZH   Language = "zh"
)

// KnownLanguages returns every known language code in a stable order (base
// language first). This is the full allowed set (the game-data coverage), not
// the renderable subset — use (*Translator).Languages for what the UI can
// actually render. A fresh slice is returned each call, so callers may keep or
// mutate it freely.
func KnownLanguages() []Language {
	return []Language{
		LanguageEN, LanguageDE, LanguageES, LanguageFR,
		LanguagePL, LanguagePTBR, LanguageRU, LanguageZH,
	}
}

// knownSet backs Valid; built once from KnownLanguages.
var knownSet = func() map[Language]bool {
	langs := KnownLanguages()
	m := make(map[Language]bool, len(langs))
	for _, l := range langs {
		m[l] = true
	}
	return m
}()

// Valid reports whether l is a known language code (one of the constants above).
// This checks the static known set, NOT whether the UI can render it — use
// (*Translator).HasLanguage for the renderable subset.
func (l Language) Valid() bool { return knownSet[l] }
