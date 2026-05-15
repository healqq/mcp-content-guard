// Benchmark runner for mcp-context-guard.
//
// Usage:
//
//	go build -o mcp-context-guard.exe .
//	go run ./bench/
//	go run ./bench/ -scenarios bench/scenarios -threshold 10240 -v
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const maxScanToken = 16 * 1024 * 1024 // 16 MB

// Scenario is a single MCP server + a list of benchmark cases.
type Scenario struct {
	Name  string   `json:"name"`
	MCP   string   `json:"mcp"`
	Cmd   []string `json:"cmd"`
	Cases []Case   `json:"cases"`
	Notes string   `json:"notes"` // ignored by runner, free-text for humans
}

// Case is one tool call + a seek_result filter to apply afterwards.
type Case struct {
	Name      string          `json:"name"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	Filter    string          `json:"filter"` // jq expr or grep:<pattern>
}

// Result holds raw vs proxied byte counts for one case.
type Result struct {
	Scenario    string
	Case        string
	RawBytes    int
	StubBytes   int  // content bytes of the stub response
	FilterBytes int  // content bytes of the seek_result response
	Cached      bool // was the response above threshold and cached?
	Err         string
}

func (r Result) totalProxied() int {
	return r.StubBytes + r.FilterBytes
}

func (r Result) savings() float64 {
	if !r.Cached || r.RawBytes == 0 {
		return 0
	}
	saved := r.RawBytes - r.totalProxied()
	if saved < 0 {
		saved = 0
	}
	return float64(saved) / float64(r.RawBytes) * 100
}

func main() {
	scenariosDir := flag.String("scenarios", "bench/scenarios", "directory with scenario JSON files")
	threshold := flag.Int("threshold", 10240, "proxy cache threshold in bytes")
	proxyBin := flag.String("proxy", "", "mcp-context-guard binary (auto-detected if empty)")
	verbose := flag.Bool("v", false, "verbose: print stderr from MCP servers")
	flag.Parse()

	bin := findProxy(*proxyBin)
	if bin == "" {
		fmt.Fprintln(os.Stderr, "error: mcp-context-guard binary not found; build it first:\n  go build -o mcp-context-guard.exe .")
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "proxy binary : %s\n", bin)
	fmt.Fprintf(os.Stderr, "threshold    : %d bytes\n", *threshold)
	fmt.Fprintf(os.Stderr, "scenarios    : %s\n\n", *scenariosDir)

	entries, err := os.ReadDir(*scenariosDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading %s: %v\n", *scenariosDir, err)
		os.Exit(1)
	}

	var results []Result
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(*scenariosDir, e.Name()))
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", e.Name(), err)
			continue
		}
		var s Scenario
		if err := json.Unmarshal(data, &s); err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: parse: %v\n", e.Name(), err)
			continue
		}
		cmd := expandEnvSlice(s.Cmd)
		fmt.Printf("=== %s (%s) ===\n", s.Name, s.MCP)
		for _, c := range s.Cases {
			fmt.Printf("  %-30s ... ", c.Name)
			r := benchCase(bin, cmd, c, *threshold, *verbose)
			r.Scenario = s.Name
			results = append(results, r)
			switch {
			case r.Err != "":
				fmt.Printf("ERROR: %s\n", r.Err)
			case r.Cached:
				fmt.Printf("raw=%s  proxied=%s  savings=%.0f%%\n",
					humanBytes(r.RawBytes), humanBytes(r.totalProxied()), r.savings())
			default:
				fmt.Printf("raw=%s (below threshold, passthrough)\n", humanBytes(r.RawBytes))
			}
		}
		fmt.Println()
	}

	printReport(results)
}

// benchCase runs one case: direct call + proxied call, returns metrics.
func benchCase(bin string, cmd []string, c Case, threshold int, verbose bool) Result {
	r := Result{Case: c.Name}

	// expand env vars in arguments JSON (supports $VAR in string values)
	args := expandEnvJSON(c.Arguments)

	rawBytes, err := runDirect(cmd, c.Tool, args, verbose)
	if err != nil {
		r.Err = fmt.Sprintf("direct: %v", err)
		return r
	}
	r.RawBytes = rawBytes

	stubBytes, filterBytes, cached, err := runProxied(bin, cmd, c.Tool, args, c.Filter, threshold, verbose)
	if err != nil {
		r.Err = fmt.Sprintf("proxied: %v", err)
		return r
	}
	r.StubBytes = stubBytes
	r.FilterBytes = filterBytes
	r.Cached = cached
	return r
}

// runDirect sends a tool call directly to the upstream MCP server and returns
// the byte length of the content JSON array.
func runDirect(cmd []string, toolName string, arguments json.RawMessage, verbose bool) (int, error) {
	c, err := startMCP(cmd, verbose)
	if err != nil {
		return 0, err
	}
	defer c.close()
	if err := initialize(c); err != nil {
		return 0, fmt.Errorf("initialize: %w", err)
	}
	resp, err := callTool(c, 1, toolName, arguments)
	if err != nil {
		return 0, err
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		return 0, fmt.Errorf("unexpected response: %v", resp)
	}
	b, _ := json.Marshal(result["content"])
	return len(b), nil
}

// runProxied sends a tool call through mcp-context-guard and optionally follows
// up with seek_result. Returns stub bytes, filter result bytes, whether caching
// occurred, and any error.
func runProxied(bin string, cmd []string, toolName string, arguments json.RawMessage, filterExpr string, threshold int, verbose bool) (stubBytes, filterBytes int, cached bool, err error) {
	proxyArgs := append([]string{bin, fmt.Sprintf("--threshold=%d", threshold), "--"}, cmd...)
	c, err := startMCP(proxyArgs, verbose)
	if err != nil {
		return 0, 0, false, err
	}
	defer c.close()
	if err := initialize(c); err != nil {
		return 0, 0, false, fmt.Errorf("initialize: %w", err)
	}

	resp, err := callTool(c, 1, toolName, arguments)
	if err != nil {
		return 0, 0, false, err
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		return 0, 0, false, fmt.Errorf("unexpected response: %v", resp)
	}
	contentRaw, _ := json.Marshal(result["content"])

	// detect whether response was cached: proxy replaces content with a human-readable
	// stub containing seek_result(id="<uuid>", ...) — extract the id with regex.
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		return len(contentRaw), 0, false, nil
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	stubRe := regexp.MustCompile(`seek_result\(id="([^"]+)"`)
	m := stubRe.FindStringSubmatch(text)
	if m == nil {
		return len(contentRaw), 0, false, nil // not cached
	}
	cacheID := m[1]
	stubBytes = len(contentRaw)

	seekArgs, _ := json.Marshal(map[string]string{"id": cacheID, "filter": filterExpr})
	seekResp, err := callTool(c, 2, "seek_result", seekArgs)
	if err != nil {
		return stubBytes, 0, true, fmt.Errorf("seek_result: %w", err)
	}
	seekResult, ok := seekResp["result"].(map[string]any)
	if !ok {
		return stubBytes, 0, true, fmt.Errorf("no seek result: %v", seekResp)
	}
	seekContentRaw, _ := json.Marshal(seekResult["content"])
	return stubBytes, len(seekContentRaw), true, nil
}

// ─── MCP connection ──────────────────────────────────────────────────────────

type mcpConn struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
}

func startMCP(args []string, verbose bool) (*mcpConn, error) {
	cmd := exec.Command(args[0], args[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if verbose {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = io.Discard
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %q: %w", args[0], err)
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, maxScanToken), maxScanToken)
	return &mcpConn{cmd: cmd, stdin: stdin, stdout: sc}, nil
}

func (c *mcpConn) send(msg any) error {
	b, _ := json.Marshal(msg)
	b = append(b, '\n')
	_, err := c.stdin.Write(b)
	return err
}

// recv reads the next non-notification JSON-RPC message with a timeout.
// Notifications (no "id" field but have "method") are silently skipped.
func (c *mcpConn) recv(timeout time.Duration) (map[string]any, error) {
	type scanResult struct {
		msg map[string]any
		err error
	}
	ch := make(chan scanResult, 1)
	go func() {
		for c.stdout.Scan() {
			var msg map[string]any
			if err := json.Unmarshal(c.stdout.Bytes(), &msg); err != nil {
				ch <- scanResult{err: err}
				return
			}
			_, hasID := msg["id"]
			_, hasMethod := msg["method"]
			if !hasID && hasMethod {
				continue // pure notification
			}
			ch <- scanResult{msg: msg}
			return
		}
		if err := c.stdout.Err(); err != nil {
			ch <- scanResult{err: err}
		} else {
			ch <- scanResult{err: fmt.Errorf("connection closed")}
		}
	}()
	select {
	case r := <-ch:
		return r.msg, r.err
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout after %s", timeout)
	}
}

func (c *mcpConn) close() {
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}
}

func initialize(c *mcpConn) error {
	if err := c.send(map[string]any{
		"jsonrpc": "2.0", "id": 0, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":   map[string]any{},
			"clientInfo":     map[string]any{"name": "bench", "version": "1"},
		},
	}); err != nil {
		return err
	}
	_, err := c.recv(20 * time.Second)
	return err
}

func callTool(c *mcpConn, id int, name string, arguments json.RawMessage) (map[string]any, error) {
	if err := c.send(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": arguments},
	}); err != nil {
		return nil, err
	}
	return c.recv(60 * time.Second)
}

// ─── Reporting ───────────────────────────────────────────────────────────────

func printReport(results []Result) {
	if len(results) == 0 {
		fmt.Println("No results.")
		return
	}

	// cached cases sorted by savings desc, then uncached, then errors
	sort.Slice(results, func(i, j int) bool {
		ei, ej := results[i].Err != "", results[j].Err != ""
		if ei != ej {
			return !ei
		}
		ci, cj := results[i].Cached, results[j].Cached
		if ci != cj {
			return ci
		}
		return results[i].savings() > results[j].savings()
	})

	sep := strings.Repeat("─", 95)
	fmt.Println("\n" + sep)
	fmt.Println("BENCHMARK REPORT")
	fmt.Println(sep)
	fmt.Printf("%-38s %10s %10s %10s %10s %8s\n",
		"Case", "Raw", "Stub", "Filter", "Total", "Savings")
	fmt.Println(strings.Repeat("─", 95))
	for _, r := range results {
		label := r.Scenario + "/" + r.Case
		if len(label) > 37 {
			label = label[:34] + "..."
		}
		switch {
		case r.Err != "":
			fmt.Printf("%-38s  ERROR: %s\n", label, r.Err)
		case !r.Cached:
			fmt.Printf("%-38s %10s %10s %10s %10s %8s\n",
				label, humanBytes(r.RawBytes), "—", "—", humanBytes(r.RawBytes), "—")
		default:
			fmt.Printf("%-38s %10s %10s %10s %10s %7.0f%%\n",
				label,
				humanBytes(r.RawBytes),
				humanBytes(r.StubBytes),
				humanBytes(r.FilterBytes),
				humanBytes(r.totalProxied()),
				r.savings(),
			)
		}
	}
	fmt.Println(strings.Repeat("─", 95))

	fmt.Println("\nToken estimates (bytes ÷ 4, cached cases only):")
	fmt.Printf("%-38s %12s %12s %8s\n", "Case", "Raw tokens", "Proxy tokens", "Savings")
	fmt.Println(strings.Repeat("─", 75))
	any := false
	for _, r := range results {
		if r.Err != "" || !r.Cached {
			continue
		}
		any = true
		label := r.Scenario + "/" + r.Case
		if len(label) > 37 {
			label = label[:34] + "..."
		}
		fmt.Printf("%-38s %12s %12s %7.0f%%\n",
			label, humanTokens(r.RawBytes/4), humanTokens(r.totalProxied()/4), r.savings())
	}
	if !any {
		fmt.Println("  (no cached results — try lowering -threshold)")
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func findProxy(hint string) string {
	if hint != "" {
		if _, err := os.Stat(hint); err == nil {
			return hint
		}
		return hint // trust the hint even if stat fails (PATH lookup)
	}
	for _, p := range []string{
		"./mcp-context-guard.exe",
		"./mcp-context-guard",
		"mcp-context-guard.exe",
		"mcp-context-guard",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func expandEnvSlice(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = os.ExpandEnv(s)
	}
	return out
}

// expandEnvJSON expands $VAR and ${VAR} inside a JSON-encoded string.
// Works on the raw bytes before parsing, so it handles string values that
// contain env var references like "$BENCH_GIT_REPO".
func expandEnvJSON(raw json.RawMessage) json.RawMessage {
	expanded := os.ExpandEnv(string(raw))
	return json.RawMessage(expanded)
}

func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func humanTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("~%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("~%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("~%d", n)
	}
}
