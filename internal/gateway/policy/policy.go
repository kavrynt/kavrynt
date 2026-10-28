// Package policy decides whether the Gateway forwards an MCP request
// (SPEC-0012, ADR-0003). Policies are Rego files, mounted by the kubelet from
// a ConfigMap and evaluated by Open Policy Agent embedded in the Gateway.
// The query data.kavrynt.authz.decision must produce {"allow": true}; every
// other answer, error, or timeout denies.
package policy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/rego"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	// Query is the decision the policy must define.
	Query = "data.kavrynt.authz.decision"
	// EvalTimeout bounds one evaluation (SPEC-0012-R07).
	EvalTimeout = 100 * time.Millisecond
	// maxReasonLength bounds the reason returned to clients (SPEC-0012-R08).
	maxReasonLength = 200
	// maxPolicyBytes bounds the policy files read from the mount.
	maxPolicyBytes = 4 << 20
)

// deniedBuiltins could reach the network or the Gateway's environment.
var deniedBuiltins = map[string]bool{
	"http.send":          true,
	"net.lookup_ip_addr": true,
	"opa.runtime":        true,
}

// Input is the document a policy decides on (SPEC-0012-R06).
type Input struct {
	// Identity of the caller (SPEC-0006); empty without client
	// authentication.
	Subject  string   `json:"subject"`
	ClientID string   `json:"client_id"`
	Issuer   string   `json:"issuer"`
	Scopes   []string `json:"scopes"`

	Route      Route  `json:"route"`
	HTTPMethod string `json:"http_method"`
	// MCPMethod is the JSON-RPC method of a POST, empty otherwise (for
	// example a GET event stream or a JSON-RPC response).
	MCPMethod string `json:"mcp_method"`
	// Tool is params.name of tools/call, empty otherwise.
	Tool string `json:"tool"`
	// ToolAnnotations are the behavior hints the Operator recorded for Tool
	// on this route, by MCP name, absent hints absent; {} otherwise. They
	// are the MCP server's own claims (SPEC-0028-R03).
	ToolAnnotations map[string]bool `json:"tool_annotations"`
	// ToolDiscovered reports whether Tool is among the route's discovered
	// tools (SPEC-0028-R04).
	ToolDiscovered bool `json:"tool_discovered"`
	// Time is the request time, RFC 3339 in UTC.
	Time string `json:"time"`
}

// Route names the MCPServer a request is for.
type Route struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// Decision is the outcome of one evaluation.
type Decision struct {
	Allow  bool
	Reason string
}

type compiled struct {
	query    rego.PreparedEvalQuery
	revision string
}

// Engine holds the active policy and replaces it when the files change.
type Engine struct {
	dir    string
	logger *slog.Logger

	current atomic.Pointer[compiled]
	// mu serializes reloads.
	mu sync.Mutex

	loadFailures prometheus.Counter
	info         *prometheus.GaugeVec
	decisions    *prometheus.CounterVec
}

// New compiles the policy in dir. A policy that does not compile is an
// error naming the file and line, and the Gateway does not start with it
// (SPEC-0012-R03).
func New(dir string, logger *slog.Logger) (*Engine, error) {
	if logger == nil {
		logger = slog.Default()
	}
	e := &Engine{
		dir:    dir,
		logger: logger,
		loadFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "kavrynt_gateway_policy_load_failures_total",
			Help: "Policy updates that did not compile; the previous policy stayed in force.",
		}),
		info: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "kavrynt_gateway_policy_info",
			Help: "The active policy, by the SHA-256 of its files (value 1).",
		}, []string{"revision"}),
		decisions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kavrynt_gateway_policy_decisions_total",
			Help: "Policy decisions by result (allow, deny, error).",
		}, []string{"result"}),
	}
	c, err := e.compile(context.Background())
	if err != nil {
		return nil, err
	}
	e.activate(c)
	return e, nil
}

// Collectors are the engine's metrics, for the Gateway's registry
// (SPEC-0012-R04, R05).
func (e *Engine) Collectors() []prometheus.Collector {
	return []prometheus.Collector{e.loadFailures, e.info, e.decisions}
}

// Revision is the SHA-256 of the active policy files.
func (e *Engine) Revision() string {
	return e.current.Load().revision
}

// Reload compiles the files again and switches to them atomically when they
// changed. A policy that does not compile is logged and counted, and the
// previous one stays in force (SPEC-0012-R04).
func (e *Engine) Reload(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	modules, revision, err := e.read()
	if err == nil && revision == e.Revision() {
		return nil
	}
	if err == nil {
		var c *compiled
		c, err = e.prepare(ctx, modules, revision)
		if err == nil {
			e.activate(c)
			e.logger.Info("policy loaded", "revision", revision)
			return nil
		}
	}
	e.loadFailures.Inc()
	e.logger.Error("policy update rejected; the previous policy stays in force", "revision", e.Revision(), "error", err)
	return err
}

// Watch reloads the policy whenever its files change, checking every
// interval, until ctx ends. The kubelet replaces ConfigMap files atomically.
func (e *Engine) Watch(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = e.Reload(ctx)
		}
	}
}

// Decide evaluates the active policy for input. Anything but a decision
// object with allow == true denies (SPEC-0012-R07).
func (e *Engine) Decide(ctx context.Context, input Input) Decision {
	ctx, cancel := context.WithTimeout(ctx, EvalTimeout)
	defer cancel()
	results, err := e.current.Load().query.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		e.decisions.WithLabelValues("error").Inc()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return Decision{Reason: "policy evaluation timed out"}
		}
		return Decision{Reason: "policy evaluation failed"}
	}
	decision := Decision{Reason: "no policy decision"}
	if len(results) == 1 && len(results[0].Expressions) == 1 {
		if object, ok := results[0].Expressions[0].Value.(map[string]any); ok {
			allow, _ := object["allow"].(bool)
			reason, _ := object["reason"].(string)
			decision = Decision{Allow: allow, Reason: sanitize(reason)}
		}
	}
	if decision.Allow {
		e.decisions.WithLabelValues("allow").Inc()
	} else {
		e.decisions.WithLabelValues("deny").Inc()
	}
	return decision
}

func (e *Engine) activate(c *compiled) {
	if previous := e.current.Load(); previous != nil {
		e.info.DeleteLabelValues(previous.revision)
	}
	e.current.Store(c)
	e.info.WithLabelValues(c.revision).Set(1)
}

func (e *Engine) compile(ctx context.Context) (*compiled, error) {
	modules, revision, err := e.read()
	if err != nil {
		return nil, err
	}
	return e.prepare(ctx, modules, revision)
}

// read returns the .rego files of the policy directory, in name order, and
// their revision.
func (e *Engine) read() (map[string]string, string, error) {
	entries, err := os.ReadDir(e.dir)
	if err != nil {
		return nil, "", fmt.Errorf("read policy directory: %w", err)
	}
	var names []string
	for _, entry := range entries {
		// Skip the kubelet's ..data and timestamped directories.
		if strings.HasPrefix(entry.Name(), "..") || !strings.HasSuffix(entry.Name(), ".rego") {
			continue
		}
		names = append(names, entry.Name())
	}
	if len(names) == 0 {
		return nil, "", fmt.Errorf("no .rego files in %s", e.dir)
	}
	sort.Strings(names)
	hash := sha256.New()
	modules := map[string]string{}
	total := 0
	for _, name := range names {
		// #nosec G304 -- names come from listing the mounted policy directory.
		data, err := os.ReadFile(filepath.Join(e.dir, name))
		if err != nil {
			return nil, "", fmt.Errorf("read %s: %w", name, err)
		}
		if total += len(data); total > maxPolicyBytes {
			return nil, "", fmt.Errorf("policy files are larger than %d bytes", maxPolicyBytes)
		}
		modules[name] = string(data)
		fmt.Fprintf(hash, "%s\x00%d\x00", name, len(data))
		hash.Write(data)
	}
	return modules, hex.EncodeToString(hash.Sum(nil)), nil
}

func (e *Engine) prepare(ctx context.Context, modules map[string]string, revision string) (*compiled, error) {
	capabilities := ast.CapabilitiesForThisVersion()
	allowed := make([]*ast.Builtin, 0, len(capabilities.Builtins))
	for _, builtin := range capabilities.Builtins {
		if !deniedBuiltins[builtin.Name] {
			allowed = append(allowed, builtin)
		}
	}
	capabilities.Builtins = allowed
	capabilities.AllowNet = []string{} // no host is reachable from a policy

	options := []func(*rego.Rego){
		rego.Query(Query),
		rego.Capabilities(capabilities),
		rego.StrictBuiltinErrors(true),
	}
	names := make([]string, 0, len(modules))
	for name := range modules {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		options = append(options, rego.Module(name, modules[name]))
	}
	query, err := rego.New(options...).PrepareForEval(ctx)
	if err != nil {
		return nil, fmt.Errorf("compile policy: %w", err)
	}
	return &compiled{query: query, revision: revision}, nil
}

// sanitize removes control characters from a policy's reason and bounds its
// length, since it is returned to clients.
func sanitize(reason string) string {
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, reason)
	if runes := []rune(cleaned); len(runes) > maxReasonLength {
		cleaned = string(runes[:maxReasonLength])
	}
	return strings.TrimSpace(cleaned)
}
