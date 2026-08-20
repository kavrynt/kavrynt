package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"
)

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

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /", handleMCP)

	server := &http.Server{
		Addr:              ":8080",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	logger.Info("starting Alpha-0 MCP server", "addr", server.Addr)
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func handleMCP(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	var input request
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(&input); err != nil {
		writeResponse(w, http.StatusBadRequest, response{
			JSONRPC: "2.0",
			ID:      json.RawMessage("null"),
			Error:   &responseError{Code: -32700, Message: "invalid JSON-RPC request"},
		})
		return
	}

	if input.JSONRPC != "2.0" || len(input.ID) == 0 {
		writeResponse(w, http.StatusBadRequest, response{
			JSONRPC: "2.0",
			ID:      json.RawMessage("null"),
			Error:   &responseError{Code: -32600, Message: "jsonrpc 2.0 and id are required"},
		})
		return
	}

	result, ok := resultFor(input.Method)
	if !ok {
		writeResponse(w, http.StatusOK, response{
			JSONRPC: "2.0",
			ID:      input.ID,
			Error:   &responseError{Code: -32601, Message: "method not found"},
		})
		return
	}

	writeResponse(w, http.StatusOK, response{
		JSONRPC: "2.0",
		ID:      input.ID,
		Result:  result,
	})
}

func resultFor(method string) (any, bool) {
	switch method {
	case "initialize":
		return map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities": map[string]any{
				"tools": map[string]bool{"listChanged": false},
			},
			"serverInfo": map[string]string{
				"name":    "kavrynt-alpha0",
				"version": "0.0.1-beta",
			},
		}, true
	case "tools/list":
		return map[string]any{
			"tools": []map[string]any{
				{
					"name":        "echo",
					"description": "Echo a message for Kavrynt end-to-end validation.",
					"inputSchema": map[string]any{
						"type":     "object",
						"required": []string{"message"},
						"properties": map[string]any{
							"message": map[string]string{"type": "string"},
						},
					},
				},
			},
		}, true
	default:
		return nil, false
	}
}

func writeResponse(w http.ResponseWriter, status int, value response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
