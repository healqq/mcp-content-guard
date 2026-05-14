package remote_test

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mcp-context-guard/internal/remote"
)

func readLine(t *testing.T, conn *remote.Conn) string {
	t.Helper()
	sc := bufio.NewScanner(conn)
	if !sc.Scan() {
		t.Fatal("expected a line from conn, got EOF or error:", sc.Err())
	}
	return sc.Text()
}

func writeLine(t *testing.T, conn *remote.Conn, msg string) {
	t.Helper()
	if _, err := fmt.Fprintln(conn, msg); err != nil {
		t.Fatal("write:", err)
	}
}

func TestSingleJSONResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	}))
	defer srv.Close()

	conn := remote.New(srv.URL, nil)
	defer conn.Close()

	writeLine(t, conn, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	got := readLine(t, conn)
	if got != `{"jsonrpc":"2.0","id":1,"result":{}}` {
		t.Errorf("unexpected response: %s", got)
	}
}

func TestSSEMultiEventResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n\n")
		fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/initialized\"}\n\n")
	}))
	defer srv.Close()

	conn := remote.New(srv.URL, nil)
	defer conn.Close()

	writeLine(t, conn, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)

	first := readLine(t, conn)
	if !strings.Contains(first, `"result":{}`) {
		t.Errorf("first event unexpected: %s", first)
	}
	second := readLine(t, conn)
	if !strings.Contains(second, `"notifications/initialized"`) {
		t.Errorf("second event unexpected: %s", second)
	}
}

func Test202NotificationNoRead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	conn := remote.New(srv.URL, nil)
	defer conn.Close()

	writeLine(t, conn, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)

	// Read should not produce data for a 202 response
	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 256)
		n, _ := conn.Read(buf)
		done <- string(buf[:n])
	}()

	select {
	case data := <-done:
		t.Errorf("expected no data for 202, got: %q", data)
	case <-time.After(60 * time.Millisecond):
		// correct: nothing was readable
	}
}

func TestCustomHeadersPassthrough(t *testing.T) {
	var gotAuth, gotCustom string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCustom = r.Header.Get("X-Custom")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	}))
	defer srv.Close()

	headers := map[string]string{
		"Authorization": "Bearer tok",
		"X-Custom":      "val",
	}
	conn := remote.New(srv.URL, headers)
	defer conn.Close()

	writeLine(t, conn, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	readLine(t, conn)

	if gotAuth != "Bearer tok" {
		t.Errorf("Authorization header: got %q", gotAuth)
	}
	if gotCustom != "val" {
		t.Errorf("X-Custom header: got %q", gotCustom)
	}
}

func TestHTTPErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "server error")
	}))
	defer srv.Close()

	conn := remote.New(srv.URL, nil)

	_, err := fmt.Fprintln(conn, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if err == nil {
		// Write may have buffered; attempt a Read to see the connection is closed
		buf := make([]byte, 256)
		_, rerr := conn.Read(buf)
		if rerr != io.EOF {
			t.Error("expected io.EOF after HTTP error, got:", rerr)
		}
	}
	// either Write returned an error or subsequent Read returns EOF — both are acceptable
}

func TestCloseUnblocksRead(t *testing.T) {
	conn := remote.New("http://127.0.0.1:0", nil) // unreachable server — we never write

	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 4)
		_, err := conn.Read(buf)
		done <- err
	}()

	// give the goroutine time to block on Read
	time.Sleep(10 * time.Millisecond)
	conn.Close()

	select {
	case err := <-done:
		if err != io.EOF {
			t.Errorf("expected io.EOF, got %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Read did not unblock within 100ms after Close")
	}
}
