// Package discovery lists an MCP server's tools for MCPServer status
// (kavrynt-cloud LLD-0002, tool discovery). It runs one short MCP client
// session per server and is not a proxy: no client traffic passes through it.
//
// Everything an MCP server returns is untrusted. Responses are size-limited,
// tool metadata is bounded before it reaches status, and error messages never
// include text from the server.
package discovery

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	kavryntv1alpha1 "github.com/kavrynt/kavrynt/api/v1alpha1"
	"github.com/kavrynt/kavrynt/internal/mcpmeta"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	// ProtocolVersion is the MCP version the Operator offers in initialize.
	ProtocolVersion = "2025-06-18"

	// Timeout bounds one discovery session.
	Timeout = 10 * time.Second
	// MaxResponseBytes bounds each response body.
	MaxResponseBytes = 1 << 20
	// MaxPages bounds tools/list pages per discovery.
	MaxPages = 5

	// MaxDescriptionLength bounds a tool description in characters.
	MaxDescriptionLength = 1024
	// MaxSchemaBytes bounds one tool's input schema; larger schemas are omitted.
	MaxSchemaBytes = 16 << 10
	// MaxStatusBytes bounds the tools written to one MCPServer status.
	MaxStatusBytes = 256 << 10
	// MaxServerInfoLength bounds the reported serverInfo name and version in
	// characters; the CRD enforces the same limit.
	MaxServerInfoLength = 128

	closeTimeout = 2 * time.Second
)

var (
	errResponseTooLarge = errors.New("response exceeds 1 MiB")
	protocolPattern     = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
)

// Result is the bounded outcome of one discovery.
type Result struct {
	Tools []kavryntv1alpha1.MCPServerTool
	// NoToolsCapability is set when the server does not declare tools.
	NoToolsCapability bool
	// Skipped counts tools dropped for an invalid or duplicate name.
	Skipped int
	// Truncated is set when tools were dropped to stay within limits.
	Truncated bool
	// SchemasDropped is set when input schemas were omitted to fit status.
	SchemasDropped bool
	// ServerInfo is the bounded serverInfo from initialize; nil when the
	// server reported no usable name or version.
	ServerInfo *kavryntv1alpha1.MCPServerInfo
}

// Summary describes the result for a condition message.
func (r Result) Summary() string {
	if r.NoToolsCapability {
		return "MCP server declares no tools capability"
	}
	parts := []string{fmt.Sprintf("Discovered %d tools", len(r.Tools))}
	if r.Skipped > 0 {
		parts = append(parts, fmt.Sprintf("skipped %d with an invalid or duplicate name", r.Skipped))
	}
	if r.Truncated {
		parts = append(parts, "list truncated to stay within limits")
	}
	if r.SchemasDropped {
		parts = append(parts, "input schemas omitted to fit status")
	}
	return strings.Join(parts, "; ")
}

// Client discovers tools over the MCP Streamable HTTP transport.
type Client struct {
	httpClient *http.Client
	version    string
}

// New returns a Client. A nil transport uses http.DefaultTransport.
func New(version string, transport http.RoundTripper) *Client {
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &Client{
		httpClient: &http.Client{
			Transport: transport,
			// A redirect could point anywhere; report it instead of following.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		version: version,
	}
}

// ListTools initializes an MCP session with endpoint and lists its tools.
// header is sent with every request, for a server's stored credential
// (SPEC-0011-R08); it may be nil.
func (c *Client) ListTools(ctx context.Context, endpoint string, header http.Header) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()

	s := &session{client: c, endpoint: endpoint, header: header}
	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Tools json.RawMessage `json:"tools"`
		} `json:"capabilities"`
		ServerInfo json.RawMessage `json:"serverInfo"`
	}
	params := map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "kavrynt-operator", "version": c.version},
	}
	if err := s.call(ctx, "initialize", params, &initialized); err != nil {
		return Result{}, err
	}
	defer s.close(ctx)

	s.protocolVersion = ProtocolVersion
	if protocolPattern.MatchString(initialized.ProtocolVersion) {
		s.protocolVersion = initialized.ProtocolVersion
	}
	s.notify(ctx, "notifications/initialized")

	serverInfo := boundServerInfo(initialized.ServerInfo)
	if len(initialized.Capabilities.Tools) == 0 || string(initialized.Capabilities.Tools) == "null" {
		return Result{NoToolsCapability: true, ServerInfo: serverInfo}, nil
	}

	var raw []json.RawMessage
	truncated := false
	cursor := ""
	for page := 0; ; page++ {
		if page == MaxPages {
			truncated = true
			break
		}
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var listed struct {
			Tools      []json.RawMessage `json:"tools"`
			NextCursor string            `json:"nextCursor"`
		}
		if err := s.call(ctx, "tools/list", params, &listed); err != nil {
			return Result{}, err
		}
		raw = append(raw, listed.Tools...)
		if listed.NextCursor == "" {
			break
		}
		cursor = listed.NextCursor
	}

	result := buildResult(raw)
	result.Truncated = result.Truncated || truncated
	result.ServerInfo = serverInfo
	return result, nil
}

// boundServerInfo keeps a reported name and version only when each is a
// non-blank string of at most MaxServerInfoLength printable characters
// (SPEC-0004-R01). It is decoded apart from the rest of the initialize
// result so a malformed value cannot fail the whole discovery.
func boundServerInfo(raw json.RawMessage) *kavryntv1alpha1.MCPServerInfo {
	var fields struct {
		Name    json.RawMessage `json:"name"`
		Version json.RawMessage `json:"version"`
	}
	if json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	info := kavryntv1alpha1.MCPServerInfo{Name: boundInfoField(fields.Name), Version: boundInfoField(fields.Version)}
	if info.Name == "" && info.Version == "" {
		return nil
	}
	return &info
}

func boundInfoField(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) != nil || strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > MaxServerInfoLength {
		return ""
	}
	for _, r := range value {
		if r == utf8.RuneError || !unicode.IsPrint(r) {
			return ""
		}
	}
	return value
}

// buildResult validates and bounds raw tools/list entries.
func buildResult(raw []json.RawMessage) Result {
	var result Result
	seen := map[string]bool{}
	for _, entry := range raw {
		var tool struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
			Annotations json.RawMessage `json:"annotations"`
		}
		if json.Unmarshal(entry, &tool) != nil || !mcpmeta.ValidName(tool.Name, mcpmeta.MaxToolNameLength) || seen[tool.Name] {
			result.Skipped++
			continue
		}
		if len(result.Tools) == mcpmeta.MaxToolsPerServer {
			result.Truncated = true
			break
		}
		seen[tool.Name] = true
		bounded := kavryntv1alpha1.MCPServerTool{
			Name:        tool.Name,
			Description: truncate(tool.Description, MaxDescriptionLength),
		}
		if schema, err := CanonicalJSON(tool.InputSchema); err == nil && len(schema) <= MaxSchemaBytes && schema[0] == '{' {
			bounded.InputSchema = &runtime.RawExtension{Raw: schema}
		}
		bounded.Annotations = behaviorHints(tool.Annotations)
		result.Tools = append(result.Tools, bounded)
	}

	if statusSize(result.Tools) > MaxStatusBytes {
		for i := range result.Tools {
			result.Tools[i].InputSchema = nil
		}
		result.SchemasDropped = true
		for statusSize(result.Tools) > MaxStatusBytes {
			result.Tools = result.Tools[:len(result.Tools)-1]
			result.Truncated = true
		}
	}
	return result
}

// behaviorHints returns the four MCP behavior hints that raw reports as JSON
// booleans under their exact names, and nil when there are none
// (SPEC-0028-R01). Everything else in annotations, such as title, is dropped.
func behaviorHints(raw json.RawMessage) *kavryntv1alpha1.MCPServerToolAnnotations {
	var members map[string]json.RawMessage
	if json.Unmarshal(raw, &members) != nil {
		return nil
	}
	hint := func(name string) *bool {
		switch string(bytes.TrimSpace(members[name])) {
		case "true":
			return new(true)
		case "false":
			return new(false)
		}
		return nil
	}
	annotations := &kavryntv1alpha1.MCPServerToolAnnotations{
		ReadOnlyHint:    hint("readOnlyHint"),
		DestructiveHint: hint("destructiveHint"),
		IdempotentHint:  hint("idempotentHint"),
		OpenWorldHint:   hint("openWorldHint"),
	}
	if len(annotations.Hints()) == 0 {
		return nil
	}
	return annotations
}

// statusSize approximates the bytes tools take in status: names,
// descriptions, schemas, and encoded annotations (SPEC-0028-R02).
func statusSize(tools []kavryntv1alpha1.MCPServerTool) int {
	size := 0
	for _, tool := range tools {
		size += len(tool.Name) + len(tool.Description)
		if tool.InputSchema != nil {
			size += len(tool.InputSchema.Raw)
		}
		if tool.Annotations != nil {
			encoded, _ := json.Marshal(tool.Annotations)
			size += len(encoded)
		}
	}
	return size
}

// truncate shortens value to at most max characters.
func truncate(value string, max int) string {
	if utf8.RuneCountInString(value) <= max {
		return value
	}
	runes := []rune(value)
	return string(runes[:max])
}

// CanonicalJSON re-encodes one JSON value compactly with sorted object keys and
// number literals unchanged, so equal values compare equal byte for byte.
func CanonicalJSON(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing data after JSON value")
	}
	return json.Marshal(value)
}

// CanonicalTools returns a copy of tools with canonical input schemas.
func CanonicalTools(tools []kavryntv1alpha1.MCPServerTool) []kavryntv1alpha1.MCPServerTool {
	out := kavryntv1alpha1.CopyTools(tools)
	for i := range out {
		if out[i].InputSchema == nil {
			continue
		}
		if schema, err := CanonicalJSON(out[i].InputSchema.Raw); err == nil {
			out[i].InputSchema.Raw = schema
		}
	}
	return out
}

type session struct {
	client          *Client
	endpoint        string
	header          http.Header
	sessionID       string
	protocolVersion string
	nextID          int64
}

type rpcResponse struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code int `json:"code"`
	} `json:"error"`
}

func (s *session) call(ctx context.Context, method string, params any, result any) error {
	s.nextID++
	id := s.nextID
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return err
	}
	resp, err := s.send(ctx, http.MethodPost, body)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned HTTP %d", method, resp.StatusCode)
	}
	if method == "initialize" {
		sessionID := resp.Header.Get("Mcp-Session-Id")
		if !validSessionID(sessionID) {
			return fmt.Errorf("%s returned an invalid session ID", method)
		}
		s.sessionID = sessionID
	}

	message, err := readResponse(resp, id)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	if message.Error != nil {
		return fmt.Errorf("%s returned JSON-RPC error %d", method, message.Error.Code)
	}
	if err := json.Unmarshal(message.Result, result); err != nil {
		return fmt.Errorf("%s returned an invalid result", method)
	}
	return nil
}

// notify sends a JSON-RPC notification. Failures surface on the next call.
func (s *session) notify(ctx context.Context, method string) {
	body, _ := json.Marshal(map[string]string{"jsonrpc": "2.0", "method": method})
	resp, err := s.send(ctx, http.MethodPost, body)
	if err == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, MaxResponseBytes))
		_ = resp.Body.Close()
	}
}

// close ends the MCP session, best effort.
func (s *session) close(ctx context.Context) {
	if s.sessionID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
	defer cancel()
	resp, err := s.send(ctx, http.MethodDelete, nil)
	if err == nil {
		_ = resp.Body.Close()
	}
}

func (s *session) send(ctx context.Context, method string, body []byte) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.endpoint, reader)
	if err != nil {
		return nil, err
	}
	for name, values := range s.header {
		req.Header[name] = append([]string(nil), values...)
	}
	req.Header.Set("User-Agent", "kavrynt-operator/"+s.client.version)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
	}
	if s.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", s.sessionID)
	}
	if s.protocolVersion != "" {
		req.Header.Set("MCP-Protocol-Version", s.protocolVersion)
	}
	return s.client.httpClient.Do(req)
}

// validSessionID accepts an empty ID (no session) or visible ASCII, as MCP
// requires, up to 256 bytes.
func validSessionID(id string) bool {
	if len(id) > 256 {
		return false
	}
	for i := 0; i < len(id); i++ {
		if id[i] < 0x21 || id[i] > 0x7e {
			return false
		}
	}
	return true
}

// readResponse returns the JSON-RPC response with the given id from a JSON or
// server-sent events body.
func readResponse(resp *http.Response, id int64) (rpcResponse, error) {
	body := &cappedReader{r: resp.Body, remaining: MaxResponseBytes}
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	switch mediaType {
	case "application/json":
		data, err := io.ReadAll(body)
		if err != nil {
			return rpcResponse{}, err
		}
		var message rpcResponse
		if json.Unmarshal(data, &message) != nil || !matchesID(message.ID, id) {
			return rpcResponse{}, errors.New("invalid JSON-RPC response")
		}
		return message, nil
	case "text/event-stream":
		return readEventStream(body, id)
	default:
		return rpcResponse{}, errors.New("unsupported response content type")
	}
}

// readEventStream reads server-sent events until the response with id arrives.
// Other messages (server notifications and requests) are ignored.
func readEventStream(body *cappedReader, id int64) (rpcResponse, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64<<10), MaxResponseBytes)
	var data strings.Builder
	dispatch := func() (rpcResponse, bool) {
		defer data.Reset()
		var message rpcResponse
		if data.Len() == 0 || json.Unmarshal([]byte(data.String()), &message) != nil || !matchesID(message.ID, id) {
			return rpcResponse{}, false
		}
		return message, true
	}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if message, ok := dispatch(); ok {
				return message, nil
			}
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if body.exceeded {
		return rpcResponse{}, errResponseTooLarge
	}
	if err := scanner.Err(); err != nil {
		return rpcResponse{}, err
	}
	if message, ok := dispatch(); ok {
		return message, nil
	}
	return rpcResponse{}, errors.New("no JSON-RPC response in event stream")
}

func matchesID(raw json.RawMessage, id int64) bool {
	got, err := strconv.ParseInt(string(bytes.TrimSpace(raw)), 10, 64)
	return err == nil && got == id
}

// cappedReader returns errResponseTooLarge once more than remaining bytes are
// read, so callers can tell a capped body from a complete one.
type cappedReader struct {
	r         io.Reader
	remaining int64
	exceeded  bool
}

func (c *cappedReader) Read(p []byte) (int, error) {
	if c.remaining <= 0 {
		var probe [1]byte
		n, err := c.r.Read(probe[:])
		if n > 0 {
			c.exceeded = true
			return 0, errResponseTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > c.remaining {
		p = p[:c.remaining]
	}
	n, err := c.r.Read(p)
	c.remaining -= int64(n)
	return n, err
}
