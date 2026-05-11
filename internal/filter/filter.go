package filter

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/itchyny/gojq"
)

type lineMode int

const (
	modeHead lineMode = iota
	modeTail
	modeLine
)

// Apply runs filterExpr against content (a JSON-encoded MCP content array).
// Supported prefixes:
//   - ""                — no filter: return full concatenated text
//   - "grep:<pattern>"  — lines matching the regex pattern
//   - "head:<N>"        — first N lines
//   - "tail:<N>"        — last N lines
//   - "line:<N>"        — single line at 0-based index N
//
// Anything else is treated as a jq expression applied to the raw content array.
func Apply(content json.RawMessage, filterExpr string) (string, error) {
	filterExpr = stripJQShellSyntax(filterExpr)

	switch {
	case filterExpr == "":
		return applyFull(content)
	case strings.HasPrefix(filterExpr, "grep:"):
		return applyGrep(content, filterExpr[5:])
	case strings.HasPrefix(filterExpr, "head:"):
		return applyLines(content, filterExpr[5:], modeHead)
	case strings.HasPrefix(filterExpr, "tail:"):
		return applyLines(content, filterExpr[5:], modeTail)
	case strings.HasPrefix(filterExpr, "line:"):
		return applyLines(content, filterExpr[5:], modeLine)
	default:
		return applyJQ(content, filterExpr)
	}
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// stripJQShellSyntax removes the shell-style `jq 'expr'` or `jq "expr"` wrapper
// that models commonly write when they mean just the jq expression.
func stripJQShellSyntax(expr string) string {
	s := strings.TrimSpace(expr)
	if !strings.HasPrefix(s, "jq ") {
		return expr
	}
	s = strings.TrimSpace(s[3:])
	if len(s) >= 2 && ((s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"')) {
		s = s[1 : len(s)-1]
	}
	return s
}

func applyFull(content json.RawMessage) (string, error) {
	var blocks []contentBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return "", fmt.Errorf("content is not a ContentBlock array: %w", err)
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n"), nil
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

func applyLines(content json.RawMessage, nStr string, mode lineMode) (string, error) {
	n, err := strconv.Atoi(strings.TrimSpace(nStr))
	if err != nil || n < 0 {
		return "", fmt.Errorf("invalid line count %q: must be a non-negative integer", nStr)
	}
	var blocks []contentBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return "", fmt.Errorf("content is not a ContentBlock array: %w", err)
	}
	var all []string
	for _, b := range blocks {
		if b.Type == "text" {
			all = append(all, strings.Split(b.Text, "\n")...)
		}
	}
	switch mode {
	case modeHead:
		if n > len(all) {
			n = len(all)
		}
		return strings.Join(all[:n], "\n"), nil
	case modeTail:
		if n > len(all) {
			n = len(all)
		}
		return strings.Join(all[len(all)-n:], "\n"), nil
	case modeLine:
		if n >= len(all) {
			return "", fmt.Errorf("line %d out of range (content has %d lines)", n, len(all))
		}
		return all[n], nil
	}
	return "", nil
}

func applyJQ(content json.RawMessage, expr string) (string, error) {
	q, err := gojq.Parse(expr)
	if err != nil {
		return "", fmt.Errorf("invalid jq expression: %w", err)
	}

	// Unwrap MCP content blocks: if the text inside parses as JSON, use that
	// as the jq input so expressions like max_by(.price) work on the actual data.
	var input any
	var blocks []contentBlock
	if json.Unmarshal(content, &blocks) == nil {
		var text string
		for _, b := range blocks {
			if b.Type == "text" {
				text += b.Text
			}
		}
		var parsed any
		if json.Unmarshal([]byte(text), &parsed) == nil {
			input = parsed
		}
	}
	if input == nil {
		if err := json.Unmarshal(content, &input); err != nil {
			return "", fmt.Errorf("failed to parse content: %w", err)
		}
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
