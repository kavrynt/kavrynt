package clientauth

// Token exchange (RFC 8693, SPEC-0015): for an MCP server that needs a token
// on behalf of the caller, the Gateway exchanges the caller's verified access
// token at the caller's authorization server for one whose audience is that
// server. Subject tokens, exchanged tokens, and client secrets are never
// logged, returned, or part of an error.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	// exchangeCacheSize bounds the cached exchanged tokens (SPEC-0015-R04).
	exchangeCacheSize = 10000
	// exchangeEarlyExpiry is how long before expires_in a cached token is
	// exchanged again.
	exchangeEarlyExpiry = 30 * time.Second
	// maxSecretBytes bounds a client secret file.
	maxSecretBytes = 16 << 10

	grantTokenExchange = "urn:ietf:params:oauth:grant-type:token-exchange" // #nosec G101 -- an RFC 8693 grant type, not a credential.
	tokenTypeAccess    = "urn:ietf:params:oauth:token-type:access_token"   // #nosec G101 -- an RFC 8693 token type, not a credential.
)

var (
	// ErrExchangeUnreachable: the token endpoint could not be reached; the
	// Gateway answers 503 (SPEC-0015-R06).
	ErrExchangeUnreachable = errors.New("token endpoint unreachable")
	// ErrExchangeFailed: any other failure; the Gateway answers 502.
	ErrExchangeFailed = errors.New("token exchange failed")
)

// tokenValue is what an exchanged token may contain to be sent in a header;
// maxTokenBytes bounds its length.
var tokenValue = regexp.MustCompile(`^[\x21-\x7e]+$`)

const maxTokenBytes = 16 << 10

// Exchange returns a token for audience and scopes in exchange for the
// request's bearer token, which identity verified (SPEC-0015-R03). Tokens are
// cached until shortly before they expire (R04).
func (a *Authenticator) Exchange(r *http.Request, identity Identity, audience string, scopes []string) (string, error) {
	subject, ok := bearerToken(r.Header.Values("Authorization"))
	if !ok {
		return "", fmt.Errorf("%w: the request has no bearer token", ErrExchangeFailed)
	}
	keys := a.issuers[identity.Issuer]
	if keys == nil || keys.issuer.TokenExchange == nil {
		return "", a.exchangeError(identity.Issuer, fmt.Errorf("%w: issuer %s has no tokenExchange settings", ErrExchangeFailed, identity.Issuer))
	}
	scopes = slices.Sorted(slices.Values(scopes))
	digest := sha256.Sum256([]byte(subject))
	key := hex.EncodeToString(digest[:]) + "\n" + audience + "\n" + strings.Join(scopes, " ")
	if token, ok := a.exchanged.get(key, a.now()); ok {
		return token, nil
	}
	token, lifetime, err := a.exchange(r.Context(), keys, subject, audience, scopes)
	if err != nil {
		return "", a.exchangeError(identity.Issuer, err)
	}
	if ttl := lifetime - exchangeEarlyExpiry; ttl >= time.Second {
		a.exchanged.put(key, token, a.now().Add(ttl), a.now())
	}
	return token, nil
}

// exchangeError logs a failure at most once a minute per issuer, without
// tokens or secrets, and returns it.
func (a *Authenticator) exchangeError(issuer string, err error) error {
	if a.warned.due(issuer, a.now()) {
		a.logger.Warn("token exchange failed", "issuer", issuer, "error", err.Error())
	}
	return err
}

func (a *Authenticator) exchange(ctx context.Context, keys *keySet, subject, audience string, scopes []string) (string, time.Duration, error) {
	client := keys.issuer.TokenExchange
	issuer := keys.issuer.Issuer
	endpoint, err := keys.endpoint(ctx)
	if err != nil {
		return "", 0, fmt.Errorf("%w: issuer %s: %v", ErrExchangeUnreachable, issuer, err)
	}
	if err := checkURL(endpoint, keys.issuer.AllowHTTP); err != nil {
		return "", 0, fmt.Errorf("%w: issuer %s: token endpoint %v", ErrExchangeFailed, issuer, err)
	}
	secret, err := a.clientSecret(client.ClientSecretKey)
	if err != nil {
		return "", 0, fmt.Errorf("%w: issuer %s: %v", ErrExchangeFailed, issuer, err)
	}

	form := url.Values{
		"grant_type":           {grantTokenExchange},
		"subject_token":        {subject},
		"subject_token_type":   {tokenTypeAccess},
		"requested_token_type": {tokenTypeAccess},
		"audience":             {audience},
	}
	if len(scopes) > 0 {
		form.Set("scope", strings.Join(scopes, " "))
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, fmt.Errorf("%w: issuer %s: build the request", ErrExchangeFailed, issuer)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	// client_secret_basic: both parts form-encoded first (RFC 6749 2.3.1).
	req.SetBasicAuth(url.QueryEscape(client.ClientID), url.QueryEscape(secret))
	// #nosec G704 -- the token endpoint comes from Gateway configuration or the configured issuer's metadata and passed checkURL; the transport applies the egress policy.
	resp, err := keys.client.Do(req)
	if err != nil {
		// The transport error names the endpoint, never the form.
		return "", 0, fmt.Errorf("%w: issuer %s: %v", ErrExchangeUnreachable, issuer, redactURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// The error body is not read: it may echo the request.
		return "", 0, fmt.Errorf("%w: issuer %s answered HTTP %d", ErrExchangeFailed, issuer, resp.StatusCode)
	}
	if mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mediaType != "application/json" {
		return "", 0, fmt.Errorf("%w: issuer %s answered %q, not JSON", ErrExchangeFailed, issuer, mediaType)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDocumentBytes+1))
	if err != nil || len(body) > maxDocumentBytes {
		return "", 0, fmt.Errorf("%w: issuer %s: the answer is unreadable or larger than %d bytes", ErrExchangeFailed, issuer, maxDocumentBytes)
	}
	var answer struct {
		AccessToken string   `json:"access_token"`
		TokenType   string   `json:"token_type"`
		ExpiresIn   *float64 `json:"expires_in"`
	}
	if json.Unmarshal(body, &answer) != nil || len(answer.AccessToken) > maxTokenBytes || !tokenValue.MatchString(answer.AccessToken) || !strings.EqualFold(answer.TokenType, "Bearer") {
		return "", 0, fmt.Errorf("%w: issuer %s: the answer has no bearer access_token", ErrExchangeFailed, issuer)
	}
	var lifetime time.Duration
	if answer.ExpiresIn != nil && *answer.ExpiresIn > 0 && *answer.ExpiresIn < 86400*365 {
		lifetime = time.Duration(*answer.ExpiresIn * float64(time.Second))
	}
	return answer.AccessToken, lifetime, nil
}

// clientSecret reads a client secret from the mounted Secret on each
// exchange, so a rotated Secret applies as soon as the kubelet updates it.
func (a *Authenticator) clientSecret(key string) (string, error) {
	// #nosec G304 -- key matched secretKeyName (no path separators) at start; the file is in the mounted Secret directory.
	file, err := os.Open(filepath.Join(a.secretDir, key))
	if err != nil {
		return "", fmt.Errorf("key %s is not in the token exchange Secret", key)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSecretBytes+1))
	if err != nil || len(data) > maxSecretBytes {
		return "", fmt.Errorf("key %s of the token exchange Secret cannot be read", key)
	}
	secret := strings.TrimRight(string(data), "\r\n")
	if secret == "" {
		return "", fmt.Errorf("key %s of the token exchange Secret is empty", key)
	}
	return secret, nil
}

// redactURLError keeps a transport error's cause but drops its URL, which
// names only the configured endpoint but keeps logs short.
func redactURLError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}

// exchangeCache holds exchanged tokens in memory, keyed by a hash of the
// subject token, the audience, and the scopes.
type exchangeCache struct {
	mu      sync.Mutex
	max     int
	entries map[string]cachedToken
}

type cachedToken struct {
	token   string
	expires time.Time
	added   time.Time
}

func newExchangeCache(max int) *exchangeCache {
	return &exchangeCache{max: max, entries: map[string]cachedToken{}}
}

func (c *exchangeCache) get(key string, now time.Time) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || !now.Before(entry.expires) {
		delete(c.entries, key)
		return "", false
	}
	return entry.token, true
}

// put stores a token; when full, expired entries go first, then the oldest.
func (c *exchangeCache) put(key, token string, expires, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; !exists && len(c.entries) >= c.max {
		for k, entry := range c.entries {
			if !now.Before(entry.expires) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= c.max {
			oldest := ""
			for k, entry := range c.entries {
				if oldest == "" || entry.added.Before(c.entries[oldest].added) {
					oldest = k
				}
			}
			delete(c.entries, oldest)
		}
	}
	c.entries[key] = cachedToken{token: token, expires: expires, added: now}
}

func (c *exchangeCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// warnOnce rate-limits a warning per key to once a minute.
type warnOnce struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (w *warnOnce) due(key string, now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.last == nil {
		w.last = map[string]time.Time{}
	}
	if last, ok := w.last[key]; ok && now.Sub(last) < time.Minute {
		return false
	}
	w.last[key] = now
	return true
}
