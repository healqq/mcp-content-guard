package httpserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mcp-context-guard/internal/cache"
	"mcp-context-guard/internal/httpserver"
)

func newServer(t *testing.T, upstream *httptest.Server, threshold int) http.Handler {
	t.Helper()
	srv, err := httpserver.New(upstream.URL+"/mcp", nil, cache.New(), int64(threshold), nil)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func post(t *testing.T, srv http.Handler, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	resp := w.Result()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func get(t *testing.T, srv http.Handler, path string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

func mcpUpstream(sseForDump bool) http.Handler {
	large := strings.Repeat("x", 200)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" {
			// transparent for other paths
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"path":%q}`, r.URL.Path)
			return
		}
		var msg map[string]any
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		method, _ := msg["method"].(string)
		id := msg["id"]
		respond := func(result any) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
		}
		switch method {
		case "initialize":
			respond(map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "mock", "version": "1"}})
		case "tools/list":
			respond(map[string]any{"tools": []any{
				map[string]any{"name": "echo", "description": "small", "inputSchema": map[string]any{"type": "object"}},
				map[string]any{"name": "dump", "description": "large", "inputSchema": map[string]any{"type": "object"}},
			}})
		case "tools/call":
			params, _ := msg["params"].(map[string]any)
			name, _ := params["name"].(string)
			switch name {
			case "echo":
				respond(map[string]any{"content": []any{map[string]any{"type": "text", "text": "hello"}}})
			case "dump":
				result := map[string]any{"content": []any{map[string]any{"type": "text", "text": large}}}
				if sseForDump {
					b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: %s\n\n", b)
				} else {
					respond(result)
				}
			}
		}
	})
}

func TestHTTPToolsListInjectsSeekResult(t *testing.T) {
	upstream := httptest.NewServer(mcpUpstream(false))
	defer upstream.Close()
	srv := newServer(t, upstream, 100)

	_, resp := post(t, srv, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	result := resp["result"].(map[string]any)
	tools := result["tools"].([]any)
	var names []string
	for _, tool := range tools {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	found := false
	for _, n := range names {
		if n == "seek_result" {
			found = true
		}
	}
	if !found {
		t.Errorf("seek_result not injected; tools: %v", names)
	}
}

func TestHTTPSmallResponsePassthrough(t *testing.T) {
	upstream := httptest.NewServer(mcpUpstream(false))
	defer upstream.Close()
	srv := newServer(t, upstream, 100)

	_, resp := post(t, srv, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{}}}`)
	result := resp["result"].(map[string]any)
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	if text != "hello" {
		t.Errorf("expected 'hello', got %q", text)
	}
}

func TestHTTPLargeResponseCached(t *testing.T) {
	upstream := httptest.NewServer(mcpUpstream(false))
	defer upstream.Close()
	srv := newServer(t, upstream, 100)

	_, resp := post(t, srv, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dump","arguments":{}}}`)
	result := resp["result"].(map[string]any)
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "bytes cached") {
		t.Errorf("expected cache stub, got: %q", text)
	}
}

func TestHTTPSeekResult(t *testing.T) {
	upstream := httptest.NewServer(mcpUpstream(false))
	defer upstream.Close()

	c := cache.New()
	srv, _ := httpserver.New(upstream.URL+"/mcp", nil, c, 100, nil)

	// trigger caching
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"dump","arguments":{}}}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	var first map[string]any
	_ = json.NewDecoder(w.Body).Decode(&first)
	stub := first["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)

	// extract cache id
	var cacheID string
	for _, part := range strings.Split(stub, `"`) {
		if len(part) == 32 { // hex id is 32 chars
			cacheID = part
			break
		}
	}
	if cacheID == "" {
		// fallback: find id= substring
		idx := strings.Index(stub, `id="`)
		if idx < 0 {
			t.Fatalf("no cache id in stub: %q", stub)
		}
		end := strings.Index(stub[idx+4:], `"`)
		cacheID = stub[idx+4 : idx+4+end]
	}

	_, seekResp := post(t, srv, "/mcp", fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"seek_result","arguments":{"id":%q}}}`, cacheID))
	text := seekResp["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, strings.Repeat("x", 200)) {
		t.Errorf("seek_result did not return full payload; got: %q", text[:min(len(text), 40)])
	}
}

func TestHTTP401Passthrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="mcp"`)
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"unauthorized"}`)
	}))
	defer upstream.Close()
	srv := newServer(t, upstream, 100)

	status, _ := post(t, srv, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if status != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", status)
	}
}

func TestHTTPWellKnownPassthrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-authorization-server" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"issuer":"https://auth.example.com"}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()
	srv := newServer(t, upstream, 100)

	status, body := get(t, srv, "/.well-known/oauth-authorization-server")
	if status != http.StatusOK {
		t.Errorf("expected 200, got %d", status)
	}
	if !strings.Contains(body, "auth.example.com") {
		t.Errorf("expected auth server metadata, got: %q", body)
	}
}

func TestHTTPAuthHeaderForwarded(t *testing.T) {
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "mock", "version": "1"}},
		})
	}))
	defer upstream.Close()
	srv := newServer(t, upstream, 100)

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer oauth-token")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if gotAuth != "Bearer oauth-token" {
		t.Errorf("Authorization not forwarded to upstream; got %q", gotAuth)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
