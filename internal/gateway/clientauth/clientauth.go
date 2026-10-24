// Package clientauth makes the Gateway an OAuth 2.1 resource server for MCP
// clients (SPEC-0006, ADR-0003): every request to /mcp/<route> needs a bearer
// JWT from a trusted authorization server whose audience is that route.
//
// Tokens are untrusted input until verified, and every failure refuses the
// request. Tokens and their claims are never logged or returned.
package clientauth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// Traffic outcomes of refused requests (SPEC-0006-R14).
const (
	OutcomeUnauthenticated = "unauthenticated"
	OutcomeForbidden       = "forbidden"
	OutcomeAuthUnavailable = "auth_unavailable"
)

// clockSkew is the leeway for exp, nbf, and iat (SPEC-0006-R08).
const clockSkew = 60 * time.Second

// allowedAlgorithms are the asymmetric JWS algorithms accepted. HMAC and
// "none" are absent, so a public key can never be used as a shared secret.
var allowedAlgorithms = []jose.SignatureAlgorithm{
	jose.RS256, jose.RS384, jose.RS512,
	jose.PS256, jose.PS384, jose.PS512,
	jose.ES256, jose.ES384, jose.ES512,
	jose.EdDSA,
}

var scopeToken = regexp.MustCompile(`^[\x21\x23-\x5b\x5d-\x7e]+$`)

// Issuer is an authorization server whose tokens the Gateway trusts.
type Issuer struct {
	// Issuer must equal the token's iss claim exactly.
	Issuer string `json:"issuer"`
	// JWKSURI is where the signing keys are; empty discovers it from the
	// authorization server metadata.
	JWKSURI string `json:"jwksURI,omitempty"`
	// AllowHTTP permits http:// metadata and key URLs, for in-cluster
	// authorization servers (SPEC-0006-R20).
	AllowHTTP bool `json:"allowHTTP,omitempty"`
	// TokenExchange is how the Gateway authenticates to this issuer for
	// token exchange (SPEC-0015-R02); nil means it does not exchange
	// tokens from this issuer.
	TokenExchange *TokenExchangeClient `json:"tokenExchange,omitempty"`
}

// TokenExchangeClient is the Gateway's OAuth client at one issuer.
type TokenExchangeClient struct {
	ClientID string `json:"clientID"`
	// ClientSecretKey names the file with the client secret in the mounted
	// token exchange Secret.
	ClientSecretKey string `json:"clientSecretKey"`
	// TokenEndpoint is used instead of the one in the issuer metadata.
	TokenEndpoint string `json:"tokenEndpoint,omitempty"`
}

// Config configures client authentication.
type Config struct {
	// ResourceURL is the Gateway's external base URL, for example
	// https://mcp.example.com. A route's audience is ResourceURL + "/mcp/" +
	// route.
	ResourceURL string
	Issuers     []Issuer
	// RequiredScopes must all be in the token's scope.
	RequiredScopes []string
	// TokenExchangeSecretDir is the mounted Secret with the client secrets
	// of the issuers' TokenExchange settings.
	TokenExchangeSecretDir string
}

// Validate reports configuration the Gateway must refuse to start with
// (SPEC-0006-R02, SPEC-0006-R20).
func (c Config) Validate() error {
	resource, err := url.Parse(c.ResourceURL)
	if c.ResourceURL == "" || err != nil || resource.Scheme != "https" || resource.Host == "" ||
		resource.User != nil || resource.RawQuery != "" || resource.Fragment != "" || strings.Trim(resource.Path, "/") != "" {
		return errors.New("the resource URL must be the Gateway's external https origin, for example https://mcp.example.com")
	}
	if len(c.Issuers) == 0 {
		return errors.New("at least one trusted issuer is required")
	}
	seen := map[string]bool{}
	for _, issuer := range c.Issuers {
		if seen[issuer.Issuer] {
			return fmt.Errorf("issuer %s is listed twice", issuer.Issuer)
		}
		seen[issuer.Issuer] = true
		if err := checkURL(issuer.Issuer, issuer.AllowHTTP); err != nil {
			return fmt.Errorf("issuer %q: %w", issuer.Issuer, err)
		}
		if issuer.JWKSURI != "" {
			if err := checkURL(issuer.JWKSURI, issuer.AllowHTTP); err != nil {
				return fmt.Errorf("issuer %s: jwksURI: %w", issuer.Issuer, err)
			}
		}
		if exchange := issuer.TokenExchange; exchange != nil {
			if err := exchange.validate(issuer.AllowHTTP, c.TokenExchangeSecretDir); err != nil {
				return fmt.Errorf("issuer %s: tokenExchange: %w", issuer.Issuer, err)
			}
		}
	}
	for _, scope := range c.RequiredScopes {
		if !scopeToken.MatchString(scope) {
			return fmt.Errorf("required scope %q is not a valid scope token", scope)
		}
	}
	return nil
}

var secretKeyName = regexp.MustCompile(`^[-._a-zA-Z0-9]{1,253}$`)

func (c *TokenExchangeClient) validate(allowHTTP bool, secretDir string) error {
	switch {
	case c.ClientID == "":
		return errors.New("clientID is required")
	case !secretKeyName.MatchString(c.ClientSecretKey) || strings.HasPrefix(c.ClientSecretKey, "."):
		return errors.New("clientSecretKey must name a key of the token exchange Secret")
	case secretDir == "":
		return errors.New("needs the token exchange Secret (--token-exchange-secret-dir)")
	}
	if c.TokenEndpoint != "" {
		if err := checkURL(c.TokenEndpoint, allowHTTP); err != nil {
			return fmt.Errorf("tokenEndpoint %w", err)
		}
	}
	return nil
}

// checkURL accepts absolute https URLs, and http ones when allowHTTP is set.
func checkURL(raw string, allowHTTP bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return errors.New("must be an absolute URL without credentials or fragment")
	}
	switch {
	case u.Scheme == "https":
		return nil
	case u.Scheme == "http" && allowHTTP:
		return nil
	case u.Scheme == "http":
		return errors.New("must use https; set allowHTTP for an in-cluster authorization server")
	default:
		return errors.New("must be an https URL")
	}
}

// Identity is the verified caller of a request.
type Identity struct {
	Subject  string
	ClientID string
	Issuer   string
	Scopes   []string
}

// Refusal is the answer to a request that is not authenticated.
type Refusal struct {
	Status int
	// WWWAuthenticate is the challenge (RFC 6750, RFC 9728).
	WWWAuthenticate string
	RetryAfter      string
	// Message is the response's error text; it never says which check failed.
	Message string
	Outcome string
}

// Authenticator verifies bearer tokens for MCP routes.
type Authenticator struct {
	resource       string
	issuers        map[string]*keySet
	issuerList     []string
	requiredScopes []string
	now            func() time.Time
	logger         *slog.Logger

	// Token exchange (SPEC-0015).
	secretDir string
	exchanged *exchangeCache
	warned    warnOnce
}

// New returns an Authenticator. Metadata and key requests go through
// transport, which must apply the Gateway's egress policy (SPEC-0006-R11).
func New(cfg Config, transport http.RoundTripper, logger *slog.Logger) (*Authenticator, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   fetchTimeout,
		// Metadata and keys are fetched from the configured URLs only.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	a := &Authenticator{
		resource:       strings.TrimRight(cfg.ResourceURL, "/"),
		issuers:        map[string]*keySet{},
		requiredScopes: slices.Clone(cfg.RequiredScopes),
		now:            time.Now,
		logger:         logger,
		secretDir:      cfg.TokenExchangeSecretDir,
		exchanged:      newExchangeCache(exchangeCacheSize),
	}
	for _, issuer := range cfg.Issuers {
		if issuer.AllowHTTP {
			logger.Warn("trusting an authorization server over plain http", "issuer", issuer.Issuer)
		}
		a.issuers[issuer.Issuer] = newKeySet(issuer, client, a.clock)
		a.issuerList = append(a.issuerList, issuer.Issuer)
	}
	return a, nil
}

func (a *Authenticator) clock() time.Time { return a.now() }

// ResourceURI is the canonical URI of a route, the audience its tokens must
// carry (RFC 8707).
func (a *Authenticator) ResourceURI(route string) string {
	return a.resource + "/mcp/" + route
}

// MetadataPath is the path of a route's Protected Resource Metadata.
func MetadataPath(route string) string {
	return "/.well-known/oauth-protected-resource/mcp/" + route
}

// MetadataURL is the absolute URL of a route's Protected Resource Metadata
// (RFC 9728 section 3.1).
func (a *Authenticator) MetadataURL(route string) string {
	return a.resource + MetadataPath(route)
}

// ProtectedResourceMetadata is the RFC 9728 document of one route
// (SPEC-0006-R07).
type ProtectedResourceMetadata struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
	ScopesSupported        []string `json:"scopes_supported,omitempty"`
}

// Metadata returns the Protected Resource Metadata of a route.
func (a *Authenticator) Metadata(route string) ProtectedResourceMetadata {
	return ProtectedResourceMetadata{
		Resource:               a.ResourceURI(route),
		AuthorizationServers:   slices.Clone(a.issuerList),
		BearerMethodsSupported: []string{"header"},
		ScopesSupported:        slices.Clone(a.requiredScopes),
	}
}

// claims are the access token claims the Gateway reads.
type claims struct {
	jwt.Claims
	Scope    string          `json:"scope"`
	SCP      jsonStringOrSet `json:"scp"`
	ClientID string          `json:"client_id"`
	AZP      string          `json:"azp"`
}

// Authenticate verifies the request's bearer token for route
// (SPEC-0006-R03 to R09, R12).
func (a *Authenticator) Authenticate(r *http.Request, route string) (Identity, *Refusal) {
	raw, ok := bearerToken(r.Header.Values("Authorization"))
	if !ok {
		return Identity{}, a.refuse(route, http.StatusUnauthorized, "", "authentication required", OutcomeUnauthenticated)
	}

	token, err := jwt.ParseSigned(raw, allowedAlgorithms)
	if err != nil || len(token.Headers) != 1 {
		return a.invalid(route, "malformed token or algorithm not allowed")
	}
	var unverified claims
	if err := token.UnsafeClaimsWithoutVerification(&unverified); err != nil {
		return a.invalid(route, "malformed claims")
	}
	keys, trusted := a.issuers[unverified.Issuer]
	if !trusted {
		return a.invalid(route, "untrusted issuer")
	}

	header := token.Headers[0]
	candidates, err := keys.keys(r.Context(), header.KeyID)
	if err != nil {
		a.logger.Warn("authorization server keys unavailable", "issuer", unverified.Issuer, "error", err)
		refusal := a.refuse(route, http.StatusServiceUnavailable, "", "authorization server keys unavailable", OutcomeAuthUnavailable)
		refusal.RetryAfter = "60"
		return Identity{}, refusal
	}
	var verified claims
	verifiedOK := false
	for _, key := range candidates {
		if token.Claims(key.Key, &verified) == nil {
			verifiedOK = true
			break
		}
	}
	if !verifiedOK {
		return a.invalid(route, "signature does not verify")
	}

	if verified.Expiry == nil {
		return a.invalid(route, "no exp claim")
	}
	expected := jwt.Expected{Issuer: unverified.Issuer, AnyAudience: jwt.Audience{a.ResourceURI(route)}, Time: a.now()}
	if err := verified.ValidateWithLeeway(expected, clockSkew); err != nil {
		return a.invalid(route, err.Error())
	}

	scopes := verified.scopes()
	for _, required := range a.requiredScopes {
		if !slices.Contains(scopes, required) {
			return Identity{}, a.refuse(route, http.StatusForbidden, "insufficient_scope", "insufficient scope", OutcomeForbidden)
		}
	}
	clientID := verified.ClientID
	if clientID == "" {
		clientID = verified.AZP
	}
	return Identity{Subject: verified.Subject, ClientID: clientID, Issuer: verified.Issuer, Scopes: scopes}, nil
}

func (c claims) scopes() []string {
	scopes := strings.Fields(c.Scope)
	for _, scope := range c.SCP {
		scopes = append(scopes, strings.Fields(scope)...)
	}
	return scopes
}

// invalid refuses a token without telling the caller why; the reason is
// logged at debug level, never with the token (SPEC-0006-R05, R14).
func (a *Authenticator) invalid(route, reason string) (Identity, *Refusal) {
	a.logger.Debug("refused an MCP access token", "route", route, "reason", reason)
	return Identity{}, a.refuse(route, http.StatusUnauthorized, "invalid_token", "invalid token", OutcomeUnauthenticated)
}

func (a *Authenticator) refuse(route string, status int, code, message, outcome string) *Refusal {
	params := []string{}
	if code != "" {
		params = append(params, `error="`+code+`"`)
	}
	if len(a.requiredScopes) > 0 && (status == http.StatusUnauthorized || status == http.StatusForbidden) {
		params = append(params, `scope="`+strings.Join(a.requiredScopes, " ")+`"`)
	}
	params = append(params, `resource_metadata="`+a.MetadataURL(route)+`"`)
	return &Refusal{
		Status:          status,
		WWWAuthenticate: "Bearer " + strings.Join(params, ", "),
		Message:         message,
		Outcome:         outcome,
	}
}

// InvalidRequest is the answer to a token sent anywhere but the
// Authorization header (SPEC-0006-R04).
func (a *Authenticator) InvalidRequest(route string) *Refusal {
	refusal := a.refuse(route, http.StatusBadRequest, "invalid_request", "send the access token in the Authorization header only", OutcomeUnauthenticated)
	return refusal
}

// bearerToken returns the token of a single "Authorization: Bearer" header.
func bearerToken(values []string) (string, bool) {
	if len(values) != 1 {
		return "", false
	}
	scheme, token, found := strings.Cut(strings.TrimSpace(values[0]), " ")
	token = strings.TrimSpace(token)
	if !found || !strings.EqualFold(scheme, "Bearer") || token == "" || strings.ContainsAny(token, " \t") {
		return "", false
	}
	return token, true
}

type identityKey struct{}

// WithIdentity returns ctx carrying the verified caller, for the policy and
// audit steps that follow authentication.
func WithIdentity(ctx context.Context, identity Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, identity)
}

// IdentityFrom returns the verified caller of a request, if any.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityKey{}).(Identity)
	return identity, ok
}
