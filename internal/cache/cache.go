package cache

import (
	"container/list"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
)

// DefaultMaxBytes is the per-cache size budget when New is called.
// LRU eviction kicks in once the total cached content exceeds this.
const DefaultMaxBytes int64 = 256 << 20 // 256 MiB

type entry struct {
	id      string
	content json.RawMessage
}

// Cache is a bounded, in-memory LRU cache of MCP content payloads.
// All methods are safe for concurrent use. The capacity is a soft bound
// on the sum of entry payload sizes; per-entry overhead is not counted.
type Cache struct {
	mu       sync.Mutex
	items    map[string]*list.Element // id → list element (entry)
	lru      *list.List               // front = most recent, back = least recent
	maxBytes int64
	curBytes int64
}

// New returns a cache bounded by DefaultMaxBytes.
func New() *Cache {
	return NewWithCapacity(DefaultMaxBytes)
}

// NewWithCapacity returns a cache bounded by maxBytes. Values <= 0 fall back
// to DefaultMaxBytes.
func NewWithCapacity(maxBytes int64) *Cache {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	return &Cache{
		items:    make(map[string]*list.Element),
		lru:      list.New(),
		maxBytes: maxBytes,
	}
}

// Store saves content and returns a new random id. Returns an error if the
// system entropy source fails — callers must fail the request rather than
// degrade to a predictable id.
//
// If the new entry would exceed the byte budget, older entries are evicted
// LRU-first. A single entry larger than the entire budget is still admitted
// (eviction empties the cache); rejecting it would silently break filters.
func (c *Cache) Store(content json.RawMessage) (string, error) {
	id, err := newID()
	if err != nil {
		return "", err
	}
	size := int64(len(content))

	c.mu.Lock()
	defer c.mu.Unlock()

	for c.curBytes+size > c.maxBytes && c.lru.Len() > 0 {
		c.evictOldestLocked()
	}

	el := c.lru.PushFront(&entry{id: id, content: content})
	c.items[id] = el
	c.curBytes += size
	return id, nil
}

// Get returns the cached content and marks the entry as most-recently-used.
func (c *Cache) Get(id string) (json.RawMessage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[id]
	if !ok {
		return nil, false
	}
	c.lru.MoveToFront(el)
	return el.Value.(*entry).content, true
}

// Len returns the current number of cached entries (for tests/metrics).
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lru.Len()
}

// Bytes returns the current size of cached content (for tests/metrics).
func (c *Cache) Bytes() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.curBytes
}

func (c *Cache) evictOldestLocked() {
	el := c.lru.Back()
	if el == nil {
		return
	}
	e := el.Value.(*entry)
	c.lru.Remove(el)
	delete(c.items, e.id)
	c.curBytes -= int64(len(e.content))
}

func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("cache: read entropy: %w", err)
	}
	return hex.EncodeToString(b), nil
}
