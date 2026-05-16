package cache

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

func TestStoreGetRoundtrip(t *testing.T) {
	c := New()
	id, err := c.Store(json.RawMessage(`{"hello":"world"}`))
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	got, ok := c.Get(id)
	if !ok {
		t.Fatalf("Get %q: missing", id)
	}
	if string(got) != `{"hello":"world"}` {
		t.Fatalf("payload mismatch: %s", got)
	}
}

func TestStoreIDsAreUnique(t *testing.T) {
	c := NewWithCapacity(1 << 30) // large; we want N entries to coexist
	const N = 10_000
	seen := make(map[string]struct{}, N)
	for i := 0; i < N; i++ {
		id, err := c.Store(json.RawMessage(fmt.Sprintf(`"%d"`, i)))
		if err != nil {
			t.Fatalf("Store %d: %v", i, err)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate id %q at iteration %d", id, i)
		}
		seen[id] = struct{}{}
	}
}

func TestLRUEvictsOldest(t *testing.T) {
	// Capacity room for 2 entries of size 10.
	c := NewWithCapacity(25)
	a, _ := c.Store(json.RawMessage(`"aaaaaaaa"`)) // 10 bytes JSON
	b, _ := c.Store(json.RawMessage(`"bbbbbbbb"`))
	// touch a so b is now the LRU
	if _, ok := c.Get(a); !ok {
		t.Fatal("a missing before eviction")
	}
	cID, _ := c.Store(json.RawMessage(`"cccccccc"`)) // forces eviction of b
	if _, ok := c.Get(b); ok {
		t.Fatal("b should have been evicted (LRU)")
	}
	if _, ok := c.Get(a); !ok {
		t.Fatal("a should still be present (recently used)")
	}
	if _, ok := c.Get(cID); !ok {
		t.Fatal("c should be present")
	}
}

func TestStoreAdmitsOversizedPayload(t *testing.T) {
	// A single payload larger than the entire budget should still be admitted,
	// emptying the cache rather than failing (rejection would silently break
	// seek_result for that response).
	c := NewWithCapacity(8)
	if _, err := c.Store(json.RawMessage(`"small"`)); err != nil {
		t.Fatal(err)
	}
	big := json.RawMessage(`"` + string(make([]byte, 100)) + `"`)
	id, err := c.Store(big)
	if err != nil {
		t.Fatalf("oversized Store: %v", err)
	}
	if _, ok := c.Get(id); !ok {
		t.Fatal("oversized entry should be retrievable")
	}
	if c.Len() != 1 {
		t.Fatalf("cache should hold only the oversized entry, got %d entries", c.Len())
	}
}

func TestConcurrentStoreGet(t *testing.T) {
	c := NewWithCapacity(1 << 30)
	const workers = 16
	const perWorker = 500
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				id, err := c.Store(json.RawMessage(fmt.Sprintf(`"%d-%d"`, w, i)))
				if err != nil {
					t.Errorf("Store: %v", err)
					return
				}
				if _, ok := c.Get(id); !ok {
					t.Errorf("Get %q missing", id)
					return
				}
			}
		}(w)
	}
	wg.Wait()
}
