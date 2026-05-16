package stats

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

// Totals is the on-disk accumulated record for one config key.
type Totals struct {
	BytesCached     int64 `json:"bytes_cached"`
	BytesSent       int64 `json:"bytes_sent"`
	ResponsesCached int64 `json:"responses_cached"`
	SeekCalls       int64 `json:"seek_calls"`
}

// BytesSaved returns bytes intercepted minus bytes sent as stubs.
func (t Totals) BytesSaved() int64 {
	saved := t.BytesCached - t.BytesSent
	if saved < 0 {
		return 0
	}
	return saved
}

// EstimatedTokensSaved returns a rough token estimate (bytes / 4).
func (t Totals) EstimatedTokensSaved() int64 {
	return t.BytesSaved() / 4
}

// Stats accumulates per-session counters and merges them into a shared file on
// each update. A nil *Stats is safe everywhere — all methods are no-ops.
type Stats struct {
	path string

	bytesCached     atomic.Int64
	bytesSent       atomic.Int64
	responsesCached atomic.Int64
	seekCalls       atomic.Int64

	// flushMu serialises read-modify-write against the on-disk file so
	// the per-process delta is added to the existing totals exactly once.
	flushMu sync.Mutex
}

// New creates a Stats tracker backed by path (a per-config stats file).
// The directory is created if it does not exist.
func New(path string) (*Stats, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("stats: create dir: %w", err)
	}
	return &Stats{path: path}, nil
}

// RecordCache records one cached response. bytesCached is the intercepted
// content size; bytesSent is the stub message size sent to the client.
func (s *Stats) RecordCache(bytesCached, bytesSent int64) {
	if s == nil {
		return
	}
	s.bytesCached.Add(bytesCached)
	s.bytesSent.Add(bytesSent)
	s.responsesCached.Add(1)
	s.flush() //nolint:errcheck
}

// RecordSeek records one seek_result call.
func (s *Stats) RecordSeek() {
	if s == nil {
		return
	}
	s.seekCalls.Add(1)
	s.flush() //nolint:errcheck
}

// Save flushes the current session delta to disk. Call on process exit.
func (s *Stats) Save() error {
	if s == nil {
		return nil
	}
	return s.flush()
}

// flush reads the existing totals, adds the unflushed delta, and writes back
// atomically. Resets the in-memory counters so the next flush does not
// re-add the same delta. Multiple processes pointed at the same file may
// still race on the rename — one write wins — so cross-process counts are
// approximate, but the file is never corrupted (atomic rename guarantees
// a complete write or none).
func (s *Stats) flush() error {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()

	bc := s.bytesCached.Swap(0)
	bs := s.bytesSent.Swap(0)
	rc := s.responsesCached.Swap(0)
	sk := s.seekCalls.Swap(0)
	if bc == 0 && bs == 0 && rc == 0 && sk == 0 {
		return nil
	}

	existing := readTotals(s.path)
	merged := Totals{
		BytesCached:     existing.BytesCached + bc,
		BytesSent:       existing.BytesSent + bs,
		ResponsesCached: existing.ResponsesCached + rc,
		SeekCalls:       existing.SeekCalls + sk,
	}
	return writeTotals(s.path, merged)
}

func readTotals(path string) Totals {
	data, err := os.ReadFile(path)
	if err != nil {
		return Totals{}
	}
	var t Totals
	json.Unmarshal(data, &t) //nolint:errcheck
	return t
}

func writeTotals(path string, t Totals) error {
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// DefaultDir returns the directory that holds all stats files.
func DefaultDir() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		cacheDir = os.TempDir()
	}
	return filepath.Join(cacheDir, "mcp-context-guard")
}

// PathForKey derives a stats file path from a config key (upstream URL or
// command string). The key is SHA-256 hashed so names are short and safe.
func PathForKey(key string) string {
	h := sha256.Sum256([]byte(key))
	name := "stats-" + hex.EncodeToString(h[:8]) + ".json"
	return filepath.Join(DefaultDir(), name)
}

// Load reads the totals for a single config key file.
func Load(path string) Totals {
	return readTotals(path)
}

// LoadAll reads every stats-*.json file in dir and returns the aggregate.
func LoadAll(dir string) Totals {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Totals{}
	}
	var total Totals
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "stats-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		t := readTotals(filepath.Join(dir, name))
		total.BytesCached += t.BytesCached
		total.BytesSent += t.BytesSent
		total.ResponsesCached += t.ResponsesCached
		total.SeekCalls += t.SeekCalls
	}
	return total
}

// Format returns a human-readable one-line summary.
func Format(t Totals) string {
	if t.ResponsesCached == 0 && t.SeekCalls == 0 {
		return "No stats recorded yet."
	}
	return fmt.Sprintf(
		"responses cached: %d | intercepted: %s | saved: %s (~%d tokens) | seek_result calls: %d",
		t.ResponsesCached,
		formatBytes(t.BytesCached), formatBytes(t.BytesSaved()),
		t.EstimatedTokensSaved(),
		t.SeekCalls,
	)
}

func formatBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
