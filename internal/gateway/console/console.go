// Package console serves the Gateway's read-only web console: MCPServer
// inventory and discovered tools, a playground that calls tools through the
// Gateway's own routes, and recent traffic. It is static files embedded in
// the binary and reads only the Gateway's JSON endpoints; it holds no
// credentials and writes no Kubernetes resources.
package console

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var static embed.FS

// securityHeaders lock the console to its own files and endpoints. Tool
// metadata it displays is untrusted and is only ever rendered as text.
var securityHeaders = map[string]string{
	"Content-Security-Policy": "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
		"connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'",
	"X-Content-Type-Options": "nosniff",
	"Referrer-Policy":        "no-referrer",
	"Cache-Control":          "no-cache",
}

// Handler serves the console. Paths are relative to the console root.
func Handler() http.Handler {
	files, err := fs.Sub(static, "static")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(files))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		for name, value := range securityHeaders {
			w.Header().Set(name, value)
		}
		fileServer.ServeHTTP(w, r)
	})
}
