package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// runIssuer serves a test-only OAuth authorization server for the end-to-end
// test of MCP client authentication (SPEC-0006): RFC 8414 metadata, a JWKS
// with one ES256 key generated at start, GET /mint, which signs any access
// token it is asked for, and POST /token, an RFC 8693 token exchange for the
// client CLIENT_ID with secret CLIENT_SECRET (SPEC-0015). It must never run
// outside a test cluster.
func runIssuer() int {
	issuer := os.Getenv("ISSUER")
	if issuer == "" {
		slog.Error("ISSUER is required")
		return 2
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		slog.Error("generate key", "error", err)
		return 1
	}
	public, err := key.PublicKey.ECDH()
	if err != nil {
		slog.Error("encode key", "error", err)
		return 1
	}
	point := public.Bytes() // 0x04 || X || Y
	jwks := map[string]any{"keys": []map[string]string{{
		"kty": "EC", "crv": "P-256", "kid": "e2e", "alg": "ES256", "use": "sig",
		"x": b64(point[1:33]), "y": b64(point[33:]),
	}}}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]string{"issuer": issuer, "jwks_uri": issuer + "/jwks", "token_endpoint": issuer + "/token"})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, jwks) })
	// /mint?aud=...&scope=...&sub=...&client_id=...&ttl=60s
	mux.HandleFunc("GET /mint", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		ttl, err := time.ParseDuration(q.Get("ttl"))
		if err != nil {
			ttl = time.Minute
		}
		claims := map[string]any{
			"iss": issuer, "aud": q.Get("aud"), "sub": q.Get("sub"), "client_id": q.Get("client_id"),
			"scope": q.Get("scope"), "iat": time.Now().Unix(), "exp": time.Now().Add(ttl).Unix(),
		}
		token, err := signES256(key, claims)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(token))
	})

	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		id, secret, ok := r.BasicAuth()
		clientID, _ := url.QueryUnescape(id)
		clientSecret, _ := url.QueryUnescape(secret)
		if !ok || os.Getenv("CLIENT_ID") == "" || clientID != os.Getenv("CLIENT_ID") || clientSecret != os.Getenv("CLIENT_SECRET") {
			w.WriteHeader(http.StatusUnauthorized)
			writeJSON(w, map[string]string{"error": "invalid_client"})
			return
		}
		subject, err := verifyES256(&key.PublicKey, r.PostFormValue("subject_token"))
		if err != nil || r.PostFormValue("grant_type") != "urn:ietf:params:oauth:grant-type:token-exchange" ||
			r.PostFormValue("subject_token_type") != "urn:ietf:params:oauth:token-type:access_token" || r.PostFormValue("audience") == "" {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]string{"error": "invalid_request"})
			return
		}
		claims := map[string]any{
			"iss": issuer, "aud": r.PostFormValue("audience"), "sub": subject["sub"], "client_id": clientID,
			"act": map[string]any{"sub": subject["client_id"]}, "scope": r.PostFormValue("scope"),
			"iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix(),
		}
		token, err := signES256(key, claims)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// #nosec G101 -- a token the test issuer just signed, not a hardcoded credential.
		writeJSON(w, map[string]any{"access_token": token, "issued_token_type": "urn:ietf:params:oauth:token-type:access_token", "token_type": "Bearer", "expires_in": 300})
	})

	server := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	slog.Info("starting test authorization server", "issuer", issuer)
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server stopped", "error", err)
		return 1
	}
	return 0
}

func signES256(key *ecdsa.PrivateKey, claims map[string]any) (string, error) {
	header, _ := json.Marshal(map[string]string{"alg": "ES256", "kid": "e2e", "typ": "at+jwt"})
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	input := b64(header) + "." + b64(payload)
	digest := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		return "", err
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	return input + "." + b64(signature), nil
}

// verifyES256 returns the claims of a token signed with key that has not
// expired.
func verifyES256(key *ecdsa.PublicKey, token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("not a JWT")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != 64 {
		return nil, errors.New("bad signature encoding")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(key, digest[:], new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])) {
		return nil, errors.New("signature does not verify")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	if exp, ok := claims["exp"].(float64); !ok || time.Now().Unix() > int64(exp) {
		return nil, errors.New("expired")
	}
	return claims, nil
}

func b64(data []byte) string {
	return strings.TrimRight(base64.URLEncoding.EncodeToString(data), "=")
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
