package cache

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"
)

type Cache struct {
	mu    sync.RWMutex
	items map[string]json.RawMessage
}

func New() *Cache {
	return &Cache{items: make(map[string]json.RawMessage)}
}

// Store saves content and returns a new random id.
func (c *Cache) Store(content json.RawMessage) string {
	id := newID()
	c.mu.Lock()
	c.items[id] = content
	c.mu.Unlock()
	return id
}

func (c *Cache) Get(id string) (json.RawMessage, bool) {
	c.mu.RLock()
	v, ok := c.items[id]
	c.mu.RUnlock()
	return v, ok
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
