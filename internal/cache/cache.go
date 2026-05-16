package cache

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
)

type Cache struct {
	mu    sync.RWMutex
	items map[string]json.RawMessage
}

func New() *Cache {
	return &Cache{items: make(map[string]json.RawMessage)}
}

// Store saves content and returns a new random id. Returns an error if the
// system entropy source fails — callers must fail the request rather than
// degrade to a predictable id (which would let any client read any payload).
func (c *Cache) Store(content json.RawMessage) (string, error) {
	id, err := newID()
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	c.items[id] = content
	c.mu.Unlock()
	return id, nil
}

func (c *Cache) Get(id string) (json.RawMessage, bool) {
	c.mu.RLock()
	v, ok := c.items[id]
	c.mu.RUnlock()
	return v, ok
}

func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("cache: read entropy: %w", err)
	}
	return hex.EncodeToString(b), nil
}
