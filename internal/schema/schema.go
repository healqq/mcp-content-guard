package schema

import (
	"encoding/json"
	"fmt"
)

var seekResultTool = json.RawMessage(`{
  "name": "seek_result",
  "description": "Retrieve content from a previously cached tool response. When an upstream tool returned a payload too large to inline, the response you saw was a short stub like '[cached id=\"abc123\" (12345 bytes)]'. Call seek_result with that id (and optionally a filter) to read the cached payload.\n\nStrategy:\n  - Prefer a targeted filter over fetching the whole payload — that's the whole point of caching. Only omit filter when the payload is small or you genuinely need everything.\n  - If the cached payload is JSON-shaped (API responses, structured tool output, accessibility-tree snapshots from browser tools), a jq expression is almost always the right tool. Examples: '.[0].text' for the first content block; '..|select(.role?==\"button\" and .name?==\"Submit\")' to find a node in a deep tree; '.results|length' for a count.\n  - For free-form text (logs, source code, READMEs), 'grep:<regex>' returns just the matching lines.\n  - For a quick peek at structure, 'head:20' or 'line:0' is usually enough to decide what jq/grep to run next.\n  - If your first filter returns nothing, broaden it (drop trailing spaces, use a shorter prefix, switch grep ↔ jq) before falling back to fetching the full payload or using a different tool.\n\nThe id is opaque — pass it verbatim, including any quotes shown in the stub. If you misremember an id you'll get a 'no cached result' error; reopen the original tool call to get a fresh id.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "id":     { "type": "string", "description": "The cache id from the stub message of a prior tool response." },
      "filter": { "type": "string", "description": "Optional extractor. Omit (or pass empty string) for the full payload. Supported forms:\n  - jq expression — any valid jq, run against the cached payload. If the payload is a JSON string (e.g. an MCP text block whose body is JSON), it is parsed first so you can write '.users[0].id' directly. Examples: 'length', '.[0]', 'map(.name)', '..|select(.type?==\"text\")'.\n  - 'grep:<regex>' — Go regexp, matched per line. Example: 'grep:^ERROR'.\n  - 'head:<N>' / 'tail:<N>' — first/last N lines.\n  - 'line:<N>' — the line at 0-based index N.\nA bare jq expression (no prefix) is treated as jq; only use the prefixes for grep/head/tail/line." }
    },
    "required": ["id"]
  }
}`)

// InjectSeekResult appends the seek_result tool to a tools/list result payload.
func InjectSeekResult(result json.RawMessage) (json.RawMessage, error) {
	var r map[string]json.RawMessage
	if err := json.Unmarshal(result, &r); err != nil {
		return nil, fmt.Errorf("parse tools/list result: %w", err)
	}
	if r == nil {
		// JSON "null" unmarshals to a nil map; treat it as empty so we can
		// still attach the seek_result tool.
		r = make(map[string]json.RawMessage)
	}
	var tools []json.RawMessage
	if raw, ok := r["tools"]; ok {
		if err := json.Unmarshal(raw, &tools); err != nil {
			return nil, fmt.Errorf("parse tools array: %w", err)
		}
	}
	tools = append(tools, seekResultTool)
	toolsJSON, err := json.Marshal(tools)
	if err != nil {
		return nil, err
	}
	r["tools"] = toolsJSON
	return json.Marshal(r)
}
