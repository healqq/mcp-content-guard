package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"mcp-context-guard/internal/cache"
	"mcp-context-guard/internal/proxy"
	"mcp-context-guard/internal/remote"
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
	var rawHeaders headerFlags

	flag.StringVar(&configPath, "config", "", "path to config JSON file")
	flag.Int64Var(&thresholdFlag, "threshold", 0, "response size threshold in bytes (overrides config)")
	flag.StringVar(&upstreamURL, "upstream-url", "", "URL of remote MCP server (Streamable HTTP)")
	flag.Var(&rawHeaders, "upstream-header", `header for remote server, e.g. "Authorization: Bearer token" (repeatable)`)
	flag.Parse()

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

	hasRemote := upstreamURL != "" || cfg.UpstreamURL != ""
	hasLocal := len(flag.Args()) > 0

	if hasRemote && hasLocal {
		fmt.Fprintln(os.Stderr, "error: --upstream-url and a command are mutually exclusive")
		os.Exit(1)
	}
	if !hasRemote && !hasLocal {
		fmt.Fprintln(os.Stderr, "usage: mcp-context-guard [--config path] [--threshold N] -- <cmd> [args...]")
		fmt.Fprintln(os.Stderr, "       mcp-context-guard [--config path] [--threshold N] --upstream-url <url> [--upstream-header K:V]...")
		os.Exit(1)
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
		key := strings.TrimSpace(h[:idx])
		val := strings.TrimSpace(h[idx+1:])
		headers[key] = val
	}

	effectiveURL := upstreamURL
	if effectiveURL == "" {
		effectiveURL = cfg.UpstreamURL
	}

	c := cache.New()

	if hasRemote {
		conn := remote.New(effectiveURL, headers)
		p := proxy.New(conn, conn, nil, c, cfg.Threshold)
		p.Run()
		return
	}

	args := flag.Args()
	cmd := exec.Command(args[0], args[1:]...)

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

	p := proxy.New(upstreamIn, upstreamOut, upstreamErr, c, cfg.Threshold)
	p.Run()

	cmd.Wait()
}
