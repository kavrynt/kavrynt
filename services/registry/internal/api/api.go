package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kavrynt/registry/internal/model"
	"github.com/kavrynt/registry/internal/store"
)

type Metadata struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"buildDate"`
}

type Handler struct {
	store    store.Store
	metadata Metadata
	requests atomic.Uint64
}

type errorResponse struct {
	Error string `json:"error"`
}

func NewHandler(store store.Store, metadata Metadata) http.Handler {
	h := &Handler{store: store, metadata: metadata}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("GET /readyz", h.ready)
	mux.HandleFunc("GET /version", h.version)
	mux.HandleFunc("GET /metrics", h.metrics)
	mux.HandleFunc("GET /v1/servers", h.listServers)
	mux.HandleFunc("POST /v1/servers", h.upsertServer)
	mux.HandleFunc("GET /v1/servers/{id}", h.getServer)
	mux.HandleFunc("DELETE /v1/servers/{id}", h.deleteServer)
	return h.instrument(mux)
}

func (h *Handler) instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.requests.Add(1)
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) ready(w http.ResponseWriter, _ *http.Request) {
	if _, err := h.store.Count(); err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (h *Handler) version(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.metadata)
}

func (h *Handler) metrics(w http.ResponseWriter, _ *http.Request) {
	count, err := h.store.Count()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = fmt.Fprintf(w, "kavrynt_registry_requests_total %d\n", h.requests.Load())
	_, _ = fmt.Fprintf(w, "kavrynt_registry_servers %d\n", count)
}

func (h *Handler) listServers(w http.ResponseWriter, _ *http.Request) {
	servers, err := h.store.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]model.ServerRecord{"servers": servers})
}

func (h *Handler) upsertServer(w http.ResponseWriter, r *http.Request) {
	var manifest model.Manifest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}

	record, created, err := h.store.Upsert(manifest, time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, record)
}

func (h *Handler) getServer(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("server id is required"))
		return
	}
	record, err := h.store.Get(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func (h *Handler) deleteServer(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("server id is required"))
		return
	}
	if err := h.store.Delete(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, errorResponse{Error: err.Error()})
}
