package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	auditv1 "github.com/kavrynt/kavrynt/api/audit/v1"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

const testToken = "s3cret-cluster-token"

// fakeCloud records the batches it receives and answers with respond.
type fakeCloud struct {
	mu      sync.Mutex
	batches [][]json.RawMessage
	headers []http.Header
	urls    []string
	respond func(attempt int) *http.Response
}

func (c *fakeCloud) RoundTrip(r *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	attempt := len(c.headers)
	c.headers = append(c.headers, r.Header.Clone())
	c.urls = append(c.urls, r.URL.String())
	c.mu.Unlock()
	resp := &http.Response{StatusCode: http.StatusAccepted, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}
	if c.respond != nil {
		resp = c.respond(attempt)
	}
	if resp == nil {
		return nil, errors.New("connection refused")
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var batch struct {
			Events []json.RawMessage `json:"events"`
		}
		if err := json.Unmarshal(body, &batch); err != nil {
			return nil, fmt.Errorf("batch is not JSON: %w", err)
		}
		if len(body) > MaxBatchBytes {
			return nil, fmt.Errorf("batch of %d bytes", len(body))
		}
		c.mu.Lock()
		c.batches = append(c.batches, batch.Events)
		c.mu.Unlock()
	}
	return resp, nil
}

func (c *fakeCloud) delivered() [][]json.RawMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([][]json.RawMessage(nil), c.batches...)
}

func answer(status int, header ...string) *http.Response {
	resp := &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("token " + testToken + " echoed"))}
	for i := 0; i+1 < len(header); i += 2 {
		resp.Header.Set(header[i], header[i+1])
	}
	return resp
}

func newTestShipper(t *testing.T, cloud *fakeCloud, buffer int) *Shipper {
	t.Helper()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte(testToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewShipper(CloudConfig{URL: "https://cloud.example.com", TokenFile: tokenFile, Buffer: buffer, Version: "1.2.3", Transport: cloud})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func event(seq int, size int) []byte {
	return []byte(fmt.Sprintf(`{"type":"kavrynt.audit.v1","instance":"gw-a/1","seq":%d,"reason":%q}`, seq, strings.Repeat("x", size)))
}

func seqs(t *testing.T, batches [][]json.RawMessage) []uint64 {
	t.Helper()
	var out []uint64
	for _, batch := range batches {
		for _, raw := range batch {
			var e struct {
				Seq uint64 `json:"seq"`
			}
			if err := json.Unmarshal(raw, &e); err != nil {
				t.Fatal(err)
			}
			out = append(out, e.Seq)
		}
	}
	return out
}

// Verifies SPEC-0017-R01: batches hold at most 500 events and 1 MiB, go to
// the Cloud ingestion endpoint with the cluster token, in order.
func TestShipperBatches(t *testing.T) {
	cloud := &fakeCloud{}
	s := newTestShipper(t, cloud, 0)
	for i := 1; i <= 1200; i++ {
		s.Add(event(i, 10))
	}
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	batches := cloud.delivered()
	if len(batches) != 3 || len(batches[0]) != 500 || len(batches[1]) != 500 || len(batches[2]) != 200 {
		t.Fatalf("batch sizes = %d batches", len(batches))
	}
	got := seqs(t, batches)
	for i, seq := range got {
		if seq != uint64(i+1) {
			t.Fatalf("event %d has seq %d: out of order", i, seq)
		}
	}
	if cloud.urls[0] != "https://cloud.example.com/v1/sync/audit-events" ||
		cloud.headers[0].Get("Authorization") != "Bearer "+testToken ||
		cloud.headers[0].Get("Content-Type") != "application/json" ||
		cloud.headers[0].Get("User-Agent") != "kavrynt-gateway/1.2.3" {
		t.Fatalf("request = %s %v", cloud.urls[0], cloud.headers[0])
	}

	// Large events: no request over 1 MiB (the fake Cloud refuses it).
	for i := 1; i <= 10; i++ {
		s.Add(event(i, 300<<10))
	}
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(cloud.delivered()); n != 3+4 {
		t.Fatalf("10 events of 300 KiB took %d batches, want 4", n-3)
	}
}

// Verifies SPEC-0017-R01: waiting events are sent at least every flush
// interval, and a full batch at once.
func TestShipperSendsOnTheInterval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cloud := &fakeCloud{}
	s := newTestShipper(t, cloud, 0)
	s.interval = 50 * time.Millisecond
	go s.Run(ctx)
	s.Add(event(1, 10))
	waitFor(t, func() bool { return len(seqs(t, cloud.delivered())) == 1 })

	full := &fakeCloud{}
	s = newTestShipper(t, full, 0)
	s.interval = time.Hour
	go s.Run(ctx)
	for i := 1; i <= MaxBatchEvents; i++ {
		s.Add(event(i, 10))
	}
	waitFor(t, func() bool { return len(seqs(t, full.delivered())) == MaxBatchEvents })
}

func waitFor(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Verifies SPEC-0017-R02 and SPEC-0017-R03: while Cloud answers 503,
// events stay on standard output and in the buffer; once it recovers,
// every event is delivered with no gap in seq.
func TestDeliveryRecoversWithoutGaps(t *testing.T) {
	down := true
	var mu sync.Mutex
	cloud := &fakeCloud{respond: func(int) *http.Response {
		mu.Lock()
		defer mu.Unlock()
		if down {
			return answer(http.StatusServiceUnavailable, "Retry-After", "30")
		}
		return answer(http.StatusAccepted)
	}}
	s := newTestShipper(t, cloud, 0)
	out := &syncBuffer{}
	w, err := New(out, "gw-a/1", Reject, 1000)
	if err != nil {
		t.Fatal(err)
	}
	w.SetSink(s)
	for i := 0; i < 1000; i++ {
		ok := w.Reserve()
		w.Emit(auditv1.Event{RequestID: "r", Decision: auditv1.DecisionAllow}, ok)
	}
	if err := w.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		err := s.Flush(context.Background())
		var deliveryErr *DeliveryError
		if !errors.As(err, &deliveryErr) || deliveryErr.Result != resultServerError || deliveryErr.RetryAfter != 30*time.Second {
			t.Fatalf("flush while down = %v", err)
		}
	}
	if summary, err := auditv1.Verify(strings.NewReader(out.String())); err != nil || summary.Events != 1000 {
		t.Fatalf("standard output: %+v %v", summary, err)
	}
	if len(cloud.delivered()) != 0 || s.Len() != 1000 {
		t.Fatalf("delivered %d batches and kept %d events while Cloud was down", len(cloud.delivered()), s.Len())
	}
	mu.Lock()
	down = false
	mu.Unlock()
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := seqs(t, cloud.delivered())
	if len(got) != 1000 {
		t.Fatalf("delivered %d events, want 1000", len(got))
	}
	for i, seq := range got {
		if seq != uint64(i+1) {
			t.Fatalf("gap: event %d has seq %d", i, seq)
		}
	}
	if s.Len() != 0 {
		t.Fatalf("%d events left", s.Len())
	}
}

// Verifies SPEC-0017-R03: retries back off exponentially with jitter up to
// 5 minutes, and Retry-After is honored up to an hour.
func TestDeliveryBackoff(t *testing.T) {
	for failures, limit := range map[int]time.Duration{1: time.Second, 3: 4 * time.Second, 20: maxCloudBackoff} {
		for i := 0; i < 50; i++ {
			if d := cloudBackoff(failures); d < limit/2 || d > limit {
				t.Fatalf("backoff after %d failures = %v, want in [%v, %v]", failures, d, limit/2, limit)
			}
		}
	}
	if got := cloudRetryAfter("120"); got != 2*time.Minute {
		t.Errorf("Retry-After 120 = %v", got)
	}
	if got := cloudRetryAfter("86400"); got != time.Hour {
		t.Errorf("Retry-After a day = %v, want capped at an hour", got)
	}
	cloud := &fakeCloud{respond: func(int) *http.Response { return answer(http.StatusTooManyRequests, "Retry-After", "7") }}
	s := newTestShipper(t, cloud, 0)
	s.Add(event(1, 1))
	var deliveryErr *DeliveryError
	if err := s.Flush(context.Background()); !errors.As(err, &deliveryErr) || deliveryErr.Result != resultThrottled || deliveryErr.RetryAfter != 7*time.Second {
		t.Fatalf("throttled flush = %v", err)
	}
}

// Verifies SPEC-0017-R04: a full buffer drops the oldest undelivered events
// and counts them, without blocking writers while Cloud hangs.
func TestFullBufferDropsTheOldest(t *testing.T) {
	release := make(chan struct{})
	cloud := &fakeCloud{respond: func(attempt int) *http.Response {
		if attempt == 0 {
			<-release
		}
		return answer(http.StatusAccepted)
	}}
	s := newTestShipper(t, cloud, 5)
	for i := 1; i <= 3; i++ {
		s.Add(event(i, 1))
	}
	flushed := make(chan error, 1)
	go func() { flushed <- s.Flush(context.Background()) }()
	waitFor(t, func() bool { cloud.mu.Lock(); defer cloud.mu.Unlock(); return len(cloud.headers) == 1 })

	// Cloud hangs on the batch of 1-3; writers keep adding.
	start := time.Now()
	for i := 4; i <= 10; i++ {
		s.Add(event(i, 1))
	}
	if time.Since(start) > time.Second {
		t.Fatal("Add blocked while Cloud hung")
	}
	if s.Len() != 5 || testutil.ToFloat64(s.dropped) != 5 {
		t.Fatalf("buffer %d events, %v dropped; want 5 and 5", s.Len(), testutil.ToFloat64(s.dropped))
	}
	close(release)
	if err := <-flushed; err != nil {
		t.Fatal(err)
	}
	// The in-flight batch held 1-3, all dropped meanwhile; 6-10 remain and
	// are sent once each.
	if got := fmt.Sprint(seqs(t, cloud.delivered())); got != "[1 2 3 6 7 8 9 10]" {
		t.Fatalf("delivered %s", got)
	}
	if s.Len() != 0 {
		t.Fatalf("%d left", s.Len())
	}
}

// Verifies SPEC-0017-R05: delivery exports its results, buffer, drops, and
// last accepted batch.
func TestDeliveryMetrics(t *testing.T) {
	fail := true
	cloud := &fakeCloud{respond: func(int) *http.Response {
		if fail {
			return nil
		}
		return answer(http.StatusOK)
	}}
	s := newTestShipper(t, cloud, 0)
	started := testutil.ToFloat64(s.lastSuccess)
	if started <= 0 {
		t.Fatal("last success does not start at the Gateway's start")
	}
	s.Add(event(1, 1))
	s.Add(event(2, 1))
	if testutil.ToFloat64(s.buffered) != 2 {
		t.Fatal("buffer length not exported")
	}
	if err := s.Flush(context.Background()); err == nil {
		t.Fatal("flush to an unreachable Cloud succeeded")
	}
	fail = false
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if testutil.ToFloat64(s.attempts.WithLabelValues(resultNetwork)) != 1 ||
		testutil.ToFloat64(s.attempts.WithLabelValues(resultDelivered)) != 1 ||
		testutil.ToFloat64(s.buffered) != 0 || testutil.ToFloat64(s.lastSuccess) < started {
		t.Fatal("delivery metrics wrong")
	}
	if len(s.Collectors()) != 4 {
		t.Fatalf("%d collectors, want delivery, dropped, last success, and buffer", len(s.Collectors()))
	}
}

// Verifies SPEC-0017-R06: the token is never in errors or logs, redirects
// are not followed, and only https URLs without credentials are accepted.
func TestDeliveryKeepsTheTokenToCloud(t *testing.T) {
	var logs bytes.Buffer
	for _, status := range []int{http.StatusUnauthorized, http.StatusBadRequest, http.StatusInternalServerError, http.StatusFound} {
		cloud := &fakeCloud{respond: func(int) *http.Response { return answer(status, "Location", "https://attacker.example/") }}
		s := newTestShipper(t, cloud, 0)
		s.logger = slog.New(slog.NewTextHandler(&logs, nil))
		s.Add(event(1, 1))
		err := s.Flush(context.Background())
		if err == nil || strings.Contains(err.Error(), testToken) {
			t.Fatalf("HTTP %d: error %v", status, err)
		}
		if len(cloud.urls) != 1 {
			t.Fatalf("HTTP %d: %d requests; a redirect was followed", status, len(cloud.urls))
		}
		s.logger.Error("audit event delivery failed", "error", err)
	}
	if strings.Contains(logs.String(), testToken) {
		t.Fatalf("logs contain the token:\n%s", logs.String())
	}
	for _, bad := range []string{"http://cloud.example.com", "https://user:pw@cloud.example.com", "https://cloud.example.com?x=1", "https://", "cloud.example.com"} {
		if _, err := NewShipper(CloudConfig{URL: bad, TokenFile: "t", Transport: &fakeCloud{}}); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	if _, err := NewShipper(CloudConfig{URL: "https://cloud.example.com", TokenFile: "t"}); err == nil {
		t.Error("a shipper without the egress transport was created")
	}
	empty := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewShipper(CloudConfig{URL: "https://cloud.example.com", TokenFile: empty, Transport: &fakeCloud{}})
	if err != nil {
		t.Fatal(err)
	}
	s.Add(event(1, 1))
	var deliveryErr *DeliveryError
	if err := s.Flush(context.Background()); !errors.As(err, &deliveryErr) || deliveryErr.Result != resultToken {
		t.Fatalf("empty token: %v", err)
	}
}
