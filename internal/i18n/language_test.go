package i18n_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kweezl/spacecraft-corporation/internal/i18n"
)

func TestLanguageValid(t *testing.T) {
	for _, l := range i18n.KnownLanguages() {
		assert.Truef(t, l.Valid(), "%q should be valid", l)
	}
	assert.True(t, i18n.LanguagePTBR.Valid())
	assert.False(t, i18n.Language("xx").Valid(), "unknown code is invalid")
	assert.False(t, i18n.Language("EN").Valid(), "wrong case is not a known code")
	assert.False(t, i18n.Language("").Valid())
}

func TestKnownLanguages(t *testing.T) {
	known := i18n.KnownLanguages()
	assert.Len(t, known, 8)
	assert.Equal(t, i18n.LanguageEN, known[0], "base language is first")
	assert.Contains(t, known, i18n.LanguageZH)
}
