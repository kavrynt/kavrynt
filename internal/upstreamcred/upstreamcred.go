// Package upstreamcred reads the credentials MCP servers need from the
// upstream credentials Secret, mounted by the kubelet into the Gateway and
// the Operator (SPEC-0011). Neither component has RBAC on Secrets.
//
// The Secret holds, per MCPServer, the key <namespace>.<name> with the value
// and the key <namespace>.<name>.origin with the endpoint origin the value may
// be sent to. A credential is used only when the pin matches the server's
// current endpoint, so editing an MCPServer cannot redirect it elsewhere.
// Values are never logged and never part of an error.
package upstreamcred

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// maxValueBytes bounds a credential or pin read from the Secret.
const maxValueBytes = 16 << 10

// ErrUnavailable means the credential is missing, empty, or not pinned to the
// endpoint. Errors wrapping it name the Secret key, never the value.
var ErrUnavailable = errors.New("upstream credential unavailable")

// Store reads credentials from Dir, the mounted Secret. An empty Dir has no
// credentials.
type Store struct {
	Dir string
}

// Lookup returns the credential of the MCPServer with route name
// <namespace>.<name> for endpoint. It reads the files on every call, so a
// rotated Secret applies as soon as the kubelet updates the mount
// (SPEC-0011-R03, SPEC-0011-R04).
func (s Store) Lookup(route, endpoint string) (string, error) {
	if s.Dir == "" {
		return "", fmt.Errorf("%w: no upstream credentials Secret is mounted", ErrUnavailable)
	}
	if route == "" || strings.ContainsAny(route, `/\`) || strings.HasPrefix(route, ".") {
		return "", fmt.Errorf("%w: invalid route name", ErrUnavailable)
	}
	value, err := s.read(route)
	if err != nil {
		return "", err
	}
	if strings.ContainsAny(value, "\r\n") {
		return "", fmt.Errorf("%w: key %s holds more than one line", ErrUnavailable, route)
	}
	pin, err := s.read(route + ".origin")
	if err != nil {
		return "", err
	}
	pinned, err := Origin(pin)
	if err != nil {
		return "", fmt.Errorf("%w: key %s.origin is not an http or https origin", ErrUnavailable, route)
	}
	target, err := Origin(endpoint)
	if err != nil || target != pinned {
		return "", fmt.Errorf("%w: the endpoint's origin does not match key %s.origin", ErrUnavailable, route)
	}
	return value, nil
}

// Available reports whether the store could serve route's credential for
// endpoint, without returning it (for /v1/servers, SPEC-0011-R07).
func (s Store) Available(route, endpoint string) bool {
	_, err := s.Lookup(route, endpoint)
	return err == nil
}

// read returns the trimmed content of one Secret key.
func (s Store) read(key string) (string, error) {
	// #nosec G304 -- key is a route name without path separators, inside the mounted Secret directory.
	file, err := os.Open(filepath.Join(s.Dir, key))
	if err != nil {
		return "", fmt.Errorf("%w: key %s is not in the upstream credentials Secret", ErrUnavailable, key)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxValueBytes+1))
	if err != nil {
		return "", fmt.Errorf("%w: key %s cannot be read", ErrUnavailable, key)
	}
	if len(data) > maxValueBytes {
		return "", fmt.Errorf("%w: key %s is larger than %d bytes", ErrUnavailable, key, maxValueBytes)
	}
	value := strings.TrimRight(string(data), "\r\n")
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%w: key %s is empty", ErrUnavailable, key)
	}
	return value, nil
}

// Origin returns scheme://host:port of an http or https URL, lower-cased,
// with the default port made explicit.
func Origin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return "", errors.New("not an http or https URL")
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	return u.Scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port), nil
}
