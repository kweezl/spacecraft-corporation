package token_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	mycrypto "github.com/kweezl/spacecraft-cadet/internal/crypto"
	"github.com/kweezl/spacecraft-cadet/internal/migrator"
	"github.com/kweezl/spacecraft-cadet/internal/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestListEnabled_DecryptsTokens(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	_, _ = pool.Exec(ctx, `DROP TABLE IF EXISTS ping_log, bot_tokens, goose_db_version`)
	require.NoError(t, migrator.Run(pool, zap.NewNop()))

	key := make([]byte, 32)
	_, _ = rand.Read(key)
	cipher, err := mycrypto.NewCipher(base64.StdEncoding.EncodeToString(key))
	require.NoError(t, err)

	encEnabled, _ := cipher.Encrypt("enabled-token")
	encDisabled, _ := cipher.Encrypt("disabled-token")
	_, err = pool.Exec(ctx,
		`INSERT INTO bot_tokens (guild_id, token, enabled) VALUES ($1,$2,true),($3,$4,false)`,
		"g1", encEnabled, "g2", encDisabled)
	require.NoError(t, err)

	repo := token.NewRepositoryForTest(pool, cipher)

	got, err := repo.ListEnabled(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "g1", got[0].GuildID)
	assert.Equal(t, "enabled-token", got[0].Token)
}
