package stats

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestRecordCachePersists(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "stats.json")
	s, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	s.RecordCache(1000, 100)
	s.RecordCache(2000, 200)
	s.RecordSeek()
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	got := Load(p)
	if got.BytesCached != 3000 || got.BytesSent != 300 || got.ResponsesCached != 2 || got.SeekCalls != 1 {
		t.Fatalf("totals wrong: %+v", got)
	}
	if got.BytesSaved() != 2700 {
		t.Fatalf("BytesSaved=%d want 2700", got.BytesSaved())
	}
}

func TestNilStatsIsSafe(t *testing.T) {
	var s *Stats
	s.RecordCache(1, 1) // must not panic
	s.RecordSeek()
	if err := s.Save(); err != nil {
		t.Fatalf("nil Save: %v", err)
	}
}

func TestConcurrentRecord(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "stats.json")
	s, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 8
	const perWorker = 200
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				s.RecordCache(10, 1)
			}
		}()
	}
	wg.Wait()
	_ = s.Save()

	got := Load(p)
	// In-process counters are exact (atomics). The file may have skipped some
	// flush states due to read-modify-write races against itself, but the
	// final Save reflects the full session delta on top of an existing read.
	if got.ResponsesCached < int64(workers*perWorker) {
		t.Fatalf("expected at least %d responsesCached, got %d", workers*perWorker, got.ResponsesCached)
	}
}

func TestPathForKeyIsDeterministicAndSafe(t *testing.T) {
	a := PathForKey("local:python mock.py")
	b := PathForKey("local:python mock.py")
	c := PathForKey("local:python other.py")
	if a != b {
		t.Fatalf("same key produced different paths: %s vs %s", a, b)
	}
	if a == c {
		t.Fatalf("different keys produced same path: %s", a)
	}
	// no path separators or shell metachars in the basename
	base := filepath.Base(a)
	for _, ch := range base {
		if ch == '/' || ch == '\\' || ch == '*' || ch == '?' {
			t.Fatalf("unsafe char %q in basename %q", ch, base)
		}
	}
}
