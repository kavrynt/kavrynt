// Package v1 is the audit event format of the Kavrynt Gateway (SPEC-0013):
// one JSON object per line on the Gateway's standard output, chained per
// Gateway instance with SHA-256 so that a missing, reordered, or altered
// event is detectable. kavryctl verifies chains with this package.
package v1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// Type identifies version 1 events.
const Type = "kavrynt.audit.v1"

// GenesisHash is the prev_hash of an instance's first event.
var GenesisHash = strings.Repeat("0", 64)

// Decisions.
const (
	DecisionAllow           = "allow"
	DecisionDeny            = "deny"
	DecisionUnauthenticated = "unauthenticated"
	DecisionForbidden       = "forbidden"
	DecisionInvalid         = "invalid"
	DecisionUnavailable     = "unavailable"
)

// Event is one audit event. Every field is always present; fields that do
// not apply are empty (SPEC-0013-R03). It never holds tool arguments,
// results, bodies, tokens, credentials, cookies, or headers (R04).
//
// The canonical form is this struct encoded by encoding/json without Hash:
// fields in declaration order, no maps, no insignificant whitespace.
type Event struct {
	Type           string  `json:"type"`
	Time           string  `json:"time"`
	Instance       string  `json:"instance"`
	Seq            uint64  `json:"seq"`
	RequestID      string  `json:"request_id"`
	Subject        string  `json:"subject"`
	ClientID       string  `json:"client_id"`
	Issuer         string  `json:"issuer"`
	Route          Route   `json:"route"`
	HTTPMethod     string  `json:"http_method"`
	MCPMethod      string  `json:"mcp_method"`
	Tool           string  `json:"tool"`
	Decision       string  `json:"decision"`
	Reason         string  `json:"reason"`
	PolicyRevision string  `json:"policy_revision"`
	Outcome        string  `json:"outcome"`
	Status         int     `json:"status"`
	DurationMS     float64 `json:"duration_ms"`
	BytesIn        int64   `json:"bytes_in"`
	BytesOut       int64   `json:"bytes_out"`
	PrevHash       string  `json:"prev_hash"`
	Hash           string  `json:"hash,omitempty"`
}

// Route names the MCPServer of a request.
type Route struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// Canonical returns the bytes the hash covers: the event without Hash.
func Canonical(e Event) []byte {
	e.Hash = ""
	data, err := json.Marshal(e)
	if err != nil {
		// Unreachable: the struct has only strings and numbers.
		panic(err)
	}
	return data
}

// ComputeHash returns the hex SHA-256 of e.PrevHash followed by the
// canonical form of e (SPEC-0013-R06).
func ComputeHash(e Event) string {
	sum := sha256.New()
	sum.Write([]byte(e.PrevHash))
	sum.Write(Canonical(e))
	return hex.EncodeToString(sum.Sum(nil))
}
