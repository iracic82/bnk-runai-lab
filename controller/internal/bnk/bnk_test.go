package bnk

import (
	"context"
	"strings"
	"testing"

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

func TestVIPAllocationIsStableAndUnique(t *testing.T) {
	vips, _ := ParseVIPRange("172.16.201.110-172.16.201.111")
	c := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build()
	a := &Applier{Client: c, Config: DefaultConfig(), VIPs: vips}
	ctx := context.Background()

	// Create the first tenant's gateway the way Apply would, then allocate for both.
	gw := Render(a.Config, desired("tenant-a", "runai-tenant-a", 40), vips[0].String())[0]
	if err := c.Create(ctx, gw); err != nil {
		t.Fatal(err)
	}
	if ip, _ := a.vipFor(ctx, "tenant-a"); ip != vips[0] {
		t.Errorf("tenant-a keeps %v, got %v", vips[0], ip)
	}
	if ip, _ := a.vipFor(ctx, "tenant-b"); ip != vips[1] {
		t.Errorf("tenant-b gets the next free %v, got %v", vips[1], ip)
	}
	gw2 := Render(a.Config, desired("tenant-b", "runai-tenant-b", 10), vips[1].String())[0]
	_ = c.Create(ctx, gw2)
	if _, err := a.vipFor(ctx, "tenant-c"); err == nil {
		t.Error("an exhausted pool must be an error")
	}
}
