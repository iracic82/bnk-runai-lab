package bnk

import (
	"context"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/f5devcentral/bnk-runai-tenancy/internal/reconcile"
	"github.com/f5devcentral/bnk-runai-tenancy/internal/tenant"
)

func desired(project, ns string, rps float64) reconcile.Desired {
	return reconcile.Desired{
		Tenant:      tenant.Spec{Project: project, Department: "acme", Namespace: ns},
		RateLimit:   rps,
		Criticality: tenant.Critical,
	}
}

func TestRenderPlacesObjects(t *testing.T) {
	objs := Render(DefaultConfig(), desired("tenant-a", "runai-tenant-a", 40), "172.16.201.110")
	want := map[string]string{ // kind -> namespace
		"Gateway": "f5-bnk", "F5EPP": "runai-tenant-a", "InferencePool": "runai-tenant-a",
		"HTTPRoute": "runai-tenant-a", "F5BigCneIrule": "f5-bnk", "NetPolicy": "f5-bnk",
	}
	if len(objs) != len(want) {
		t.Fatalf("got %d objects, want %d", len(objs), len(want))
	}
	for _, o := range objs {
		if ns, ok := want[o.GetKind()]; !ok || o.GetNamespace() != ns {
			t.Errorf("%s in %q, want %q", o.GetKind(), o.GetNamespace(), ns)
		}
		l := o.GetLabels()
		if l[LabelProject] != "tenant-a" || l[LabelDepartment] != "acme" || l[LabelManagedBy] != ManagedBy {
			t.Errorf("%s labels = %v", o.GetKind(), l)
		}
	}
	gw := objs[0]
	addrs, _, _ := unstructured.NestedSlice(gw.Object, "spec", "addresses")
	if addrs[0].(map[string]any)["value"] != "172.16.201.110" {
		t.Errorf("gateway address = %v", addrs)
	}
	ls, _, _ := unstructured.NestedSlice(gw.Object, "spec", "listeners")
	from, _, _ := unstructured.NestedString(ls[0].(map[string]any), "allowedRoutes", "namespaces", "from")
	only, _, _ := unstructured.NestedString(ls[0].(map[string]any), "allowedRoutes", "namespaces", "selector", "matchLabels", "kubernetes.io/metadata.name")
	if from != "Selector" || only != "runai-tenant-a" {
		t.Errorf("gateway must accept routes only from runai-tenant-a, got from=%q selector=%q", from, only)
	}
}

func TestLimitIRule(t *testing.T) {
	r := LimitIRule("tenant-b", 10)
	for _, want := range []string{`if { $rl_n > 10 }`, `HTTP::respond 429`, `"X-BNK-Tenant" "tenant-b"`, `"rl_tenant_b"`, `scan $rl_d %d rl_n`} {
		if !strings.Contains(r, want) {
			t.Errorf("iRule missing %q:\n%s", want, r)
		}
	}
	if !strings.Contains(LimitIRule("x", 0.2), "$rl_n > 1 }") {
		t.Error("limit must never drop below 1 req/s")
	}
	if !strings.Contains(LimitIRule("x", 12.5), "$rl_n > 13 }") {
		t.Error("fractional limits round up")
	}
}

func TestParseVIPRange(t *testing.T) {
	ips, err := ParseVIPRange("172.16.201.110-172.16.201.112")
	if err != nil || len(ips) != 3 || ips[2].String() != "172.16.201.112" {
		t.Fatalf("ips=%v err=%v", ips, err)
	}
	if _, err := ParseVIPRange("172.16.201.9-172.16.201.1"); err == nil {
		t.Error("reversed range must fail")
	}
}

func newApplier(t *testing.T, n int) *Applier {
	t.Helper()
	sc := runtime.NewScheme()
	_ = corev1.AddToScheme(sc)
	vips, _ := ParseVIPRange(fmt.Sprintf("172.16.201.110-172.16.201.%d", 109+n))
	return &Applier{Client: fake.NewClientBuilder().WithScheme(sc).Build(), Config: DefaultConfig(), VIPs: vips}
}

func TestVIPAllocationIsStableAndUnique(t *testing.T) {
	a := newApplier(t, 2)
	ctx := context.Background()
	ipA, _ := a.vipFor(ctx, "tenant-a")
	ipB, _ := a.vipFor(ctx, "tenant-b")
	if ipA == ipB {
		t.Fatalf("both tenants got %v", ipA)
	}
	if again, _ := a.vipFor(ctx, "tenant-a"); again != ipA {
		t.Errorf("tenant-a keeps %v, got %v", ipA, again)
	}
	if _, err := a.vipFor(ctx, "tenant-c"); err == nil {
		t.Error("an exhausted pool must be an error")
	}
}

func TestReleasedVIPIsHeldBack(t *testing.T) {
	a := newApplier(t, 1)
	ctx := context.Background()
	ipA, _ := a.vipFor(ctx, "tenant-a")
	if err := a.releaseVIP(ctx, "tenant-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.vipFor(ctx, "tenant-b"); err == nil {
		t.Fatal("a just-released VIP must not go to a new tenant")
	}
	old := VIPHold
	VIPHold = 0
	defer func() { VIPHold = old }()
	if ip, err := a.vipFor(ctx, "tenant-b"); err != nil || ip != ipA {
		t.Errorf("after the hold the VIP is reusable: ip=%v err=%v", ip, err)
	}
}

func TestApplyRefusesForeignObjects(t *testing.T) {
	a := newApplier(t, 1)
	ctx := context.Background()
	// Someone created a Gateway by hand with the name tenant-a's would get.
	foreign := &unstructured.Unstructured{}
	foreign.SetGroupVersionKind(GatewayGVK)
	foreign.SetNamespace("f5-bnk")
	foreign.SetName(GatewayName("tenant-a"))
	foreign.SetLabels(map[string]string{"owner": "someone-else"})
	if err := a.Client.Create(ctx, foreign); err != nil {
		t.Fatal(err)
	}
	gw := Render(a.Config, desired("tenant-a", "runai-tenant-a", 40), "172.16.201.110")[0]
	if err := a.mustOwn(ctx, gw, "tenant-a"); err == nil {
		t.Fatal("must refuse to take over an object it does not manage")
	}
	// An object we manage for a different project is refused too.
	foreign.SetLabels(map[string]string{LabelManagedBy: ManagedBy, LabelProject: "tenant-b"})
	_ = a.Client.Update(ctx, foreign)
	if err := a.mustOwn(ctx, gw, "tenant-a"); err == nil {
		t.Fatal("must refuse an object managed for another project")
	}
	foreign.SetLabels(map[string]string{LabelManagedBy: ManagedBy, LabelProject: "tenant-a"})
	_ = a.Client.Update(ctx, foreign)
	if err := a.mustOwn(ctx, gw, "tenant-a"); err != nil {
		t.Errorf("own object must be accepted: %v", err)
	}
}
