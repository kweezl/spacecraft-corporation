package db

import (
	"net/url"
	"testing"

	"github.com/caarlos0/env/v11"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_DSN_BuildsFromParts(t *testing.T) {
	dsn := Config{
		Host: "db", Port: 5432, User: "u", Password: "p", DB: "app", SSLMode: "disable",
	}.DSN()
	assert.Equal(t, "postgres://u:p@db:5432/app?sslmode=disable", dsn)
}

func TestConfig_DSN_PrefersPasswordFile(t *testing.T) {
	// PasswordFile holds the file's contents (env resolves the ,file option);
	// it wins over Password and is trimmed of a trailing newline.
	dsn := Config{
		Host: "db", Port: 5432, User: "u", Password: "from-env", PasswordFile: "from-file\n", DB: "app", SSLMode: "disable",
	}.DSN()
	assert.Equal(t, "postgres://u:from-file@db:5432/app?sslmode=disable", dsn)
}

func TestConfig_DSN_EncodesCredentials(t *testing.T) {
	// Special characters in the password must survive a round-trip.
	dsn := Config{
		Host: "db", Port: 5432, User: "u", Password: "p@ss:w/rd?", DB: "app", SSLMode: "disable",
	}.DSN()

	u, err := url.Parse(dsn)
	require.NoError(t, err)
	pw, _ := u.User.Password()
	assert.Equal(t, "p@ss:w/rd?", pw)
	assert.Equal(t, "app", u.Path[1:])
}

func TestConfig_DSN_Defaults(t *testing.T) {
	// Empty environment => the envDefaults yield the local DSN. Isolated from the
	// ambient env so it's deterministic regardless of any POSTGRES_* exported.
	cfg, err := env.ParseAsWithOptions[Config](env.Options{Environment: map[string]string{}})
	require.NoError(t, err)
	assert.Equal(t, "postgres://bot:bot@localhost:5432/spacecraft?sslmode=disable", cfg.DSN())
}

func TestConfig_DSN_FromEnv(t *testing.T) {
	cfg, err := env.ParseAsWithOptions[Config](env.Options{Environment: map[string]string{
		"POSTGRES_HOST":     "h",
		"POSTGRES_PORT":     "6543",
		"POSTGRES_USER":     "u",
		"POSTGRES_PASSWORD": "p",
		"POSTGRES_DB":       "d",
		"POSTGRES_SSLMODE":  "require",
	}})
	require.NoError(t, err)
	assert.Equal(t, "postgres://u:p@h:6543/d?sslmode=require", cfg.DSN())
}
