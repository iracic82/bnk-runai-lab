// Command mcp is bnk-tenancy-mcp: a stateless MCP server (Streamable HTTP) with read-only tools that show
// what F5 BNK built for each NVIDIA Run:ai project — its front door (VIP, Gateway status, rate limit,
// tenant header) and its metered tokens.
//
// It reads the Kubernetes API directly on every call (no informer cache, no sessions), so it keeps no
// state between requests and scales horizontally behind a plain ClusterIP Service.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/f5devcentral/bnk-runai-tenancy/internal/tenantmcp"
)

var version = "0.1.0"

func main() {
	var listen, tokenFile, bnkNS, ledgerNS string
	flag.StringVar(&listen, "listen", ":8090", "address for /mcp and /healthz")
	flag.StringVar(&tokenFile, "token-file", "", "file holding the bearer token clients must present (required)")
	flag.StringVar(&bnkNS, "bnk-namespace", "f5-bnk", "namespace of the tenant Gateways and iRules")
	flag.StringVar(&ledgerNS, "ledger-namespace", "bnk-runai-tenancy", "namespace of the bnk-runai-usage ledger ConfigMap")
	flag.Parse()
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	if err := run(listen, tokenFile, tenantmcp.Config{BNKNamespace: bnkNS, LedgerNamespace: ledgerNS}, log); err != nil {
		log.Error("bnk-tenancy-mcp stopped", "err", err)
		os.Exit(1)
	}
}

func run(listen, tokenFile string, cfg tenantmcp.Config, log *slog.Logger) error {
	if tokenFile == "" {
		return errors.New("--token-file is required")
	}
	raw, err := os.ReadFile(tokenFile)
	if err != nil {
		return err
	}
	token := bytes.TrimSpace(raw)
	if len(token) < 16 {
		return errors.New("token in --token-file must be at least 16 characters")
	}

	restCfg, err := ctrl.GetConfig()
	if err != nil {
		return err
	}
	// A direct client: every read goes to the API server, nothing is cached in this process.
	c, err := client.New(restCfg, client.Options{Scheme: clientgoscheme.Scheme})
	if err != nil {
		return err
	}
	srv := tenantmcp.NewServer(&tenantmcp.Tools{Reader: c, Config: cfg}, version)

	httpSrv := &http.Server{
		Addr:              listen,
		Handler:           tenantmcp.Handler(srv, token, log),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.ListenAndServe() }()
	log.Info("bnk-tenancy-mcp listening", "addr", listen, "version", version, "transport", "streamable-http (stateless)")

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdown)
	}
}
