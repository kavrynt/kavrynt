package audit

// Delivery of audit events to Kavrynt Cloud (SPEC-0017). Events leave the
// cluster soon after they are written, so their hash chains can be checked
// where an in-cluster attacker cannot rewrite them. Standard output stays
// the record of every event; delivery is at least once and never blocks MCP
// traffic.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	// CloudPath is the Kavrynt Cloud endpoint that receives audit events.
	CloudPath = "/v1/sync/audit-events"
	// DefaultCloudBuffer is how many undelivered events are kept.
	DefaultCloudBuffer = 100000
	// MaxBatchEvents and MaxBatchBytes bound one request (SPEC-0017-R01).
	MaxBatchEvents = 500
	MaxBatchBytes  = 1 << 20
	// FlushInterval is the longest an event waits for a batch.
	FlushInterval = 10 * time.Second
	// CloudRequestTimeout bounds one request.
	CloudRequestTimeout = 30 * time.Second

	minCloudBackoff    = time.Second
	maxCloudBackoff    = 5 * time.Minute
	maxCloudRetryAfter = time.Hour
	maxCloudTokenBytes = 16 << 10
	maxCloudDrainBytes = 64 << 10
	// batchOverhead is the size of {"events":[]}.
	batchOverhead = len(`{"events":[]}`)
)

// Results of a delivery attempt, the values of the delivery metric's result
// label.
const (
	resultDelivered   = "delivered"
	resultThrottled   = "throttled"
	resultRejected    = "rejected"
	resultServerError = "server_error"
	resultNetwork     = "network"
	resultToken       = "token"
)

// CloudConfig configures a Shipper.
type CloudConfig struct {
	// URL is the Kavrynt Cloud base URL; it must be https.
	URL string
	// TokenFile holds the cluster token. It is read before every request, so
	// a rotated Secret takes effect without a restart.
	TokenFile string
	// Buffer is how many undelivered events are kept (DefaultCloudBuffer
	// when zero).
	Buffer int
	// Version is the Gateway version, sent in the User-Agent.
	Version string
	// Transport sends the requests; the Gateway passes egress.Transport.
	Transport http.RoundTripper
	// FlushInterval overrides the 10 seconds (tests).
	FlushInterval time.Duration
	Logger        *slog.Logger
}

// Shipper buffers written events and sends them to Kavrynt Cloud in
// batches. It implements Sink.
type Shipper struct {
	client    *http.Client
	endpoint  string
	tokenFile string
	userAgent string
	interval  time.Duration
	logger    *slog.Logger

	mu sync.Mutex
	// queue[head:] are the undelivered events; base is the position of
	// queue[head] in the stream of events added.
	queue    [][]byte
	head     int
	base     uint64
	bytes    int
	capacity int
	wake     chan struct{}

	attempts    *prometheus.CounterVec
	dropped     prometheus.Counter
	lastSuccess prometheus.Gauge
	buffered    prometheus.GaugeFunc
}

// ValidateCloudURL checks a Kavrynt Cloud base URL: https, a host, and no
// credentials, query, or fragment.
func ValidateCloudURL(raw string) (*url.URL, error) {
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

// NewShipper returns a Shipper; Run sends what it buffers.
func NewShipper(cfg CloudConfig) (*Shipper, error) {
	base, err := ValidateCloudURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.TokenFile) == "" {
		return nil, errors.New("cloud token file is required")
	}
	capacity := cfg.Buffer
	if capacity == 0 {
		capacity = DefaultCloudBuffer
	}
	if capacity < 0 {
		return nil, errors.New("audit cloud buffer must be positive")
	}
	if cfg.Transport == nil {
		return nil, errors.New("cloud transport is required")
	}
	interval := cfg.FlushInterval
	if interval <= 0 {
		interval = FlushInterval
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	s := &Shipper{
		client: &http.Client{
			Transport: cfg.Transport,
			Timeout:   CloudRequestTimeout,
			// The token must only reach the configured URL.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		endpoint:  base.JoinPath(CloudPath).String(),
		tokenFile: cfg.TokenFile,
		userAgent: "kavrynt-gateway/" + cfg.Version,
		interval:  interval,
		logger:    logger,
		capacity:  capacity,
		wake:      make(chan struct{}, 1),
		attempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kavrynt_gateway_audit_delivery_total",
			Help: "Audit event batches sent to Kavrynt Cloud, by result.",
		}, []string{"result"}),
		dropped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "kavrynt_gateway_audit_delivery_dropped_total",
			Help: "Undelivered audit events dropped because the delivery buffer was full; standard output has them.",
		}),
		lastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "kavrynt_gateway_audit_delivery_last_success_timestamp_seconds",
			Help: "Unix time of the last batch Kavrynt Cloud accepted; the Gateway's start until then.",
		}),
	}
	s.buffered = prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "kavrynt_gateway_audit_delivery_buffer_length",
		Help: "Audit events waiting to be delivered to Kavrynt Cloud.",
	}, func() float64 { return float64(s.Len()) })
	s.lastSuccess.SetToCurrentTime()
	return s, nil
}

// Collectors are the shipper's metrics (SPEC-0017-R05).
func (s *Shipper) Collectors() []prometheus.Collector {
	return []prometheus.Collector{s.attempts, s.dropped, s.lastSuccess, s.buffered}
}

// Endpoint is where batches are sent.
func (s *Shipper) Endpoint() string { return s.endpoint }

// Add buffers one written event. When the buffer is full the oldest
// undelivered event is dropped (SPEC-0017-R04). It never blocks.
func (s *Shipper) Add(line []byte) {
	s.mu.Lock()
	if s.pending() >= s.capacity {
		s.bytes -= len(s.queue[s.head])
		s.queue[s.head] = nil
		s.head++
		s.base++
		s.dropped.Inc()
	}
	s.queue = append(s.queue, line)
	s.bytes += len(line)
	full := s.pending() >= MaxBatchEvents || s.bytes+s.pending()+batchOverhead >= MaxBatchBytes
	s.compact()
	s.mu.Unlock()
	if full {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}

// Len is the number of undelivered events.
func (s *Shipper) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending()
}

// pending is Len for callers holding mu.
func (s *Shipper) pending() int { return len(s.queue) - s.head }

// compact drops delivered entries from the front of the queue once they are
// half of it, so memory follows the undelivered events.
func (s *Shipper) compact() {
	if s.head > 0 && s.head >= len(s.queue)/2 {
		s.queue = append(s.queue[:0:0], s.queue[s.head:]...)
		s.head = 0
	}
}

// batch returns the oldest undelivered events that fit one request, and the
// stream position just after the last of them; nil when none wait.
func (s *Shipper) batch() ([]byte, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending() == 0 {
		return nil, s.base
	}
	var body bytes.Buffer
	body.WriteString(`{"events":[`)
	end := s.base
	for i, line := range s.queue[s.head:] {
		if i == MaxBatchEvents || (i > 0 && body.Len()+1+len(line)+2 > MaxBatchBytes) {
			break
		}
		if i > 0 {
			body.WriteByte(',')
		}
		body.Write(line)
		end++
	}
	body.WriteString(`]}`)
	return body.Bytes(), end
}

// delivered removes the events before stream position end, except those
// overflow has dropped meanwhile.
func (s *Shipper) delivered(end uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.base < end && s.pending() > 0 {
		s.bytes -= len(s.queue[s.head])
		s.queue[s.head] = nil
		s.head++
		s.base++
	}
	s.compact()
}

// Run sends batches when MaxBatchEvents or MaxBatchBytes are waiting and at
// least every flush interval, retrying failed batches with backoff
// (SPEC-0017-R03), until ctx ends.
func (s *Shipper) Run(ctx context.Context) {
	s.logger.Info("delivering audit events to Kavrynt Cloud", "endpoint", s.endpoint)
	timer := time.NewTimer(s.interval)
	defer timer.Stop()
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
			if failures > 0 {
				continue
			}
		case <-timer.C:
		}
		wait := s.interval
		if err := s.Flush(ctx); err != nil {
			failures++
			wait = cloudBackoff(failures)
			var deliveryErr *DeliveryError
			if errors.As(err, &deliveryErr) && deliveryErr.RetryAfter > wait {
				wait = deliveryErr.RetryAfter
			}
			s.logger.Error("audit event delivery failed", "error", err, "retryIn", wait.Round(time.Second), "waiting", s.Len())
		} else if failures > 0 {
			s.logger.Info("audit event delivery recovered")
			failures = 0
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(wait)
	}
}

// Flush sends batches until the buffer is empty or one fails.
func (s *Shipper) Flush(ctx context.Context) error {
	for {
		body, end := s.batch()
		if body == nil {
			return nil
		}
		if err := s.send(ctx, body); err != nil {
			return err
		}
		s.delivered(end)
	}
}

// DeliveryError is a failed batch. Its message never includes the token or
// text returned by Cloud (SPEC-0017-R06).
type DeliveryError struct {
	Result string
	// RetryAfter is Cloud's Retry-After on 429 or 503, if any.
	RetryAfter time.Duration
	err        error
}

func (e *DeliveryError) Error() string { return e.err.Error() }
func (e *DeliveryError) Unwrap() error { return e.err }

func (s *Shipper) fail(result string, err error) *DeliveryError {
	s.attempts.WithLabelValues(result).Inc()
	return &DeliveryError{Result: result, err: err}
}

func (s *Shipper) send(ctx context.Context, body []byte) error {
	token, err := readCloudToken(s.tokenFile)
	if err != nil {
		return s.fail(resultToken, err)
	}
	ctx, cancel := context.WithTimeout(ctx, CloudRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return s.fail(resultNetwork, fmt.Errorf("build audit delivery request: %w", err))
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", s.userAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		// The URL is ours and holds no secret; the error may repeat it.
		return s.fail(resultNetwork, fmt.Errorf("send audit events: %w", err))
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxCloudDrainBytes))

	switch code := resp.StatusCode; {
	case code >= 200 && code < 300:
		s.attempts.WithLabelValues(resultDelivered).Inc()
		s.lastSuccess.SetToCurrentTime()
		return nil
	case code == http.StatusTooManyRequests:
		e := s.fail(resultThrottled, fmt.Errorf("audit delivery throttled by Kavrynt Cloud (HTTP %d)", code))
		e.RetryAfter = cloudRetryAfter(resp.Header.Get("Retry-After"))
		return e
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return s.fail(resultRejected, fmt.Errorf("cluster token rejected by Kavrynt Cloud (HTTP %d); check that it is current and may send audit events", code))
	case code >= 500:
		e := s.fail(resultServerError, fmt.Errorf("audit delivery failed in Kavrynt Cloud (HTTP %d)", code))
		if code == http.StatusServiceUnavailable {
			e.RetryAfter = cloudRetryAfter(resp.Header.Get("Retry-After"))
		}
		return e
	default:
		return s.fail(resultRejected, fmt.Errorf("audit events rejected by Kavrynt Cloud (HTTP %d)", code))
	}
}

func readCloudToken(path string) (string, error) {
	file, err := os.Open(path) // #nosec G304 -- the operator-set --audit-cloud-token-file.
	if err != nil {
		return "", fmt.Errorf("read cluster token: %w", err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxCloudTokenBytes+1))
	if err != nil {
		return "", fmt.Errorf("read cluster token: %w", err)
	}
	if len(raw) > maxCloudTokenBytes {
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

func cloudBackoff(failures int) time.Duration {
	d := maxCloudBackoff
	if failures < 16 {
		d = min(minCloudBackoff<<(failures-1), maxCloudBackoff)
	}
	return d/2 + rand.N(d/2+1) // #nosec G404 -- backoff jitter needs no cryptographic randomness.
}

// cloudRetryAfter parses a Retry-After header in seconds or as an HTTP date.
func cloudRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return min(time.Duration(seconds)*time.Second, maxCloudRetryAfter)
	}
	if at, err := http.ParseTime(value); err == nil {
		return min(max(time.Until(at), 0), maxCloudRetryAfter)
	}
	return 0
}
