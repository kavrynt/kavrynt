// Command mcp-server is the MCP server used by the Kavrynt demo
// (docs/DEMO.md). One binary serves one of two profiles, selected with
// DEMO_PROFILE:
//
//   - incidents: list, inspect, and acknowledge on-call incidents;
//   - runbooks:  search and read operational runbooks.
//
// All data is fictional and kept in memory. The server speaks the JSON
// response mode of MCP Streamable HTTP: POST carries one JSON-RPC message,
// notifications are answered with 202, and GET and DELETE return 405 because
// the server neither pushes messages nor keeps sessions. That is enough for
// MCP SDK clients such as MCP Inspector and Claude Code.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const protocolVersion = "2025-06-18"

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *responseError  `json:"error,omitempty"`
}

type responseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	call        func(args json.RawMessage) (string, error)
}

// profile is one demo MCP server: its identity and its tools.
type profile struct {
	name  string
	tools []tool
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	name := os.Getenv("DEMO_PROFILE")
	p, err := newProfile(name)
	if err != nil {
		logger.Error("invalid profile", "error", err)
		os.Exit(2)
	}

	server := &http.Server{
		Addr:              ":8080",
		Handler:           newHandler(p, logger),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	logger.Info("starting demo MCP server", "profile", p.name, "addr", server.Addr)
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func newProfile(name string) (*profile, error) {
	switch name {
	case "incidents":
		return incidentsProfile(), nil
	case "runbooks":
		return runbooksProfile(), nil
	default:
		return nil, fmt.Errorf("DEMO_PROFILE must be incidents or runbooks, got %q", name)
	}
}

func newHandler(p *profile, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	// Demo-only: echoes the request headers the server received, so the
	// presenter can show that the Gateway strips caller credentials.
	mux.HandleFunc("GET /debug/headers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(r.Header)
	})
	mux.HandleFunc("POST /", func(w http.ResponseWriter, r *http.Request) {
		p.serveMCP(w, r, logger)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	return mux
}

func (p *profile) serveMCP(w http.ResponseWriter, r *http.Request, logger *slog.Logger) {
	defer r.Body.Close()

	var input request
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(&input); err != nil || input.JSONRPC != "2.0" || input.Method == "" {
		writeResponse(w, http.StatusBadRequest, response{
			JSONRPC: "2.0",
			ID:      json.RawMessage("null"),
			Error:   &responseError{Code: -32600, Message: "invalid JSON-RPC request"},
		})
		return
	}

	// Notifications (no id) such as notifications/initialized need no answer.
	if len(input.ID) == 0 || string(input.ID) == "null" {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	result, rpcErr := p.handle(input.Method, input.Params)
	if rpcErr != nil {
		writeResponse(w, http.StatusOK, response{JSONRPC: "2.0", ID: input.ID, Error: rpcErr})
		return
	}
	if input.Method == "tools/call" {
		// Log the tool name only: arguments are caller data.
		var call struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(input.Params, &call)
		logger.Info("tool called", "tool", call.Name)
	}
	writeResponse(w, http.StatusOK, response{JSONRPC: "2.0", ID: input.ID, Result: result})
}

func (p *profile) handle(method string, params json.RawMessage) (any, *responseError) {
	switch method {
	case "initialize":
		return map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities": map[string]any{
				"tools": map[string]bool{"listChanged": false},
			},
			"serverInfo": map[string]string{
				"name":    "kavrynt-demo-" + p.name,
				"version": "0.1.0",
			},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": p.tools}, nil
	case "tools/call":
		var call struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(params, &call); err != nil {
			return nil, &responseError{Code: -32602, Message: "invalid tools/call params"}
		}
		for _, t := range p.tools {
			if t.Name == call.Name {
				return toolResult(t.call(call.Arguments)), nil
			}
		}
		return toolResult("", fmt.Errorf("unknown tool %q", call.Name)), nil
	default:
		return nil, &responseError{Code: -32601, Message: "method not found"}
	}
}

// toolResult reports tool failures as MCP tool errors (result.isError), which
// the Gateway counts as outcome="tool_error".
func toolResult(text string, err error) map[string]any {
	if err != nil {
		return map[string]any{
			"isError": true,
			"content": []map[string]string{{"type": "text", "text": err.Error()}},
		}
	}
	return map[string]any{
		"content": []map[string]string{{"type": "text", "text": text}},
	}
}

func writeResponse(w http.ResponseWriter, status int, value response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func objectSchema(required []string, properties map[string]any) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func decodeArgs(raw json.RawMessage, into any) error {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

// toJSON renders tool output for people: indented, and without escaping the
// <placeholders> in runbook steps.
func toJSON(value any) (string, error) {
	var out strings.Builder
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	err := encoder.Encode(value)
	return strings.TrimSuffix(out.String(), "\n"), err
}

type incident struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Service      string `json:"service"`
	Severity     string `json:"severity"`
	Status       string `json:"status"`
	Opened       string `json:"opened"`
	Summary      string `json:"summary"`
	Runbook      string `json:"runbook"`
	Acknowledged string `json:"acknowledgedNote,omitempty"`
}

func incidentsProfile() *profile {
	var mu sync.Mutex
	incidents := map[string]*incident{
		"INC-1042": {
			ID: "INC-1042", Title: "checkout p99 latency above 2s", Service: "checkout",
			Severity: "sev2", Status: "open", Opened: "2026-10-22T08:14:00Z",
			Summary: "p99 latency rose after the 08:00 deploy; error rate is normal.",
			Runbook: "RB-7",
		},
		"INC-1043": {
			ID: "INC-1043", Title: "payments pods in CrashLoopBackOff", Service: "payments",
			Severity: "sev1", Status: "open", Opened: "2026-10-22T09:02:00Z",
			Summary: "payments-api restarts on startup: missing DB_PASSWORD after Secret rotation.",
			Runbook: "RB-3",
		},
		"INC-1039": {
			ID: "INC-1039", Title: "search index lag", Service: "search",
			Severity: "sev3", Status: "resolved", Opened: "2026-10-21T17:40:00Z",
			Summary: "Indexer fell behind during a reindex; caught up after scaling to 3 replicas.",
			Runbook: "RB-12",
		},
	}

	return &profile{name: "incidents", tools: []tool{
		{
			Name:        "list_incidents",
			Description: "List on-call incidents, optionally filtered by status (open or resolved).",
			InputSchema: objectSchema(nil, map[string]any{
				"status": map[string]any{"type": "string", "enum": []string{"open", "resolved"}},
			}),
			call: func(raw json.RawMessage) (string, error) {
				var args struct {
					Status string `json:"status"`
				}
				if err := decodeArgs(raw, &args); err != nil {
					return "", err
				}
				mu.Lock()
				defer mu.Unlock()
				list := make([]incident, 0, len(incidents))
				for _, inc := range incidents {
					if args.Status == "" || inc.Status == args.Status {
						list = append(list, *inc)
					}
				}
				sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
				return toJSON(list)
			},
		},
		{
			Name:        "get_incident",
			Description: "Get one incident by id, including its summary and linked runbook.",
			InputSchema: objectSchema([]string{"id"}, map[string]any{
				"id": map[string]any{"type": "string", "description": "Incident id, e.g. INC-1043"},
			}),
			call: func(raw json.RawMessage) (string, error) {
				var args struct {
					ID string `json:"id"`
				}
				if err := decodeArgs(raw, &args); err != nil {
					return "", err
				}
				mu.Lock()
				defer mu.Unlock()
				inc, ok := incidents[strings.ToUpper(args.ID)]
				if !ok {
					return "", fmt.Errorf("incident %q not found", args.ID)
				}
				return toJSON(inc)
			},
		},
		{
			Name: "acknowledge_incident",
			Description: "Acknowledge an open incident with a note. This changes state: " +
				"Kavrynt does not authorize tool calls yet, so any client that reaches the Gateway can call it.",
			InputSchema: objectSchema([]string{"id", "note"}, map[string]any{
				"id":   map[string]any{"type": "string"},
				"note": map[string]any{"type": "string"},
			}),
			call: func(raw json.RawMessage) (string, error) {
				var args struct {
					ID   string `json:"id"`
					Note string `json:"note"`
				}
				if err := decodeArgs(raw, &args); err != nil {
					return "", err
				}
				mu.Lock()
				defer mu.Unlock()
				inc, ok := incidents[strings.ToUpper(args.ID)]
				if !ok {
					return "", fmt.Errorf("incident %q not found", args.ID)
				}
				if inc.Status != "open" {
					return "", fmt.Errorf("incident %s is %s", inc.ID, inc.Status)
				}
				inc.Status = "acknowledged"
				inc.Acknowledged = args.Note
				return fmt.Sprintf("%s acknowledged", inc.ID), nil
			},
		},
	}}
}

type runbook struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	Tags  []string `json:"tags"`
	Steps []string `json:"steps"`
}

func runbooksProfile() *profile {
	runbooks := []runbook{
		{
			ID: "RB-3", Title: "Pod in CrashLoopBackOff after a Secret rotation",
			Tags: []string{"kubernetes", "secrets", "crashloop"},
			Steps: []string{
				"kubectl describe pod <pod> and read the last termination reason.",
				"kubectl logs <pod> --previous to find the missing or invalid key.",
				"Compare the Secret keys with the Deployment's env and envFrom references.",
				"Restore the key or roll back the Secret, then kubectl rollout restart deployment/<name>.",
			},
		},
		{
			ID: "RB-7", Title: "Latency regression after a deploy",
			Tags: []string{"latency", "deploy", "rollback"},
			Steps: []string{
				"Confirm the regression started with the deploy (rollout history and dashboards).",
				"kubectl rollout undo deployment/<name> if the error budget is at risk.",
				"Open a follow-up with the diff between the two revisions.",
			},
		},
		{
			ID: "RB-12", Title: "Search indexer lag",
			Tags: []string{"search", "scaling"},
			Steps: []string{
				"Check consumer lag on the indexing queue.",
				"Scale the indexer: kubectl scale deployment/indexer --replicas=3.",
				"Scale back once lag is under one minute.",
			},
		},
	}

	return &profile{name: "runbooks", tools: []tool{
		{
			Name:        "search_runbooks",
			Description: "Search runbooks by keyword in their title and tags.",
			InputSchema: objectSchema([]string{"query"}, map[string]any{
				"query": map[string]any{"type": "string"},
			}),
			call: func(raw json.RawMessage) (string, error) {
				var args struct {
					Query string `json:"query"`
				}
				if err := decodeArgs(raw, &args); err != nil {
					return "", err
				}
				query := strings.ToLower(strings.TrimSpace(args.Query))
				type hit struct {
					ID    string `json:"id"`
					Title string `json:"title"`
				}
				hits := []hit{}
				for _, rb := range runbooks {
					text := strings.ToLower(rb.Title + " " + strings.Join(rb.Tags, " "))
					if query == "" || strings.Contains(text, query) {
						hits = append(hits, hit{ID: rb.ID, Title: rb.Title})
					}
				}
				return toJSON(hits)
			},
		},
		{
			Name:        "get_runbook",
			Description: "Get the steps of one runbook by id, e.g. RB-3.",
			InputSchema: objectSchema([]string{"id"}, map[string]any{
				"id": map[string]any{"type": "string"},
			}),
			call: func(raw json.RawMessage) (string, error) {
				var args struct {
					ID string `json:"id"`
				}
				if err := decodeArgs(raw, &args); err != nil {
					return "", err
				}
				for _, rb := range runbooks {
					if strings.EqualFold(rb.ID, args.ID) {
						return toJSON(rb)
					}
				}
				return "", fmt.Errorf("runbook %q not found", args.ID)
			},
		},
	}}
}
