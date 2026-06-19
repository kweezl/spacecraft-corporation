// Package uuidv7 generates application-side UUIDv7 identifiers. New tables use
// app-supplied v7 ids (no DB DEFAULT), so every INSERT must provide one — this
// is the single source of that id across modules.
package uuidv7

import (
	"fmt"

	"github.com/google/uuid"
)

// New returns a fresh UUIDv7 as a string. v7 embeds a timestamp, so ids sort in
// creation order — friendlier for indexes and pagination than random v4.
func New() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("uuidv7: generate: %w", err)
	}
	return id.String(), nil
}
