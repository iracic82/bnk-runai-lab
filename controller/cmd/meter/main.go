// Command meter collects the token usage the tenant iRules record at the BNK front door.
//
// Every interval it asks each tenant Gateway's iRule to flush its parked usage lines (GET /bnk/flush);
// the iRule sends them over TCP to this process (:5514), which counts them into Prometheus series on
// :9102/metrics and writes each Run:ai Project's token total back as annotations, rolled up per Department.
package main

import (
	"context"
	"errors"
	"flag"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	kaiv1alpha1 "github.com/kai-scheduler/kai-resource-management-api/kai/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/f5devcentral/bnk-runai-tenancy/internal/meter"
)

func main() {
	var (
		listen, metricsAddr, peers, tokenFile, bnkNamespace string
		writeEvery, flushEvery                              time.Duration
		writeback                                           bool
		ledgerNamespace                                     string
	)
	flag.StringVar(&listen, "listen", ":5514", "TCP address the iRules send usage lines to")
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":9102", "Prometheus /metrics endpoint")
	flag.StringVar(&peers, "allowed-peers", "", "comma-separated IPs/CIDRs allowed to send usage (TMM self-IPs); empty rejects all")
	flag.StringVar(&tokenFile, "metering-token-file", "", "file holding the shared metering token (iRule auth line and /bnk/flush)")
	flag.StringVar(&bnkNamespace, "bnk-namespace", "f5-bnk", "namespace of the tenant Gateways to flush")
	flag.DurationVar(&flushEvery, "flush-interval", 5*time.Second, "how often each tenant iRule is asked to send its usage")
	flag.DurationVar(&writeEvery, "writeback-interval", 30*time.Second, "how often token totals are written to the ledger")
	flag.BoolVar(&writeback, "writeback", true, "keep per-project and per-department token totals (ledger ConfigMap + Events on the Run:ai objects)")
	flag.StringVar(&ledgerNamespace, "ledger-namespace", "bnk-runai-tenancy", "namespace of the bnk-runai-usage ledger ConfigMap")
	flag.Parse()
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	fail := func(msg string, err error) {
		log.Error(msg, "err", err)
		os.Exit(1)
	}

	allowed, err := meter.ParsePeers(peers)
	if err != nil {
		fail("allowed-peers", err)
	}
	if len(allowed) == 0 {
		log.Warn("--allowed-peers is empty: every usage connection will be rejected")
	}
	token := ""
	if tokenFile != "" {
		b, err := os.ReadFile(tokenFile)
		if err != nil {
			fail("metering-token-file", err)
		}
		token = strings.TrimSpace(string(b))
	}
	if token == "" {
		log.Warn("no metering token: usage lines are accepted on the peer allowlist alone, and tenant iRules cannot be flushed")
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	m := meter.NewMetrics(reg)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	l, err := net.Listen("tcp", listen)
	if err != nil {
		fail("listen", err)
	}
	srv := &meter.Server{Metrics: m, Log: log, AllowedPeers: allowed, Token: token}
	go func() {
		if err := srv.Serve(ctx, l); err != nil {
			fail("usage listener", err)
		}
	}()

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	hs := &http.Server{Addr: metricsAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fail("metrics server", err)
		}
	}()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		fail("scheme", err)
	}
	if err := kaiv1alpha1.AddToScheme(scheme); err != nil {
		fail("scheme", err)
	}
	cfg, err := ctrl.GetConfig()
	if err != nil {
		fail("kubeconfig", err)
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		fail("client", err)
	}
	if token != "" {
		f := &meter.Flusher{Client: c, Namespace: bnkNamespace, Token: token, Log: log,
			HTTP: &http.Client{Timeout: 5 * time.Second}}
		go f.Run(ctx, flushEvery)
	}
	log.Info("meter started", "listen", listen, "metrics", metricsAddr, "allowedPeers", peers,
		"auth", token != "", "writeback", writeback)
	if writeback {
		bc := kubernetes.NewForConfigOrDie(cfg)
		bcast := record.NewBroadcaster()
		bcast.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: bc.CoreV1().Events("")})
		defer bcast.Shutdown()
		rec := bcast.NewRecorder(scheme, corev1.EventSource{Component: "bnk-runai-meter"})
		(&meter.Writeback{Client: c, Namespace: ledgerNamespace, Recorder: rec, Metrics: m, Log: log}).Run(ctx, writeEvery)
	} else {
		<-ctx.Done()
	}
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = hs.Shutdown(sctx)
}
