package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"

	"mcp-context-guard/internal/cache"
	"mcp-context-guard/internal/proxy"
)

const defaultThreshold = 10240 // 10 KB

type Config struct {
	Threshold int64            `json:"threshold"`
	Tools     map[string]int64 `json:"tools"` // reserved for per-tool overrides
}

func main() {
	var configPath string
	var thresholdFlag int64

	flag.StringVar(&configPath, "config", "", "path to config JSON file")
	flag.Int64Var(&thresholdFlag, "threshold", 0, "response size threshold in bytes (overrides config)")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: mcp-context-guard [--config path] [--threshold N] -- <cmd> [args...]")
		os.Exit(1)
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

	c := cache.New()
	p := proxy.New(upstreamIn, upstreamOut, upstreamErr, c, cfg.Threshold)
	p.Run()

	cmd.Wait()
}
