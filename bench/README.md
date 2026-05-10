# mcp-context-guard benchmark suite

Measures the real-world impact of `mcp-context-guard` across four MCP server scenarios: not just byte savings, but whether the model still answers correctly when forced to use `seek_result` with jq/grep filters instead of receiving full payloads directly.

Each scenario runs the same task twice — once against the MCP server directly ("direct"), and once routed through `mcp-context-guard` ("proxied") — and compares token usage and task correctness.

## What is measured

- **Token savings**: total prompt + completion tokens consumed across all turns (reported by OpenAI)
- **Task correctness**: whether the model's final answer passes the rubric assertion
- **Turn count**: how many tool-call rounds were needed to complete the task

The proxy intercepts large tool responses (above `--threshold` bytes), caches them, and returns a human-readable stub: `[Response too large to return (N bytes cached). Call seek_result(id="...", filter="...") to retrieve specific content.]`. The model must then call `seek_result` with a jq expression or grep pattern to extract the relevant data. This forces the model to be precise about what it needs rather than receiving entire large payloads.

## Requirements

- **Go** — to build `mcp-context-guard` (`go build -o mcp-context-guard.exe .` from repo root)
- **Node.js >= 22.22.0** + **npm** — for promptfoo and MCP servers
- **Python 3.8+** — to generate test data (SQLite DB, JSON, logs, HTML)
- **uv/uvx** — for the git scenario (`pip install uv` or `brew install uv`)
- **Chromium** — for the playwright scenario (auto-installed by `@playwright/mcp`)
- **`OPENAI_API_KEY`** environment variable set to a valid OpenAI key

## Setup

```bash
# 1. Build the proxy binary (from repo root)
go build -o mcp-context-guard.exe .

# 2. Run setup (generates data, clones React repo, installs npm deps)
cd bench
bash setup.sh
```

`setup.sh` does three things:
1. Runs `data/gen_db.py` to create `data/bench.db` (SQLite with 200 customers, 100 products, 500 orders)
2. Clones React v18.2.0 at depth 300 into `data/react-repo/` (skipped if already present)
3. Runs `npm install` in `eval/`

The static data files (`data/codebase/data/products.json`, `data/codebase/logs/app.log`, `data/webapp/index.html`) are pre-generated and committed to the repo.

## Running evaluations

```bash
cd bench/eval
export OPENAI_API_KEY=sk-...

# Run individual scenarios
npx promptfoo eval -c sqlite.yaml      --output results-sqlite.json
npx promptfoo eval -c filesystem.yaml  --output results-filesystem.json
npx promptfoo eval -c playwright.yaml  --output results-playwright.json
npx promptfoo eval -c git.yaml         --output results-git.json

# Or use npm scripts
npm run eval:sqlite
npm run eval:filesystem
npm run eval:playwright
npm run eval:git

# Compare all results
node compare.js results-*.json
```

## Scenarios

### SQLite (`sqlite.yaml`)
MCP server: `@modelcontextprotocol/server-sqlite` pointing at `data/bench.db`

Note: smart models use efficient SQL (`SELECT WHERE`, `COUNT(*)`) so proxy savings here
are lower than other scenarios — the bench still verifies correctness under the proxied path.

Tasks (all deterministic answers):
- Customer email for id=42 → `customer042@example.com`
- Status of order id=409 → `cancelled`
- Order id with amount exactly $265.99 → `296`
- Count of products in the Electronics category → `20`
- Count of orders placed in February 2024 → `29`

### Filesystem (`filesystem.yaml`)
MCP server: `@modelcontextprotocol/server-filesystem` pointing at `data/codebase/`

`products.json` is NDJSON (one JSON object per line, ~35 KB) so `grep:PROD-XXXX` returns
the full product record. `app.log` has one complete record per line (~46 KB). Both files
exceed the threshold on every `read_file` call.

Tasks:
- Price of `PROD-0042` in `products.json` → `$96.99`
- Most expensive product in `products.json` → `PROD-0142` at `$199.99`
- Count of `electronics` products in `products.json` → `40`
- Log level for `req-0237` in `app.log` → `WARN`
- Count of `ERROR` entries in `app.log` → `50`

### Playwright (`playwright.yaml`)
MCP server: `@playwright/mcp` in headless mode, navigating a local interactive HTML file

The page has a summary bar, Status/Region dropdown filters (JS-powered), and a sortable
Amount column. `browser_snapshot()` of the full 300-row table (~75 KB) exceeds the threshold.
Tasks test five different capabilities — not all the same column lookup.

Amount formula uses `((i * 97) % 4900) + 100` so the sort order differs from table order.

Tasks:
- Read the total revenue from the summary bar → `$763,950.00`
- Apply Status filter → Cancelled, read first visible row → `ORD-0004`
- Apply Region filter → APAC, read the shown-count → `75`
- Click Amount header to sort descending, read first row → `ORD-0101`
- Single-row baseline: status of `ORD-0182` → `Shipped`

### Git (`git.yaml`)
MCP server: `mcp-server-git` (via uvx) pointing at `data/react-repo/` (React v18.2.0)

`git_log(max_count=300)` returns ~300 commits which reliably exceeds the threshold.
Assertions use flexible rubrics (format/plausibility checks) since exact git history
values depend on the cloned repo at runtime.

Tasks:
- Message of the most recent commit (plausible commit message format)
- Count of commits mentioning `fix` in 300 commits (integer > 5)
- Author of the oldest commit in the 300-commit window (person's name format)
- Message of the 10th most recent commit (plausible commit message format)
- Hash of first commit by `Andrew Clark`, or `"not-found"` (hex string or sentinel)

Git log responses for 300 commits are large and will trigger caching.

## Interpreting results

After running `node compare.js results-*.json`, you'll see a table comparing:

| Metric | direct | proxied | delta |
|---|---|---|---|
| pass rate | % | % | |
| avg prompt tokens | N | N | -X% |
| avg total tokens | N | N | -X% |
| avg turns | N | N | |

A good outcome is: similar or equal pass rate with meaningfully fewer prompt tokens in the proxied column. If pass rate drops significantly, the model may be struggling to write effective jq/grep filters for this task — consider adjusting the threshold or reviewing `seek_result` filter guidance.

## File structure

```
bench/
├── README.md              # this file
├── setup.sh               # one-shot setup script
├── data/
│   ├── gen_db.py          # generates bench.db
│   ├── bench.db           # (gitignored) SQLite test database
│   ├── codebase/
│   │   ├── data/
│   │   │   └── products.json   # 200 products
│   │   └── logs/
│   │       └── app.log         # 500 log entries
│   ├── webapp/
│   │   └── index.html          # 300-row orders table
│   └── react-repo/        # (gitignored) cloned React v18.2.0
└── eval/
    ├── provider.js         # custom promptfoo provider (OpenAI + MCP agentic loop)
    ├── compare.js          # result comparison script
    ├── sqlite.yaml         # SQLite scenario
    ├── filesystem.yaml     # filesystem scenario
    ├── playwright.yaml     # playwright scenario
    ├── git.yaml            # git scenario
    ├── test-one.yaml       # smoke test against mock upstream
    ├── package.json
    └── .gitignore          # excludes node_modules/
```
