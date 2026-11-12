package api

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// stalledRequest opens a connection to the server, sends the request line,
// headers that announce more body than it sends, and then nothing.
func stalledRequest(t *testing.T, server *httptest.Server, headers string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(conn, "POST /mcp/team-a.demo HTTP/1.1\r\nHost: gateway.test\r\n%sContent-Length: 1000\r\n\r\n{\"jsonrpc\"", headers); err != nil {
		t.Fatal(err)
	}
	return conn
}

// A client that stops sending its body is answered and disconnected at the
// request timeout, whether the Gateway reads the body for the request's
// method or, with client authentication and before the token is checked,
// for a token in a form (the caller has none, so the answer is 401).
func TestStalledRequestBodyEndsAtTheRequestTimeout(t *testing.T) {
	open, calls := demoHandler(t, okResponse)
	authenticated, received, _ := authHandler(t, true)
	for name, c := range map[string]struct {
		handler *Handler
		headers string
		status  int
	}{
		"JSON body":                  {open, "Content-Type: application/json\r\n", http.StatusRequestTimeout},
		"form body before the token": {authenticated, "Content-Type: application/x-www-form-urlencoded\r\n", http.StatusUnauthorized},
	} {
		t.Run(name, func(t *testing.T) {
			c.handler.SetRequestTimeout(150 * time.Millisecond)
			server := httptest.NewServer(c.handler)
			defer server.Close()
			conn := stalledRequest(t, server, c.headers)
			// Before the server's Close, which waits for the connection.
			defer conn.Close()

			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatalf("no answer to a stalled body: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != c.status || !resp.Close {
				t.Fatalf("status %d, close %t, want %d and a closed connection", resp.StatusCode, resp.Close, c.status)
			}
			// The server ends the connection instead of waiting for the rest.
			if _, err := io.Copy(io.Discard, conn); err != nil {
				t.Fatalf("the connection stayed open: %v", err)
			}
		})
	}
	if *calls != 0 || len(*received) != 0 {
		t.Fatal("a stalled request reached the MCP server")
	}
}

// A body larger than the part the Gateway reads itself is bound too: a
// client that stalls after that part is answered at the request timeout and
// disconnected, although the MCP server is still waiting for the rest.
func TestStalledUploadPastTheInspectedPartEndsAtTheRequestTimeout(t *testing.T) {
	handler, _ := demoHandler(t, func(req *http.Request) (*http.Response, error) {
		go func() { _, _ = io.Copy(io.Discard, req.Body) }()
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	handler.SetRequestTimeout(150 * time.Millisecond)
	server := httptest.NewServer(handler)
	defer server.Close()
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	sent := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"x":"` + strings.Repeat("a", maxInspectBytes+1)
	if _, err := fmt.Fprintf(conn, "POST /mcp/team-a.demo HTTP/1.1\r\nHost: gateway.test\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", len(sent)+1000, sent); err != nil {
		t.Fatal(err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("no answer to a stalled upload: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestTimeout || !resp.Close {
		t.Fatalf("status %d, close %t, want 408 and a closed connection", resp.StatusCode, resp.Close)
	}
	if _, err := io.Copy(io.Discard, conn); err != nil {
		t.Fatalf("the connection stayed open: %v", err)
	}
}

// A complete body that the MCP server never reads is the server's timeout,
// not the client's.
func TestUnreadBodyIsAnUpstreamTimeout(t *testing.T) {
	handler, _ := demoHandler(t, func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	handler.SetRequestTimeout(50 * time.Millisecond)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"x":"` + strings.Repeat("a", maxInspectBytes+1) + `"}}}`
	if rr := post(handler, body, nil); rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("status %d, want 504", rr.Code)
	}
}

// The body deadline ends once the body is read: an answer that streams for
// longer than the request timeout is not cut.
func TestBodyDeadlineDoesNotCutLongAnswers(t *testing.T) {
	reader, writer := io.Pipe()
	handler, _ := demoHandler(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       reader,
		}, nil
	})
	handler.SetRequestTimeout(100 * time.Millisecond)
	server := httptest.NewServer(handler)
	defer server.Close()

	go func() {
		_, _ = io.WriteString(writer, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n")
		time.Sleep(400 * time.Millisecond)
		_, _ = io.WriteString(writer, "data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n\n")
		_ = writer.Close()
	}()
	resp, err := http.Post(server.URL+"/mcp/team-a.demo", "application/json", strings.NewReader(callEcho))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || !strings.Contains(string(body), `"result"`) {
		t.Fatalf("stream after the request timeout: %q, %v", body, err)
	}
}
