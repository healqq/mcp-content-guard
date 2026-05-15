package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"mcp-context-guard/internal/cache"
	"mcp-context-guard/internal/httpserver"
	"mcp-context-guard/internal/proxy"
	"mcp-context-guard/internal/stats"
)

const defaultThreshold = 10240 // 10 KB

type Config struct {
	Threshold       int64             `json:"threshold"`
	UpstreamURL     string            `json:"upstream_url,omitempty"`
	UpstreamHeaders map[string]string `json:"upstream_headers,omitempty"`
}

// headerFlags is a repeatable flag that accumulates "Key: Value" strings.
type headerFlags []string

func (h *headerFlags) String() string { return strings.Join(*h, ", ") }
func (h *headerFlags) Set(v string) error {
	*h = append(*h, v)
	return nil
}

func main() {
	var configPath string
	var thresholdFlag int64
	var upstreamURL string
	var listenAddr string
	var rawHeaders headerFlags
	var enableStats bool

	flag.StringVar(&configPath, "config", "", "path to config JSON file")
	flag.Int64Var(&thresholdFlag, "threshold", 0, "response size threshold in bytes (overrides config)")
	flag.StringVar(&upstreamURL, "upstream-url", "", "URL of remote MCP server (Streamable HTTP); requires --listen")
	flag.StringVar(&listenAddr, "listen", "", "address to listen on as HTTP server, e.g. :8080 (requires --upstream-url)")
	flag.Var(&rawHeaders, "upstream-header", `header added to every upstream request, e.g. "Authorization: Bearer token" (repeatable)`)
	flag.BoolVar(&enableStats, "collect-stats", false, "persist token-saving stats to local disk")
	flag.Parse()

	// Extract optional subcommand (start|stats) from positional args.
	// Handles both orderings:
	//   HTTP:  mcp-context-guard --listen :PORT --upstream-url <url> [start|stats]
	//   Local: mcp-context-guard [start|stats] -- <cmd> [args...]
	posArgs := flag.Args()
	subcommand := "start"
	if len(posArgs) > 0 && (posArgs[0] == "start" || posArgs[0] == "stats") {
		subcommand = posArgs[0]
		posArgs = posArgs[1:]
	}
	// Strip "--" separator that may follow the subcommand in local mode.
	if len(posArgs) > 0 && posArgs[0] == "--" {
		posArgs = posArgs[1:]
	}

	cfg := Config{Threshold: defaultThreshold}
	if configPath != "" {
		data, err := os.ReadFile(configPath)
		if err != nil {
			log.Fatalf("read config: %v", err)
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			log.Fatalf("parse config: %v", err)
		}
	}
	if thresholdFlag > 0 {
		cfg.Threshold = thresholdFlag
	}

	// Build merged headers: config-file values as base, CLI flags win on conflict.
	headers := make(map[string]string)
	for k, v := range cfg.UpstreamHeaders {
		headers[k] = v
	}
	for _, h := range rawHeaders {
		idx := strings.Index(h, ":")
		if idx < 0 {
			log.Fatalf("invalid --upstream-header %q: must be 'Key: Value' or 'Key:Value'", h)
		}
		headers[strings.TrimSpace(h[:idx])] = strings.TrimSpace(h[idx+1:])
	}

	effectiveURL := upstreamURL
	if effectiveURL == "" {
		effectiveURL = cfg.UpstreamURL
	}

	hasListen := listenAddr != ""
	hasURL := effectiveURL != ""
	hasLocal := len(posArgs) > 0

	// Derive the config key used for the stats file.
	// Must be done before mode validation so the stats subcommand can also
	// resolve the path without needing a running upstream.
	var configKey string
	switch {
	case hasListen && hasURL:
		configKey = "http:" + effectiveURL
	case hasLocal:
		configKey = "local:" + strings.Join(posArgs, " ")
	}

	if subcommand == "stats" {
		if configKey == "" {
			fmt.Fprintln(os.Stderr, "usage: mcp-context-guard [flags] stats -- <cmd> [args...]")
			fmt.Fprintln(os.Stderr, "       mcp-context-guard [flags] --listen :PORT --upstream-url <url> stats")
			os.Exit(1)
		}
		fmt.Println(stats.Format(stats.Load(stats.PathForKey(configKey))))
		return
	}

	switch {
	case hasURL && !hasListen:
		fmt.Fprintln(os.Stderr, "error: --upstream-url requires --listen")
		fmt.Fprintln(os.Stderr, "usage: mcp-context-guard --listen :PORT --upstream-url <url> [--upstream-header K:V]...")
		os.Exit(1)
	case hasListen && !hasURL:
		fmt.Fprintln(os.Stderr, "error: --listen requires --upstream-url")
		os.Exit(1)
	case hasListen && hasLocal:
		fmt.Fprintln(os.Stderr, "error: --listen and a command are mutually exclusive")
		os.Exit(1)
	case !hasListen && !hasLocal:
		fmt.Fprintln(os.Stderr, "usage: mcp-context-guard [--config path] [--threshold N] [--collect-stats] -- <cmd> [args...]")
		fmt.Fprintln(os.Stderr, "       mcp-context-guard [--config path] [--threshold N] [--collect-stats] --listen :PORT --upstream-url <url> [--upstream-header K:V]...")
		os.Exit(1)
	}

	c := cache.New()

	var st *stats.Stats
	if enableStats {
		var err error
		st, err = stats.New(stats.PathForKey(configKey))
		if err != nil {
			log.Fatalf("stats: %v", err)
		}
		defer st.Save() //nolint:errcheck
	}

	if hasListen {
		srv, err := httpserver.New(effectiveURL, headers, c, cfg.Threshold, st)
		if err != nil {
			log.Fatalf("httpserver: %v", err)
		}
		log.Printf("mcp-context-guard listening on %s → %s", listenAddr, effectiveURL)
		log.Fatal(http.ListenAndServe(listenAddr, srv))
		return
	}

	// Local subprocess mode.
	cmd := exec.Command(posArgs[0], posArgs[1:]...)

	upstreamIn, err := cmd.StdinPipe()
	if err != nil {
		log.Fatalf("stdin pipe: %v", err)
	}
	upstreamOut, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatalf("stdout pipe: %v", err)
	}
	upstreamErr, err := cmd.StderrPipe()
	if err != nil {
		log.Fatalf("stderr pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		log.Fatalf("start upstream: %v", err)
	}

	p := proxy.New(upstreamIn, upstreamOut, upstreamErr, c, cfg.Threshold, st)
	p.Run()

	_ = cmd.Wait()
}
