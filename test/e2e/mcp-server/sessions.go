package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

// stateful and sessions implement the STATEFUL server mode.
var (
	stateful bool
	sessions sync.Map
)

func newSession() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	sessions.Store(id, true)
	return id
}

// runSessions runs agents one after another, each with an MCP session of
// calls tools/call requests, at url. Every request uses a new connection, so
// requests spread across Gateway replicas and, without session affinity,
// across the server's pods. scripts/e2e-kind.sh runs it in the cluster and
// reads the last line: done sessions=N ok=N failed=N.
func runSessions(args []string) int {
	flags := flag.NewFlagSet("sessions", flag.ContinueOnError)
	url := flags.String("url", "", "Gateway MCP route URL")
	agents := flags.Int("agents", 10, "Sessions to run")
	calls := flags.Int("calls", 20, "tools/call requests per session")
	timeout := flags.Duration("timeout", 10*time.Second, "Timeout of each request")
	if err := flags.Parse(args); err != nil || *url == "" {
		fmt.Fprintln(os.Stderr, "usage: mcp-server sessions -url <route URL> [-agents 10] [-calls 20] [-timeout 10s]")
		return 2
	}
	client := &http.Client{Timeout: *timeout, Transport: &http.Transport{DisableKeepAlives: true}}
	post := func(session, body string) (int, string, error) {
		req, _ := http.NewRequest(http.MethodPost, *url, bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if session != "" {
			req.Header.Set("Mcp-Session-Id", session)
		}
		res, err := client.Do(req)
		if err != nil {
			return 0, "", err
		}
		defer res.Body.Close()
		_, _ = io.Copy(io.Discard, res.Body)
		return res.StatusCode, res.Header.Get("Mcp-Session-Id"), nil
	}

	var started, ok, failed int
	for agent := range *agents {
		status, session, err := post("", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"e2e-agent","version":"1"}}}`)
		if err != nil || status != http.StatusOK || session == "" {
			failed++
			fmt.Printf("FAIL agent=%d initialize status=%d session=%t err=%v\n", agent, status, session != "", err)
			continue
		}
		started++
		for call := range *calls {
			status, _, err := post(session, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"echo","arguments":{"message":"hi"}}}`, call+2))
			if err != nil || status != http.StatusOK {
				failed++
				fmt.Printf("FAIL agent=%d call=%d status=%d err=%v\n", agent, call, status, err)
				continue
			}
			ok++
		}
	}
	fmt.Printf("done sessions=%d ok=%d failed=%d\n", started, ok, failed)
	return 0
}
