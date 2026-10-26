package alerts

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Verifies SPEC-0020-R03: the chart's values override the defaults, and
// the minimum calls follow the Prometheus rules' request rates.
func TestParseConfig(t *testing.T) {
	cfg, err := ParseConfig([]byte(`{"errorRatio":0.1,"minRequestsPerSecond":0.1,"latencyP95Seconds":2,
		"toolErrorRatio":0.5,"minToolRequestsPerSecond":0.05,"notReadyFor":"30s","discoveryFailingFor":"1h",
		"runbookBaseURL":"https://runbooks.example.com/kavrynt/"}`), DefaultConfig("1.2.3"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ErrorRatio != 0.1 || cfg.ToolErrorRatio != 0.5 || cfg.LatencyP95Seconds != 2 ||
		time.Duration(cfg.NotReadyFor) != 30*time.Second || time.Duration(cfg.DiscoveryFailingFor) != time.Hour ||
		cfg.RunbookBaseURL != "https://runbooks.example.com/kavrynt" {
		t.Errorf("config = %+v", cfg)
	}
	if got := minCalls(cfg.MinRequestsPerSecond); got != 60 {
		t.Errorf("minimum route calls = %d, want 60", got)
	}
	if got, want := minCalls(DefaultConfig("").MinRequestsPerSecond), uint64(30); got != want {
		t.Errorf("default minimum route calls = %d, want %d", got, want)
	}
	if got, want := minCalls(DefaultConfig("").MinToolRequestsPerSecond), uint64(12); got != want {
		t.Errorf("default minimum tool calls = %d, want %d", got, want)
	}

	defaults := DefaultConfig("v1.2.3")
	if defaults.RunbookBaseURL != "https://github.com/kavrynt/kavrynt/blob/v1.2.3/docs/runbooks" {
		t.Errorf("default runbooks = %q", defaults.RunbookBaseURL)
	}
	for _, bad := range []string{
		`{"errorRatio":0}`, `{"toolErrorRatio":2}`, `{"latencyP95Seconds":0}`, `{"notReadyFor":"soon"}`,
		`{"runbookBaseURL":"http://runbooks.example.com"}`, `{"unknown":1}`, `not json`,
	} {
		if _, err := ParseConfig([]byte(bad), defaults); err == nil {
			t.Errorf("%s: accepted", bad)
		}
	}
}

// Verifies SPEC-0020-R03: the Gateway rereads the mounted file when it
// changes and keeps the last valid configuration.
func TestFileConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	defaults := DefaultConfig("1.2.3")
	config := FileConfig(path, defaults, nil)
	if config().ErrorRatio != defaults.ErrorRatio {
		t.Fatal("a missing file does not give the defaults")
	}
	write := func(content string, at time.Time) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"errorRatio":0.25}`, t0)
	if got := config().ErrorRatio; got != 0.25 {
		t.Fatalf("errorRatio = %v, want 0.25", got)
	}
	write(`{"errorRatio":"broken"}`, t0.Add(time.Minute))
	if got := config().ErrorRatio; got != 0.25 {
		t.Fatalf("after an invalid update errorRatio = %v, want the last valid 0.25", got)
	}
	write(`{"errorRatio":0.5}`, t0.Add(2*time.Minute))
	if got := config().ErrorRatio; got != 0.5 {
		t.Fatalf("errorRatio = %v, want 0.5", got)
	}
}
