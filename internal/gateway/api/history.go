package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/kavrynt/kavrynt/internal/gateway/alerts"
	"github.com/kavrynt/kavrynt/internal/gateway/history"
	"github.com/kavrynt/kavrynt/internal/mcpmeta"
)

// historyScope is what /v1/history covers: all routes, a route, or a tool.
type historyScope struct {
	Route string `json:"route,omitempty"`
	Tool  string `json:"tool,omitempty"`
}

// historyView is the answer of /v1/history (SPEC-0024-R04): route and tool
// names, counts, latencies, sizes, and console alerts only.
type historyView struct {
	// Instance is the replica whose calls are counted.
	Instance string       `json:"instance"`
	Scope    historyScope `json:"scope"`
	history.View
	Alerts []alerts.Alert `json:"alerts"`
	// GrafanaURL is gateway.console.grafanaURL, for cluster-wide history
	// (SPEC-0024-R10).
	GrafanaURL string `json:"grafanaURL,omitempty"`
}

// EnableHistory replaces the history with one that keeps at most maxTools
// named tools, and returns it so its series of deleted routes can be
// dropped (SPEC-0024-R02).
func (h *Handler) EnableHistory(maxTools int) *history.History {
	h.history = history.New(durationBuckets, maxTools, time.Now())
	return h.history
}

// SetGrafanaURL is the Grafana the console links for cluster-wide history.
func (h *Handler) SetGrafanaURL(url string) {
	h.grafanaURL = url
}

func (h *Handler) listHistory(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	name := query.Get("range")
	if name == "" {
		name = "1h"
	}
	route, tool := query.Get("route"), query.Get("tool")
	switch _, known := history.Ranges[name]; {
	case !known:
		writeError(w, http.StatusBadRequest, errors.New("range must be 1h, 6h, or 24h"))
		return
	case tool != "" && route == "":
		writeError(w, http.StatusBadRequest, errors.New("tool needs a route"))
		return
	case tool != "" && !mcpmeta.ValidName(tool, maxToolLength):
		writeError(w, http.StatusBadRequest, errors.New("tool is not a valid tool name"))
		return
	}
	if route != "" {
		if _, routed := h.table.Get(route); !routed {
			writeError(w, http.StatusBadRequest, errors.New("route is not routed"))
			return
		}
	}

	view := historyView{
		Instance:   h.instance,
		Scope:      historyScope{Route: route, Tool: tool},
		View:       h.history.Query(time.Now(), name, route, tool),
		Alerts:     []alerts.Alert{},
		GrafanaURL: h.grafanaURL,
	}
	if h.alerts != nil {
		view.Alerts = h.alerts.Within(view.Start, view.End, route, tool)
	}
	writeJSON(w, http.StatusOK, view)
}
