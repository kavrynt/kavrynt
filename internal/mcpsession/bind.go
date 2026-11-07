package mcpsession

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strings"
)

// Bound session IDs tie an MCP session to the caller that started it
// (SPEC-0042). The ID the client holds is
//
//	s1.<32 lowercase hex>.<ID>
//
// where the hex is the first 16 bytes of an HMAC-SHA-256, keyed with a secret
// only the Gateway has, over the caller's issuer, subject, and client ID, the
// route, and the ID that follows. The ID is whatever the server (or session
// affinity, SPEC-0026) made, so it may contain dots and stays in the visible
// ASCII that MCP requires of session IDs.

const (
	// boundVersion is the format version at the start of a bound ID.
	boundVersion = "s1"
	// macLength is the length of the tag in hex characters.
	macLength = 32
	// MinKeyLength is the shortest key the Gateway accepts, in bytes.
	MinKeyLength = 32
)

// Caller is who started a session: the verified token's issuer, subject, and
// client ID. A client that gets a new client ID starts new sessions.
type Caller struct {
	Issuer, Subject, ClientID string
}

// mac returns the tag of id for the caller on route. Every input is
// length-prefixed, so no two different inputs encode the same way.
func mac(key []byte, caller Caller, route, id string) string {
	h := hmac.New(sha256.New, key)
	for _, field := range []string{"kavrynt.mcp-session.s1", caller.Issuer, caller.Subject, caller.ClientID, route, id} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(field)))
		h.Write(length[:])
		h.Write([]byte(field))
	}
	return hex.EncodeToString(h.Sum(nil)[:macLength/2])
}

// Bind returns the session ID the client gets for id (SPEC-0042-R03).
func Bind(key []byte, caller Caller, route, id string) string {
	return boundVersion + "." + mac(key, caller, route, id) + "." + id
}

// Open returns the ID inside a bound session ID from a client, and reports
// whether the tag matches the caller and route. A missing tag, a version it
// does not know, and a tag that does not match all report false the same way
// (SPEC-0042-R04, R10).
func Open(key []byte, caller Caller, route, bound string) (string, bool) {
	rest, ok := strings.CutPrefix(bound, boundVersion+".")
	if !ok || len(rest) < macLength+2 || rest[macLength] != '.' {
		return "", false
	}
	tag, id := rest[:macLength], rest[macLength+1:]
	// The tag is lower-case hex; anything else would never match and is not
	// worth comparing.
	if strings.Trim(tag, "0123456789abcdef") != "" {
		return "", false
	}
	if !hmac.Equal([]byte(tag), []byte(mac(key, caller, route, id))) {
		return "", false
	}
	return id, true
}
