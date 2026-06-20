// Package uuidv7 generates application-side UUIDv7 identifiers. New tables use
// app-supplied v7 ids (no DB DEFAULT), so every INSERT must provide one — this
// is the single source of that id across modules.
package uuidv7

import (
	"fmt"

	"github.com/google/uuid"
)

// NewUUID returns a fresh UUIDv7. v7 embeds a timestamp, so ids sort in creation
// order — friendlier for indexes and pagination than random v4. Prefer this when
// the caller wants a typed uuid.UUID (e.g. to return an inserted row's id); use
// New for the string form.
func NewUUID() (uuid.UUID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("uuidv7: generate: %w", err)
	}
	return id, nil
}

// New returns a fresh UUIDv7 as a string. v7 embeds a timestamp, so ids sort in
// creation order — friendlier for indexes and pagination than random v4.
func New() (string, error) {
	id, err := NewUUID()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}
