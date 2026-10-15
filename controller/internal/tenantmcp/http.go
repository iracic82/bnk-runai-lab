package tenantmcp

import (
	"crypto/sha256"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MaxBodyBytes bounds a JSON-RPC request; every tool takes at most one short string.
const MaxBodyBytes = 64 << 10

// Handler serves MCP on /mcp and a liveness probe on /healthz.
//
// The MCP endpoint is the go-sdk Streamable HTTP handler in stateless mode: no Mcp-Session-Id is issued
// or read, GET/DELETE answer 405, and each POST is a complete exchange answered with application/json.
// Every request carries its own bearer token, so requests can land on any replica.
func Handler(srv *mcp.Server, token []byte, log *slog.Logger) http.Handler {
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{
			Stateless:           true,
			JSONResponse:        true,
			Logger:              log,
			MaxRequestBodyBytes: MaxBodyBytes,
		})
	mux := http.NewServeMux()
	mux.Handle("/mcp", RequireBearer(token, mcpHandler))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

// RequireBearer admits a request only when it carries "Authorization: Bearer <token>". The comparison is
// over SHA-256 digests in constant time, so neither the token's content nor its length leaks through timing.
func RequireBearer(token []byte, next http.Handler) http.Handler {
	want := sha256.Sum256(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := bearer(r.Header.Get("Authorization"))
		sum := sha256.Sum256([]byte(got))
		if !ok || len(token) == 0 || subtle.ConstantTimeCompare(sum[:], want[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="bnk-tenancy-mcp"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearer(h string) (string, bool) {
	scheme, tok, ok := strings.Cut(strings.TrimSpace(h), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	tok = strings.TrimSpace(tok)
	return tok, tok != ""
}
