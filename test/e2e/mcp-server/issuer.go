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
	"net/http"
	"os"
	"strings"
	"time"
)

// runIssuer serves a test-only OAuth authorization server for the end-to-end
// test of MCP client authentication (SPEC-0006): RFC 8414 metadata, a JWKS
// with one ES256 key generated at start, and GET /mint, which signs any
// access token it is asked for. It must never run outside a test cluster.
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
		writeJSON(w, map[string]string{"issuer": issuer, "jwks_uri": issuer + "/jwks"})
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

func b64(data []byte) string {
	return strings.TrimRight(base64.URLEncoding.EncodeToString(data), "=")
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
