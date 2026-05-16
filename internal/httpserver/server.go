package httpserver

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"mcp-context-guard/internal/cache"
	"mcp-context-guard/internal/filter"
	"mcp-context-guard/internal/rpc"
	"mcp-context-guard/internal/schema"
	"mcp-context-guard/internal/stats"
)

// Server is an HTTP handler that proxies MCP traffic to a remote upstream,
// intercepting tool call responses for caching and injecting seek_result.
// All non-tool-call traffic (including 401s, /.well-known/*, /token, etc.)
// is forwarded unchanged, enabling the MCP client to handle OAuth itself.
// Config carries optional limits and timeouts. Zero/negative values use the
// package defaults documented on each field.
type Config struct {
	// MaxBodyBytes caps the size of an inbound client body and an upstream
	// response body. Defaults to DefaultMaxBodyBytes if <= 0.
	MaxBodyBytes int64
	// UpstreamHeaderTimeout caps how long the upstream may take to send
	// response headers. The body itself is read until EOF so SSE streams are
	// not killed mid-flight. Defaults to DefaultUpstreamHeaderTimeout.
	UpstreamHeaderTimeout time.Duration
	// UpstreamDialTimeout bounds the TCP/TLS handshake. Defaults to
	// DefaultUpstreamDialTimeout.
	UpstreamDialTimeout time.Duration
}

const (
	DefaultMaxBodyBytes          int64 = 64 << 20 // 64 MiB
	DefaultUpstreamHeaderTimeout       = 30 * time.Second
	DefaultUpstreamDialTimeout         = 10 * time.Second
)

type Server struct {
	mcpPath      string // path segment to intercept, e.g. "/mcp"
	upstream     *url.URL
	headers      map[string]string
	cache        *cache.Cache
	threshold    int64
	stats        *stats.Stats
	rp           *httputil.ReverseProxy
	client       *http.Client
	maxBodyBytes int64
}

// New creates an HTTP server that proxies to upstreamURL, applying tool call
// interception and caching. headers are added to every forwarded request.
// Pass nil cfg for defaults.
func New(upstreamURL string, headers map[string]string, c *cache.Cache, threshold int64, st *stats.Stats, cfg *Config) (*Server, error) {
	u, err := url.Parse(upstreamURL)
	if err != nil {
		return nil, fmt.Errorf("httpserver: parse upstream URL: %w", err)
	}
	if cfg == nil {
		cfg = &Config{}
	}
	maxBody := cfg.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = DefaultMaxBodyBytes
	}
	headerTimeout := cfg.UpstreamHeaderTimeout
	if headerTimeout <= 0 {
		headerTimeout = DefaultUpstreamHeaderTimeout
	}
	dialTimeout := cfg.UpstreamDialTimeout
	if dialTimeout <= 0 {
		dialTimeout = DefaultUpstreamDialTimeout
	}

	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   dialTimeout,
		ResponseHeaderTimeout: headerTimeout,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	base := &url.URL{Scheme: u.Scheme, Host: u.Host}
	rp := httputil.NewSingleHostReverseProxy(base)
	rp.Transport = transport
	rp.Director = func(req *http.Request) {
		req.URL.Scheme = base.Scheme
		req.URL.Host = base.Host
		req.Host = base.Host
		for k, v := range headers {
			req.Header.Set(k, v)
		}
	}

	return &Server{
		mcpPath:      u.Path,
		upstream:     u,
		headers:      headers,
		cache:        c,
		threshold:    threshold,
		stats:        st,
		rp:           rp,
		client:       &http.Client{Transport: transport},
		maxBodyBytes: maxBody,
	}, nil
}

// ServeHTTP routes MCP POST requests through the interception logic and
// forwards everything else transparently (including OAuth endpoints and 401s).
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && r.URL.Path == s.mcpPath &&
		strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		s.handleMCPPost(w, r)
		return
	}
	s.rp.ServeHTTP(w, r)
}

func (s *Server) handleMCPPost(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var mb *http.MaxBytesError
		if errors.As(err, &mb) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	var msg rpc.Message
	if err := json.Unmarshal(body, &msg); err != nil {
		// not valid JSON-RPC — forward transparently
		s.forwardRaw(w, r, body)
		return
	}

	// Handle seek_result locally without touching upstream.
	if msg.IsRequest() && msg.Method == "tools/call" {
		var params struct {
			Name      string `json:"name"`
			Arguments struct {
				ID     string `json:"id"`
				Filter string `json:"filter"`
			} `json:"arguments"`
		}
		if err := json.Unmarshal(msg.Params, &params); err == nil && params.Name == "seek_result" {
			s.writeJSON(w, s.handleSeekResult(msg, params.Arguments.ID, params.Arguments.Filter))
			return
		}
	}

	// Forward to upstream and intercept the response.
	upResp, upBody, err := s.doUpstream(r, body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer upResp.Body.Close()

	// Non-2xx (including 401): pass through completely unchanged.
	if upResp.StatusCode < 200 || upResp.StatusCode >= 300 {
		copyResponse(w, upResp, upBody)
		return
	}

	// Parse the upstream response as JSON-RPC.
	var respMsg rpc.Message
	if err := json.Unmarshal(upBody, &respMsg); err != nil {
		// Not JSON-RPC — forward as-is.
		copyResponse(w, upResp, upBody)
		return
	}

	if respMsg.IsResponse() {
		// Determine what method this response is to by checking the original request.
		switch msg.Method {
		case "tools/list":
			if modified, err := schema.InjectSeekResult(respMsg.Result); err == nil {
				respMsg.Result = modified
				if b, err := json.Marshal(respMsg); err == nil {
					s.writeJSON(w, b)
					return
				}
			}
		case "tools/call":
			if b := s.maybeCache(respMsg); b != nil {
				s.writeJSON(w, b)
				return
			}
		}
	}

	s.writeJSON(w, upBody)
}

// doUpstream sends the request body to the upstream MCP endpoint and returns
// the response. SSE responses are collected into a single JSON-RPC message.
func (s *Server) doUpstream(r *http.Request, body []byte) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.upstream.String(), bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	// Forward client's request headers (e.g. Authorization from OAuth).
	// Use Add (not Set) so multi-valued headers like Forwarded or
	// X-Forwarded-For survive instead of being silently collapsed to one.
	for k, vv := range r.Header {
		switch strings.ToLower(k) {
		case "content-length", "content-type", "accept", "host":
			// skip — already set above or managed by http.Client
		default:
			for _, v := range vv {
				req.Header.Add(k, v)
			}
		}
	}
	// Apply configured static headers (lower priority than client headers).
	for k, v := range s.headers {
		if req.Header.Get(k) == "" {
			req.Header.Set(k, v)
		}
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, nil, err
	}

	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "text/event-stream") {
		collected, err := collectSSE(io.LimitReader(resp.Body, s.maxBodyBytes))
		if err != nil {
			resp.Body.Close()
			return nil, nil, fmt.Errorf("read SSE: %w", err)
		}
		resp.Body.Close()
		// Replace body with a no-op closer so callers can still defer Close.
		resp.Body = io.NopCloser(bytes.NewReader(collected))
		resp.Header.Set("Content-Type", "application/json")
		return resp, collected, nil
	}

	out, err := io.ReadAll(io.LimitReader(resp.Body, s.maxBodyBytes))
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(out))
	return resp, out, err
}

// collectSSE reads a text/event-stream body and returns the last data: line
// that parses as a JSON-RPC response. Earlier lines (notifications) are discarded
// because they are not correlated to the client request.
func collectSSE(body io.Reader) ([]byte, error) {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var last []byte
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data: ") {
			data := []byte(line[6:])
			var probe rpc.Message
			if json.Unmarshal(data, &probe) == nil && probe.IsResponse() {
				last = data
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if last == nil {
		return []byte("{}"), nil
	}
	return last, nil
}

// maybeCache checks whether the tools/call response exceeds the threshold.
// Returns a replacement stub response, or nil if the original should be used.
func (s *Server) maybeCache(msg rpc.Message) []byte {
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
	if !ok || int64(len(contentRaw)) < s.threshold {
		return nil
	}
	cacheID, err := s.cache.Store(contentRaw)
	if err != nil {
		return nil
	}
	stubText := fmt.Sprintf(
		"[Response too large to return (%d bytes cached, id=%q). Call seek_result(id) to get the full payload, or seek_result(id, filter) to extract a subset (jq expression, grep:<pattern>, head:<N>, tail:<N>, line:<N>).]",
		len(contentRaw), cacheID,
	)
	contentJSON, err := json.Marshal([]map[string]string{{"type": "text", "text": stubText}})
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
	s.stats.RecordCache(int64(len(contentRaw)), int64(len(stubText)))
	return b
}

// handleSeekResult looks up a cached result and applies the filter expression.
func (s *Server) handleSeekResult(req rpc.Message, id, filterExpr string) []byte {
	if id == "" {
		return rpc.EncodeErrorResult(req.ID, "seek_result: id is required")
	}
	content, ok := s.cache.Get(id)
	if !ok {
		return rpc.EncodeErrorResult(req.ID, fmt.Sprintf("no cached result with id %q", id))
	}
	result, err := filter.Apply(content, filterExpr)
	if err != nil {
		return rpc.EncodeErrorResult(req.ID, fmt.Sprintf("filter error: %s", err))
	}
	s.stats.RecordSeek()
	return rpc.EncodeTextResult(req.ID, result)
}

func (s *Server) forwardRaw(w http.ResponseWriter, r *http.Request, body []byte) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.upstream.String(), bytes.NewReader(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	for k, vv := range r.Header {
		for _, v := range vv {
			req.Header.Add(k, v)
		}
	}
	resp, err := s.client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, s.maxBodyBytes))
	copyResponse(w, resp, out)
}

func (s *Server) writeJSON(w http.ResponseWriter, b []byte) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

func copyResponse(w http.ResponseWriter, resp *http.Response, body []byte) {
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}
