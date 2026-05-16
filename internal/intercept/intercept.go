// Package intercept holds the logic that is identical in both transports:
// turning a large tools/call response into a cached stub, and answering
// a seek_result tool call from the cache.
package intercept

import (
	"encoding/json"
	"fmt"

	"mcp-context-guard/internal/cache"
	"mcp-context-guard/internal/filter"
	"mcp-context-guard/internal/rpc"
	"mcp-context-guard/internal/stats"
)

// MaybeCache inspects a tools/call response. If the content payload exceeds
// threshold, it is cached and a replacement response carrying a stub message
// is returned. Returns nil to mean "forward msg unchanged".
//
// isError responses are never cached. structuredContent is left untouched
// regardless of size (it is forwarded by the caller).
func MaybeCache(c *cache.Cache, st *stats.Stats, threshold int64, msg rpc.Message) []byte {
	var result map[string]json.RawMessage
	if err := json.Unmarshal(msg.Result, &result); err != nil {
		return nil
	}
	if raw, ok := result["isError"]; ok {
		var isErr bool
		if json.Unmarshal(raw, &isErr) == nil && isErr {
			return nil
		}
	}
	contentRaw, ok := result["content"]
	if !ok || int64(len(contentRaw)) < threshold {
		return nil
	}
	cacheID, err := c.Store(contentRaw)
	if err != nil {
		return nil
	}
	stubText := fmt.Sprintf(
		"[Response too large to return (%d bytes cached, id=%q). Call seek_result(id) to get the full payload, or seek_result(id, filter) to extract a subset (jq expression, grep:<pattern>, head:<N>, tail:<N>, line:<N>).]",
		len(contentRaw), cacheID,
	)
	contentJSON, err := json.Marshal([]map[string]string{
		{"type": "text", "text": stubText},
	})
	if err != nil {
		return nil
	}
	result["content"] = contentJSON
	msg.Result, err = json.Marshal(result)
	if err != nil {
		return nil
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return nil
	}
	st.RecordCache(int64(len(contentRaw)), int64(len(stubText)))
	return b
}

// SeekResult looks up id in the cache and applies the (optional) filter.
// Returns a fully-encoded JSON-RPC response (either text or error) — never nil.
func SeekResult(c *cache.Cache, st *stats.Stats, req rpc.Message, id, filterExpr string) []byte {
	if id == "" {
		return rpc.EncodeErrorResult(req.ID, "seek_result: id is required")
	}
	content, ok := c.Get(id)
	if !ok {
		return rpc.EncodeErrorResult(req.ID, fmt.Sprintf("no cached result with id %q", id))
	}
	result, err := filter.Apply(content, filterExpr)
	if err != nil {
		return rpc.EncodeErrorResult(req.ID, fmt.Sprintf("filter error: %s", err))
	}
	st.RecordSeek()
	return rpc.EncodeTextResult(req.ID, result)
}
