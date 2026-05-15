package filter

import (
	"encoding/json"
	"testing"
)

// makeContent builds a JSON content array with a single text block.
func makeContent(text string) json.RawMessage {
	b, _ := json.Marshal([]map[string]string{{"type": "text", "text": text}})
	return b
}

func TestHead(t *testing.T) {
	content := makeContent("a\nb\nc\nd\ne")
	got, err := Apply(content, "head:3")
	if err != nil {
		t.Fatal(err)
	}
	if got != "a\nb\nc" {
		t.Errorf("head:3 = %q, want %q", got, "a\nb\nc")
	}
}

func TestHeadExceedsLength(t *testing.T) {
	content := makeContent("a\nb")
	got, err := Apply(content, "head:100")
	if err != nil {
		t.Fatal(err)
	}
	if got != "a\nb" {
		t.Errorf("head:100 on 2-line content = %q, want %q", got, "a\nb")
	}
}

func TestTail(t *testing.T) {
	content := makeContent("a\nb\nc\nd\ne")
	got, err := Apply(content, "tail:3")
	if err != nil {
		t.Fatal(err)
	}
	if got != "c\nd\ne" {
		t.Errorf("tail:3 = %q, want %q", got, "c\nd\ne")
	}
}

func TestTailExceedsLength(t *testing.T) {
	content := makeContent("a\nb")
	got, err := Apply(content, "tail:100")
	if err != nil {
		t.Fatal(err)
	}
	if got != "a\nb" {
		t.Errorf("tail:100 on 2-line content = %q, want %q", got, "a\nb")
	}
}

func TestLine(t *testing.T) {
	content := makeContent("alpha\nbeta\ngamma")
	got, err := Apply(content, "line:1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "beta" {
		t.Errorf("line:1 = %q, want %q", got, "beta")
	}
}

func TestLineOutOfRange(t *testing.T) {
	content := makeContent("alpha\nbeta")
	_, err := Apply(content, "line:5")
	if err == nil {
		t.Error("expected error for out-of-range line index, got nil")
	}
}

func TestLineInvalidN(t *testing.T) {
	content := makeContent("alpha")
	_, err := Apply(content, "line:abc")
	if err == nil {
		t.Error("expected error for non-integer line index, got nil")
	}
}

func TestHeadZero(t *testing.T) {
	content := makeContent("a\nb\nc")
	got, err := Apply(content, "head:0")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("head:0 = %q, want empty string", got)
	}
}

func TestMultiBlockTail(t *testing.T) {
	// two text blocks; tail should span across them
	b, _ := json.Marshal([]map[string]string{
		{"type": "text", "text": "a\nb"},
		{"type": "text", "text": "c\nd"},
	})
	got, err := Apply(b, "tail:2")
	if err != nil {
		t.Fatal(err)
	}
	if got != "c\nd" {
		t.Errorf("tail:2 across two blocks = %q, want %q", got, "c\nd")
	}
}

func TestEmptyFilterReturnsFull(t *testing.T) {
	content := makeContent("hello\nworld")
	got, err := Apply(content, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello\nworld" {
		t.Errorf("empty filter = %q, want %q", got, "hello\nworld")
	}
}

func TestEmptyFilterMultiBlock(t *testing.T) {
	b, _ := json.Marshal([]map[string]string{
		{"type": "text", "text": "foo"},
		{"type": "text", "text": "bar"},
	})
	got, err := Apply(b, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "foo\nbar" {
		t.Errorf("empty filter multi-block = %q, want %q", got, "foo\nbar")
	}
}

func TestJQInvalidExpression(t *testing.T) {
	content := makeContent(`{"a":1}`)
	_, err := Apply(content, ".a ][[ bad")
	if err == nil {
		t.Error("expected error for invalid jq expression, got nil")
	}
}

func TestJQShellSyntaxStripped(t *testing.T) {
	b, _ := json.Marshal([]map[string]string{{"type": "text", "text": `[1,2,3]`}})
	got, err := Apply(b, "jq '.[-1]'")
	if err != nil {
		t.Fatal(err)
	}
	if got != "3" {
		t.Errorf("jq shell syntax: got %q, want %q", got, "3")
	}
}
