# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

**mcp-context-guard** is a lightweight Go wrapper around an existing MCP (Model Context Protocol) server. It sits between an MCP client and an upstream server, intercepts tool call responses, caches them, and exposes a `seek_result` tool that lets the client query cached responses with jq or grep filters.

## Goals

- Inject one extra tool (`seek_result`) into the upstream schema; leave everything else untouched.
- For tool call responses above a configurable byte threshold: cache the response, return a stub `{id, size_bytes}` instead of the full payload.
- For responses below the threshold: return the full response directly, no caching.
- `seek_result` is handled entirely by the wrapper — never forwarded to upstream.
- Always pass `structuredContent` through unchanged (it is typed by `outputSchema` and almost always small).

## Architecture

```
MCP Client → mcp-context-guard (proxy + cache + filter) → upstream MCP server (subprocess)
```

Transport: **stdio on both sides**. The client spawns the wrapper; the wrapper spawns the upstream server. All messages are newline-delimited JSON-RPC 2.0.

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
  "description": "Query a cached tool response. Use the id returned by a previous tool call.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "id":     { "type": "string" },
      "filter": { "type": "string", "description": "jq expression (e.g. '.items[:10]') or grep pattern prefixed with 'grep:' (e.g. 'grep:error')" }
    },
    "required": ["id", "filter"]
  }
}
```

### Stub response shape

```json
{ "id": "a3f9...", "size_bytes": 142830 }
```

Returned as a `text` ContentBlock. The LLM already knows the tool's output structure from the schema, so no preview is included.

### Cache

In-memory `map[string]json.RawMessage` (uuid → raw `content` JSON). Scoped to the wrapper process lifetime (one client session). `structuredContent` is never cached — always forwarded directly.

### Filter DSL

- **jq**: any jq expression applied to the cached `content` value
- **grep**: prefix `grep:` followed by a regex pattern, matched against each `ContentBlock.text` line

External `jq` binary via `gjson` or equivalent — no custom DSL.

## Commands

```bash
go build -o mcp-context-guard.exe .          # build
go test -v -timeout 30s ./...                # run all tests
go test -v -run TestName ./...               # run a single test
GOOS=linux GOARCH=arm64 go build -o mcp-context-guard-linux-arm64 .  # cross-compile
```

Tests are integration tests in `proxy_test.go` — they spawn the compiled binary against a Python mock upstream.

## Tech stack

- **Language**: Go
- **JSON querying**: `gjson` (single dependency, no cgo)
- **Grep**: stdlib `regexp`
- **Everything else**: stdlib only (`encoding/json`, `bufio`, `os/exec`, `sync`)

## Key invariants

- `structuredContent` is always forwarded as-is; only `content` is subject to caching/filtering.
- The wrapper never modifies `inputSchema` or `outputSchema` of upstream tools.
- `seek_result` calls are never forwarded upstream.
- Threshold is configurable (default TBD); can be set per-tool or globally.
