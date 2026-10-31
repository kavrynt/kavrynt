package clientauth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4/jwt"
)

// ConsoleProviderConfig configures console sign-in with OpenID Connect
// (SPEC-0032).
type ConsoleProviderConfig struct {
	Issuer string
	// AllowHTTP permits http:// for the Gateway's own requests to an
	// in-cluster issuer (metadata, keys, token endpoint), never for the
	// authorization endpoint the browser reaches (SPEC-0032-R03).
	AllowHTTP bool
	ClientID  string
	// SecretFile is the client secret in the kubelet-mounted Secret,
	// read on every exchange (SPEC-0032-R02).
	SecretFile string
}

// ConsoleProvider signs people in to the console with the authorization
// code flow: it names the authorization endpoint, exchanges a code for an
// ID token, and verifies the ID token. It never keeps or logs a code or
// token (SPEC-0032-R09).
type ConsoleProvider struct {
	keys       *keySet
	issuer     string
	clientID   string
	secretFile string
	now        func() time.Time
}

// ConsoleClaims are the verified ID token's subject and its other claims,
// for the allow lists and the display name.
type ConsoleClaims struct {
	Subject string
	Claims  map[string]any
}

// NewConsoleProvider returns a ConsoleProvider. Its requests go through
// transport, which must apply the Gateway's egress policy (SPEC-0032-R05).
func NewConsoleProvider(cfg ConsoleProviderConfig, transport http.RoundTripper) (*ConsoleProvider, error) {
	if err := checkURL(cfg.Issuer, cfg.AllowHTTP); err != nil {
		return nil, fmt.Errorf("console sign-in issuer %w", err)
	}
	if cfg.ClientID == "" || cfg.SecretFile == "" {
		return nil, errors.New("console sign-in needs a client ID and the client secret file")
	}
	client := &http.Client{
		Transport:     transport,
		Timeout:       fetchTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	p := &ConsoleProvider{issuer: cfg.Issuer, clientID: cfg.ClientID, secretFile: cfg.SecretFile, now: time.Now}
	p.keys = newKeySet(Issuer{Issuer: cfg.Issuer, AllowHTTP: cfg.AllowHTTP}, client, func() time.Time { return p.now() })
	return p, nil
}

// AuthorizationEndpoint returns the issuer's authorization endpoint from its
// metadata. It must be https even with AllowHTTP: the browser reaches it
// (SPEC-0032-R03).
func (p *ConsoleProvider) AuthorizationEndpoint(ctx context.Context) (string, error) {
	s := p.keys
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.authorizationEndpoint == "" && (s.discoveredAt.IsZero() || s.now().Sub(s.discoveredAt) >= minRefetch) {
		ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
		defer cancel()
		if err := s.discover(ctx); err != nil {
			return "", err
		}
	}
	if s.authorizationEndpoint == "" {
		return "", errors.New("the issuer's metadata names no authorization_endpoint")
	}
	if err := checkURL(s.authorizationEndpoint, false); err != nil {
		return "", fmt.Errorf("the authorization endpoint %w", err)
	}
	return s.authorizationEndpoint, nil
}

// ExchangeCode trades an authorization code for the ID token at the token
// endpoint, with client_secret_basic and the PKCE verifier
// (SPEC-0032-R05). Every other token in the answer is discarded.
func (p *ConsoleProvider) ExchangeCode(ctx context.Context, code, verifier, redirectURI string) (string, error) {
	endpoint, err := p.keys.endpoint(ctx)
	if err != nil {
		return "", err
	}
	if err := checkURL(endpoint, p.keys.issuer.AllowHTTP); err != nil {
		return "", fmt.Errorf("token endpoint %w", err)
	}
	secret, err := p.clientSecret()
	if err != nil {
		return "", err
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", errors.New("build the token request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(url.QueryEscape(p.clientID), url.QueryEscape(secret))
	// #nosec G704 -- the token endpoint comes from the configured issuer's metadata and passed checkURL; the transport applies the egress policy.
	resp, err := p.keys.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("token endpoint unreachable: %v", redactURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// The error body is not read: it may echo the request.
		return "", fmt.Errorf("token endpoint answered HTTP %d", resp.StatusCode)
	}
	if mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mediaType != "application/json" {
		return "", fmt.Errorf("token endpoint answered %q, not JSON", mediaType)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDocumentBytes+1))
	if err != nil || len(body) > maxDocumentBytes {
		return "", errors.New("the token answer is unreadable or too large")
	}
	var answer struct {
		IDToken string `json:"id_token"`
	}
	if json.Unmarshal(body, &answer) != nil || answer.IDToken == "" || len(answer.IDToken) > maxTokenBytes {
		return "", errors.New("the token answer has no id_token")
	}
	return answer.IDToken, nil
}

func (p *ConsoleProvider) clientSecret() (string, error) {
	// #nosec G304 -- the file is the operator-set path of the mounted Secret's key.
	file, err := os.Open(p.secretFile)
	if err != nil {
		return "", errors.New("the console sign-in client secret is not mounted")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSecretBytes+1))
	if err != nil || len(data) > maxSecretBytes {
		return "", errors.New("the console sign-in client secret cannot be read")
	}
	secret := strings.TrimRight(string(data), "\r\n")
	if secret == "" {
		return "", errors.New("the console sign-in client secret is empty")
	}
	return secret, nil
}

// idClaims are the ID token claims checked here; the rest stay in the map.
type idClaims struct {
	jwt.Claims
	Nonce string `json:"nonce"`
	AZP   string `json:"azp"`
}

// VerifyIDToken checks an ID token's signature, issuer, and times as
// SPEC-0006-R08 does, then that its audience contains the client ID, that
// azp is the client ID when there are other audiences, and its nonce
// (SPEC-0032-R06).
func (p *ConsoleProvider) VerifyIDToken(ctx context.Context, raw, nonce string) (ConsoleClaims, error) {
	token, err := jwt.ParseSigned(raw, allowedAlgorithms)
	if err != nil || len(token.Headers) != 1 {
		return ConsoleClaims{}, errors.New("malformed ID token or algorithm not allowed")
	}
	keys, err := p.keys.keys(ctx, token.Headers[0].KeyID)
	if err != nil {
		return ConsoleClaims{}, fmt.Errorf("issuer keys unavailable: %v", err)
	}
	var claims idClaims
	var all map[string]any
	verified := false
	for _, key := range keys {
		if token.Claims(key.Key, &claims, &all) == nil {
			verified = true
			break
		}
	}
	if !verified {
		return ConsoleClaims{}, errors.New("ID token signature does not verify")
	}
	if claims.Expiry == nil {
		return ConsoleClaims{}, errors.New("ID token has no exp")
	}
	if err := claims.ValidateWithLeeway(jwt.Expected{Issuer: p.issuer, Time: p.now()}, clockSkew); err != nil {
		return ConsoleClaims{}, fmt.Errorf("ID token: %v", err)
	}
	if !claims.Audience.Contains(p.clientID) {
		return ConsoleClaims{}, errors.New("ID token audience is not this client")
	}
	if len(claims.Audience) > 1 && claims.AZP != p.clientID {
		return ConsoleClaims{}, errors.New("ID token has other audiences and azp is not this client")
	}
	if nonce == "" || subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(nonce)) != 1 {
		return ConsoleClaims{}, errors.New("ID token nonce does not match")
	}
	if claims.Subject == "" {
		return ConsoleClaims{}, errors.New("ID token has no sub")
	}
	return ConsoleClaims{Subject: claims.Subject, Claims: all}, nil
}

// StringClaims returns a claim as strings: a string, or the strings of an
// array. Other types give none.
func (c ConsoleClaims) StringClaims(name string) []string {
	switch v := c.Claims[name].(type) {
	case string:
		return []string{v}
	case []any:
		var out []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return slices.Clip(out)
	}
	return nil
}
