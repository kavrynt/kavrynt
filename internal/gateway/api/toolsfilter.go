package api

// Policy-filtered tools/list (SPEC-0016): with the tool authorization policy
// on, a tools/list answer keeps only the tools the policy would let the same
// caller call. Anything the filter cannot finish answers with no tools.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/kavrynt/kavrynt/internal/gateway/policy"
)

// filterDeadline bounds all policy evaluations for one tools/list answer
// (SPEC-0016-R04).
const filterDeadline = 500 * time.Millisecond

var errNotToolsList = errors.New("the answer is not a tools/list result")

// toolsFilter removes the tools a policy denies from tools/list answers.
type toolsFilter struct {
	decide func(context.Context, policy.Input) policy.Decision
	// input is the request's policy input; mcp_method and tool are set per
	// tool.
	input policy.Input
	// id is the JSON-RPC id of the tools/list request.
	id json.RawMessage
}

// filter returns message without the tools the policy denies, and how many
// it removed (SPEC-0016-R01, R03). A JSON-RPC error answer is returned
// unchanged. Every field but the removed tools is kept.
func (f *toolsFilter) filter(ctx context.Context, message []byte) ([]byte, int, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(message, &envelope); err != nil {
		return nil, 0, errNotToolsList
	}
	rawResult, hasResult := envelope["result"]
	if !hasResult {
		if _, isError := envelope["error"]; isError {
			return message, 0, nil
		}
		return nil, 0, errNotToolsList
	}
	var result map[string]json.RawMessage
	var tools []json.RawMessage
	if json.Unmarshal(rawResult, &result) != nil || json.Unmarshal(result["tools"], &tools) != nil || tools == nil {
		return nil, 0, errNotToolsList
	}

	ctx, cancel := context.WithTimeout(ctx, filterDeadline)
	defer cancel()
	kept := make([]json.RawMessage, 0, len(tools))
	for _, tool := range tools {
		var named struct {
			Name *string `json:"name"`
		}
		if json.Unmarshal(tool, &named) != nil || named.Name == nil {
			// A tool without a name cannot be called, so it is not listed.
			continue
		}
		input := f.input
		input.MCPMethod, input.Tool = "tools/call", *named.Name
		decision := f.decide(ctx, input)
		if ctx.Err() != nil {
			return nil, len(tools), fmt.Errorf("the policy did not decide on %d tools within %s", len(tools), filterDeadline)
		}
		if decision.Allow {
			kept = append(kept, tool)
		}
	}
	var err error
	if result["tools"], err = json.Marshal(kept); err != nil {
		return nil, 0, err
	}
	if envelope["result"], err = json.Marshal(result); err != nil {
		return nil, 0, err
	}
	filtered, err := json.Marshal(envelope)
	return filtered, len(tools) - len(kept), err
}

// emptyAnswer is the fail-closed answer: the request's id and no tools
// (SPEC-0016-R04).
func (f *toolsFilter) emptyAnswer() []byte {
	id := f.id
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	answer, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"tools": []any{}}})
	return answer
}

// isAnswer reports whether message is the JSON-RPC response to the request.
func (f *toolsFilter) isAnswer(message []byte) bool {
	return len(f.id) > 0 && isResponseTo(message, f.id)
}

// filterJSON reads a JSON answer within the inspection limit and returns it
// filtered, or the empty answer and the reason it could not be filtered.
func (f *toolsFilter) filterJSON(ctx context.Context, body io.Reader) (answer []byte, removed int, reason error) {
	data, err := io.ReadAll(io.LimitReader(body, maxInspectBytes+1))
	switch {
	case err != nil:
		return f.emptyAnswer(), 0, fmt.Errorf("read the answer: %w", err)
	case len(data) > maxInspectBytes:
		return f.emptyAnswer(), 0, fmt.Errorf("the answer is larger than %d bytes", maxInspectBytes)
	}
	filtered, removed, err := f.filter(ctx, data)
	if err != nil {
		return f.emptyAnswer(), removed, err
	}
	return filtered, removed, nil
}

// filterStream copies a text/event-stream answer event by event, flushing
// each, and filters the event that carries the JSON-RPC response to the
// request; other events pass unchanged (SPEC-0016-R02). An event larger than
// the inspection limit, or a response it cannot filter, ends the stream
// with the empty answer and the reason. err reports a failed copy.
func (f *toolsFilter) filterStream(ctx context.Context, w io.Writer, flush func() error, body io.Reader) (written int64, removed int, reason, err error) {
	reader := bufio.NewReaderSize(body, maxInspectBytes)
	var event bytes.Buffer
	var data [][]byte
	write := func(p []byte) error {
		n, err := w.Write(p)
		written += int64(n)
		if err == nil {
			err = flush()
		}
		return err
	}
	for {
		line, readErr := reader.ReadSlice('\n')
		if errors.Is(readErr, bufio.ErrBufferFull) || event.Len()+len(line) > maxInspectBytes {
			reason = fmt.Errorf("an event is larger than %d bytes", maxInspectBytes)
			return written, removed, reason, write([]byte("data: " + string(f.emptyAnswer()) + "\n\n"))
		}
		event.Write(line)
		trimmed := bytes.TrimRight(line, "\r\n")
		if field, value, ok := bytes.Cut(trimmed, []byte(":")); ok && string(field) == "data" {
			data = append(data, bytes.TrimPrefix(value, []byte(" ")))
		}
		if (len(line) > 0 && len(trimmed) == 0) || (readErr != nil && event.Len() > 0) {
			out := event.Bytes()
			if message := bytes.Join(data, []byte("\n")); len(data) > 0 && f.isAnswer(message) {
				filtered, n, ferr := f.filter(ctx, message)
				removed += n
				if ferr != nil {
					return written, removed, ferr, write([]byte("data: " + string(f.emptyAnswer()) + "\n\n"))
				}
				out = rewriteData(out, filtered)
			}
			if werr := write(out); werr != nil {
				return written, removed, nil, werr
			}
			event.Reset()
			data = nil
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return written, removed, nil, nil
			}
			return written, removed, nil, readErr
		}
	}
}

// rewriteData replaces the data lines of an event with one carrying message,
// keeping its other fields.
func rewriteData(event, message []byte) []byte {
	var out bytes.Buffer
	wroteData := false
	for _, line := range bytes.SplitAfter(event, []byte("\n")) {
		if field, _, ok := bytes.Cut(bytes.TrimRight(line, "\r\n"), []byte(":")); ok && string(field) == "data" {
			if !wroteData {
				out.WriteString("data: ")
				out.Write(message)
				out.WriteString("\n")
				wroteData = true
			}
			continue
		}
		out.Write(line)
	}
	return out.Bytes()
}

// serveFilteredToolsList writes a tools/list answer through the filter and
// returns the bytes written, the tools removed, and a failed copy. Answers
// other than 2xx pass unchanged; a 2xx answer that is neither JSON nor an
// event stream is replaced by the empty answer.
func (h *Handler) serveFilteredToolsList(w http.ResponseWriter, r *http.Request, route string, f *toolsFilter, resp *http.Response) (int64, int, error) {
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		w.WriteHeader(resp.StatusCode)
		n, err := io.Copy(w, resp.Body)
		return n, 0, err
	}
	w.Header().Del("Content-Length")
	var written int64
	var removed int
	var reason, err error
	if isEventStream(resp.Header.Get("Content-Type")) {
		w.WriteHeader(resp.StatusCode)
		controller := http.NewResponseController(w)
		written, removed, reason, err = f.filterStream(r.Context(), w, controller.Flush, resp.Body)
	} else {
		var answer []byte
		if isJSON(resp.Header.Get("Content-Type")) {
			answer, removed, reason = f.filterJSON(r.Context(), resp.Body)
		} else {
			answer, reason = f.emptyAnswer(), fmt.Errorf("the answer is %q, not JSON", resp.Header.Get("Content-Type"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		var n int
		n, err = w.Write(answer)
		written = int64(n)
	}
	if reason != nil {
		slog.Warn("answered tools/list with no tools: the policy filter could not finish", "route", route, "reason", reason.Error())
	}
	if removed > 0 {
		h.filteredTools.WithLabelValues(route).Add(float64(removed))
	}
	return written, removed, err
}
