package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/kavrynt/kavrynt/internal/gateway/clientauth"
)

// Console sign-in with OpenID Connect (SPEC-0032).
const (
	// StartPath redirects the browser to the identity provider.
	StartPath = "/v1/session/oidc/start"
	// CallbackPath receives the provider's answer.
	CallbackPath = "/v1/session/oidc/callback"
	// stateCookieName holds state, nonce, and the PKCE verifier between the
	// two (SPEC-0032-R04).
	stateCookieName = "kavrynt_oidc"
	stateLifetime   = 10 * time.Minute

	methodAdmin = "admin"
	methodOIDC  = "oidc"
	// maxDisplayName bounds the name shown in the console.
	maxDisplayName = 128
)

// OIDCProvider is the identity provider a console sign-in uses;
// clientauth.ConsoleProvider implements it.
type OIDCProvider interface {
	AuthorizationEndpoint(ctx context.Context) (string, error)
	ExchangeCode(ctx context.Context, code, verifier, redirectURI string) (string, error)
	VerifyIDToken(ctx context.Context, raw, nonce string) (clientauth.ConsoleClaims, error)
}

// OIDC configures console sign-in through an identity provider.
type OIDC struct {
	Provider        OIDCProvider
	ProviderName    string
	ClientID        string
	RedirectURL     string
	Scopes          []string
	AllowedSubjects []string
	AllowedGroups   []string
	// GroupsClaim defaults to "groups"; DisplayClaim to
	// "preferred_username", falling back to sub.
	GroupsClaim  string
	DisplayClaim string
	// KeepAdminLogin leaves the admin password login on beside SSO
	// (SPEC-0032-R10).
	KeepAdminLogin bool
}

// EnableOIDC turns on console sign-in through the identity provider.
func (a *Authenticator) EnableOIDC(o OIDC) {
	if o.GroupsClaim == "" {
		o.GroupsClaim = "groups"
	}
	if o.DisplayClaim == "" {
		o.DisplayClaim = "preferred_username"
	}
	if o.ProviderName == "" {
		o.ProviderName = "your identity provider"
	}
	a.oidc = &o
}

// adminLoginEnabled reports whether the admin password opens the console.
func (a *Authenticator) adminLoginEnabled() bool {
	return a.oidc == nil || a.oidc.KeepAdminLogin
}

// signInState is kept in the signed state cookie.
type signInState struct {
	State    string `json:"state"`
	Nonce    string `json:"nonce"`
	Verifier string `json:"verifier"`
	Expires  int64  `json:"exp"`
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ServeOIDC serves StartPath and CallbackPath.
func (a *Authenticator) ServeOIDC(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.oidc == nil || r.Method != http.MethodGet {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	switch r.URL.Path {
	case StartPath:
		a.startSignIn(w, r)
	case CallbackPath:
		a.finishSignIn(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

// startSignIn redirects to the authorization endpoint with state, nonce, and
// a PKCE S256 challenge (SPEC-0032-R03), keeping them in a signed cookie
// (R04).
func (a *Authenticator) startSignIn(w http.ResponseWriter, r *http.Request) {
	creds, err := a.credentials()
	if err != nil {
		a.logger.Error("console sign-in unavailable", "error", errNotConfigured, "cause", err)
		page(w, http.StatusServiceUnavailable, "Sign-in is not configured on this Gateway.")
		return
	}
	endpoint, err := a.oidc.Provider.AuthorizationEndpoint(r.Context())
	if err != nil {
		a.logger.Warn("console sign-in unavailable", "error", err)
		page(w, http.StatusServiceUnavailable, "The identity provider cannot be reached. Try again in a minute.")
		return
	}
	target, err := url.Parse(endpoint)
	if err != nil {
		page(w, http.StatusServiceUnavailable, "The identity provider's sign-in address is not valid.")
		return
	}
	st := signInState{Expires: a.now().Add(stateLifetime).Unix()}
	for _, field := range []*string{&st.State, &st.Nonce, &st.Verifier} {
		if *field, err = randomToken(); err != nil {
			page(w, http.StatusInternalServerError, "Cannot start the sign-in.")
			return
		}
	}
	payload, _ := json.Marshal(st)
	enc := base64.RawURLEncoding
	// #nosec G124 -- Secure is set whenever the request arrived over HTTPS,
	// as for the session cookie (SPEC-0005-R05); the redirect URL is https,
	// so the callback that reads this cookie always is.
	http.SetCookie(w, &http.Cookie{
		Name: stateCookieName, Value: enc.EncodeToString(payload) + "." + enc.EncodeToString(creds.mac(payload)),
		Path: CallbackPath, MaxAge: int(stateLifetime / time.Second), HttpOnly: true,
		// Lax: the provider's redirect back is a cross-site navigation.
		SameSite: http.SameSiteLaxMode, Secure: secureRequest(r),
	})
	challenge := sha256.Sum256([]byte(st.Verifier))
	query := target.Query()
	query.Set("response_type", "code")
	query.Set("client_id", a.oidc.ClientID)
	query.Set("redirect_uri", a.oidc.RedirectURL)
	query.Set("scope", strings.Join(append([]string{"openid"}, a.oidc.Scopes...), " "))
	query.Set("state", st.State)
	query.Set("nonce", st.Nonce)
	query.Set("code_challenge", enc.EncodeToString(challenge[:]))
	query.Set("code_challenge_method", "S256")
	target.RawQuery = query.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

// finishSignIn checks the provider's answer, exchanges the code, verifies
// the ID token, and checks the allow lists (SPEC-0032-R05 to R08). It never
// logs the code or a token (R09).
func (a *Authenticator) finishSignIn(w http.ResponseWriter, r *http.Request) {
	// The state cookie is cleared whatever the outcome (SPEC-0032-R04).
	// #nosec G124 -- an expired cookie; Secure as for the session cookie.
	http.SetCookie(w, &http.Cookie{Name: stateCookieName, Value: "", Path: CallbackPath, MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secureRequest(r)})
	refuse := func(status int, reason, message string) {
		a.logger.Warn("console sign-in refused", "reason", reason, "client", a.clientAddress(r))
		page(w, status, message)
	}
	query := r.URL.Query()
	if providerError := query.Get("error"); providerError != "" {
		refuse(http.StatusBadRequest, "the identity provider answered "+boundedText(providerError, 64), "The identity provider did not sign you in.")
		return
	}
	creds, err := a.credentials()
	if err != nil {
		page(w, http.StatusServiceUnavailable, "Sign-in is not configured on this Gateway.")
		return
	}
	st, ok := readSignInState(r, creds, a.now())
	if !ok || subtle.ConstantTimeCompare([]byte(query.Get("state")), []byte(st.State)) != 1 {
		refuse(http.StatusBadRequest, "missing, expired, or mismatched state", "This sign-in link is not valid here. Start again from the console.")
		return
	}
	code := query.Get("code")
	if code == "" {
		refuse(http.StatusBadRequest, "no code", "The identity provider did not return a sign-in code.")
		return
	}
	idToken, err := a.oidc.Provider.ExchangeCode(r.Context(), code, st.Verifier, a.oidc.RedirectURL)
	if err != nil {
		refuse(http.StatusBadGateway, "code exchange failed: "+err.Error(), "The identity provider did not complete the sign-in. Try again.")
		return
	}
	claims, err := a.oidc.Provider.VerifyIDToken(r.Context(), idToken, st.Nonce)
	if err != nil {
		refuse(http.StatusBadRequest, "ID token refused: "+err.Error(), "The identity provider's answer could not be verified.")
		return
	}
	if !a.allowed(claims) {
		a.logger.Warn("console sign-in refused: not an allowed subject or group", "subject", boundedText(claims.Subject, 128))
		page(w, http.StatusForbidden, "Your account is not allowed to use this console. Ask the platform team to add you or your group.")
		return
	}
	name := claims.Subject
	if display := claims.StringClaims(a.oidc.DisplayClaim); len(display) > 0 && display[0] != "" {
		name = display[0]
	}
	name = boundedText(name, maxDisplayName)
	expires := a.now().Add(a.ttl)
	token, err := creds.sign(session{Subject: name, Expires: expires.Unix(), Method: methodOIDC})
	if err != nil {
		page(w, http.StatusInternalServerError, "Cannot create the session.")
		return
	}
	a.setCookie(w, r, token, int(a.ttl/time.Second))
	a.logger.Info("console sign-in succeeded", "user", name, "method", methodOIDC)
	http.Redirect(w, r, "/ui/", http.StatusSeeOther)
}

// allowed reports whether the subject or one of its groups is listed
// (SPEC-0032-R07).
func (a *Authenticator) allowed(claims clientauth.ConsoleClaims) bool {
	if slices.Contains(a.oidc.AllowedSubjects, claims.Subject) {
		return true
	}
	for _, group := range claims.StringClaims(a.oidc.GroupsClaim) {
		if slices.Contains(a.oidc.AllowedGroups, group) {
			return true
		}
	}
	return false
}

func readSignInState(r *http.Request, creds credentials, now time.Time) (signInState, bool) {
	cookie, err := r.Cookie(stateCookieName)
	if err != nil {
		return signInState{}, false
	}
	encoded, sig, ok := strings.Cut(cookie.Value, ".")
	if !ok {
		return signInState{}, false
	}
	enc := base64.RawURLEncoding
	payload, err := enc.DecodeString(encoded)
	if err != nil {
		return signInState{}, false
	}
	mac, err := enc.DecodeString(sig)
	if err != nil || subtle.ConstantTimeCompare(mac, creds.mac(payload)) != 1 {
		return signInState{}, false
	}
	var st signInState
	if json.Unmarshal(payload, &st) != nil || st.State == "" || now.Unix() >= st.Expires {
		return signInState{}, false
	}
	return st, true
}

// boundedText removes control characters and bounds s, for names shown or
// logged.
func boundedText(s string, limit int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if len(s) > limit {
		s = s[:limit]
	}
	return s
}

func secureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// page answers a browser navigation with a short HTML page that links back
// to the console. The message is fixed text, escaped anyway.
func page(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Kavrynt console sign-in</title>`+
		`<p style="font-family:sans-serif">%s</p><p style="font-family:sans-serif"><a href="/ui/">Back to the console</a></p>`,
		html.EscapeString(message))
}
