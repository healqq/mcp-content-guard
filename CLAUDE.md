# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

**mcp-context-guard** is a lightweight Go wrapper around an existing MCP (Model Context Protocol) server. It sits between an MCP client and an upstream server, intercepts tool call responses, caches them, and exposes a `seek_result` tool that lets the client query cached responses with jq or grep filters.

## Goals

- Inject one extra tool (`seek_result`) into the upstream schema; leave everything else untouched.
- For tool call responses above a configurable byte threshold: cache the response, return a stub text message instead of the full payload.
- For responses below the threshold: return the full response directly, no caching.
- `seek_result` is handled entirely by the wrapper — never forwarded to upstream.
- Always pass `structuredContent` through unchanged (it is typed by `outputSchema` and almost always small).

## Architecture

```
MCP Client → mcp-context-guard (proxy + cache + filter) → upstream MCP server
                                                            (subprocess or remote HTTP)
```

Three operating modes depending on flags:

| Mode | Client side | Upstream side | Selected by |
|---|---|---|---|
| **Local** | stdio | subprocess (stdin/stdout) | `-- <cmd>` |
| **Remote stdio** | stdio | Streamable HTTP | `--upstream-url` |
| **HTTP server** | HTTP | Streamable HTTP | `--listen :PORT --upstream-url` |

**Local / Remote stdio**: the client spawns the wrapper; messages are newline-delimited JSON-RPC 2.0 on stdin/stdout. `internal/remote` implements the HTTP client transport; both are abstracted by the same `io.WriteCloser` / `io.Reader` pair passed to `proxy.New()`.

**HTTP server mode**: the wrapper itself listens as an HTTP server (`--listen :PORT`). Both client and upstream speak Streamable HTTP. All traffic that is not an MCP tool call — including `401` responses, `/.well-known/oauth-authorization-server`, `/register`, `/token`, redirects — is forwarded byte-for-byte via `httputil.ReverseProxy`. This makes OAuth completely transparent: the MCP client handles the OAuth flow directly with the auth server; the proxy never sees or stores tokens. Implemented in `internal/httpserver`.

### Message handling

| Message | Action |
|---|---|
| `tools/list` response | Inject `seek_result` tool definition, forward |
| `tools/call` for any upstream tool | Forward to upstream; if response `content` ≥ threshold → cache + return stub; else return full response |
| `tools/call` for `seek_result` | Handle locally: look up cache by id, apply filter, return result. Never forwarded. |
| All other messages / notifications | Forward unchanged in both directions |

### seek_result tool schema

```json
{
  "name": "seek_result",
  "description": "Retrieve content from a cached tool response. When a tool response was too large to return directly, you receive a stub message with an id. Call this tool with that id to get the content. Omit filter to get the full payload, or use a filter to extract a subset.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "id":     { "type": "string" },
      "filter": { "type": "string", "description": "Optional. Omit to return the full payload. Options: jq expression (e.g. 'max_by(.price)'); 'grep:<pattern>' to return matching lines; 'head:<N>' for first N lines; 'tail:<N>' for last N lines; 'line:<N>' for a single 0-based line index." }
    },
    "required": ["id"]
  }
}
```

### Stub response shape

When a response exceeds the threshold, the client receives a `text` ContentBlock with a human-readable message:

```
[Response too large to return (142830 bytes cached, id="a3f9..."). Call seek_result(id) to get the full payload, or seek_result(id, filter) to extract a subset (jq expression, grep:<pattern>, head:<N>, tail:<N>, line:<N>).]
```

### Cache

In-memory `map[string]json.RawMessage` (hex-encoded random id → raw `content` JSON). Scoped to the wrapper process lifetime (one client session). `structuredContent` is never cached — always forwarded directly.

### Filter DSL

- **empty / omitted**: return the full concatenated text of all content blocks
- **jq**: any jq expression; if the cached content is JSON text, the expression runs against the parsed data directly (e.g. `max_by(.price)`). Shell-style `jq 'expr'` syntax is also accepted and normalised automatically.
- **grep**: prefix `grep:` followed by a regex pattern, matched line-by-line against text content blocks
- **head**: prefix `head:N` — first N lines
- **tail**: prefix `tail:N` — last N lines
- **line**: prefix `line:N` — single line at 0-based index N

Implemented in Go using `github.com/itchyny/gojq` (pure Go, no cgo, no external binary).

## Workflow

When making changes, keep documentation in sync:

- **`README.md`** — update whenever user-facing behaviour changes: new CLI flags, new filter syntax, changed defaults, new transport modes, or revised usage examples. This is the primary user reference.
- **`CLAUDE.md` (this file)** — update the Architecture, Key invariants, or Filter DSL sections whenever the internal design changes (new packages, changed interfaces, revised message-handling rules). Do not let architecture prose drift from the code.

Both files must be updated in the same commit as the code change. Do not leave a feature undocumented.

## Commands

```bash
go build -o mcp-context-guard.exe .          # build
go test -v -timeout 30s ./...                # run all tests
go test -v -run TestName ./...               # run a single test
GOOS=linux GOARCH=arm64 go build -o mcp-context-guard-linux-arm64 .  # cross-compile
```

Integration tests in `proxy_test.go` spawn the compiled binary against either a Python mock upstream (local mode) or a Go `httptest.NewServer` (remote stdio mode). Unit tests for the HTTP transport live in `internal/remote/conn_test.go`. Unit tests for HTTP server mode live in `internal/httpserver/server_test.go`.

## Tech stack

- **Language**: Go
- **JSON querying**: `github.com/itchyny/gojq` (pure Go jq implementation, no cgo)
- **Grep / line filters**: stdlib `regexp`, `strings`
- **HTTP transport**: stdlib `net/http`, `bufio` (SSE parsing) — no new dependencies
- **Everything else**: stdlib only (`encoding/json`, `bufio`, `os/exec`, `sync`)

## Key invariants

- `structuredContent` is always forwarded as-is; only `content` is subject to caching/filtering.
- The wrapper never modifies `inputSchema` or `outputSchema` of upstream tools.
- `seek_result` calls are never forwarded upstream.
- Threshold is configurable via `--threshold` flag or config file (default: 10240 bytes); applies globally.
- `--upstream-url`, `-- <cmd>`, and `--listen` are three mutually exclusive modes; exactly one must be provided.
- `--listen` requires `--upstream-url`; it enables HTTP server mode with transparent OAuth passthrough.
- Remote headers can be set via `--upstream-header "Key: Value"` (repeatable) or `upstream_headers` in the config file; CLI wins on key conflicts.
- In HTTP server mode the proxy never inspects or stores auth tokens — they pass through unchanged from client to upstream.
