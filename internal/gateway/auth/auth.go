// Package auth is the console admin login (SPEC-0005): one user, admin, whose
// password and session signing key are files in a directory, normally a
// Secret mounted by the kubelet so the Gateway needs no RBAC on Secrets.
//
// It protects the console and the endpoints the console reads. It does not
// authenticate MCP clients: /mcp/ routes stay open (ADR-0003 is not
// implemented).
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// Username is the only console user.
	Username = "admin"
	// CookieName holds the signed session. It is scoped to /v1/, the only
	// paths that read it, so browsers never send it to /mcp/ routes.
	CookieName = "kavrynt_session"
	cookiePath = "/v1/"

	// DefaultSessionTTL is how long a session lasts.
	DefaultSessionTTL = 12 * time.Hour

	// PasswordFile and SessionKeyFile are the file names (Secret keys) in the
	// credentials directory.
	PasswordFile   = "password"
	SessionKeyFile = "sessionKey"

	// Failed logins per client address in failureWindow before further
	// logins are refused with 429.
	maxFailures       = 5
	failureWindow     = time.Minute
	maxTrackedClients = 10000

	maxLoginBody = 4 << 10
)

var errNotConfigured = errors.New("console login is not configured")

// Authenticator checks console sessions and serves /v1/session.
type Authenticator struct {
	dir    string
	ttl    time.Duration
	now    func() time.Time
	logger *slog.Logger
	// mcpAuth is reported by GET /v1/session so the console can say whether
	// MCP routes need tokens (SPEC-0006-R15).
	mcpAuth bool
	// gatewayAudience lists the routes that accept the Gateway audience,
	// reported by GET /v1/session for the console (SPEC-0031-R06).
	gatewayAudience []string

	// trustedProxies are the addresses whose X-Forwarded-For names the
	// client (SPEC-0030-R01).
	trustedProxies []netip.Prefix

	mu       sync.Mutex
	failures map[string]*failures
}

// overflowClient counts the failures of clients beyond a limiter full of
// refused clients, together (SPEC-0030-R03).
const overflowClient = ""

type failures struct {
	count int
	since time.Time
}

// New returns an Authenticator that reads credentials from dir on every use,
// so a rotated Secret takes effect without a restart.
func New(dir string, ttl time.Duration) *Authenticator {
	if ttl <= 0 {
		ttl = DefaultSessionTTL
	}
	return &Authenticator{dir: dir, ttl: ttl, now: time.Now, logger: slog.Default(), failures: map[string]*failures{}}
}

type credentials struct {
	password []byte
	key      []byte
}

// credentials reads the password and signing key. Missing or empty files
// fail closed.
func (a *Authenticator) credentials() (credentials, error) {
	read := func(name string) ([]byte, error) {
		// #nosec G304 -- the directory is an operator-set flag and the names are constants.
		data, err := os.ReadFile(filepath.Join(a.dir, name))
		if err != nil {
			return nil, err
		}
		// Secrets created from files often end with a newline.
		data = []byte(strings.TrimRight(string(data), "\r\n"))
		if len(data) == 0 {
			return nil, fmt.Errorf("%s is empty", name)
		}
		return data, nil
	}
	password, err := read(PasswordFile)
	if err != nil {
		return credentials{}, err
	}
	key, err := read(SessionKeyFile)
	if err != nil {
		return credentials{}, err
	}
	return credentials{password: password, key: key}, nil
}

type session struct {
	Subject string `json:"sub"`
	Expires int64  `json:"exp"`
}

// mac binds a session to the signing key and to the current password, so a
// password change ends every session (SPEC-0005-R06). The random key keeps a
// stolen cookie from being used to guess the password offline.
func (c credentials) mac(payload []byte) []byte {
	digest := sha256.Sum256(c.password)
	m := hmac.New(sha256.New, c.key)
	m.Write(payload)
	m.Write(digest[:])
	return m.Sum(nil)
}

func (c credentials) sign(s session) (string, error) {
	payload, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString(payload) + "." + enc.EncodeToString(c.mac(payload)), nil
}

// verify returns the session in token when it is signed with c and has not
// expired.
func (c credentials) verify(token string, now time.Time) (session, bool) {
	encoded, sig, ok := strings.Cut(token, ".")
	if !ok {
		return session{}, false
	}
	enc := base64.RawURLEncoding
	payload, err := enc.DecodeString(encoded)
	if err != nil {
		return session{}, false
	}
	mac, err := enc.DecodeString(sig)
	if err != nil || !hmac.Equal(mac, c.mac(payload)) {
		return session{}, false
	}
	var s session
	if json.Unmarshal(payload, &s) != nil || s.Subject != Username || now.Unix() >= s.Expires {
		return session{}, false
	}
	return s, true
}

// current returns the request's valid session.
func (a *Authenticator) current(r *http.Request) (session, bool) {
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return session{}, false
	}
	creds, err := a.credentials()
	if err != nil {
		return session{}, false
	}
	return creds.verify(cookie.Value, a.now())
}

// Authorized reports whether r carries a valid session.
func (a *Authenticator) Authorized(r *http.Request) bool {
	_, ok := a.current(r)
	return ok
}

// ReportMCPAuth sets whether GET /v1/session reports MCP client
// authentication as enabled (SPEC-0006-R15).
func (a *Authenticator) ReportMCPAuth(enabled bool) {
	a.mcpAuth = enabled
}

// ReportGatewayAudience sets the routes GET /v1/session reports as
// accepting the Gateway audience (SPEC-0031-R06).
func (a *Authenticator) ReportGatewayAudience(routes []string) {
	a.gatewayAudience = append([]string{}, routes...)
}

// ServeHTTP serves /v1/session: GET reports the session, POST signs in, and
// DELETE signs out.
func (a *Authenticator) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet:
		s, ok := a.current(r)
		if !ok {
			// The Gateway audience routes are configuration for signed-in
			// users only; the login page does not name them.
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "login required", "enabled": true, "mcpAuth": a.mcpAuth})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled":   true,
			"username":  s.Subject,
			"expiresAt": time.Unix(s.Expires, 0).UTC().Format(time.RFC3339),
			"mcpAuth":   a.mcpAuth,
			// Routes reachable with one token for the Gateway audience.
			"gatewayAudience": append([]string{}, a.gatewayAudience...),
		})
	case http.MethodPost:
		a.login(w, r)
	case http.MethodDelete:
		a.setCookie(w, r, "", -1)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (a *Authenticator) login(w http.ResponseWriter, r *http.Request) {
	// JSON only: a cross-site HTML form cannot send it without a CORS
	// preflight, and the Gateway's origin check still applies.
	if mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mediaType != "application/json" {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "send the login as application/json"})
		return
	}
	client := a.clientAddress(r)
	if wait := a.blocked(client); wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int((wait+time.Second-1)/time.Second)))
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many failed logins; try again later"})
		a.logger.Warn("console login refused: too many failed attempts", "client", client)
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxLoginBody)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid login request"})
		return
	}
	creds, err := a.credentials()
	if err != nil {
		a.logger.Error("console login unavailable", "error", errNotConfigured, "cause", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": errNotConfigured.Error()})
		return
	}
	if !equal(body.Username, Username) || !equal(body.Password, string(creds.password)) {
		a.fail(client)
		a.logger.Warn("console login failed", "client", client)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid username or password"})
		return
	}
	a.reset(client)
	expires := a.now().Add(a.ttl)
	token, err := creds.sign(session{Subject: Username, Expires: expires.Unix()})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot create session"})
		return
	}
	a.setCookie(w, r, token, int(a.ttl/time.Second))
	a.logger.Info("console login succeeded", "client", client, "user", Username)
	w.WriteHeader(http.StatusNoContent)
}

// equal compares digests in constant time, so neither the content nor the
// length of the stored value leaks through timing.
func equal(got, want string) bool {
	g, w := sha256.Sum256([]byte(got)), sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(g[:], w[:]) == 1
}

func (a *Authenticator) setCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	// #nosec G124 -- Secure is set whenever the request arrived over HTTPS
	// (SPEC-0005-R05). A Gateway reached over plain HTTP, such as an internal
	// Ingress, would otherwise never keep the session.
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    value,
		Path:     cookiePath,
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
	})
}

// TrustProxies makes logins from these addresses count against the client
// their X-Forwarded-For names (SPEC-0030).
func (a *Authenticator) TrustProxies(prefixes []netip.Prefix) {
	a.trustedProxies = append([]netip.Prefix(nil), prefixes...)
}

// blocked returns how long client must wait before its next login.
func (a *Authenticator) blocked(client string) time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.failures[a.key(client)]
	if f == nil {
		return 0
	}
	left := f.since.Add(failureWindow).Sub(a.now())
	if left <= 0 {
		delete(a.failures, a.key(client))
		return 0
	}
	if f.count >= maxFailures {
		return left
	}
	return 0
}

func (a *Authenticator) fail(client string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	client = a.key(client)
	f := a.failures[client]
	if f == nil || a.now().Sub(f.since) >= failureWindow {
		f = &failures{since: a.now()}
		a.failures[client] = f
	}
	f.count++
}

// key is the entry client's failures count in: its own, or, when the
// limiter is full and cannot make room, the shared overflow entry. To make
// room it forgets the clients whose window has ended, then the oldest one
// that is not refused, never a refused one (SPEC-0030-R03). a.mu is held.
func (a *Authenticator) key(client string) string {
	if _, tracked := a.failures[client]; tracked || a.named() < maxTrackedClients {
		return client
	}
	now := a.now()
	for name, f := range a.failures {
		if now.Sub(f.since) >= failureWindow {
			delete(a.failures, name)
		}
	}
	if a.named() < maxTrackedClients {
		return client
	}
	oldest := ""
	for name, f := range a.failures {
		if name != overflowClient && f.count < maxFailures && (oldest == "" || f.since.Before(a.failures[oldest].since)) {
			oldest = name
		}
	}
	if oldest == "" {
		return overflowClient
	}
	delete(a.failures, oldest)
	return client
}

func (a *Authenticator) reset(client string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.failures, client)
}

// named is the number of clients tracked by name, without the overflow
// entry. a.mu is held.
func (a *Authenticator) named() int {
	if _, ok := a.failures[overflowClient]; ok {
		return len(a.failures) - 1
	}
	return len(a.failures)
}

// clientAddress is the address login failures count against: the
// connection's remote host or, when that is a trusted proxy, the right-most
// X-Forwarded-For address that is not a trusted proxy (SPEC-0030-R02). A
// value that is not an address stops the walk and keeps the connection's,
// so a client cannot choose where its failures count. Without trusted
// proxies, behind an Ingress every login counts against the Ingress.
func (a *Authenticator) clientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	remote, err := netip.ParseAddr(host)
	if err != nil || !a.trusted(remote) {
		return host
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			return host
		}
		if !a.trusted(hop) {
			return hop.Unmap().String()
		}
	}
	return host
}

func (a *Authenticator) trusted(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, prefix := range a.trustedProxies {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
