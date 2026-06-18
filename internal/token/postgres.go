package token

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kweezl/spacecraft-cadet/internal/crypto"
)

type pgRepository struct {
	pool   *pgxpool.Pool
	cipher *crypto.Cipher
}

func (r *pgRepository) ListEnabled(ctx context.Context) ([]Token, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT guild_id, token FROM bot_tokens WHERE enabled = true ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Token
	for rows.Next() {
		var guildID, enc string
		if err := rows.Scan(&guildID, &enc); err != nil {
			return nil, err
		}
		plain, err := r.cipher.Decrypt(enc)
		if err != nil {
			return nil, fmt.Errorf("decrypt token for guild %s: %w", guildID, err)
		}
		out = append(out, Token{GuildID: guildID, Token: plain})
	}
	return out, rows.Err()
}
