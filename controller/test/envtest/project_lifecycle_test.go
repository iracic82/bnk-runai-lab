// Package envtest runs the Run:ai project reconciler against a real kube-apiserver (K8s 1.30,
// the BNK 2.4 cluster version) with the kai.resources CRDs that the Run:ai cluster chart ships.
// Run with: make envtest
package envtest

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	kaiv1alpha1 "github.com/kai-scheduler/kai-resource-management-api/kai/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/f5devcentral/bnk-runai-tenancy/internal/reconcile"
)

type applier struct {
	mu      sync.Mutex
	applied map[string]reconcile.Desired
}

func (a *applier) Apply(_ context.Context, d reconcile.Desired) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.applied[d.Tenant.Project] = d
	return nil
}

func (a *applier) Delete(_ context.Context, p string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.applied, p)
	return nil
}

func (a *applier) Projects(_ context.Context) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for p := range a.applied {
		out = append(out, p)
	}
	return out, nil
}

func (a *applier) get(p string) (reconcile.Desired, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	d, ok := a.applied[p]
	return d, ok
}

func eventually(t *testing.T, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second) // SC-001: converge within 30 s
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func TestProjectLifecycle(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set; run `make envtest`")
	}
	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "crds")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = env.Stop() }()

	s := runtime.NewScheme()
	if err := kaiv1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{Scheme: s, Metrics: metricsserver.Options{BindAddress: "0"}})
	if err != nil {
		t.Fatal(err)
	}
	a := &applier{applied: map[string]reconcile.Desired{}}
	r := &reconcile.ProjectReconciler{Client: mgr.GetClient(), Applier: a, Policy: reconcile.DefaultPolicy(),
		Requeue: 200 * time.Millisecond}
	if err := r.SetupWithManager(mgr); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = mgr.Start(ctx) }()

	c, err := client.New(cfg, client.Options{Scheme: s})
	if err != nil {
		t.Fatal(err)
	}

	prio := int32(150)
	p := &kaiv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "tenant-a"},
		Spec: kaiv1alpha1.ProjectSpec{Parent: "acme", Queues: []kaiv1alpha1.QueueConfig{{
			Name: "tenant-a", Nodepool: "default", Priority: &prio,
			Resources: &kaiv1alpha1.QueueResourcesConfig{GPU: kaiv1alpha1.SystemResource{Deserved: 2, Limit: -1}},
		}}},
	}
	if err := c.Create(ctx, p); err != nil {
		t.Fatal(err)
	}

	// Until Run:ai reports the project Ready with a namespace, BNK gets nothing.
	time.Sleep(500 * time.Millisecond)
	if _, ok := a.get("tenant-a"); ok {
		t.Fatal("BNK tenant written before the Run:ai project was ready")
	}

	// Play the Run:ai project controller: report Ready and the namespace it chose.
	p.Status = kaiv1alpha1.ProjectStatus{Namespace: "runai-tenant-a", Phase: kaiv1alpha1.Ready}
	if err := c.Status().Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	eventually(t, "BNK tenant for tenant-a", func() bool {
		d, ok := a.get("tenant-a")
		return ok && d.Tenant.Namespace == "runai-tenant-a" && d.RateLimit == 40
	})

	// The controller never writes to the Run:ai project (Run:ai's webhook would refuse it).
	var got kaiv1alpha1.Project
	if err := c.Get(ctx, types.NamespacedName{Name: "tenant-a"}, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Finalizers) != 0 {
		t.Fatalf("controller added finalizers %v to the Run:ai project", got.Finalizers)
	}

	// Delete in Run:ai: the BNK tenant goes with it.
	if err := c.Delete(ctx, &got); err != nil {
		t.Fatal(err)
	}
	eventually(t, "BNK tenant removed and project gone", func() bool {
		_, ok := a.get("tenant-a")
		err := c.Get(ctx, types.NamespacedName{Name: "tenant-a"}, &kaiv1alpha1.Project{})
		return !ok && client.IgnoreNotFound(err) == nil && err != nil
	})
}
