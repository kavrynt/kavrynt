// Package cloudsync sends the cluster's MCPServer inventory to Kavrynt Cloud
// (kavrynt-cloud LLD-0002, tool discovery and inventory sync). It is the
// Operator's connector: off unless enabled, run on the leader replica only.
//
// Each sync is a full snapshot of every MCPServer in the cluster, because
// Cloud treats a server or tool missing from a snapshot as removed. The
// snapshot never carries tenant or cluster IDs; Cloud derives them from the
// cluster token.
package cloudsync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/meta"
	toolscache "k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	// SyncPath is the Cloud endpoint that receives inventory snapshots.
	SyncPath = "/v1/sync/mcpservers"
	// DefaultInterval is how often a snapshot is sent without changes.
	DefaultInterval = 60 * time.Second
	// ChangeDelay is how soon after an MCPServer change a snapshot is sent.
	ChangeDelay = 5 * time.Second
	// RequestTimeout bounds one sync request.
	RequestTimeout = 30 * time.Second
	// MaxSnapshotBytes is the largest request body Cloud reads.
	MaxSnapshotBytes = 1 << 20
	// DefaultTokenFile is where the chart mounts the cluster token.
	DefaultTokenFile = "/var/run/secrets/kavrynt/cloud/token"

	minBackoff    = 5 * time.Second
	maxBackoff    = 5 * time.Minute
	maxRetryAfter = time.Hour
	maxTokenBytes = 16 << 10
	maxDrainBytes = 64 << 10
)

var (
	syncSuccess = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kavrynt_operator_cloud_sync_success_total",
		Help: "Inventory snapshots Kavrynt Cloud accepted.",
	})
	syncFailure = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kavrynt_operator_cloud_sync_failure_total",
		Help: "Inventory syncs that failed, by reason.",
	}, []string{"reason"})
	syncLastSuccess = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kavrynt_operator_cloud_sync_last_success_timestamp_seconds",
		Help: "Unix time of the last snapshot Kavrynt Cloud accepted.",
	})
)

func init() {
	metrics.Registry.MustRegister(syncSuccess, syncFailure, syncLastSuccess)
}

// Failure reasons, the values of the failure metric's reason label.
const (
	reasonToken       = "token"
	reasonInventory   = "inventory"
	reasonTooLarge    = "too_large"
	reasonNetwork     = "network"
	reasonRejected    = "rejected"
	reasonThrottled   = "throttled"
	reasonServerError = "server_error"
)

// Config configures a Syncer.
type Config struct {
	// URL is the Kavrynt Cloud base URL; it must be https.
	URL string
	// TokenFile holds the cluster token. It is read before every sync, so a
	// rotated Secret takes effect without a restart.
	TokenFile string
	// Interval is how often a snapshot is sent without changes.
	Interval time.Duration
	// Version is the Operator version, sent in the User-Agent.
	Version string
	// Transport overrides http.DefaultTransport (tests).
	Transport http.RoundTripper
}

// Syncer sends inventory snapshots to Kavrynt Cloud. It implements the
// controller-runtime Runnable and LeaderElectionRunnable interfaces.
type Syncer struct {
	reader     client.Reader
	informers  cache.Informers
	httpClient *http.Client
	endpoint   string
	tokenFile  string
	interval   time.Duration
	userAgent  string
	changes    chan struct{}
	trimmed    trimLevel
}

// ValidateURL checks a Kavrynt Cloud base URL: https, a host, and no
// credentials, query, or fragment.
func ValidateURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, errors.New("cloud URL is not a valid URL")
	}
	switch {
	case parsed.Scheme != "https":
		return nil, errors.New("cloud URL must use https")
	case parsed.Host == "":
		return nil, errors.New("cloud URL has no host")
	case parsed.User != nil:
		return nil, errors.New("cloud URL must not contain credentials")
	case parsed.RawQuery != "" || parsed.Fragment != "":
		return nil, errors.New("cloud URL must not contain a query or fragment")
	}
	return parsed, nil
}

// New returns a Syncer that lists MCPServers through reader. informers, when
// set, is watched so that changes are sent within ChangeDelay.
func New(reader client.Reader, informers cache.Informers, cfg Config) (*Syncer, error) {
	base, err := ValidateURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.TokenFile) == "" {
		return nil, errors.New("cloud token file is required")
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	transport := cfg.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &Syncer{
		reader:    reader,
		informers: informers,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   RequestTimeout,
			// The token must only reach the configured URL.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		endpoint:  base.JoinPath(SyncPath).String(),
		tokenFile: cfg.TokenFile,
		interval:  interval,
		userAgent: "kavrynt-operator/" + cfg.Version,
		changes:   make(chan struct{}, 1),
	}, nil
}

// NeedLeaderElection keeps the connector to one replica.
func (s *Syncer) NeedLeaderElection() bool { return true }

// Notify asks for a snapshot within ChangeDelay.
func (s *Syncer) Notify() {
	select {
	case s.changes <- struct{}{}:
	default:
	}
}

// Start sends a snapshot at once, then every interval, within ChangeDelay of
// an MCPServer change, and with backoff after a failure.
func (s *Syncer) Start(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx).WithName("cloud-sync")
	if s.informers != nil {
		informer, err := s.informers.GetInformer(ctx, &kavryntv1alpha1.MCPServer{})
		if err != nil {
			return fmt.Errorf("watch MCPServers: %w", err)
		}
		notify := func(any) { s.Notify() }
		if _, err := informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
			AddFunc:    notify,
			UpdateFunc: func(_, obj any) { s.Notify() },
			DeleteFunc: notify,
		}); err != nil {
			return fmt.Errorf("watch MCPServers: %w", err)
		}
	}
	log.Info("sending MCP server inventory to Kavrynt Cloud", "endpoint", s.endpoint, "interval", s.interval)

	failures := 0
	due := time.Now()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.changes:
			// A change only brings the next sync forward, and never during
			// backoff, so a stream of changes cannot postpone or flood it.
			if failures == 0 && time.Until(due) > ChangeDelay {
				due = time.Now().Add(ChangeDelay)
				resetTimer(timer, ChangeDelay)
			}
		case <-timer.C:
			wait := s.interval
			if err := s.SyncOnce(ctx); err != nil {
				failures++
				wait = backoff(failures)
				var syncErr *Error
				if errors.As(err, &syncErr) && syncErr.RetryAfter > wait {
					wait = min(syncErr.RetryAfter, maxRetryAfter)
				}
				log.Error(err, "inventory sync failed", "retryIn", wait.Round(time.Second))
			} else {
				if failures > 0 {
					log.Info("inventory sync recovered")
				}
				failures = 0
			}
			due = time.Now().Add(wait)
			timer.Reset(wait)
		}
	}
}

// Error is a failed sync. Its message never includes the token or text
// returned by Cloud.
type Error struct {
	Reason string
	// RetryAfter is Cloud's Retry-After on 429 or 503, if any.
	RetryAfter time.Duration
	err        error
}

func (e *Error) Error() string { return e.err.Error() }
func (e *Error) Unwrap() error { return e.err }

func fail(reason string, err error) *Error {
	syncFailure.WithLabelValues(reason).Inc()
	return &Error{Reason: reason, err: err}
}

// SyncOnce sends one snapshot.
func (s *Syncer) SyncOnce(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx).WithName("cloud-sync")
	token, err := s.readToken()
	if err != nil {
		return fail(reasonToken, err)
	}
	var list kavryntv1alpha1.MCPServerList
	if err := s.reader.List(ctx, &list); err != nil {
		return fail(reasonInventory, fmt.Errorf("list MCPServers: %w", err))
	}
	body, level, err := encodeSnapshot(buildSnapshot(list.Items), MaxSnapshotBytes)
	if err != nil {
		return fail(reasonTooLarge, err)
	}
	if level != s.trimmed {
		if level != trimNone {
			log.Info("inventory snapshot trimmed to fit Kavrynt Cloud's request limit; Cloud keeps no "+level.String()+" until it fits again",
				"servers", len(list.Items))
		}
		s.trimmed = level
	}

	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return fail(reasonNetwork, fmt.Errorf("build sync request: %w", err))
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", s.userAgent)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		// The URL is ours and holds no secret; the error may repeat it.
		return fail(reasonNetwork, fmt.Errorf("send sync request: %w", err))
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainBytes))

	switch code := resp.StatusCode; {
	case code >= 200 && code < 300:
		syncSuccess.Inc()
		syncLastSuccess.SetToCurrentTime()
		return nil
	case code == http.StatusTooManyRequests:
		e := fail(reasonThrottled, fmt.Errorf("sync throttled by Kavrynt Cloud (HTTP %d)", code))
		e.RetryAfter = retryAfter(resp.Header.Get("Retry-After"))
		return e
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return fail(reasonRejected, fmt.Errorf("cluster token rejected by Kavrynt Cloud (HTTP %d); check that it is current and has the registry:sync scope", code))
	case code >= 500:
		e := fail(reasonServerError, fmt.Errorf("sync failed in Kavrynt Cloud (HTTP %d)", code))
		if code == http.StatusServiceUnavailable {
			e.RetryAfter = retryAfter(resp.Header.Get("Retry-After"))
		}
		return e
	default:
		return fail(reasonRejected, fmt.Errorf("sync rejected by Kavrynt Cloud (HTTP %d)", code))
	}
}

func (s *Syncer) readToken() (string, error) {
	file, err := os.Open(s.tokenFile)
	if err != nil {
		return "", fmt.Errorf("read cluster token: %w", err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxTokenBytes+1))
	if err != nil {
		return "", fmt.Errorf("read cluster token: %w", err)
	}
	if len(raw) > maxTokenBytes {
		return "", errors.New("cluster token file is larger than 16 KiB")
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", errors.New("cluster token file is empty")
	}
	for _, r := range token {
		if r <= ' ' || r == 0x7f {
			return "", errors.New("cluster token contains whitespace or control characters")
		}
	}
	return token, nil
}

// snapshot is the body of POST /v1/sync/mcpservers. Cloud rejects unknown
// fields, so it carries only fields Cloud defines.
type snapshot struct {
	Servers []server `json:"servers"`
}

type server struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Transport string `json:"transport"`
	Endpoint  string `json:"endpoint,omitempty"`
	Version   string `json:"version,omitempty"`
	Owner     string `json:"owner"`
	Status    string `json:"status"`
	Tools     []tool `json:"tools"`
}

// tool leaves risk classification out, so Cloud keeps what users set.
type tool struct {
	Name            string          `json:"name"`
	Description     string          `json:"description,omitempty"`
	InputSchemaJSON json.RawMessage `json:"inputSchemaJson,omitempty"`
}

func buildSnapshot(items []kavryntv1alpha1.MCPServer) snapshot {
	out := snapshot{Servers: make([]server, 0, len(items))}
	for _, item := range items {
		status := "not_ready"
		if meta.IsStatusConditionTrue(item.Status.Conditions, kavryntv1alpha1.ConditionReady) {
			status = "ready"
		}
		srv := server{
			Name:      item.Name,
			Namespace: item.Namespace,
			Transport: item.Spec.Transport,
			Endpoint:  redactEndpoint(item.Spec.Endpoint),
			Version:   item.Spec.Version,
			Owner:     item.Namespace,
			Status:    status,
			Tools:     make([]tool, 0, len(item.Status.Tools)),
		}
		for _, t := range item.Status.Tools {
			entry := tool{Name: t.Name, Description: t.Description}
			if t.InputSchema != nil && len(t.InputSchema.Raw) > 0 {
				entry.InputSchemaJSON = json.RawMessage(t.InputSchema.Raw)
			}
			srv.Tools = append(srv.Tools, entry)
		}
		out.Servers = append(out.Servers, srv)
	}
	sort.Slice(out.Servers, func(i, j int) bool {
		a, b := out.Servers[i], out.Servers[j]
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name
	})
	return out
}

// redactEndpoint drops the query and fragment, which may carry secrets.
// Admission already rejects credentials in the URL.
func redactEndpoint(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String()
}

// trimLevel records what a snapshot left out to fit the request limit.
type trimLevel int

const (
	trimNone trimLevel = iota
	trimSchemas
	trimDescriptions
)

func (l trimLevel) String() string {
	switch l {
	case trimSchemas:
		return "tool input schemas"
	case trimDescriptions:
		return "tool input schemas or descriptions"
	default:
		return "nothing"
	}
}

// encodeSnapshot marshals snap within limit bytes: whole, then without input
// schemas, then without descriptions. It never drops servers or tools,
// because Cloud would mark them removed.
func encodeSnapshot(snap snapshot, limit int) ([]byte, trimLevel, error) {
	for level := trimNone; level <= trimDescriptions; level++ {
		if level > trimNone {
			snap = trimSnapshot(snap, level)
		}
		body, err := json.Marshal(snap)
		if err != nil {
			return nil, level, fmt.Errorf("encode snapshot: %w", err)
		}
		if len(body) <= limit {
			return body, level, nil
		}
	}
	return nil, trimDescriptions, fmt.Errorf("inventory of %d MCP servers exceeds Kavrynt Cloud's %d byte request limit even without tool schemas and descriptions", len(snap.Servers), limit)
}

// trimSnapshot returns a copy of snap without input schemas and, at
// trimDescriptions, without descriptions.
func trimSnapshot(snap snapshot, level trimLevel) snapshot {
	out := snapshot{Servers: make([]server, len(snap.Servers))}
	for i, srv := range snap.Servers {
		srv.Tools = append([]tool(nil), srv.Tools...)
		if srv.Tools == nil {
			srv.Tools = []tool{}
		}
		for j := range srv.Tools {
			srv.Tools[j].InputSchemaJSON = nil
			if level == trimDescriptions {
				srv.Tools[j].Description = ""
			}
		}
		out.Servers[i] = srv
	}
	return out
}

// backoff returns the wait after the given number of consecutive failures:
// exponential from minBackoff to maxBackoff, with jitter.
func backoff(failures int) time.Duration {
	d := maxBackoff
	if failures < 16 {
		d = min(minBackoff<<(failures-1), maxBackoff)
	}
	return d/2 + rand.N(d/2+1)
}

// retryAfter parses a Retry-After header in seconds or as an HTTP date.
func retryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return min(time.Duration(seconds)*time.Second, maxRetryAfter)
	}
	if at, err := http.ParseTime(value); err == nil {
		return min(max(time.Until(at), 0), maxRetryAfter)
	}
	return 0
}

func resetTimer(timer *time.Timer, d time.Duration) {
	timer.Stop()
	timer.Reset(d)
}
