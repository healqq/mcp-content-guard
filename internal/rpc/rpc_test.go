package rpc

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMessagePredicates(t *testing.T) {
	cases := []struct {
		name                 string
		raw                  string
		req, notify, resp    bool
	}{
		{"request", `{"jsonrpc":"2.0","id":1,"method":"x"}`, true, false, false},
		{"notification", `{"jsonrpc":"2.0","method":"x"}`, false, true, false},
		{"response", `{"jsonrpc":"2.0","id":1,"result":{}}`, false, false, true},
		{"error response", `{"jsonrpc":"2.0","id":1,"error":{}}`, false, false, true},
		{"empty", `{}`, false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var m Message
			if err := json.Unmarshal([]byte(c.raw), &m); err != nil {
				t.Fatal(err)
			}
			if got := m.IsRequest(); got != c.req {
				t.Errorf("IsRequest=%v want %v", got, c.req)
			}
			if got := m.IsNotification(); got != c.notify {
				t.Errorf("IsNotification=%v want %v", got, c.notify)
			}
			if got := m.IsResponse(); got != c.resp {
				t.Errorf("IsResponse=%v want %v", got, c.resp)
			}
		})
	}
}

func TestEncodeTextResult(t *testing.T) {
	b := EncodeTextResult(7, "hello")
	var parsed struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.JSONRPC != "2.0" || parsed.ID != 7 {
		t.Fatalf("envelope wrong: %s", b)
	}
	if len(parsed.Result.Content) != 1 || parsed.Result.Content[0].Text != "hello" {
		t.Fatalf("payload wrong: %s", b)
	}
}

func TestEncodeErrorResultSetsIsError(t *testing.T) {
	b := EncodeErrorResult("abc", "nope")
	if !strings.Contains(string(b), `"isError":true`) {
		t.Fatalf("isError flag missing: %s", b)
	}
	if !strings.Contains(string(b), `"nope"`) {
		t.Fatalf("error text missing: %s", b)
	}
}
