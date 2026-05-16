package filter

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestApplyFullWithMixedBlocks(t *testing.T) {
	in, _ := json.Marshal([]any{
		map[string]string{"type": "text", "text": "hello"},
		map[string]string{"type": "image", "data": "ignored"},
		map[string]string{"type": "text", "text": "world"},
	})
	got, err := Apply(in, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello\nworld" {
		t.Fatalf("got %q", got)
	}
}

func TestJQOnNonJSONText(t *testing.T) {
	// When the text body isn't JSON, jq runs against the raw content array
	// (list of content blocks), so `.[0].text` should still work.
	in, _ := json.Marshal([]map[string]string{{"type": "text", "text": "not json"}})
	got, err := Apply(in, `.[0].text`)
	if err != nil {
		t.Fatal(err)
	}
	if got != `"not json"` {
		t.Fatalf("got %q", got)
	}
}

func TestJQEmptyContentArray(t *testing.T) {
	got, err := Apply(json.RawMessage(`[]`), `length`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "0" {
		t.Fatalf("got %q", got)
	}
}

func TestJQRespectsResultLimit(t *testing.T) {
	old := MaxJQResults
	MaxJQResults = 100
	defer func() { MaxJQResults = old }()

	in, _ := json.Marshal([]map[string]string{{"type": "text", "text": "[1,2,3]"}})
	_, err := Apply(in, `range(10000)`)
	if err == nil || !strings.Contains(err.Error(), "too many results") {
		t.Fatalf("expected too-many-results error, got %v", err)
	}
}

func TestJQRespectsDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	in, _ := json.Marshal([]map[string]string{{"type": "text", "text": "[]"}})
	// `until(false; .+1)` loops forever; the ctx should cut it off.
	_, err := ApplyContext(ctx, in, `0 | until(false; .+1)`)
	if err == nil {
		t.Fatal("expected deadline error")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "context") {
		t.Fatalf("expected context error, got %v", err)
	}
}

func TestGrepRespectsDeadline(t *testing.T) {
	// Generate enough lines that the periodic ctx check trips.
	var sb strings.Builder
	for i := 0; i < 50_000; i++ {
		sb.WriteString("line\n")
	}
	in, _ := json.Marshal([]map[string]string{{"type": "text", "text": sb.String()}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled
	_, err := ApplyContext(ctx, in, `grep:line`)
	if err == nil {
		t.Fatal("expected cancellation")
	}
}

func FuzzApply(f *testing.F) {
	seed := func(content, expr string) {
		f.Add([]byte(content), expr)
	}
	seed(`[{"type":"text","text":"hi"}]`, "")
	seed(`[{"type":"text","text":"a\nb"}]`, "grep:a")
	seed(`[{"type":"text","text":"a\nb"}]`, "head:1")
	seed(`[{"type":"text","text":"[1,2]"}]`, ".[0]")
	seed(`[]`, "length")
	seed(`null`, "")
	f.Fuzz(func(t *testing.T, content []byte, expr string) {
		// Must not panic on any input.
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_, _ = ApplyContext(ctx, json.RawMessage(content), expr)
	})
}
