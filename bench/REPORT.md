# Benchmark Report

**Date:** 2026-05-11  
**Model:** gpt-5.4-mini-2026-03-17  
**Threshold:** 10 240 bytes  
**Suite:** filesystem MCP server (`@modelcontextprotocol/server-filesystem`)  
**Tool:** [promptfoo](https://promptfoo.dev) — see [`eval/filesystem.yaml`](eval/filesystem.yaml)

---

## Summary

| | Direct | Proxied |
|---|---|---|
| Tasks | 5 / 5 passed | 5 / 5 passed |
| Total prompt tokens | 80 044 | 33 737 |
| Token reduction | — | **58 %** |

The proxy intercepts every `read_file` response (both test files exceed the 10 KB threshold), caches the payload, and returns a stub. The model then calls `seek_result` with a filter to extract only the bytes it needs.

---

## Per-task results

### products.json (35 KB, 200 products)

| Task | Provider | Turns | Tokens | Filters used |
|---|---|---|---|---|
| Price of PROD-0042 | direct | 2 | 13 725 | — |
| Price of PROD-0042 | proxied | 3 | 4 807 | `grep:PROD-0042` |
| Most expensive product | direct | 2 | 13 745 | — |
| Most expensive product | proxied | 4 | 10 865 | `head:40` → `grep:"price":199.99` |
| Count of electronics | direct | 2 | 13 700 | — |
| Count of electronics | proxied | 3 | 6 931 | `grep:"category":"electronics"` |

### app.log (46 KB, 500 log lines)

| Task | Provider | Turns | Tokens | Filters used |
|---|---|---|---|---|
| Log level for req-0237 | direct | 2 | 19 457 | — |
| Log level for req-0237 | proxied | 3 | 4 788 | `grep:req-0237` |
| Count of ERROR entries | direct | 2 | 19 417 | — |
| Count of ERROR entries | proxied | 3 | 6 346 | `grep:ERROR` |

---

## Filter strategy observed

The model consistently chose appropriate filters without any prompt engineering:

- **grep** — used for all lookup and counting tasks; one call usually sufficed.
- **head / tail** — used as a reconnaissance step before grepping when the data structure was unfamiliar.
- **jq** — attempted for aggregate queries (e.g. `max_by(.price)`); the model wrote standard shell-style `jq 'expr'` syntax which the proxy now normalises automatically.

No task required more than one `seek_result` call to obtain a correct answer, except "most expensive product" where the model used a two-step grep strategy (head to inspect structure, then grep for the known price).

---

## Notes

- Direct mode reads the entire file into context in a single tool call, which accounts for the large prompt token counts.
- Proxied mode costs one extra turn per task (stub → seek_result), but prompt tokens are dominated by the filter result rather than the full file.
- The `seek_result` cache persists for the session; repeated queries against the same id cost nothing extra.
- `jq` expressions operate on the parsed JSON content of text blocks, so standard jq queries against JSON files work without any wrapper syntax.
