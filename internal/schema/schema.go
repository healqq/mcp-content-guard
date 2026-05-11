package schema

import (
	"encoding/json"
	"fmt"
)

var seekResultTool = json.RawMessage(`{
  "name": "seek_result",
  "description": "Retrieve content from a cached tool response. When a tool response was too large to return directly, you receive a stub message with an id. Call this tool with that id to get the content. Omit filter to get the full payload, or use a filter to extract a subset.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "id":     { "type": "string" },
      "filter": { "type": "string", "description": "Optional. How to extract content. Omit (or pass empty string) to return the full payload. Options: jq expression (e.g. '.[0].text'); 'grep:<pattern>' to return matching lines; 'head:<N>' for first N lines; 'tail:<N>' for last N lines; 'line:<N>' for a single 0-based line index." }
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
