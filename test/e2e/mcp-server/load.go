package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// runLoad calls tools/list at url until duration ends, the way an MCP client
// with a pooled HTTP client would. scripts/e2e-kind.sh runs it in the
// cluster during a Gateway rolling restart: every failure is printed as a
// FAIL line at once, and a progress line every five seconds.
func runLoad(args []string) int {
	flags := flag.NewFlagSet("load", flag.ContinueOnError)
	url := flags.String("url", "", "Gateway MCP route URL")
	duration := flags.Duration("duration", 2*time.Minute, "How long to call")
	interval := flags.Duration("interval", 50*time.Millisecond, "Pause between calls")
	if err := flags.Parse(args); err != nil || *url == "" {
		fmt.Fprintln(os.Stderr, "usage: mcp-server load -url <route URL> [-duration 2m] [-interval 50ms]")
		return 2
	}

	client := &http.Client{Timeout: 10 * time.Second}
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	var ok, failed int
	deadline := time.Now().Add(*duration)
	nextReport := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodPost, *url, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		switch {
		case err != nil:
			failed++
			fmt.Printf("FAIL %s %v\n", time.Now().UTC().Format(time.RFC3339Nano), err)
		case res.StatusCode != http.StatusOK:
			failed++
			fmt.Printf("FAIL %s HTTP %d\n", time.Now().UTC().Format(time.RFC3339Nano), res.StatusCode)
		default:
			ok++
		}
		if res != nil {
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
		}
		if time.Now().After(nextReport) {
			fmt.Printf("progress ok=%d failed=%d\n", ok, failed)
			nextReport = time.Now().Add(5 * time.Second)
		}
		time.Sleep(*interval)
	}
	fmt.Printf("done ok=%d failed=%d\n", ok, failed)
	return 0
}
