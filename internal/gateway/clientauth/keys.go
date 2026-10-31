package clientauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
)

const (
	// fetchTimeout and maxDocumentBytes bound every metadata and key request
	// (SPEC-0006-R11).
	fetchTimeout     = 5 * time.Second
	maxDocumentBytes = 1 << 20
	// keyRefresh is how often a key set is fetched again.
	keyRefresh = 15 * time.Minute
	// minRefetch spaces fetches caused by unknown key IDs or failures, so a
	// caller cannot make the Gateway hammer an authorization server.
	minRefetch = time.Minute
)

var errNoKeys = errors.New("no signing keys loaded")

// keySet caches one issuer's signing keys (SPEC-0006-R10, R12).
type keySet struct {
	issuer Issuer
	client *http.Client
	now    func() time.Time

	mu      sync.Mutex
	jwksURI string
	// tokenEndpoint is the configured or discovered token endpoint, for
	// token exchange (SPEC-0015) and console sign-in (SPEC-0032).
	tokenEndpoint string
	// authorizationEndpoint is the discovered authorization endpoint, for
	// console sign-in (SPEC-0032).
	authorizationEndpoint string
	// discoveredAt is the last metadata fetch, successful or not.
	discoveredAt time.Time
	keySet       jose.JSONWebKeySet
	fetchedAt    time.Time // last successful fetch
	triedAt      time.Time // last attempt, successful or not
	lastErr      error
}

func newKeySet(issuer Issuer, client *http.Client, now func() time.Time) *keySet {
	s := &keySet{issuer: issuer, client: client, now: now, jwksURI: issuer.JWKSURI}
	if issuer.TokenExchange != nil {
		s.tokenEndpoint = issuer.TokenExchange.TokenEndpoint
	}
	return s
}

// keys returns the keys that may have signed a token with key ID kid. It
// loads the set when it has none or it is older than keyRefresh, and again
// when kid is unknown, at most once per minRefetch. Keys already loaded keep
// working while a refresh fails.
func (s *keySet) keys(ctx context.Context, kid string) ([]jose.JSONWebKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	stale := s.fetchedAt.IsZero() || now.Sub(s.fetchedAt) >= keyRefresh
	if stale && now.Sub(s.triedAt) >= minRefetch {
		s.fetch(ctx)
	}
	if s.fetchedAt.IsZero() {
		if s.lastErr != nil {
			return nil, s.lastErr
		}
		return nil, errNoKeys
	}

	found := s.match(kid)
	if len(found) == 0 && kid != "" && now.Sub(s.triedAt) >= minRefetch {
		// A new key may have been published since the last fetch.
		s.fetch(ctx)
		found = s.match(kid)
	}
	return found, nil
}

func (s *keySet) match(kid string) []jose.JSONWebKey {
	if kid != "" {
		return s.keySet.Key(kid)
	}
	return s.keySet.Keys
}

// fetch loads the key set; on failure it keeps the keys it has.
func (s *keySet) fetch(ctx context.Context) {
	s.triedAt = s.now()
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	if s.jwksURI == "" {
		if err := s.discover(ctx); err != nil {
			s.lastErr = err
			return
		}
	}
	var set jose.JSONWebKeySet
	if err := s.getJSON(ctx, s.jwksURI, &set); err != nil {
		s.lastErr = fmt.Errorf("fetch keys: %w", err)
		return
	}
	verifying := jose.JSONWebKeySet{}
	for _, key := range set.Keys {
		if key.Use != "" && key.Use != "sig" {
			continue
		}
		// Only public keys verify; a published private key is reduced to its
		// public half.
		key = key.Public()
		if key.Key == nil {
			continue
		}
		verifying.Keys = append(verifying.Keys, key)
	}
	if len(verifying.Keys) == 0 {
		s.lastErr = errors.New("the key set has no public signing keys")
		return
	}
	s.keySet = verifying
	s.fetchedAt = s.now()
	s.lastErr = nil
}

// discover reads the authorization server metadata (RFC 8414, then OpenID
// Connect discovery), checks it names the configured issuer, and fills in
// jwks_uri and token_endpoint where they are not configured. The caller
// holds s.mu.
func (s *keySet) discover(ctx context.Context) error {
	s.discoveredAt = s.now()
	var errs []error
	for _, metadataURL := range metadataURLs(s.issuer.Issuer) {
		var metadata struct {
			Issuer                string `json:"issuer"`
			JWKSURI               string `json:"jwks_uri"`
			TokenEndpoint         string `json:"token_endpoint"`
			AuthorizationEndpoint string `json:"authorization_endpoint"`
		}
		if err := s.getJSON(ctx, metadataURL, &metadata); err != nil {
			errs = append(errs, err)
			continue
		}
		if metadata.Issuer != s.issuer.Issuer {
			return fmt.Errorf("authorization server metadata names issuer %q, not %q", metadata.Issuer, s.issuer.Issuer)
		}
		if s.jwksURI == "" {
			if err := checkURL(metadata.JWKSURI, s.issuer.AllowHTTP); err != nil {
				return fmt.Errorf("authorization server metadata: jwks_uri %w", err)
			}
			s.jwksURI = metadata.JWKSURI
		}
		if s.tokenEndpoint == "" && metadata.TokenEndpoint != "" {
			if err := checkURL(metadata.TokenEndpoint, s.issuer.AllowHTTP); err != nil {
				return fmt.Errorf("authorization server metadata: token_endpoint %w", err)
			}
			s.tokenEndpoint = metadata.TokenEndpoint
		}
		// The browser reaches it, so it is checked by its user (SPEC-0032-R03).
		s.authorizationEndpoint = metadata.AuthorizationEndpoint
		return nil
	}
	return fmt.Errorf("discover authorization server metadata: %w", errors.Join(errs...))
}

// endpoint returns the issuer's token endpoint, from its settings or its
// metadata, fetching the metadata at most once per minRefetch.
func (s *keySet) endpoint(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tokenEndpoint == "" && (s.discoveredAt.IsZero() || s.now().Sub(s.discoveredAt) >= minRefetch) {
		ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
		defer cancel()
		if err := s.discover(ctx); err != nil {
			return "", err
		}
	}
	if s.tokenEndpoint == "" {
		return "", errors.New("the authorization server metadata names no token_endpoint")
	}
	return s.tokenEndpoint, nil
}

// metadataURLs are the RFC 8414 and OpenID Connect discovery URLs of an
// issuer. RFC 8414 inserts the well-known segment before the issuer's path.
func metadataURLs(issuer string) []string {
	u, err := url.Parse(issuer)
	if err != nil {
		return nil
	}
	path := strings.TrimRight(u.Path, "/")
	oauth := *u
	oauth.Path = "/.well-known/oauth-authorization-server" + path
	oidc := *u
	oidc.Path = path + "/.well-known/openid-configuration"
	return []string{oauth.String(), oidc.String()}
}

func (s *keySet) getJSON(ctx context.Context, rawURL string, into any) error {
	if err := checkURL(rawURL, s.issuer.AllowHTTP); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	// #nosec G704 -- the URL comes from Gateway configuration or the configured issuer's metadata and passed checkURL; the transport applies the egress policy.
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered HTTP %d", rawURL, resp.StatusCode)
	}
	if mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mediaType != "" &&
		mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json") {
		return fmt.Errorf("%s answered %s, not JSON", rawURL, mediaType)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDocumentBytes+1))
	if err != nil {
		return err
	}
	if len(body) > maxDocumentBytes {
		return fmt.Errorf("%s answered more than %d bytes", rawURL, maxDocumentBytes)
	}
	return json.Unmarshal(body, into)
}

// jsonStringOrSet decodes a claim that is a string or an array of strings,
// such as scp.
type jsonStringOrSet []string

func (s *jsonStringOrSet) UnmarshalJSON(data []byte) error {
	var one string
	if json.Unmarshal(data, &one) == nil {
		*s = []string{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return errors.New("must be a string or an array of strings")
	}
	*s = many
	return nil
}
