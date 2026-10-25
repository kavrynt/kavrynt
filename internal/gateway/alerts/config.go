package alerts

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Config holds the thresholds the conditions share with the chart's
// Prometheus rules (SPEC-0020-R03). The chart writes it from
// metrics.prometheusRule into the file named by --alerts-config.
type Config struct {
	ErrorRatio               float64  `json:"errorRatio"`
	MinRequestsPerSecond     float64  `json:"minRequestsPerSecond"`
	LatencyP95Seconds        float64  `json:"latencyP95Seconds"`
	ToolErrorRatio           float64  `json:"toolErrorRatio"`
	MinToolRequestsPerSecond float64  `json:"minToolRequestsPerSecond"`
	NotReadyFor              Duration `json:"notReadyFor"`
	DiscoveryFailingFor      Duration `json:"discoveryFailingFor"`
	RunbookBaseURL           string   `json:"runbookBaseURL"`
}

// Duration is a time.Duration written like a Prometheus duration ("10m").
type Duration time.Duration

func (d *Duration) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return errors.New("duration must be a string such as 10m")
	}
	parsed, err := time.ParseDuration(s)
	if err != nil || parsed <= 0 {
		return fmt.Errorf("invalid duration %q", s)
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// DefaultConfig is the chart's defaults, with runbooks in this repository
// at the Gateway's release tag.
func DefaultConfig(version string) Config {
	return Config{
		ErrorRatio:               0.05,
		MinRequestsPerSecond:     0.05,
		LatencyP95Seconds:        5,
		ToolErrorRatio:           0.2,
		MinToolRequestsPerSecond: 0.02,
		NotReadyFor:              Duration(10 * time.Minute),
		DiscoveryFailingFor:      Duration(15 * time.Minute),
		RunbookBaseURL:           "https://github.com/kavrynt/kavrynt/blob/v" + strings.TrimPrefix(version, "v") + "/docs/runbooks",
	}
}

// ParseConfig reads a configuration over defaults: absent fields keep
// their default.
func ParseConfig(data []byte, defaults Config) (Config, error) {
	cfg := defaults
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, err
	}
	switch {
	case cfg.ErrorRatio <= 0 || cfg.ErrorRatio > 1:
		return Config{}, errors.New("errorRatio must be in (0, 1]")
	case cfg.ToolErrorRatio <= 0 || cfg.ToolErrorRatio > 1:
		return Config{}, errors.New("toolErrorRatio must be in (0, 1]")
	case cfg.LatencyP95Seconds <= 0:
		return Config{}, errors.New("latencyP95Seconds must be positive")
	case cfg.MinRequestsPerSecond < 0 || cfg.MinToolRequestsPerSecond < 0:
		return Config{}, errors.New("minimum request rates must not be negative")
	}
	base, err := url.Parse(cfg.RunbookBaseURL)
	if err != nil || base.Scheme != "https" || base.Host == "" {
		return Config{}, errors.New("runbookBaseURL must be an https URL")
	}
	cfg.RunbookBaseURL = strings.TrimSuffix(cfg.RunbookBaseURL, "/")
	return cfg, nil
}

// minCalls is how many calls in the window a rate over it needs, like
// rate(...[10m]) >= perSecond in the Prometheus rules.
func minCalls(perSecond float64) uint64 {
	return uint64(math.Ceil(perSecond * Window10m.Seconds()))
}

// FileConfig returns the configuration in path, reread when the file
// changes (the kubelet updates a mounted ConfigMap in place). A missing
// file gives the defaults; an invalid one keeps the last valid
// configuration and is logged.
func FileConfig(path string, defaults Config, logger *slog.Logger) func() Config {
	if logger == nil {
		logger = slog.Default()
	}
	var (
		mu      sync.Mutex
		current = defaults
		modTime time.Time
		size    int64 = -1
	)
	return func() Config {
		mu.Lock()
		defer mu.Unlock()
		info, err := os.Stat(path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				logger.Warn("cannot read the alerts configuration; keeping the last one", "path", path, "error", err)
			}
			return current
		}
		if info.ModTime().Equal(modTime) && info.Size() == size {
			return current
		}
		modTime, size = info.ModTime(), info.Size()
		data, err := os.ReadFile(path) // #nosec G304 -- the operator-set --alerts-config file.
		if err != nil {
			logger.Warn("cannot read the alerts configuration; keeping the last one", "path", path, "error", err)
			return current
		}
		cfg, err := ParseConfig(data, defaults)
		if err != nil {
			logger.Warn("invalid alerts configuration; keeping the last one", "path", path, "error", err)
			return current
		}
		current = cfg
		return current
	}
}
