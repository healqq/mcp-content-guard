# mcp-context-guard

A lightweight wrapper for any [MCP](https://modelcontextprotocol.io) server that prevents large tool responses from flooding your context window.

When a tool response exceeds a configurable size threshold, the proxy caches it and returns a small stub instead. You then query the cached response using the injected `seek_result` tool, applying a jq expression or grep pattern to extract only what you need.

## How it works

```
MCP Client → mcp-context-guard → your MCP server (local subprocess or remote HTTP)
```

The wrapper sits between your MCP client and any MCP server — local (stdio subprocess) or remote (Streamable HTTP). It:

1. **Passes through all tool schemas unchanged** — the client sees the same tools as before, plus one new tool: `seek_result`.
2. **Intercepts large responses** — when a tool call response exceeds the size threshold, it is cached and the client receives a human-readable stub:
   ```
   [Response too large to return (142830 bytes cached, id="3f9a..."). Call seek_result(id) to get the full payload, or seek_result(id, filter) to extract a subset (jq expression, grep:<pattern>, head:<N>, tail:<N>, line:<N>).]
   ```
3. **Lets you query the cache** — call `seek_result` with the id and a filter to extract the relevant portion.

Small responses (under the threshold) are returned directly with no change in behaviour.

## Installation

**Download a pre-built binary** from the [releases page](https://github.com/healqq/mcp-context-guard/releases) — builds are provided for Linux, macOS, and Windows (amd64 and arm64).

**Install with Go:**
```bash
go install github.com/healqq/mcp-context-guard@latest
```

**Build from source:**
```bash
git clone https://github.com/healqq/mcp-context-guard
cd mcp-context-guard
go build -o mcp-context-guard .
```

Requires Go 1.21+. Produces a single static binary with no runtime dependencies.

## Usage

### Local server (stdio subprocess)

```bash
mcp-context-guard [--threshold N] [--config path] -- <upstream-command> [args...]
```

Anywhere you currently point your MCP client at an upstream server, point it at `mcp-context-guard` instead and pass the original command after `--`.

**Example — wrap the filesystem MCP server:**
```bash
mcp-context-guard --threshold 10240 -- npx -y @modelcontextprotocol/server-filesystem /home/user
```

**Example — with a config file:**
```bash
mcp-context-guard --config guard.json -- python my_server.py
```

### Remote server — stdio client (Streamable HTTP)

```bash
mcp-context-guard [--threshold N] [--config path] --upstream-url <url> [--upstream-header "Key: Value"]...
```

Point the proxy at any remote MCP server that speaks the [Streamable HTTP transport](https://modelcontextprotocol.io/specification/2025-03-26/basic/transports#streamable-http). Your MCP client still spawns the proxy over stdio. Static auth tokens can be passed via `--upstream-header`.

**Example — wrap a hosted MCP API with a static token:**
```bash
mcp-context-guard \
  --upstream-url https://api.example.com/mcp \
  --upstream-header "Authorization: Bearer sk-..."
```

### Remote server — HTTP server mode (OAuth-compatible)

```bash
mcp-context-guard [--threshold N] [--config path] --listen :PORT --upstream-url <url>
```

Runs the proxy as an HTTP server. Your MCP client connects to `http://localhost:PORT` via HTTP transport instead of spawning the proxy over stdio. This mode is fully transparent for OAuth: `401` responses, `/.well-known/oauth-authorization-server`, `/register`, `/token` and all other non-tool-call traffic are forwarded byte-for-byte — the MCP client handles the OAuth flow directly with the auth server.

**Example — wrap a remote MCP server with OAuth:**
```bash
# Proxy listens on :8080; MCP client connects to http://localhost:8080/mcp
mcp-context-guard --listen :8080 --upstream-url https://api.example.com/mcp
```

Configure your MCP client with `http://localhost:8080/mcp` as the server URL. When the upstream requires OAuth, the client will discover the auth server via `/.well-known/oauth-authorization-server` (which the proxy forwards), complete the flow, and include the resulting token in subsequent requests — which the proxy forwards unchanged.

**Example — test against the official reference server:**
```bash
# Terminal 1
npx -y @modelcontextprotocol/server-everything --port 3001

# Terminal 2
mcp-context-guard --upstream-url http://localhost:3001/mcp
```

### Options

| Flag | Default | Description |
|---|---|---|
| `--threshold N` | `10240` | Response size in bytes above which responses are cached instead of returned directly |
| `--config path` | — | Path to a JSON config file (see below) |
| `--upstream-url url` | — | URL of a remote MCP server (Streamable HTTP) |
| `--listen addr` | — | Listen as an HTTP server on this address (e.g. `:8080`); requires `--upstream-url` |
| `--upstream-header K:V` | — | HTTP header added to every request to the remote server (repeatable) |

### Config file

```json
{
  "threshold": 10240,
  "upstream_url": "https://api.example.com/mcp",
  "upstream_headers": {
    "Authorization": "Bearer sk-..."
  }
}
```

`upstream_url` and `upstream_headers` in the config file are overridden by the corresponding CLI flags when both are provided.

## seek_result tool

When a response is cached, use `seek_result` to query it.

**Parameters:**

| Parameter | Required | Description |
|---|---|---|
| `id` | yes | The cache id from the stub response |
| `filter` | no | How to extract content. Omit to return the full payload. |

**jq filter** — any valid jq expression. If the cached content is JSON, the expression runs directly against the parsed data:

```
max_by(.price)                      → object with the highest price field
.[0]                                → first element of an array
map(select(.category == "books"))   → filter an array by field value
```

**grep filter** — prefix `grep:` followed by a regular expression, matched line-by-line against text content blocks:

```
grep:error             → lines containing "error"
grep:^import           → lines starting with "import"
grep:TODO|FIXME        → lines containing TODO or FIXME
```

**line-range filters** — navigate to a specific position in large text responses:

```
head:20                → first 20 lines
tail:20                → last 20 lines
line:9                 → single line at 0-based index 9
```

These are useful when the data you need is at a known position — for example, `tail:50` to read the oldest entries in a git log, or `line:0` to read the first record of a large list.

The same `id` can be queried multiple times with different filters — the cached response is not consumed.

## Cross-compilation

The binary compiles for any platform Go supports:

```bash
GOOS=linux   GOARCH=amd64  go build -o mcp-context-guard-linux-amd64 .
GOOS=linux   GOARCH=arm64  go build -o mcp-context-guard-linux-arm64 .
GOOS=darwin  GOARCH=arm64  go build -o mcp-context-guard-darwin-arm64 .
GOOS=windows GOARCH=amd64  go build -o mcp-context-guard-windows-amd64.exe .
```

## Benchmark

Tested across four MCP servers with model `gpt-5.4-mini-2026-03-17`. The proxy pays off whenever tool responses exceed the threshold. SQLite is the counter-example: query results are small and never cached, so the proxy adds a small overhead from the extra tool in the schema.

| Suite | Direct tokens | Proxied tokens | Savings | Pass rate |
|---|---:|---:|---:|:---:|
| Filesystem (35–46 KB files) | 79 812 | 33 249 | **−58 %** | 5/5 vs 5/5 |
| Git log (300 commits, ~120 KB) | 218 704 | 74 285 | **−66 %** | 5/5 vs 4/5 |
| SQLite (small query results) | 8 140 | 10 300 | **+27 %** | 5/5 vs 5/5 |
| Playwright (75 KB page snapshots) | 640 708 | 87 685 | **−86 %** | 5/5 vs 3/5 |

See [bench/REPORT.md](bench/REPORT.md) for the full per-task breakdown including turn counts, token diffs, and filter expressions used.

## Limitations

- The cache is in-memory and scoped to the lifetime of the wrapper process. Restarting the wrapper clears all cached responses.
- `seek_result` is not forwarded to the upstream server — it is handled entirely by the wrapper.
- Structured content (`structuredContent` field) is always forwarded unchanged and is never cached.
