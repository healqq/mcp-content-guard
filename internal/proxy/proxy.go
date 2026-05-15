package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"

	"mcp-context-guard/internal/cache"
	"mcp-context-guard/internal/filter"
	"mcp-context-guard/internal/rpc"
	"mcp-context-guard/internal/schema"
	"mcp-context-guard/internal/stats"
)

type Proxy struct {
	upstreamIn  io.WriteCloser
	upstreamOut io.Reader
	cache       *cache.Cache
	threshold   int64
	stats       *stats.Stats
	out         chan []byte

	mu      sync.Mutex
	pending map[string]rpc.Message // idKey → original request
}

func New(upstreamIn io.WriteCloser, upstreamOut io.Reader, upstreamErr io.Reader, c *cache.Cache, threshold int64, st *stats.Stats) *Proxy {
	p := &Proxy{
		upstreamIn:  upstreamIn,
		upstreamOut: upstreamOut,
		cache:       c,
		threshold:   threshold,
		stats:       st,
		out:         make(chan []byte, 64),
		pending:     make(map[string]rpc.Message),
	}
	if upstreamErr != nil {
		go func() { _, _ = io.Copy(os.Stderr, upstreamErr) }()
	}
	return p
}

func (p *Proxy) Run() {
	var wg sync.WaitGroup
	wg.Add(2)

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		w := bufio.NewWriter(os.Stdout)
		for line := range p.out {
			_, _ = w.Write(line)
			_ = w.WriteByte('\n')
			_ = w.Flush()
		}
	}()

	// goroutine A: client stdin → upstream stdin
	go func() {
		defer wg.Done()
		defer p.upstreamIn.Close()
		r := bufio.NewReader(os.Stdin)
		uw := bufio.NewWriter(p.upstreamIn)
		for {
			line, err := r.ReadBytes('\n')
			line = bytes.TrimRight(line, "\r\n")
			if len(line) > 0 {
				p.handleClientMessage(line, uw)
			}
			if err != nil {
				break
			}
		}
	}()

	// goroutine B: upstream stdout → client stdout
	go func() {
		defer wg.Done()
		r := bufio.NewReader(p.upstreamOut)
		for {
			line, err := r.ReadBytes('\n')
			line = bytes.TrimRight(line, "\r\n")
			if len(line) > 0 {
				p.handleUpstreamMessage(line)
			}
			if err != nil {
				break
			}
		}
	}()

	go func() {
		wg.Wait()
		close(p.out)
	}()

	<-writerDone
}

func (p *Proxy) handleClientMessage(line []byte, uw *bufio.Writer) {
	var msg rpc.Message
	if err := json.Unmarshal(line, &msg); err != nil {
		forward(uw, line)
		return
	}

	if msg.IsNotification() {
		forward(uw, line)
		return
	}

	if msg.IsRequest() {
		if msg.Method == "tools/call" {
			var params struct {
				Name      string `json:"name"`
				Arguments struct {
					ID     string `json:"id"`
					Filter string `json:"filter"`
				} `json:"arguments"`
			}
			if err := json.Unmarshal(msg.Params, &params); err == nil && params.Name == "seek_result" {
				p.handleSeekResult(msg, params.Arguments.ID, params.Arguments.Filter)
				return
			}
		}
		key := idKey(msg.ID)
		p.mu.Lock()
		p.pending[key] = msg
		p.mu.Unlock()
		forward(uw, line)
		return
	}

	// client-side response or unknown — forward as-is
	forward(uw, line)
}

func (p *Proxy) handleUpstreamMessage(line []byte) {
	var msg rpc.Message
	if err := json.Unmarshal(line, &msg); err != nil {
		p.out <- clone(line)
		return
	}

	if msg.IsNotification() {
		p.out <- clone(line)
		return
	}

	if msg.IsResponse() {
		key := idKey(msg.ID)
		p.mu.Lock()
		req, ok := p.pending[key]
		if ok {
			delete(p.pending, key)
		}
		p.mu.Unlock()

		if ok {
			switch req.Method {
			case "tools/list":
				if modified, err := schema.InjectSeekResult(msg.Result); err == nil {
					msg.Result = modified
					if b, err := json.Marshal(msg); err == nil {
						p.out <- b
						return
					}
				}
			case "tools/call":
				if b := p.maybeCache(msg); b != nil {
					p.out <- b
					return
				}
			}
		}
	}

	p.out <- clone(line)
}

func (p *Proxy) handleSeekResult(req rpc.Message, id, filterExpr string) {
	if id == "" {
		p.out <- rpc.EncodeErrorResult(req.ID, "seek_result: id is required")
		return
	}
	content, ok := p.cache.Get(id)
	if !ok {
		p.out <- rpc.EncodeErrorResult(req.ID, fmt.Sprintf("no cached result with id %q", id))
		return
	}
	result, err := filter.Apply(content, filterExpr)
	if err != nil {
		p.out <- rpc.EncodeErrorResult(req.ID, fmt.Sprintf("filter error: %s", err))
		return
	}
	p.stats.RecordSeek()
	p.out <- rpc.EncodeTextResult(req.ID, result)
}

// maybeCache checks whether the tools/call response is large enough to cache.
// Returns a replacement response with a stub, or nil if forwarding unchanged.
func (p *Proxy) maybeCache(msg rpc.Message) []byte {
	var result map[string]json.RawMessage
	if err := json.Unmarshal(msg.Result, &result); err != nil {
		return nil
	}
	// never cache error responses
	if raw, ok := result["isError"]; ok {
		var isErr bool
		if json.Unmarshal(raw, &isErr) == nil && isErr {
			return nil
		}
	}
	contentRaw, ok := result["content"]
	if !ok || int64(len(contentRaw)) < p.threshold {
		return nil
	}
	cacheID := p.cache.Store(contentRaw)
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
	p.stats.RecordCache(int64(len(contentRaw)), int64(len(stubText)))
	return b
}

func idKey(id any) string {
	b, _ := json.Marshal(id)
	return string(b)
}

func forward(w *bufio.Writer, line []byte) {
	_, _ = w.Write(line)
	_ = w.WriteByte('\n')
	_ = w.Flush()
}

func clone(b []byte) []byte {
	c := make([]byte, len(b))
	copy(c, b)
	return c
}
