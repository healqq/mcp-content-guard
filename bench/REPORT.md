# Benchmark Report

**Date:** 2026-05-16
**Model:** gpt-5.4-mini-2026-03-17
**Threshold:** 10 240 bytes
**Concurrency:** 1 (serial — playwright was previously flaky under parallel runs because instances raced on the browser lock)
**Evaluation tool:** [promptfoo](https://promptfoo.dev)
**Notable change since the previous report:** the `seek_result` tool description was rewritten to teach the model when to reach for jq vs grep and to suggest filter patterns for accessibility-tree snapshots. The per-response stub was shortened in exchange (filter syntax now lives in the tool schema, not the stub).

---

## Summary

| Suite | MCP server | Direct tokens | Proxied tokens | Δ tokens | Direct pass | Proxied pass |
|---|---|---:|---:|---:|:---:|:---:|
| [SQLite](#sqlite) | `mcp-server-sqlite` (small query results) | 7 315 | 18 014 | **+146 %** | 5 / 5 | 5 / 5 |
| [Filesystem](#filesystem) | `@modelcontextprotocol/server-filesystem` (35–46 KB files) | 80 044 | 54 826 | **−31 %** | 4 / 5 | 5 / 5 |
| [Git](#git) | `mcp-server-git` (~120 KB log) | 219 105 | 103 663 | **−53 %** | 5 / 5 | 5 / 5 |
| [Playwright](#playwright) | `@playwright/mcp` (~75 KB snapshots) | 385 462 | 130 041 | **−66 %** | 5 / 5 | 5 / 5 |
| **Combined** | | **691 926** | **306 544** | **−56 %** | 19 / 20 | **20 / 20** |

**Headline:** the proxy now passes every task across all four suites while spending 56 % fewer tokens overall. The filesystem suite even shows proxied beating direct on correctness (direct miscounted log entries on one task).

**Cost:** SQLite is now firmly net-negative on tokens (+146 %, was +27 %). The richer `seek_result` description that fixed the playwright failures gets paid on every turn, and SQLite never actually caches anything (query results fit under the threshold). If you run an MCP server whose responses are reliably small, run it without the proxy.

---

## SQLite

**Config:** [`eval/sqlite.yaml`](eval/sqlite.yaml)
**Data:** SQLite database with orders, customers, and products tables (~500 rows each)

| Task | Direct turns | Direct tokens | Proxied turns | Proxied tokens | Δ | Result |
|---|:---:|---:|:---:|---:|---:|:---:|
| customer email by id | 2 | 687 | 4 | 3 936 | +473 % | ✓ / ✓ |
| order status lookup | 4 | 1 970 | 2 | 1 739 | −12 % | ✓ / ✓ |
| find order by exact amount | 4 | 1 993 | 4 | 4 109 | +106 % | ✓ / ✓ |
| product category count | 4 | 1 901 | 4 | 4 017 | +111 % | ✓ / ✓ |
| orders in february 2024 | 2 | 764 | 4 | 4 213 | +451 % | ✓ / ✓ |
| **Total** | | **7 315** | | **18 014** | **+146 %** | 5/5 / 5/5 |

Query results are well under the 10 KB threshold — the proxy never caches anything. The overhead is the injected `seek_result` tool definition, sent in `tools/list` and present in every subsequent prompt. With the new (longer) tool description this overhead grew. Correctness is unaffected.

---

## Filesystem

**Config:** [`eval/filesystem.yaml`](eval/filesystem.yaml)
**Data:** 35 KB JSON product catalogue (200 records), 46 KB log file (500 lines)

| Task | Direct turns | Direct tokens | Proxied turns | Proxied tokens | Δ | Result |
|---|:---:|---:|:---:|---:|---:|:---:|
| product price lookup | 2 | 13 725 | 3 | 5 835 | −57 % | ✓ / ✓ |
| product most expensive | 2 | 13 745 | 5 | 21 573 | +57 % | ✓ / ✓ |
| electronics category count | 2 | 13 700 | 4 | 10 052 | −27 % | ✓ / ✓ |
| log level for request | 2 | 19 457 | 3 | 5 848 | −70 % | ✓ / ✓ |
| error entry count | 2 | 19 417 | 5 | 11 518 | −41 % | **✗ / ✓** |
| **Total** | | **80 044** | | **54 826** | **−31 %** | 4/5 / 5/5 |

**Direct failure (error entry count):** direct returned `100` after reading the 46 KB log in one shot; the correct answer required `grep:ERROR | wc -l` semantics. The proxied path was forced to use `grep:ERROR` via `seek_result`, which produced the right count. Forcing extraction made the model more precise.

**"product most expensive" regression (+57 %):** the model needed multiple `seek_result` rounds to scan the catalogue (`head:40` then targeted greps). The full payload fit in a single direct read.

---

## Git

**Config:** [`eval/git.yaml`](eval/git.yaml)
**Data:** React v18.2.0 repository, `git_log(max_count=300)` returns ~300 commit records (~120 KB)

| Task | Direct turns | Direct tokens | Proxied turns | Proxied tokens | Δ | Result |
|---|:---:|---:|:---:|---:|---:|:---:|
| most recent commit message | 3 | 3 140 | 3 | 4 727 | +51 % | ✓ / ✓ |
| fix commits count | 3 | 53 964 | 5 | 67 267 | +25 % | ✓ / ✓ |
| oldest commit author in window | 3 | 53 974 | 4 | 7 162 | **−87 %** | ✓ / ✓ |
| 10th commit message | 3 | 53 985 | 4 | 6 411 | **−88 %** | ✓ / ✓ |
| author search by name | 3 | 54 042 | 6 | 18 096 | −67 % | ✓ / ✓ |
| **Total** | | **219 105** | | **103 663** | **−53 %** | 5/5 / 5/5 |

**"oldest commit author" went from FAIL → PASS** since the previous report — bumping `maxTurns` to 15 (from 8) lets the model navigate to the end of the log with `tail`.

**"fix commits count" regression (+25 %):** the model issued `grep:fix` then took extra turns reasoning about case sensitivity and the regex it should use. Direct just read the whole log and grep'd locally in its head.

---

## Playwright

**Config:** [`eval/playwright.yaml`](eval/playwright.yaml)
**Data:** 300-row interactive orders table rendered as a web app. `browser_snapshot` returns ~75 KB of page content per call.

| Task | Direct turns | Direct tokens | Proxied turns | Proxied tokens | Δ | Result |
|---|:---:|---:|:---:|---:|---:|:---:|
| read summary stat | 3 | 6 992 | 4 | 11 431 | +63 % | ✓ / ✓ |
| filter by status, first visible row | 5 | 255 905 | 8 | 29 915 | **−88 %** | ✓ / ✓ |
| filter by region, count results | 5 | 13 052 | 11 | 35 125 | +169 % | ✓ / ✓ |
| sort by amount, read first row | 4 | 56 071 | 5 | 38 979 | −30 % | ✓ / ✓ |
| single row lookup baseline | 3 | 53 442 | 5 | 14 591 | **−73 %** | ✓ / ✓ |
| **Total** | | **385 462** | | **130 041** | **−66 %** | 5/5 / 5/5 |

**Pass rate jumped from 3/5 to 5/5** since the previous report. The drivers:

- **`maxTurns` bumped to 15** — the sort task takes more turns to click + re-snapshot + extract.
- **`seek_result` tool description rewritten** to nudge the model toward jq selectors on accessibility-tree snapshots and to broaden filters when the first attempt returns nothing. Previously the model fell back to `browser_evaluate(JS)` after one or two empty grep results, which over-counted on the APAC test (counted the dropdown label *plus* the rows). Now it stays on `seek_result` and finds the "Showing 75 of 300" counter cleanly.

**"filter by region, count results" regression (+169 %):** the proxied path used 11 turns of `seek_result` exploration to find the counter line; direct just read the snapshot once. Still passes — the budget is just higher.
