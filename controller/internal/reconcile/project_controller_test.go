package reconcile

import (
	"context"
	"k8s.io/client-go/tools/record"
	"sync"
	"testing"

	kaiv1alpha1 "github.com/kai-scheduler/kai-resource-management-api/kai/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// recordingApplier stands in for the BNK side and records what it was asked to do.
type recordingApplier struct {
	mu      sync.Mutex
	applied map[string]Desired
	deleted []string
}

func newRecorder() *recordingApplier { return &recordingApplier{applied: map[string]Desired{}} }

func (a *recordingApplier) Apply(_ context.Context, d Desired) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.applied[d.Tenant.Project] = d
	return nil
}

func (a *recordingApplier) Projects(_ context.Context) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for p := range a.applied {
		out = append(out, p)
	}
	return out, nil
}

func (a *recordingApplier) Delete(_ context.Context, project string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.applied, project)
	a.deleted = append(a.deleted, project)
	return nil
}

type memGauge map[string]string

func (g memGauge) Set(p, s string) { g[p] = s }
func (g memGauge) Forget(p string) { delete(g, p) }

func readyProject(name, ns string, gpu float64, prio int32) *kaiv1alpha1.Project {
	return &kaiv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: kaiv1alpha1.ProjectSpec{Parent: "acme", Queues: []kaiv1alpha1.QueueConfig{{
			Name: name, Nodepool: "default", Priority: &prio,
			Resources: &kaiv1alpha1.QueueResourcesConfig{GPU: kaiv1alpha1.SystemResource{Deserved: gpu}},
		}}},
		Status: kaiv1alpha1.ProjectStatus{Namespace: ns, Phase: kaiv1alpha1.Ready},
	}
}

func setup(t *testing.T, objs ...client.Object) (*ProjectReconciler, *recordingApplier, memGauge) {
	t.Helper()
	s := runtime.NewScheme()
	if err := kaiv1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	// Run:ai creates every ready project's namespace; add the ones a test did not provide itself.
	have := map[string]bool{}
	for _, o := range objs {
		if ns, ok := o.(*corev1.Namespace); ok {
			have[ns.Name] = true
		}
	}
	for _, o := range objs {
		if p, ok := o.(*kaiv1alpha1.Project); ok && p.Status.Namespace != "" && !have[p.Status.Namespace] {
			have[p.Status.Namespace] = true
			objs = append(objs, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: p.Status.Namespace}})
		}
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).
		WithStatusSubresource(&kaiv1alpha1.Project{}).Build()
	a, g := newRecorder(), memGauge{}
	return &ProjectReconciler{Client: c, Applier: a, Policy: DefaultPolicy(), State: g}, a, g
}

func reconcileOnce(t *testing.T, r *ProjectReconciler, name string) ctrl.Result {
	t.Helper()
	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: name}})
	if err != nil {
		t.Fatalf("reconcile %s: %v", name, err)
	}
	return res
}

func TestProjectBecomesTenant(t *testing.T) {
	r, a, g := setup(t, readyProject("tenant-a", "runai-tenant-a", 2, 150))
	reconcileOnce(t, r, "tenant-a")

	d, ok := a.applied["tenant-a"]
	if !ok {
		t.Fatal("no BNK tenant applied for tenant-a")
	}
	if d.Tenant.Namespace != "runai-tenant-a" || d.Tenant.Department != "acme" {
		t.Errorf("tenant = %+v", d.Tenant)
	}
	if d.RateLimit != 40 || d.Criticality != "Critical" {
		t.Errorf("limit/criticality = %v/%v, want 40/Critical", d.RateLimit, d.Criticality)
	}
	if g["tenant-a"] != StateServing {
		t.Errorf("state = %q", g["tenant-a"])
	}
	// Run:ai's webhook refuses writes to projects from anyone but Run:ai: the controller must not write.
	var p kaiv1alpha1.Project
	_ = r.Get(context.Background(), types.NamespacedName{Name: "tenant-a"}, &p)
	if len(p.Finalizers) != 0 || p.ResourceVersion != "999" {
		t.Errorf("controller wrote to the Run:ai project: finalizers=%v resourceVersion=%s", p.Finalizers, p.ResourceVersion)
	}
}

func TestQuotaChangeUpdatesLimit(t *testing.T) {
	r, a, _ := setup(t, readyProject("tenant-b", "runai-tenant-b", 0.5, 50))
	reconcileOnce(t, r, "tenant-b")
	if a.applied["tenant-b"].RateLimit != 10 {
		t.Fatalf("initial limit = %v", a.applied["tenant-b"].RateLimit)
	}

	var p kaiv1alpha1.Project
	_ = r.Get(context.Background(), types.NamespacedName{Name: "tenant-b"}, &p)
	p.Spec.Queues[0].Resources.GPU.Deserved = 1
	if err := r.Update(context.Background(), &p); err != nil {
		t.Fatal(err)
	}
	reconcileOnce(t, r, "tenant-b")
	if a.applied["tenant-b"].RateLimit != 20 {
		t.Errorf("limit after quota change = %v, want 20", a.applied["tenant-b"].RateLimit)
	}
}

func TestNotReadyIsRequeued(t *testing.T) {
	p := readyProject("tenant-a", "", 2, 150)
	p.Status.Phase = kaiv1alpha1.NotReady
	r, a, g := setup(t, p)
	res := reconcileOnce(t, r, "tenant-a")
	if res.RequeueAfter == 0 || len(a.applied) != 0 || g["tenant-a"] != StatePending {
		t.Errorf("res=%+v applied=%v state=%q", res, a.applied, g["tenant-a"])
	}
}

func TestDeleteRemovesTenant(t *testing.T) {
	p := readyProject("tenant-b", "runai-tenant-b", 0.5, 50)
	r, a, g := setup(t, p)
	reconcileOnce(t, r, "tenant-b")

	if err := r.Delete(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	reconcileOnce(t, r, "tenant-b")
	if len(a.deleted) != 1 || a.deleted[0] != "tenant-b" {
		t.Errorf("deleted = %v", a.deleted)
	}
	if _, ok := a.applied["tenant-b"]; ok {
		t.Error("tenant-b must have no BNK tenant after its project is gone")
	}
	if _, ok := g["tenant-b"]; ok {
		t.Error("state should be forgotten after delete")
	}
}

// A project deleted while the controller was down leaves BNK objects behind; Sweep removes them
// and leaves live tenants alone.
func TestSweepRemovesOrphans(t *testing.T) {
	r, a, _ := setup(t, readyProject("tenant-a", "runai-tenant-a", 2, 150))
	reconcileOnce(t, r, "tenant-a")
	a.applied["ghost"] = Desired{}
	if err := r.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.applied["ghost"]; ok {
		t.Error("orphaned tenant 'ghost' should be removed")
	}
	if _, ok := a.applied["tenant-a"]; !ok {
		t.Error("live tenant-a must survive the sweep")
	}
}

func TestNamespaceConflictWritesNothing(t *testing.T) {
	r, a, g := setup(t, readyProject("x", "runai-shared", 1, 50), readyProject("y", "runai-shared", 1, 50))
	reconcileOnce(t, r, "x")
	reconcileOnce(t, r, "y")
	if len(a.applied) != 0 {
		t.Errorf("no BNK config may be written on conflict, got %v", a.applied)
	}
	if g["x"] != StateError || g["y"] != StateError {
		t.Errorf("states = %v", g)
	}
}

// A tenant already serving must lose its BNK config the moment another project claims its
// namespace, or the newcomer's pods would receive the first tenant's traffic.
func TestConflictRemovesExistingTenant(t *testing.T) {
	r, a, g := setup(t, readyProject("tenant-a", "runai-shared", 2, 150))
	reconcileOnce(t, r, "tenant-a")
	if _, ok := a.applied["tenant-a"]; !ok {
		t.Fatal("tenant-a should be serving before the conflict")
	}

	if err := r.Create(context.Background(), readyProject("intruder", "runai-shared", 1, 50)); err != nil {
		t.Fatal(err)
	}
	reconcileOnce(t, r, "tenant-a")
	reconcileOnce(t, r, "intruder")

	if len(a.applied) != 0 {
		t.Errorf("no project in a conflict may keep BNK config, got %v", a.applied)
	}
	if g["tenant-a"] != StateError || g["intruder"] != StateError {
		t.Errorf("states = %v", g)
	}
}

func TestBadAnnotationIsAnErrorNotACrash(t *testing.T) {
	p := readyProject("tenant-a", "runai-tenant-a", 2, 150)
	p.Annotations = map[string]string{"tenancy.f5.com/rps": "fast"}
	r, a, g := setup(t, p)
	reconcileOnce(t, r, "tenant-a")
	if len(a.applied) != 0 || g["tenant-a"] != StateError {
		t.Errorf("applied=%v state=%q", a.applied, g["tenant-a"])
	}
}

func TestSkipReleasesTenant(t *testing.T) {
	r, a, _ := setup(t, readyProject("tenant-a", "runai-tenant-a", 2, 150))
	reconcileOnce(t, r, "tenant-a")

	var p kaiv1alpha1.Project
	_ = r.Get(context.Background(), types.NamespacedName{Name: "tenant-a"}, &p)
	p.Annotations = map[string]string{"tenancy.f5.com/skip": "true"}
	if err := r.Update(context.Background(), &p); err != nil {
		t.Fatal(err)
	}
	reconcileOnce(t, r, "tenant-a")
	if _, ok := a.applied["tenant-a"]; ok {
		t.Error("skipped project must have no BNK tenant")
	}
}

func TestReconcileIsIdempotent(t *testing.T) {
	r, a, _ := setup(t, readyProject("tenant-a", "runai-tenant-a", 2, 150))
	for i := 0; i < 3; i++ {
		reconcileOnce(t, r, "tenant-a")
	}
	if len(a.applied) != 1 || len(a.deleted) != 0 {
		t.Errorf("applied=%v deleted=%v", a.applied, a.deleted)
	}
}

func TestReadyEventOnlyOnChange(t *testing.T) {
	r, _, _ := setup(t, readyProject("tenant-a", "runai-tenant-a", 2, 150))
	rec := record.NewFakeRecorder(16)
	r.Recorder = rec
	for i := 0; i < 3; i++ {
		reconcileOnce(t, r, "tenant-a")
	}
	if n := len(rec.Events); n != 1 {
		t.Fatalf("3 identical reconciles posted %d events, want 1", n)
	}
	var p kaiv1alpha1.Project
	_ = r.Get(context.Background(), types.NamespacedName{Name: "tenant-a"}, &p)
	p.Spec.Queues[0].Resources.GPU.Deserved = 1
	if err := r.Update(context.Background(), &p); err != nil {
		t.Fatal(err)
	}
	reconcileOnce(t, r, "tenant-a")
	if n := len(rec.Events); n != 2 {
		t.Errorf("a quota change should post one more event, got %d total", n)
	}
}

func TestDynamoNamespaceSelectsDynamoEngine(t *testing.T) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "runai-dynamo", Annotations: map[string]string{
		"tenancy.f5.com/engine": "dynamo", "tenancy.f5.com/dynamo-graph": "mocker"}}}
	r, a, _ := setup(t, readyProject("dynamo", "runai-dynamo", 1, 50), ns)
	reconcileOnce(t, r, "dynamo")
	d := a.applied["dynamo"]
	if d.Tenant.Engine != "dynamo" || d.Tenant.DynamoGraph != "mocker" {
		t.Errorf("engine/graph = %q/%q, want dynamo/mocker", d.Tenant.Engine, d.Tenant.DynamoGraph)
	}
}

func TestPlainNamespaceIsVLLM(t *testing.T) {
	r, a, _ := setup(t, readyProject("tenant-a", "runai-tenant-a", 2, 150))
	reconcileOnce(t, r, "tenant-a")
	if e := a.applied["tenant-a"].Tenant.Engine; e != "vllm" {
		t.Errorf("engine = %q, want vllm", e)
	}
}

func TestDynamoWithoutGraphIsAnError(t *testing.T) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "runai-dynamo", Annotations: map[string]string{
		"tenancy.f5.com/engine": "dynamo"}}}
	r, a, g := setup(t, readyProject("dynamo", "runai-dynamo", 1, 50), ns)
	reconcileOnce(t, r, "dynamo")
	if _, ok := a.applied["dynamo"]; ok || g["dynamo"] != StateError {
		t.Errorf("applied=%v state=%q, want nothing applied and Error", a.applied, g["dynamo"])
	}
}

func TestMissingNamespaceWaits(t *testing.T) {
	s := runtime.NewScheme()
	_ = kaiv1alpha1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(readyProject("tenant-a", "runai-tenant-a", 2, 150)).
		WithStatusSubresource(&kaiv1alpha1.Project{}).Build()
	a, g := newRecorder(), memGauge{}
	r := &ProjectReconciler{Client: c, Applier: a, Policy: DefaultPolicy(), State: g}
	res := reconcileOnce(t, r, "tenant-a")
	if res.RequeueAfter == 0 || len(a.applied) != 0 || g["tenant-a"] != StatePending {
		t.Errorf("res=%+v applied=%v state=%q", res, a.applied, g["tenant-a"])
	}
}
