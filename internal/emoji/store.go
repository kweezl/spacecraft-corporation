// Package emoji provides fast, name-keyed access to the bot's custom emojis and
// an optional startup sync that uploads repo-bundled images as application
// emojis. Application emojis belong to the bot's application (not a guild), so a
// single upload makes them usable in every server the bot is in.
//
// Handlers and templates never deal with emoji ids: they look an emoji up by its
// stable name through the Store, which returns the ready-to-send message token
// (e.g. "<:iron_bar:1234567890>"). The Syncer populates the Store at startup and
// contributes a readiness probe so /readyz stays red until the sync completes.
package emoji

import "sync"

// Store is the fast-access, name→token map of the bot's emojis. It is written
// once by the Syncer at startup and read concurrently by handlers, so all access
// is guarded. The token is an emoji's Discord message format ("<:name:id>" or
// "<a:name:id>" for animated), ready to drop straight into message content.
type Store struct {
	mu     sync.RWMutex
	byName map[string]string
}

func newStore() *Store { return &Store{byName: map[string]string{}} }

// replace swaps in a freshly synced name→token map.
func (s *Store) replace(byName map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byName = byName
}

// Format returns the message token for the named emoji and whether the bot has
// an emoji by that name. The token is "" when ok is false, so callers that don't
// care about presence can ignore ok and render the empty string.
func (s *Store) Format(name string) (token string, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	token, ok = s.byName[name]
	return token, ok
}
