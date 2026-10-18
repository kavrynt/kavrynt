package cloudsync

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type received struct {
	auth        string
	contentType string
	body        []byte
}

type cloud struct {
	server   *httptest.Server
	mu       sync.Mutex
	requests []received
	status   int
	header   http.Header
}

func newCloud(t *testing.T) *cloud {
	t.Helper()
	c := &cloud{status: http.StatusOK}
	c.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != SyncPath {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.requests = append(c.requests, received{r.Header.Get("Authorization"), r.Header.Get("Content-Type"), body})
		status, header := c.status, c.header
		c.mu.Unlock()
		for k, v := range header {
			w.Header()[k] = v
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":"<script>ignored</script>"}`))
	}))
	t.Cleanup(c.server.Close)
	return c
}

func (c *cloud) last(t *testing.T) received {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.requests) == 0 {
		t.Fatal("cloud received no request")
	}
	return c.requests[len(c.requests)-1]
}

func (c *cloud) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.requests)
}

func writeToken(t *testing.T, value string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func newReader(t *testing.T, objects ...client.Object) client.Reader {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := kavryntv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func newSyncer(t *testing.T, c *cloud, reader client.Reader, tokenFile string) *Syncer {
	t.Helper()
	s, err := New(reader, nil, Config{
		URL:       c.server.URL + "/",
		TokenFile: tokenFile,
		Version:   "test",
		Transport: c.server.Client().Transport,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mcpServer(namespace, name string, ready bool, tools ...kavryntv1alpha1.MCPServerTool) *kavryntv1alpha1.MCPServer {
	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	return &kavryntv1alpha1.MCPServer{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: kavryntv1alpha1.MCPServerSpec{
			Version:   "1.2.0",
			Transport: "http",
			Endpoint:  "http://" + name + "." + namespace + ".svc:8080/mcp?api_key=secret#frag",
			Environment: map[string]string{
				"API_TOKEN": "must-not-be-sent",
			},
		},
		Status: kavryntv1alpha1.MCPServerStatus{
			Conditions: []metav1.Condition{{Type: kavryntv1alpha1.ConditionReady, Status: status, Reason: "Test"}},
			Tools:      tools,
		},
	}
}

func schemaTool(name, description string, schema string) kavryntv1alpha1.MCPServerTool {
	t := kavryntv1alpha1.MCPServerTool{Name: name, Description: description}
	if schema != "" {
		t.InputSchema = &runtime.RawExtension{Raw: []byte(schema)}
	}
	return t
}

func TestValidateURL(t *testing.T) {
	for _, raw := range []string{
		"http://cloud.example.com",
		"https://",
		"https://user:pass@cloud.example.com",
		"https://cloud.example.com?x=1",
		"https://cloud.example.com#frag",
		"cloud.example.com",
		"",
	} {
		if _, err := ValidateURL(raw); err == nil {
			t.Errorf("ValidateURL(%q) accepted", raw)
		}
	}
	if _, err := ValidateURL("https://app.dev.kavrynt.com"); err != nil {
		t.Errorf("valid URL rejected: %v", err)
	}
	if _, err := New(newReader(t), nil, Config{URL: "https://cloud.example.com"}); err == nil {
		t.Error("New accepted an empty token file")
	}
}

func TestSyncOnceSendsFullSnapshot(t *testing.T) {
	c := newCloud(t)
	reader := newReader(t,
		mcpServer("platform", "payments", true,
			schemaTool("refund", "Refund a payment", `{"type":"object"}`),
			schemaTool("charge", "", "")),
		mcpServer("analytics", "warehouse", false),
	)
	s := newSyncer(t, c, reader, writeToken(t, "kvc_secret\n"))
	before := metricValue(t, syncSuccess)

	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := c.last(t)
	if got.auth != "Bearer kvc_secret" {
		t.Errorf("Authorization = %q", got.auth)
	}
	if got.contentType != "application/json" {
		t.Errorf("Content-Type = %q", got.contentType)
	}
	if metricValue(t, syncSuccess) != before+1 {
		t.Error("success counter not incremented")
	}
	if metricValue(t, syncLastSuccess) == 0 {
		t.Error("last success timestamp not set")
	}

	body := string(got.body)
	for _, forbidden := range []string{"must-not-be-sent", "api_key", "frag", "organizationId", "clusterId", "riskLevel"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("snapshot contains %q: %s", forbidden, body)
		}
	}
	var snap struct {
		Servers []map[string]any `json:"servers"`
	}
	if err := json.Unmarshal(got.body, &snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Servers) != 2 {
		t.Fatalf("servers = %d, want 2", len(snap.Servers))
	}
	first, second := snap.Servers[0], snap.Servers[1]
	if first["name"] != "warehouse" || first["status"] != "not_ready" || first["owner"] != "analytics" {
		t.Errorf("first server = %v", first)
	}
	if tools, ok := first["tools"].([]any); !ok || len(tools) != 0 {
		t.Errorf("server without tools must send tools: [], got %v", first["tools"])
	}
	if second["name"] != "payments" || second["status"] != "ready" || second["version"] != "1.2.0" ||
		second["endpoint"] != "http://payments.platform.svc:8080/mcp" {
		t.Errorf("second server = %v", second)
	}
	tools := second["tools"].([]any)
	refund := tools[0].(map[string]any)
	if refund["name"] != "refund" || refund["description"] != "Refund a payment" {
		t.Errorf("tool = %v", refund)
	}
	if schema, _ := json.Marshal(refund["inputSchemaJson"]); string(schema) != `{"type":"object"}` {
		t.Errorf("inputSchemaJson = %s", schema)
	}
}

func TestSyncOnceSendsEmptyInventory(t *testing.T) {
	c := newCloud(t)
	s := newSyncer(t, c, newReader(t), writeToken(t, "kvc_secret"))
	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if body := string(c.last(t).body); body != `{"servers":[]}` {
		t.Errorf("body = %s, want an explicit empty list", body)
	}
}

func TestSyncOnceRereadsRotatedToken(t *testing.T) {
	c := newCloud(t)
	tokenFile := writeToken(t, "kvc_old")
	s := newSyncer(t, c, newReader(t), tokenFile)
	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenFile, []byte("kvc_new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := c.last(t).auth; got != "Bearer kvc_new" {
		t.Errorf("Authorization = %q after rotation", got)
	}
}

func TestSyncOnceTokenErrors(t *testing.T) {
	c := newCloud(t)
	for name, path := range map[string]string{
		"missing":    filepath.Join(t.TempDir(), "absent"),
		"empty":      writeToken(t, " \n"),
		"whitespace": writeToken(t, "kvc a"),
		"control":    writeToken(t, "kvc\x00a"),
		"too large":  writeToken(t, strings.Repeat("a", maxTokenBytes+1)),
	} {
		t.Run(name, func(t *testing.T) {
			err := newSyncer(t, c, newReader(t), path).SyncOnce(context.Background())
			var syncErr *Error
			if !errors.As(err, &syncErr) || syncErr.Reason != reasonToken {
				t.Fatalf("err = %v, want a token error", err)
			}
		})
	}
	if c.count() != 0 {
		t.Error("a request was sent without a valid token")
	}
}

func TestSyncOnceResponses(t *testing.T) {
	for _, tc := range []struct {
		status     int
		header     http.Header
		reason     string
		retryAfter time.Duration
	}{
		{http.StatusUnauthorized, nil, reasonRejected, 0},
		{http.StatusForbidden, nil, reasonRejected, 0},
		{http.StatusBadRequest, nil, reasonRejected, 0},
		{http.StatusTooManyRequests, http.Header{"Retry-After": {"120"}}, reasonThrottled, 2 * time.Minute},
		{http.StatusServiceUnavailable, http.Header{"Retry-After": {"30"}}, reasonServerError, 30 * time.Second},
		{http.StatusInternalServerError, http.Header{"Retry-After": {"30"}}, reasonServerError, 0},
		{http.StatusFound, http.Header{"Location": {"https://elsewhere.example.com/"}}, reasonRejected, 0},
	} {
		c := newCloud(t)
		c.status, c.header = tc.status, tc.header
		before := metricValue(t, syncFailure.WithLabelValues(tc.reason))
		err := newSyncer(t, c, newReader(t), writeToken(t, "kvc_secret")).SyncOnce(context.Background())
		var syncErr *Error
		if !errors.As(err, &syncErr) {
			t.Fatalf("HTTP %d: err = %v", tc.status, err)
		}
		if syncErr.Reason != tc.reason || syncErr.RetryAfter != tc.retryAfter {
			t.Errorf("HTTP %d: reason %q retryAfter %v, want %q %v", tc.status, syncErr.Reason, syncErr.RetryAfter, tc.reason, tc.retryAfter)
		}
		if strings.Contains(err.Error(), "kvc_secret") || strings.Contains(err.Error(), "script") {
			t.Errorf("HTTP %d: error leaks token or response text: %v", tc.status, err)
		}
		if metricValue(t, syncFailure.WithLabelValues(tc.reason)) != before+1 {
			t.Errorf("HTTP %d: failure counter not incremented", tc.status)
		}
		if c.count() != 1 {
			t.Errorf("HTTP %d: %d requests, want 1 (no redirect followed)", tc.status, c.count())
		}
	}
}

func TestEncodeSnapshotTrimsToFit(t *testing.T) {
	schema := `{"type":"object","description":"` + strings.Repeat("s", 4000) + `"}`
	var tools []kavryntv1alpha1.MCPServerTool
	for i := 0; i < 50; i++ {
		tools = append(tools, schemaTool("tool_"+strings.Repeat("x", i), strings.Repeat("d", 1000), schema))
	}
	snap := buildSnapshot([]kavryntv1alpha1.MCPServer{*mcpServer("ns", "big", true, tools...)})

	full, level, err := encodeSnapshot(snap, 1<<20)
	if err != nil || level != trimNone {
		t.Fatalf("level %v err %v", level, err)
	}
	body, level, err := encodeSnapshot(snap, len(full)-1)
	if err != nil || level != trimSchemas || strings.Contains(string(body), "inputSchemaJson") || !strings.Contains(string(body), "ddd") {
		t.Fatalf("schemas: level %v err %v", level, err)
	}
	body, level, err = encodeSnapshot(snap, len(body)-1)
	if err != nil || level != trimDescriptions || strings.Contains(string(body), "ddd") || strings.Count(string(body), `"name":"tool_`) != 50 {
		t.Fatalf("descriptions: level %v err %v", level, err)
	}
	if _, _, err := encodeSnapshot(snap, 100); err == nil {
		t.Fatal("an oversized inventory was sent partially")
	}
	if snap.Servers[0].Tools[0].InputSchemaJSON == nil {
		t.Error("encodeSnapshot changed the caller's snapshot")
	}
}

func TestBackoff(t *testing.T) {
	for failures := 1; failures < 40; failures++ {
		d := backoff(failures)
		if d < minBackoff/2 || d > maxBackoff {
			t.Errorf("backoff(%d) = %v", failures, d)
		}
	}
	if backoff(1) > minBackoff {
		t.Errorf("first backoff %v exceeds %v", backoff(1), minBackoff)
	}
}

func TestRetryAfter(t *testing.T) {
	if got := retryAfter("90"); got != 90*time.Second {
		t.Errorf("seconds: %v", got)
	}
	if got := retryAfter("999999"); got != maxRetryAfter {
		t.Errorf("cap: %v", got)
	}
	for _, v := range []string{"", "-5", "soon"} {
		if got := retryAfter(v); got != 0 {
			t.Errorf("retryAfter(%q) = %v", v, got)
		}
	}
	date := time.Now().Add(time.Minute).UTC().Format(http.TimeFormat)
	if got := retryAfter(date); got <= 0 || got > time.Minute {
		t.Errorf("date: %v", got)
	}
}

func TestStartSyncsAtOnce(t *testing.T) {
	c := newCloud(t)
	s := newSyncer(t, c, newReader(t), writeToken(t, "kvc_secret"))
	s.interval = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Start(ctx) }()

	waitFor(t, func() bool { return c.count() == 1 }, "initial sync")
	if !s.NeedLeaderElection() {
		t.Error("connector must run on the leader only")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func metricValue(t *testing.T, collector prometheus.Collector) float64 {
	t.Helper()
	ch := make(chan prometheus.Metric, 1)
	collector.Collect(ch)
	var m dto.Metric
	if err := (<-ch).Write(&m); err != nil {
		t.Fatal(err)
	}
	if m.Counter != nil {
		return m.Counter.GetValue()
	}
	return m.Gauge.GetValue()
}
