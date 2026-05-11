package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

// mockUpstream is a simple MCP server that responds to initialize, tools/list, and tools/call.
const mockUpstreamSrc = `
import sys, json

LARGE = "x" * 200  # 200 chars, over the 100-byte threshold used in tests

for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    msg = json.loads(line)
    m = msg.get("method", "")
    id_ = msg.get("id")
    if m == "initialize":
        print(json.dumps({"jsonrpc": "2.0", "id": id_, "result": {
            "protocolVersion": "2024-11-05", "capabilities": {}, "serverInfo": {"name": "mock", "version": "1"}
        }}), flush=True)
    elif m == "tools/list":
        print(json.dumps({"jsonrpc": "2.0", "id": id_, "result": {"tools": [
            {"name": "echo", "description": "small response", "inputSchema": {"type": "object"}},
            {"name": "dump", "description": "large response", "inputSchema": {"type": "object"}},
            {"name": "dump_sc", "description": "structured content only", "inputSchema": {"type": "object"}},
        ]}}), flush=True)
    elif m == "tools/call":
        name = msg.get("params", {}).get("name", "")
        if name == "echo":
            print(json.dumps({"jsonrpc": "2.0", "id": id_, "result": {
                "content": [{"type": "text", "text": "hello"}]
            }}), flush=True)
        elif name == "dump":
            print(json.dumps({"jsonrpc": "2.0", "id": id_, "result": {
                "content": [{"type": "text", "text": LARGE}]
            }}), flush=True)
        elif name == "dump_sc":
            print(json.dumps({"jsonrpc": "2.0", "id": id_, "result": {
                "structuredContent": {"items": list(range(50))}
            }}), flush=True)
`

type proxyConn struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
	t      *testing.T
}

func startProxy(t *testing.T, threshold int) *proxyConn {
	t.Helper()

	// write mock upstream script
	f, err := os.CreateTemp("", "mock_upstream_*.py")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(mockUpstreamSrc)
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	binary := "./mcp-context-guard.exe"
	if _, err := os.Stat(binary); os.IsNotExist(err) {
		binary = "./mcp-context-guard"
	}

	cmd := exec.Command(binary,
		fmt.Sprintf("--threshold=%d", threshold),
		"--",
		"python", f.Name(),
	)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	t.Cleanup(func() { stdin.Close(); cmd.Process.Kill(); cmd.Wait() })

	return &proxyConn{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewScanner(stdout),
		t:      t,
	}
}

func (p *proxyConn) send(msg map[string]any) {
	p.t.Helper()
	b, _ := json.Marshal(msg)
	p.stdin.Write(b)
	p.stdin.Write([]byte("\n"))
}

func (p *proxyConn) recv() map[string]any {
	p.t.Helper()
	done := make(chan bool, 1)
	go func() {
		done <- p.stdout.Scan()
	}()
	select {
	case ok := <-done:
		if !ok {
			p.t.Fatal("upstream closed stdout unexpectedly")
		}
	case <-time.After(5 * time.Second):
		p.t.Fatal("timeout waiting for response")
	}
	var msg map[string]any
	if err := json.Unmarshal(p.stdout.Bytes(), &msg); err != nil {
		p.t.Fatalf("invalid JSON from proxy: %v — %s", err, p.stdout.Bytes())
	}
	return msg
}

func TestToolsListInjectsSeekResult(t *testing.T) {
	p := startProxy(t, 100)

	p.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"},
	}})
	p.recv() // initialize response

	p.send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	resp := p.recv()

	result := resp["result"].(map[string]any)
	tools := result["tools"].([]any)

	var names []string
	for _, tool := range tools {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	if !contains(names, "seek_result") {
		t.Errorf("seek_result not injected; got tools: %v", names)
	}
	if !contains(names, "echo") {
		t.Errorf("upstream tool 'echo' missing; got tools: %v", names)
	}
}

func TestSmallResponsePassthrough(t *testing.T) {
	p := startProxy(t, 100)

	p.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"},
	}})
	p.recv()

	p.send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
		"name": "echo", "arguments": map[string]any{},
	}})
	resp := p.recv()

	result := resp["result"].(map[string]any)
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	if text != "hello" {
		t.Errorf("expected 'hello', got %q", text)
	}
}

func TestLargeResponseCached(t *testing.T) {
	p := startProxy(t, 100)

	p.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"},
	}})
	p.recv()

	p.send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
		"name": "dump", "arguments": map[string]any{},
	}})
	resp := p.recv()

	result := resp["result"].(map[string]any)
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)

	re := regexp.MustCompile(`id="([^"]+)"`)
	m := re.FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("stub missing seek_result call hint, got: %q", text)
	}
	cacheID := m[1]
	if !strings.Contains(text, "bytes cached") {
		t.Errorf("stub missing size hint: %q", text)
	}

	// now query with jq
	p.send(map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{
		"name": "seek_result", "arguments": map[string]any{"id": cacheID, "filter": ".[0].text"},
	}})
	seekResp := p.recv()
	seekResult := seekResp["result"].(map[string]any)
	seekContent := seekResult["content"].([]any)
	seekText := seekContent[0].(map[string]any)["text"].(string)
	if !strings.Contains(seekText, "x") {
		t.Errorf("seek_result jq filter returned unexpected: %q", seekText)
	}
}

func TestSeekResultGrep(t *testing.T) {
	p := startProxy(t, 100)

	p.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"},
	}})
	p.recv()

	p.send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
		"name": "dump", "arguments": map[string]any{},
	}})
	resp := p.recv()
	result := resp["result"].(map[string]any)
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	reGrep := regexp.MustCompile(`id="([^"]+)"`)
	mGrep := reGrep.FindStringSubmatch(text)
	if mGrep == nil {
		t.Fatalf("stub missing seek_result hint: %q", text)
	}
	cacheID := mGrep[1]

	// grep for "x" — should match since LARGE = "x" * 200
	p.send(map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{
		"name": "seek_result", "arguments": map[string]any{"id": cacheID, "filter": "grep:x"},
	}})
	seekResp := p.recv()
	seekResult := seekResp["result"].(map[string]any)
	seekContent := seekResult["content"].([]any)
	seekText := seekContent[0].(map[string]any)["text"].(string)
	if seekText == "" {
		t.Error("grep:x returned empty result, expected match")
	}
}

func TestSeekResultCacheMiss(t *testing.T) {
	p := startProxy(t, 100)

	p.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"},
	}})
	p.recv()

	p.send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
		"name": "seek_result", "arguments": map[string]any{"id": "nonexistent", "filter": "."},
	}})
	resp := p.recv()
	result := resp["result"].(map[string]any)
	isError, _ := result["isError"].(bool)
	if !isError {
		t.Errorf("expected isError=true for cache miss, got: %v", result)
	}
}

func TestSeekResultEmptyFilter(t *testing.T) {
	p := startProxy(t, 100)

	p.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"},
	}})
	p.recv()

	p.send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
		"name": "dump", "arguments": map[string]any{},
	}})
	resp := p.recv()
	result := resp["result"].(map[string]any)
	content := result["content"].([]any)
	stub := content[0].(map[string]any)["text"].(string)
	m := regexp.MustCompile(`id="([^"]+)"`).FindStringSubmatch(stub)
	if m == nil {
		t.Fatalf("stub missing id: %q", stub)
	}
	cacheID := m[1]

	// empty filter — should return the full payload
	p.send(map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{
		"name": "seek_result", "arguments": map[string]any{"id": cacheID},
	}})
	seekResp := p.recv()
	seekResult := seekResp["result"].(map[string]any)
	seekContent := seekResult["content"].([]any)
	text := seekContent[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, strings.Repeat("x", 200)) {
		t.Errorf("empty filter did not return full payload; got %q", text[:min(len(text), 40)])
	}
}

func TestSeekResultEmptyID(t *testing.T) {
	p := startProxy(t, 100)

	p.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"},
	}})
	p.recv()

	p.send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
		"name": "seek_result", "arguments": map[string]any{"id": ""},
	}})
	resp := p.recv()
	result := resp["result"].(map[string]any)
	isError, _ := result["isError"].(bool)
	if !isError {
		t.Errorf("expected isError=true for empty id, got: %v", result)
	}
}

func TestStructuredContentPassthrough(t *testing.T) {
	p := startProxy(t, 100)

	p.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"},
	}})
	p.recv()

	p.send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{
		"name": "dump_sc", "arguments": map[string]any{},
	}})
	resp := p.recv()
	result := resp["result"].(map[string]any)

	// structuredContent must be present and unmodified
	sc, ok := result["structuredContent"]
	if !ok {
		t.Fatal("structuredContent missing from response")
	}
	scMap, ok := sc.(map[string]any)
	if !ok {
		t.Fatalf("structuredContent is not an object: %T", sc)
	}
	if _, ok := scMap["items"]; !ok {
		t.Errorf("structuredContent.items missing; got: %v", scMap)
	}
	// content must not be present (the tool returned no content field)
	if _, ok := result["content"]; ok {
		t.Error("content field should not be present when tool returned structuredContent only")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
