package bnk

import (
	"context"
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	objs, err := Render(DefaultConfig(), desired("tenant-a", "runai-tenant-a", 40), "172.16.201.110")
	if err != nil {
		t.Fatal(err)
	}
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

func mustRender(t *testing.T, cfg Config, d reconcile.Desired, vip string) []*unstructured.Unstructured {
	t.Helper()
	objs, err := Render(cfg, d, vip)
	if err != nil {
		t.Fatal(err)
	}
	return objs
}

func mustIRule(t *testing.T, project, dept string, rps float64, sink, token string) string {
	t.Helper()
	r, err := TenantIRule(project, dept, rps, sink, token)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

const testToken = "flush-token-0123456789abcdef"

func TestLimitIRule(t *testing.T) {
	r := mustIRule(t, "tenant-b", "acme", 10, "", "")
	for _, want := range []string{`if { $rl_n > 10 }`, `HTTP::respond 429`, `"X-BNK-Tenant" "tenant-b"`, `"rl_tenant_b"`, `scan $rl_d %d rl_n`} {
		if !strings.Contains(r, want) {
			t.Errorf("iRule missing %q:\n%s", want, r)
		}
	}
	for _, absent := range []string{"connect", "HTTP::collect", "/bnk/", "%!", "@"} {
		if strings.Contains(r, absent) {
			t.Errorf("metering off must not render %q:\n%s", absent, r)
		}
	}
	if !strings.Contains(mustIRule(t, "x", "", 0.2, "", ""), "$rl_n > 1 }") {
		t.Error("limit must never drop below 1 req/s")
	}
	if !strings.Contains(mustIRule(t, "x", "", 12.5, "", ""), "$rl_n > 13 }") {
		t.Error("fractional limits round up")
	}
}

func TestMeteredIRule(t *testing.T) {
	r := mustIRule(t, "tenant-b", "acme", 10, "10.1.10.10:5514", testToken)
	for _, want := range []string{
		// rate limiting unchanged
		`if { $rl_n > 10 }`, `HTTP::respond 429`, `"X-BNK-Tenant" "tenant-b"`,
		// the flush path is token-gated and sends to the sink from a request event only
		`[HTTP::header value "X-BNK-Flush"] eq "` + testToken + `"`,
		`[HTTP::path] ne "/bnk/flush"`,
		`connect -protocol TCP "10.1.10.10:5514"`, "send -timeout 2000 $tm_c \"auth " + testToken + "\\n$tm_lines\"",
		`log local0. "BNK usage flush failed: $tm_err"`,
		// records are parked under the tenant's own subtable, values read via regexp + scan
		`"tm_tenant_b"`, `scan $tm_d %d tm_seq`,
		// the line format the meter parses
		`"project=tenant-b dept=acme model=$tm_m prompt=$tm_p completion=$tm_cp total=$tm_t code=$tm_status"`,
		`code=429 n=$tm_k`,
		// non-stream and SSE
		`HTTP::collect $tm_collect`, `\[DONE\]`, `string last {"total_tokens"}`, `HTTP::collect 1`,
		// never break the response
		`log local0. "BNK metering failed: $tm_err"`, `when CLIENT_CLOSED`,
	} {
		if !strings.Contains(r, want) {
			t.Errorf("metered iRule missing %q", want)
		}
	}
	for _, bad := range []string{"%!", "@"} {
		if strings.Contains(r, bad) {
			t.Errorf("unrendered placeholder %q in:\n%s", bad, r)
		}
	}
	// A sideband connect in a response event wedged TMM on BNK 2.4: connect only in HTTP_REQUEST.
	if i := strings.Index(r, "when HTTP_RESPONSE"); strings.Contains(r[i:], "connect ") {
		t.Error("connect must only appear in HTTP_REQUEST")
	}
	plain := strings.NewReplacer(`\{`, "", `\}`, "").Replace(r)
	if strings.Count(plain, "{") != strings.Count(plain, "}") {
		t.Error("unbalanced braces")
	}
}

func TestIRuleRefusesTclInjection(t *testing.T) {
	for _, tc := range []struct{ project, dept string }{
		{`a"; HTTP::respond 200 content x; #`, "acme"},
		{"tenant-a", `acme[exec rm]`},
		{"tenant-a", "$dept"},
		{"Tenant-A", "acme"},
		{"", "acme"},
		{"-lead", "acme"},
		{"tenant a", "acme"},
		{"tenant-a", "ac}me"},
	} {
		if _, err := TenantIRule(tc.project, tc.dept, 10, "10.1.10.10:5514", testToken); err == nil {
			t.Errorf("project=%q dept=%q must be refused", tc.project, tc.dept)
		}
	}
	if _, err := TenantIRule("tenant-a", "", 10, "", ""); err != nil {
		t.Errorf("a project without a department is fine: %v", err)
	}
	for _, sink := range []string{"meter.svc:5514", "10.1.10.10", `10.1.10.10:5514"]`} {
		if _, err := TenantIRule("tenant-a", "acme", 10, sink, testToken); err == nil {
			t.Errorf("sink %q must be refused", sink)
		}
	}
	for _, tok := range []string{"", "short", `token-with-"quote"-0123456789`} {
		if _, err := TenantIRule("tenant-a", "acme", 10, "10.1.10.10:5514", tok); err == nil {
			t.Errorf("token %q must be refused", tok)
		}
	}
	d := desired("tenant-a", "runai-tenant-a", 40)
	d.Tenant.Department = "Bad Dept"
	if _, err := Render(DefaultConfig(), d, "172.16.201.110"); err == nil {
		t.Error("Render must refuse names that are not DNS labels")
	}
}

func TestMeteringAddsFlushRoute(t *testing.T) {
	paths := func(cfg Config) []string {
		var out []string
		for _, o := range mustRender(t, cfg, desired("tenant-a", "runai-tenant-a", 40), "172.16.201.110") {
			if o.GetKind() != "HTTPRoute" {
				continue
			}
			rules, _, _ := unstructured.NestedSlice(o.Object, "spec", "rules")
			for _, m := range rules[0].(map[string]any)["matches"].([]any) {
				v, _, _ := unstructured.NestedString(m.(map[string]any), "path", "value")
				out = append(out, v)
			}
		}
		return out
	}
	if got := paths(DefaultConfig()); len(got) != 1 || got[0] != "/v1" {
		t.Errorf("metering off: matches = %v", got)
	}
	cfg := DefaultConfig()
	cfg.MeteringSink, cfg.MeteringToken = "10.1.10.10:5514", testToken
	if got := paths(cfg); len(got) != 2 || got[1] != "/bnk" {
		t.Errorf("metering on: matches = %v", got)
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

// If the VIP ConfigMap is lost, addresses held by live Gateways must still never be reused.
func TestLostVIPMapDoesNotReuseLiveAddress(t *testing.T) {
	a := newApplier(t, 2)
	ctx := context.Background()
	ipA, _ := a.vipFor(ctx, "tenant-a")
	gw := mustRender(t, a.Config, desired("tenant-a", "runai-tenant-a", 40), ipA.String())[0]
	if err := a.Client.Create(ctx, gw); err != nil {
		t.Fatal(err)
	}
	cm, _ := a.vipMap(ctx)
	if err := a.Client.Delete(ctx, cm); err != nil {
		t.Fatal(err)
	}
	ipB, err := a.vipFor(ctx, "tenant-b")
	if err != nil || ipB == ipA {
		t.Fatalf("tenant-b got %v (err %v); %v is still served by tenant-a's gateway", ipB, err, ipA)
	}
	if again, _ := a.vipFor(ctx, "tenant-a"); again != ipA {
		t.Errorf("tenant-a must keep its live address %v, got %v", ipA, again)
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
	gw := mustRender(t, a.Config, desired("tenant-a", "runai-tenant-a", 40), "172.16.201.110")[0]
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

func TestApplyCopiesPullSecretIntoTenantNamespace(t *testing.T) {
	a := newApplier(t, 1)
	ctx := context.Background()
	src := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "f5-bnk", Name: "far-pull-secret"},
		Type: corev1.SecretTypeDockerConfigJson, Data: map[string][]byte{".dockerconfigjson": []byte("{}")}}
	if err := a.Client.Create(ctx, src); err != nil {
		t.Fatal(err)
	}
	if err := a.copyPullSecret(ctx, "runai-tenant-a"); err != nil {
		t.Fatal(err)
	}
	got := &corev1.Secret{}
	if err := a.Client.Get(ctx, client.ObjectKey{Namespace: "runai-tenant-a", Name: "far-pull-secret"}, got); err != nil {
		t.Fatal(err)
	}
	if got.Type != corev1.SecretTypeDockerConfigJson || string(got.Data[".dockerconfigjson"]) != "{}" {
		t.Errorf("copied secret = %+v", got)
	}
	if err := a.DeletePullSecret(ctx, "runai-tenant-a"); err != nil {
		t.Fatal(err)
	}
	if err := a.Client.Get(ctx, client.ObjectKey{Namespace: "runai-tenant-a", Name: "far-pull-secret"}, &corev1.Secret{}); err == nil {
		t.Error("copied secret should be removed with the tenant")
	}
	for _, bad := range []string{"f5-bnk", "kube-system", "default", "runai"} {
		if err := a.copyPullSecret(ctx, bad); err == nil {
			t.Errorf("must refuse to write the registry secret into %q", bad)
		}
	}
	foreign := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "runai-tenant-b", Name: "far-pull-secret"}}
	if err := a.Client.Create(ctx, foreign); err != nil {
		t.Fatal(err)
	}
	if err := a.copyPullSecret(ctx, "runai-tenant-b"); err == nil {
		t.Error("must not overwrite a secret it does not manage")
	}
	if err := a.DeletePullSecret(ctx, "runai-tenant-b"); err != nil {
		t.Fatal(err)
	}
	if err := a.Client.Get(ctx, client.ObjectKey{Namespace: "runai-tenant-b", Name: "far-pull-secret"}, &corev1.Secret{}); err != nil {
		t.Error("must never delete a secret it does not manage")
	}
	a.Config.PullSecret = ""
	if err := a.copyPullSecret(ctx, "runai-other"); err != nil {
		t.Errorf("empty PullSecret must be a no-op, got %v", err)
	}
}

func TestDynamoTenantUsesDynamoPicker(t *testing.T) {
	d := desired("dynamo", "runai-dynamo", 20)
	d.Tenant.Engine, d.Tenant.DynamoGraph = tenant.EngineDynamo, "mocker"
	objs, err := Render(DefaultConfig(), d, "172.16.201.112")
	if err != nil {
		t.Fatal(err)
	}
	byKind := map[string]*unstructured.Unstructured{}
	for _, o := range objs {
		byKind[o.GetKind()] = o
	}
	epp := byKind["F5EPP"]
	engine, _, _ := unstructured.NestedString(epp.Object, "spec", "engine")
	nats, _, _ := unstructured.NestedString(epp.Object, "spec", "natsURL")
	port, _, _ := unstructured.NestedInt64(epp.Object, "spec", "server", "modelServerMetricsPort")
	tok, _, _ := unstructured.NestedString(epp.Object, "spec", "tokenizer", "name")
	if engine != "dynamo" || nats == "" || port != 9090 || tok != "Qwen/Qwen3-0.6B" {
		t.Errorf("F5EPP engine=%q natsURL=%q metricsPort=%d tokenizer=%q", engine, nats, port, tok)
	}
	plugins, _, _ := unstructured.NestedSlice(epp.Object, "spec", "eppConfig", "config", "plugins")
	topic, _, _ := unstructured.NestedString(plugins[0].(map[string]any), "parameters", "kv_events_topic")
	if topic != "component.backend.endpoint.generate.kv-events" {
		t.Errorf("kv_events_topic = %q", topic)
	}
	sel, _, _ := unstructured.NestedStringMap(byKind["InferencePool"].Object, "spec", "selector", "matchLabels")
	if len(sel) != 2 || sel["nvidia.com/dynamo-graph-deployment-name"] != "mocker" || sel["nvidia.com/dynamo-component-type"] != "decode" {
		t.Errorf("pool selector = %v, want the mocker graph's decode workers only", sel)
	}
}

func TestVLLMTenantKeepsVLLMPicker(t *testing.T) {
	d := desired("tenant-a", "runai-tenant-a", 40)
	d.Tenant.Engine = tenant.EngineVLLM
	objs, err := Render(DefaultConfig(), d, "172.16.201.110")
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range objs {
		switch o.GetKind() {
		case "F5EPP":
			if e, _, _ := unstructured.NestedString(o.Object, "spec", "engine"); e != "vllm" {
				t.Errorf("engine = %q", e)
			}
			if _, found, _ := unstructured.NestedString(o.Object, "spec", "natsURL"); found {
				t.Error("a vLLM picker must not get a natsURL")
			}
		case "InferencePool":
			sel, _, _ := unstructured.NestedStringMap(o.Object, "spec", "selector", "matchLabels")
			if sel["app.kubernetes.io/part-of"] != "model-server" || len(sel) != 1 {
				t.Errorf("pool selector = %v", sel)
			}
		}
	}
}

func TestDynamoWithoutNATSFails(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DynamoNATS = ""
	d := desired("dynamo", "runai-dynamo", 20)
	d.Tenant.Engine, d.Tenant.DynamoGraph = tenant.EngineDynamo, "mocker"
	if _, err := Render(cfg, d, "172.16.201.112"); err == nil {
		t.Error("rendering a Dynamo tenant without a NATS URL must fail")
	}
}
