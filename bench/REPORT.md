# Benchmark Report

**Date:** 2026-05-11  
**Model:** gpt-5.4-mini-2026-03-17  
**Threshold:** 10 240 bytes  
**Evaluation tool:** [promptfoo](https://promptfoo.dev)

---

## Summary

| Suite | MCP server | Direct tokens | Proxied tokens | Savings | Direct pass | Proxied pass |
|---|---|---:|---:|---:|:---:|:---:|
| [Filesystem](#filesystem) | `@modelcontextprotocol/server-filesystem` | 79 812 | 33 249 | **−58 %** | 5 / 5 | 5 / 5 |
| [Git](#git) | `mcp-server-git` (300 commits, ~120 KB) | 218 704 | 74 285 | **−66 %** | 5 / 5 | 4 / 5 |
| [SQLite](#sqlite) | `mcp-server-sqlite` (small query results) | 8 140 | 10 300 | **+27 %** | 5 / 5 | 5 / 5 |
| [Playwright](#playwright) | `@playwright/mcp` (75 KB page snapshots) | 640 708 | 87 685 | **−86 %** | 5 / 5 | 3 / 5 |

The proxy pays off whenever tool responses exceed the threshold. SQLite is the counter-example: SQL query results are small and never cached, so the proxy is net-negative (extra tokens from the injected `seek_result` tool schema). Setting a higher threshold or disabling the proxy for known small-response servers avoids this.

---

## Filesystem

**Config:** [`eval/filesystem.yaml`](eval/filesystem.yaml)  
**Data:** 35 KB JSON product catalogue (200 records), 46 KB log file (500 lines)

| Task | Direct turns | Direct tokens | Proxied turns | Proxied tokens | Diff | Filters used |
|---|:---:|---:|:---:|---:|---:|---|
| product price lookup | 2 | 13 678 | 3 | 4 713 | −66 % | `grep:PROD-0042` |
| product most expensive | 2 | 13 692 | 4 | 10 726 | −22 % | `head:40` → `grep:"price":199.99` |
| electronics category count | 2 | 13 656 | 3 | 6 842 | −50 % | `grep:"category":"electronics"` |
| log level for request | 2 | 19 413 | 3 | 4 705 | −76 % | `grep:req-0237` |
| error entry count | 2 | 19 373 | 3 | 6 263 | −68 % | `grep:ERROR` |
| **Total** | | **79 812** | | **33 249** | **−58 %** | |

The model consistently reached for `grep` on lookup and counting tasks. For "most expensive product" it combined `head` (to inspect structure) with a targeted grep, completing in 4 turns total.

---

## Git

**Config:** [`eval/git.yaml`](eval/git.yaml)  
**Data:** React v18.2.0 repository, `git_log(max_count=300)` returns ~300 commit records (~120 KB)

| Task | Direct turns | Direct tokens | Proxied turns | Proxied tokens | Diff | Result |
|---|:---:|---:|:---:|---:|---:|:---:|
| most recent commit message | 3 | 3 030 | 3 | 3 408 | +13 % | ✓ / ✓ |
| fix commits count | 3 | 53 890 | 5 | 40 466 | −25 % | ✓ / ✓ |
| oldest commit author in window | 3 | 53 905 | 6 | 10 106 | — | ✓ / ✗ |
| 10th commit message | 3 | 53 908 | 4 | 4 707 | −91 % | ✓ / ✓ |
| author search by name | 3 | 53 971 | 6 | 15 598 | −71 % | ✓ / ✓ |
| **Total** | | **218 704** | | **74 285** | **−66 %** | 5/5 / 4/5 |

**"most recent commit message" +13 %:** the first commit is at the top of the log and is small enough that the model reads it directly from `head:5`, but the extra `seek_result` tool in the schema adds a constant token overhead on every turn.

**"oldest commit author" FAIL:** the oldest commit is at the end of a 120 KB log. The model used `tail` to find it but exhausted the 6-turn limit before answering. Increasing `maxTurns` would likely fix this.

---

## SQLite

**Config:** [`eval/sqlite.yaml`](eval/sqlite.yaml)  
**Data:** SQLite database with orders, customers, and products tables (~500 rows each)

| Task | Direct turns | Direct tokens | Proxied turns | Proxied tokens | Diff | Result |
|---|:---:|---:|:---:|---:|---:|:---:|
| customer email by id | 4 | 1 756 | 4 | 2 124 | +21 % | ✓ / ✓ |
| order status lookup | 4 | 1 908 | 4 | 2 276 | +19 % | ✓ / ✓ |
| find order by exact amount | 4 | 1 927 | 4 | 2 295 | +19 % | ✓ / ✓ |
| product category count | 4 | 1 836 | 4 | 2 204 | +20 % | ✓ / ✓ |
| orders in february 2024 | 2 | 713 | 3 | 1 401 | +97 % | ✓ / ✓ |
| **Total** | | **8 140** | | **10 300** | **+27 %** | 5/5 / 5/5 |

SQL responses fit well under the 10 KB threshold — the proxy never caches anything. The overhead comes entirely from the injected `seek_result` tool definition appearing in every API call. Correctness is unaffected; this suite confirms the proxy does not break small-response servers.

---

## Playwright

**Config:** [`eval/playwright.yaml`](eval/playwright.yaml)  
**Data:** 300-row interactive orders table rendered as a web app. `browser_snapshot` returns ~75 KB of page content per call.

| Task | Direct turns | Direct tokens | Proxied turns | Proxied tokens | Diff | Result |
|---|:---:|---:|:---:|---:|---:|:---:|
| read summary stat | 3 | 19 471 | 4 | 9 609 | −51 % | ✓ / ✓ |
| filter by status, first visible row | 5 | 255 796 | 4 | 12 860 | −95 % | ✓ / ✓ |
| filter by region, count results | 5 | 256 558 | 8 | 21 212 | — | ✓ / ✗ |
| sort by amount, read first row | 4 | 89 424 | 7 | 34 371 | — | ✓ / ✗ |
| single row lookup baseline | 3 | 19 459 | 4 | 9 633 | −51 % | ✓ / ✓ |
| **Total** | | **640 708** | | **87 685** | **−86 %** | 5/5 / 3/5 |

Playwright snapshots are the largest payloads tested. Even with two failures the proxied total is 86 % lower. The two failures ("filter by region, count results" and "sort by amount") both hit the 8-turn limit — the model needed more iterations to interact with the page after receiving a stub. Increasing `maxTurns` to 12 would likely resolve both.
