package filter

import (
	"encoding/json"
	"testing"
)

func TestJQUnwrapsContentBlocks(t *testing.T) {
	products := `[{"id":"PROD-0001","price":9.99},{"id":"PROD-0142","price":199.99},{"id":"PROD-0050","price":50.0}]`
	content, _ := json.Marshal([]map[string]string{{"type": "text", "text": products}})
	result, err := Apply(content, `max_by(.price) | "\(.id) at $\(.price)"`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != `"PROD-0142 at $199.99"` {
		t.Fatalf("unexpected result: %q", result)
	}
}
