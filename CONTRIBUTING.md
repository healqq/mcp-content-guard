# Contributing to mcp-context-guard

Thanks for taking an interest. This guide covers the dev loop: prerequisites, build, test, lint, and the benchmark suite. End-user install/usage lives in [README.md](README.md); high-level architecture in [CLAUDE.md](CLAUDE.md).

## Prerequisites

- **Go 1.26+** — the repo's `go.mod` pins a recent toolchain; older versions will fail to build.
- **golangci-lint** — for the lint step (pre-commit hook depends on it):
  ```bash
  go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
  ```
- **Git** — that's it for core development.

The bench suite has additional dependencies (Node.js, Python, uv, an OpenAI API key) — see [`bench/README.md`](bench/README.md).

## Build

```bash
# Native build
go build -o mcp-context-guard.exe .

# Cross-compile
GOOS=linux GOARCH=arm64 go build -o mcp-context-guard-linux-arm64 .
```

The binary is fully static — no cgo, no runtime dependencies.

## Test

```bash
# Run everything (unit + integration + httpserver)
go test -timeout 60s ./...

# Single test
go test -v -run TestLRUEvictsOldest ./internal/cache

# Run the fuzzers for a few seconds each
go test -fuzz=FuzzApply -fuzztime=10s ./internal/filter
go test -fuzz=FuzzInjectSeekResult -fuzztime=10s ./internal/schema
```

The integration tests in `proxy_test.go` spawn the compiled binary against a Python mock upstream. They are skipped automatically if `python` is not on `PATH`. Build the binary (`go build`) before running them — they look for `./mcp-context-guard.exe` (or `./mcp-context-guard` on non-Windows).

The httpserver tests in `internal/httpserver/server_test.go` use `httptest` and run hermetically.

## Lint

```bash
golangci-lint run . ./internal/...
```

Configured linters: `errcheck`, `gosec`, `gosimple`, `govet` (all checks except `fieldalignment` and `shadow`), `ineffassign`, `staticcheck`, `unused`. Settings in `.golangci.yml`.

If you need to suppress a finding, use an inline `//nolint:<linter> // <reason>` rather than disabling the linter globally.

### Pre-commit hook

The repo ships a hook that runs `golangci-lint` on staged Go files. Install it once per clone:

```bash
sh scripts/install-hooks.sh
```

The hook blocks the commit on any lint issue. Fix the warning or attach an inline `//nolint` with a justification rather than skipping the hook.

## Project layout

```
.
├── main.go                      CLI entry point (flag parsing, mode dispatch)
├── proxy_test.go                Local-mode integration test (spawns binary + Python mock)
├── internal/
│   ├── proxy/                   stdio framing for local mode
│   ├── httpserver/              Streamable HTTP + OAuth passthrough
│   ├── intercept/               shared cache-stub / seek_result logic
│   ├── cache/                   bounded LRU
│   ├── filter/                  jq / grep / head / tail / line DSL
│   ├── rpc/                     JSON-RPC 2.0 message types
│   ├── schema/                  seek_result tool injection
│   └── stats/                   optional per-upstream counters
├── bench/                       benchmark suite (see bench/README.md)
└── scripts/                     install-hooks.sh, etc.
```

`CLAUDE.md` is the architectural source of truth — when you change interfaces, message-handling rules, or invariants, update it in the same commit.

## Benchmark suite

The bench suite (`bench/`) runs each scenario twice — once direct, once through the proxy — and reports tokens, turns, and pass rate. See [`bench/README.md`](bench/README.md) for setup. Latest results in [`bench/REPORT.md`](bench/REPORT.md).

Rerun if you change anything in the hot path (filter DSL, stub message, `seek_result` tool description, caching threshold). A typical full run is a few minutes per scenario.

## Pull requests

- One logical change per PR. If the work is large, stack PRs and ask for review on the bottom of the stack first.
- Keep commit messages explanatory (the *why*, not the *what*). Recent history (`git log --oneline -20`) is a good style reference.
- Both `go test ./...` and `golangci-lint run` must pass before requesting review.
- When you change user-visible behaviour, update `README.md` in the same PR.
- When you change internal architecture or invariants, update `CLAUDE.md` in the same PR.
- Don't disable `golangci-lint` rules globally — use targeted `//nolint:<rule> // <reason>` comments.

CI (GitHub Actions, `.github/workflows/ci.yml`) re-runs lint and tests on every PR; merges to `main` are squash-only.

## Releasing

Releases are driven by `goreleaser` via `.github/workflows/release.yml` on tag push. Don't tag from your fork — open a PR and let a maintainer cut the release.
