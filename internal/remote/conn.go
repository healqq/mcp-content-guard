package remote

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
)

// Conn connects to a remote MCP server using the Streamable HTTP transport.
// It implements both io.WriteCloser (client → upstream via HTTP POST) and
// io.Reader (upstream → client via POST response bodies / SSE events).
type Conn struct {
	url     string
	headers map[string]string
	client  *http.Client

	wmu  sync.Mutex
	wbuf bytes.Buffer // accumulates Write bytes until \n

	mu   sync.Mutex
	buf  []byte     // unconsumed bytes buffered for Read
	ch   chan []byte // complete JSON-RPC messages ready to be read
	once sync.Once
	done chan struct{}
}

// New creates a Conn for the given MCP endpoint URL. headers are added to every
// outgoing POST request (e.g. "Authorization": "Bearer sk-...").
func New(rawURL string, headers map[string]string) *Conn {
	return &Conn{
		url:     rawURL,
		headers: headers,
		client:  &http.Client{},
		ch:      make(chan []byte, 64),
		done:    make(chan struct{}),
	}
}

// Write buffers p and dispatches one HTTP POST per complete newline-terminated message.
func (c *Conn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	c.wbuf.Write(p)
	for {
		idx := bytes.IndexByte(c.wbuf.Bytes(), '\n')
		if idx < 0 {
			break
		}
		msg := make([]byte, idx)
		copy(msg, c.wbuf.Bytes()[:idx])
		c.wbuf.Next(idx + 1) // consume message + newline
		c.wmu.Unlock()
		if len(msg) > 0 {
			if err := c.dispatch(msg); err != nil {
				return len(p), err
			}
		}
		c.wmu.Lock()
	}
	c.wmu.Unlock()
	return len(p), nil
}

// Close signals the connection is done; any blocked Read returns io.EOF.
func (c *Conn) Close() error {
	c.once.Do(func() { close(c.done) })
	return nil
}

// Read blocks until a message is available from the upstream, then copies bytes
// into p. Messages are newline-terminated so callers using bufio.ReadBytes('\n')
// see one complete JSON-RPC message per call.
func (c *Conn) Read(p []byte) (int, error) {
	c.mu.Lock()
	if len(c.buf) > 0 {
		n := copy(p, c.buf)
		c.buf = c.buf[n:]
		c.mu.Unlock()
		return n, nil
	}
	c.mu.Unlock()

	var msg []byte
	select {
	case m, ok := <-c.ch:
		if !ok {
			return 0, io.EOF
		}
		msg = m
	case <-c.done:
		return 0, io.EOF
	}

	full := append(msg, '\n')
	n := copy(p, full)
	if n < len(full) {
		c.mu.Lock()
		c.buf = append(c.buf, full[n:]...)
		c.mu.Unlock()
	}
	return n, nil
}

// dispatch sends a single JSON-RPC message as an HTTP POST and pumps any
// response messages into c.ch.
func (c *Conn) dispatch(msg []byte) error {
	req, err := http.NewRequest(http.MethodPost, c.url, bytes.NewReader(msg))
	if err != nil {
		return fmt.Errorf("remote: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("remote: POST: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusAccepted {
		// 202: notification acknowledged, no response body
		return nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		err := fmt.Errorf("remote: upstream HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		fmt.Fprintln(os.Stderr, err)
		c.Close()
		return err
	}

	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "text/event-stream") {
		return c.readSSE(resp.Body)
	}

	// application/json or anything else — read body as a single message
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("remote: read response body: %w", err)
	}
	body = bytes.TrimSpace(body)
	if len(body) > 0 {
		out := make([]byte, len(body))
		copy(out, body)
		c.ch <- out
	}
	return nil
}

// readSSE parses a text/event-stream response body and pushes each data line
// into c.ch. The stream is finite (one POST → one SSE response body).
func (c *Conn) readSSE(body io.Reader) error {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 1<<20), 1<<20) // 1 MiB — handles large MCP payloads
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data: ") {
			data := []byte(line[6:])
			out := make([]byte, len(data))
			copy(out, data)
			c.ch <- out
		}
		// blank lines and ':' comment lines are skipped
	}
	return sc.Err()
}
