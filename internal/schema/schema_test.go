package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInjectAppendsToExistingTools(t *testing.T) {
	in := json.RawMessage(`{"tools":[{"name":"foo"}]}`)
	out, err := InjectSeekResult(in)
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Tools) != 2 || r.Tools[0].Name != "foo" || r.Tools[1].Name != "seek_result" {
		t.Fatalf("unexpected tools: %+v", r.Tools)
	}
}

func TestInjectHandlesMissingToolsField(t *testing.T) {
	in := json.RawMessage(`{}`)
	out, err := InjectSeekResult(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"seek_result"`) {
		t.Fatalf("seek_result not injected: %s", out)
	}
}

func TestInjectFailsOnNonObjectResult(t *testing.T) {
	if _, err := InjectSeekResult(json.RawMessage(`[1,2,3]`)); err == nil {
		t.Fatal("expected error for non-object result")
	}
}

func TestInjectFailsOnNonArrayTools(t *testing.T) {
	if _, err := InjectSeekResult(json.RawMessage(`{"tools":"oops"}`)); err == nil {
		t.Fatal("expected error for non-array tools field")
	}
}

func TestInjectIsIdempotentInName(t *testing.T) {
	// Calling twice produces two seek_result entries — documents current
	// behaviour; if we ever dedupe, this test catches the regression.
	once, _ := InjectSeekResult(json.RawMessage(`{"tools":[]}`))
	twice, _ := InjectSeekResult(once)
	if strings.Count(string(twice), `"seek_result"`) != 2 {
		t.Fatalf("expected 2 seek_result entries, got: %s", twice)
	}
}

func FuzzInjectSeekResult(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"tools":[]}`))
	f.Add([]byte(`{"tools":[{"name":"x"}]}`))
	f.Add([]byte(`[1,2,3]`))
	f.Add([]byte(`null`))
	f.Add([]byte(`{"tools":42}`))
	f.Fuzz(func(t *testing.T, in []byte) {
		// Must not panic on any input. Errors are fine.
		_, _ = InjectSeekResult(json.RawMessage(in))
	})
}
