// Package token loads enabled, decrypted bot tokens from Postgres for the
// session manager.
package token

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kweezl/spacecraft-cadet/internal/crypto"
	"go.uber.org/fx"
)

// Token is a decrypted bot token for one tenant guild.
type Token struct {
	GuildID string
	Token   string
}

// Repository loads enabled tokens.
type Repository interface {
	ListEnabled(ctx context.Context) ([]Token, error)
}

func newRepository(pool *pgxpool.Pool, cipher *crypto.Cipher) Repository {
	return &pgRepository{pool: pool, cipher: cipher}
}

// NewRepositoryForTest exposes the repository constructor for integration tests.
func NewRepositoryForTest(pool *pgxpool.Pool, cipher *crypto.Cipher) Repository {
	return newRepository(pool, cipher)
}

// Module provides the token Repository.
var Module = fx.Module("token", fx.Provide(newRepository))
