package filter

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/itchyny/gojq"
)

// Apply runs filterExpr against content (a JSON-encoded MCP content array).
// Prefix "grep:" for line-based regex filtering; otherwise treated as a jq expression.
// Returns a JSON string suitable for embedding in a text ContentBlock.
func Apply(content json.RawMessage, filterExpr string) (string, error) {
	if strings.HasPrefix(filterExpr, "grep:") {
		return applyGrep(content, filterExpr[5:])
	}
	return applyJQ(content, filterExpr)
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func applyGrep(content json.RawMessage, pattern string) (string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid grep pattern: %w", err)
	}
	var blocks []contentBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return "", fmt.Errorf("content is not a ContentBlock array: %w", err)
	}
	var lines []string
	for _, b := range blocks {
		if b.Type != "text" {
			continue
		}
		for _, line := range strings.Split(b.Text, "\n") {
			if re.MatchString(line) {
				lines = append(lines, line)
			}
		}
	}
	return strings.Join(lines, "\n"), nil
}

func applyJQ(content json.RawMessage, expr string) (string, error) {
	q, err := gojq.Parse(expr)
	if err != nil {
		return "", fmt.Errorf("invalid jq expression: %w", err)
	}
	var input any
	if err := json.Unmarshal(content, &input); err != nil {
		return "", fmt.Errorf("failed to parse content: %w", err)
	}
	iter := q.Run(input)
	var results []any
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if e, ok := v.(error); ok {
			return "", fmt.Errorf("jq error: %w", e)
		}
		results = append(results, v)
	}
	var out any
	if len(results) == 1 {
		out = results[0]
	} else {
		out = results
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
