package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mcp-context-guard/internal/cache"
	"mcp-context-guard/internal/httpserver"
)

func TestRequestBodyCapReturns413(t *testing.T) {
	upstream := httptest.NewServer(mcpUpstream(false))
	defer upstream.Close()

	srv, err := httpserver.New(
		upstream.URL+"/mcp",
		nil,
		cache.New(),
		100,
		nil,
		&httpserver.Config{MaxBodyBytes: 1024},
	)
	if err != nil {
		t.Fatal(err)
	}

	body := strings.Repeat("a", 2048)
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
}

func TestUpstreamHeaderTimeoutFallsBackToDefault(t *testing.T) {
	// Sanity: New must accept a nil Config and a Config with zero fields.
	upstream := httptest.NewServer(mcpUpstream(false))
	defer upstream.Close()

	if _, err := httpserver.New(upstream.URL+"/mcp", nil, cache.New(), 100, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := httpserver.New(upstream.URL+"/mcp", nil, cache.New(), 100, nil, &httpserver.Config{}); err != nil {
		t.Fatal(err)
	}
}
