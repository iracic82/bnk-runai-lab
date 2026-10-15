// Command manager runs the BNK ↔ Run:ai tenancy controller: one BNK tenant per Run:ai project.
package main

import (
	"context"
	"flag"
	"os"
	"strings"

	kaiv1alpha1 "github.com/kai-scheduler/kai-resource-management-api/kai/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/f5devcentral/bnk-runai-tenancy/internal/bnk"
	"github.com/f5devcentral/bnk-runai-tenancy/internal/reconcile"
)

// dryRunApplier logs what it would write. It lets the controller run against a real Run:ai
// install before the BNK side exists.
type dryRunApplier struct{}

func (dryRunApplier) Apply(ctx context.Context, d reconcile.Desired) error {
	ctrl.LoggerFrom(ctx).Info("dry-run: would apply BNK tenant", "project", d.Tenant.Project,
		"namespace", d.Tenant.Namespace, "department", d.Tenant.Department,
		"rps", d.RateLimit, "criticality", d.Criticality)
	return nil
}

func (dryRunApplier) Projects(context.Context) ([]string, error) { return nil, nil }

func (dryRunApplier) Delete(ctx context.Context, project string) error {
	ctrl.LoggerFrom(ctx).Info("dry-run: would delete BNK tenant", "project", project)
	return nil
}

type tenantState struct{ g *prometheus.GaugeVec }

func (s tenantState) Set(project, state string) {
	s.g.DeletePartialMatch(prometheus.Labels{"project": project})
	s.g.WithLabelValues(project, state).Set(1)
}
func (s tenantState) Forget(project string) {
	s.g.DeletePartialMatch(prometheus.Labels{"project": project})
}

func main() {
	var (
		metricsAddr, probeAddr string
		leaderElect, dryRun    bool
		bnkCfg                 = bnk.DefaultConfig()
		vipRange               string
		meteringTokenFile      string
		policy                 = reconcile.DefaultPolicy()
	)
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "metrics endpoint")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "health probe endpoint")
	flag.BoolVar(&leaderElect, "leader-elect", false, "enable leader election")
	flag.BoolVar(&dryRun, "dry-run", false, "log BNK changes instead of applying them")
	flag.StringVar(&bnkCfg.Namespace, "bnk-namespace", bnkCfg.Namespace, "namespace of BNK gateways and iRules")
	flag.StringVar(&bnkCfg.GatewayClass, "gateway-class", bnkCfg.GatewayClass, "GatewayClass served by BNK")
	flag.StringVar(&bnkCfg.PullSecret, "pull-secret", bnkCfg.PullSecret, "BNK registry secret copied into tenant namespaces (empty: don't copy)")
	flag.StringVar(&bnkCfg.Tokenizer, "tokenizer", bnkCfg.Tokenizer, "tokenizer for the Endpoint Picker's prefix-cache scorer")
	flag.StringVar(&bnkCfg.DynamoNATS, "dynamo-nats", bnkCfg.DynamoNATS, "NATS URL Dynamo workers publish KV events on (dynamo-platform chart)")
	flag.StringVar(&bnkCfg.DynamoTokenizer, "dynamo-tokenizer", bnkCfg.DynamoTokenizer, "tokenizer for Dynamo tenants' Endpoint Picker")
	flag.StringVar(&bnkCfg.MeteringSink, "metering-sink", "", "IP:port TMM sends usage lines to (the meter on the control node); empty = metering off")
	flag.StringVar(&meteringTokenFile, "metering-token-file", "", "file holding the shared metering token (required with --metering-sink)")
	flag.StringVar(&vipRange, "vip-range", "172.16.201.110-172.16.201.149", "addresses tenant gateways are served from")
	flag.Float64Var(&policy.BaseRPS, "base-rps", policy.BaseRPS, "requests/s per deserved GPU")
	flag.Float64Var(&policy.MinGPUFactor, "min-gpu-factor", policy.MinGPUFactor, "GPU floor for projects without a GPU quota")
	var threshold int
	flag.IntVar(&threshold, "critical-priority-threshold", int(policy.CriticalPriorityThreshold), "Run:ai queue priority that maps to Critical")
	opts := zap.Options{}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	policy.CriticalPriorityThreshold = int32(threshold)
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	log := ctrl.Log.WithName("setup")
	if bnkCfg.MeteringSink != "" {
		if meteringTokenFile == "" {
			log.Error(nil, "--metering-sink needs --metering-token-file")
			os.Exit(1)
		}
		b, err := os.ReadFile(meteringTokenFile)
		if err != nil {
			log.Error(err, "metering-token-file")
			os.Exit(1)
		}
		bnkCfg.MeteringToken = strings.TrimSpace(string(b))
	}
	if err := bnk.ValidateMetering(bnkCfg.MeteringSink, bnkCfg.MeteringToken); err != nil {
		log.Error(err, "metering")
		os.Exit(1)
	}

	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, kaiv1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			log.Error(err, "scheme")
			os.Exit(1)
		}
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         leaderElect,
		LeaderElectionID:       "bnk-runai-tenancy.tenancy.f5.com",
		// The VIP map is the only ConfigMap used, and RBAC grants get/update on that one name. A cache
		// would need list/watch on every ConfigMap, so ConfigMaps are read straight from the API server.
		Client: client.Options{Cache: &client.CacheOptions{DisableFor: []client.Object{&corev1.ConfigMap{}, &corev1.Secret{}}}},
	})
	if err != nil {
		log.Error(err, "manager")
		os.Exit(1)
	}

	state := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "bnk_tenant_state", Help: "Current state of the BNK tenant for each Run:ai project (1 = current).",
	}, []string{"project", "state"})
	crmetrics.Registry.MustRegister(state)

	vips, err := bnk.ParseVIPRange(vipRange)
	if err != nil {
		log.Error(err, "vip-range")
		os.Exit(1)
	}
	var applier reconcile.BundleApplier = &bnk.Applier{Client: mgr.GetClient(), Config: bnkCfg, VIPs: vips}
	if dryRun {
		applier = dryRunApplier{}
	}
	rec := &reconcile.ProjectReconciler{
		Client:   mgr.GetClient(),
		Applier:  applier,
		Policy:   policy,
		Recorder: mgr.GetEventRecorderFor("bnk-runai-tenancy"),
		State:    tenantState{state},
	}
	if err := rec.SetupWithManager(mgr); err != nil {
		log.Error(err, "controller")
		os.Exit(1)
	}
	// Once the cache is up and this replica leads: drop tenants whose project vanished while down.
	if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		if !mgr.GetCache().WaitForCacheSync(ctx) {
			return nil
		}
		if err := rec.Sweep(ctx); err != nil {
			log.Error(err, "start-up sweep of orphaned BNK tenants")
		}
		return nil
	})); err != nil {
		log.Error(err, "sweep")
		os.Exit(1)
	}
	_ = mgr.AddHealthzCheck("healthz", healthz.Ping)
	_ = mgr.AddReadyzCheck("readyz", healthz.Ping)

	log.Info("starting", "dryRun", dryRun, "policy", policy, "meteringSink", bnkCfg.MeteringSink)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "manager exited")
		os.Exit(1)
	}
}
