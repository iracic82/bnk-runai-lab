package meter

import (
	"context"
	"errors"
	"fmt"
	"io"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	kaiv1alpha1 "github.com/kai-scheduler/kai-resource-management-api/kai/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const good = "project=tenant-a dept=acme model=meta-llama/Llama-3.1-8B-Instruct prompt=12 completion=30 total=42 code=200"

func TestParse(t *testing.T) {
	r, err := Parse(good)
	if err != nil {
		t.Fatal(err)
	}
	want := Record{Project: "tenant-a", Department: "acme", Model: "meta-llama/Llama-3.1-8B-Instruct",
		Prompt: 12, Completion: 30, Total: 42, Code: 200, Count: 1}
	if r != want {
		t.Errorf("got %+v, want %+v", r, want)
	}
	r, err = Parse("project=tenant-a dept= model= prompt=0 completion=0 total=0 code=429 n=17\r")
	if err != nil || r.Count != 17 || r.Code != 429 || r.Department != "" || r.Model != "" {
		t.Errorf("folded 429 line: %+v %v", r, err)
	}
	if r, _ := Parse("total=0 code=200 project=p dept=d model=m prompt=3 completion=4"); r.Total != 7 {
		t.Errorf("missing total falls back to prompt+completion, got %d", r.Total)
	}
}

func TestParseRejects(t *testing.T) {
	for _, line := range []string{
		"",
		"garbage",
		"project=tenant-a dept=acme model=m prompt=1 completion=1 total=2",                    // no code
		"project=tenant-a dept=acme model=m prompt=1 completion=1 total=2 code=200 extra=1",   // unknown key
		"project=tenant-a project=b dept=acme model=m prompt=1 completion=1 total=2 code=200", // duplicate
		"project=Tenant-A dept=acme model=m prompt=1 completion=1 total=2 code=200",
		"project=tenant-a dept=ac$me model=m prompt=1 completion=1 total=2 code=200",
		"project=tenant-a dept=acme model=m\"x prompt=1 completion=1 total=2 code=200",
		"project=tenant-a dept=acme model=m prompt=-1 completion=1 total=2 code=200",
		"project=tenant-a dept=acme model=m prompt=x completion=1 total=2 code=200",
		"project=tenant-a dept=acme model=m prompt=1 completion=1 total=2 code=99",
		"project=tenant-a dept=acme model=m prompt=1 completion=1 total=2 code=600",
		"project=tenant-a dept=acme model=m prompt=1 completion=1 total=2 code=200 n=0",
		"project=tenant-a dept=acme model=m prompt=99999999999 completion=1 total=2 code=200",
		"project=tenant-a dept=acme model=" + strings.Repeat("a", 129) + " prompt=1 completion=1 total=2 code=200",
		good + strings.Repeat(" ", MaxLine),
	} {
		if _, err := Parse(line); !errors.Is(err, ErrMalformed) {
			t.Errorf("%q: want ErrMalformed, got %v", line, err)
		}
	}
}

func newMetrics(t *testing.T) *Metrics {
	t.Helper()
	return NewMetrics(prometheus.NewRegistry())
}

func TestIngestCountsAndSkips(t *testing.T) {
	m := newMetrics(t)
	s := &Server{Metrics: m}
	in := good + "\n" +
		"not a record\n" +
		strings.Repeat("x", 3*MaxLine) + "\n" +
		"project=tenant-a dept=acme model=meta-llama/Llama-3.1-8B-Instruct prompt=8 completion=0 total=8 code=200\n" +
		"project=tenant-a dept=acme model= prompt=0 completion=0 total=0 code=429 n=5\n" +
		"project=tenant-b dept=acme model=m prompt=1 completion=2 total=3 code=500" // no trailing newline
	n, err := s.Ingest(strings.NewReader(in))
	if err != nil || n != 4 {
		t.Fatalf("counted %d, err %v", n, err)
	}
	model := "meta-llama/Llama-3.1-8B-Instruct"
	if v := testutil.ToFloat64(m.Tokens.WithLabelValues("tenant-a", "acme", model, "prompt")); v != 20 {
		t.Errorf("prompt tokens = %v", v)
	}
	if v := testutil.ToFloat64(m.Tokens.WithLabelValues("tenant-a", "acme", model, "completion")); v != 30 {
		t.Errorf("completion tokens = %v", v)
	}
	if v := testutil.ToFloat64(m.Requests.WithLabelValues("tenant-a", "acme", model, "200")); v != 2 {
		t.Errorf("200s = %v", v)
	}
	if v := testutil.ToFloat64(m.Requests.WithLabelValues("tenant-a", "acme", NoModel, "429")); v != 5 {
		t.Errorf("folded 429s = %v", v)
	}
	if v := testutil.ToFloat64(m.Malformed); v != 2 {
		t.Errorf("malformed = %v", v)
	}
	if p := m.TakePending(); p["tenant-a"] != 50 || p["tenant-b"] != 3 {
		t.Errorf("pending = %v", p)
	}
	if p := m.TakePending(); len(p) != 0 {
		t.Errorf("pending must be cleared, got %v", p)
	}
}

func TestModelCardinalityCapped(t *testing.T) {
	m := newMetrics(t)
	for i := 0; i < MaxModels+10; i++ {
		m.Observe(Record{Project: "p", Department: "d", Model: fmt.Sprintf("model-%d", i), Prompt: 1, Total: 1, Code: 200, Count: 1})
	}
	if v := testutil.ToFloat64(m.Requests.WithLabelValues("p", "d", Other, "200")); v != 10 {
		t.Errorf("overflow models folded into %q: %v", Other, v)
	}
	if n := testutil.CollectAndCount(m.Requests); n != MaxModels+1 {
		t.Errorf("series = %d, want %d", n, MaxModels+1)
	}
}

var loopback = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}

const tok = "meter-token-0123456789abcdef"

func TestServerOverTCP(t *testing.T) {
	m := newMetrics(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- (&Server{Metrics: m, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
			AllowedPeers: loopback, Token: tok}).Serve(ctx, l)
	}()

	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintf(c, "auth %s\n%s\n%s\n", tok, good, good)
	_ = c.Close()
	deadline := time.Now().Add(2 * time.Second)
	for testutil.ToFloat64(m.Requests.WithLabelValues("tenant-a", "acme", "meta-llama/Llama-3.1-8B-Instruct", "200")) != 2 {
		if time.Now().After(deadline) {
			t.Fatal("records never counted")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("serve: %v", err)
	}
}

func scheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	sc := runtime.NewScheme()
	if err := kaiv1alpha1.AddToScheme(sc); err != nil {
		t.Fatal(err)
	}
	return sc
}

func project(name, parent string) *kaiv1alpha1.Project {
	return &kaiv1alpha1.Project{ObjectMeta: metav1.ObjectMeta{Name: name}, Spec: kaiv1alpha1.ProjectSpec{Parent: parent}}
}

func TestWritebackLedgerAndRollup(t *testing.T) {
	sc := scheme(t)
	_ = corev1.AddToScheme(sc)
	c := fake.NewClientBuilder().WithScheme(sc).WithObjects(
		project("tenant-a", "acme"), project("tenant-b", "acme"), project("tenant-c", "other-dept"),
		&kaiv1alpha1.Department{ObjectMeta: metav1.ObjectMeta{Name: "acme"}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "bnk-runai-tenancy", Name: LedgerName},
			Data: map[string]string{"project.tenant-a": "100", "project.tenant-c": "7"}},
	).Build()
	before := &kaiv1alpha1.Project{}
	ctx := context.Background()
	_ = c.Get(ctx, client.ObjectKey{Name: "tenant-a"}, before)
	m := newMetrics(t)
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	rec := record.NewFakeRecorder(16)
	w := &Writeback{Client: c, Namespace: "bnk-runai-tenancy", Recorder: rec, Metrics: m,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now }}

	m.Observe(Record{Project: "tenant-a", Department: "acme", Model: "m", Prompt: 10, Completion: 32, Total: 42, Code: 200, Count: 1})
	m.Observe(Record{Project: "tenant-b", Department: "acme", Model: "m", Prompt: 5, Completion: 3, Total: 8, Code: 200, Count: 1})
	m.Observe(Record{Project: "ghost", Department: "acme", Model: "m", Prompt: 1, Total: 1, Code: 200, Count: 1})
	w.Once(ctx)

	ledger := func() map[string]string {
		cm := &corev1.ConfigMap{}
		if err := c.Get(ctx, client.ObjectKey{Namespace: "bnk-runai-tenancy", Name: LedgerName}, cm); err != nil {
			t.Fatal(err)
		}
		return cm.Data
	}
	l := ledger()
	if l["project.tenant-a"] != "142" || l["project.tenant-b"] != "8" || l["department.acme"] != "150" ||
		l["department.other-dept"] != "7" || l["updated"] != "2026-10-15T12:00:00Z" {
		t.Errorf("ledger = %v", l)
	}
	if _, ok := l["project.ghost"]; ok {
		t.Error("usage for an unknown project must not be stored")
	}
	if v := testutil.ToFloat64(m.Departments.WithLabelValues("acme")); v != 150 {
		t.Errorf("department gauge = %v", v)
	}
	if p := m.TakePending(); len(p) != 0 {
		t.Errorf("unknown project must be dropped, not retried forever: %v", p)
	}
	if len(rec.Events) == 0 {
		t.Error("expected TokensMetered events on the Run:ai project and department")
	}
	// The Run:ai objects themselves are never written (Run:ai's webhook would refuse it).
	after := &kaiv1alpha1.Project{}
	_ = c.Get(ctx, client.ObjectKey{Name: "tenant-a"}, after)
	if after.ResourceVersion != before.ResourceVersion {
		t.Error("the Run:ai project must not be modified")
	}

	// A second pass adds only the new usage on top of the stored total.
	m.Observe(Record{Project: "tenant-a", Department: "acme", Model: "m", Total: 8, Code: 200, Count: 1})
	w.Once(ctx)
	if l := ledger(); l["project.tenant-a"] != "150" || l["department.acme"] != "158" {
		t.Errorf("after second pass = %v", l)
	}
}

func TestTokensValue(t *testing.T) {
	for in, want := range map[string]int64{"": 0, "12": 12, " 7 ": 7, "-3": 0, "x": 0} {
		if got := Tokens(in); got != want {
			t.Errorf("Tokens(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestFlusherAsksEveryTenantGateway(t *testing.T) {
	var hits, bad int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bnk/flush" || r.Header.Get("X-BNK-Flush") != "flush-token-0123456789abcdef" {
			bad++
		}
		hits++
		// First call reports a full batch, so the flusher asks again at once.
		if hits == 1 {
			w.Header().Set("X-BNK-Flushed", fmt.Sprint(flushBatch))
		} else {
			w.Header().Set("X-BNK-Flushed", "3")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))

	gw := &unstructured.Unstructured{}
	gw.SetAPIVersion("gateway.networking.k8s.io/v1")
	gw.SetKind("Gateway")
	gw.SetNamespace("f5-bnk")
	gw.SetName("tenant-a")
	gw.SetLabels(map[string]string{"app.kubernetes.io/managed-by": "bnk-runai-tenancy"})
	var p int64
	_, _ = fmt.Sscan(port, &p)
	gw.Object["spec"] = map[string]any{
		"addresses": []any{map[string]any{"type": "IPAddress", "value": host}},
		"listeners": []any{map[string]any{"name": "http", "port": p}},
	}
	foreign := gw.DeepCopy()
	foreign.SetName("not-ours")
	foreign.SetLabels(nil)

	c := fake.NewClientBuilder().WithObjects(gw, foreign).Build()
	f := &Flusher{Client: c, Namespace: "f5-bnk", Token: "flush-token-0123456789abcdef",
		HTTP: srv.Client(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	targets, err := f.Targets(context.Background())
	if err != nil || len(targets) != 1 {
		t.Fatalf("targets = %v, err %v", targets, err)
	}
	f.Once(context.Background())
	if hits != 2 || bad != 0 {
		t.Errorf("hits=%d bad=%d, want 2 good flushes (full batch then partial)", hits, bad)
	}
}

func TestIngestRequiresAuthLine(t *testing.T) {
	for _, preamble := range []string{"", "auth wrong-token-0123456789\n", "auth\n", "AUTH " + tok + "\n", good + "\n"} {
		m := newMetrics(t)
		s := &Server{Metrics: m, Token: tok}
		n, err := s.Ingest(strings.NewReader(preamble + good + "\n"))
		if n != 0 || !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("preamble %q: counted %d, err %v; want rejected", preamble, n, err)
		}
		if v := testutil.ToFloat64(m.Rejected); v != 1 {
			t.Errorf("preamble %q: rejected = %v", preamble, v)
		}
		if p := m.TakePending(); len(p) != 0 {
			t.Errorf("preamble %q: forged usage was counted: %v", preamble, p)
		}
	}
	m := newMetrics(t)
	if n, err := (&Server{Metrics: m, Token: tok}).Ingest(strings.NewReader("auth " + tok + "\r\n" + good + "\n")); n != 1 || err != nil {
		t.Errorf("good preamble: counted %d, err %v", n, err)
	}
	// Without a token the preamble is not required (peer allowlist alone applies).
	if n, _ := (&Server{Metrics: newMetrics(t)}).Ingest(strings.NewReader(good + "\n")); n != 1 {
		t.Errorf("no token configured: counted %d", n)
	}
}

func TestPeerAllowlist(t *testing.T) {
	peers, err := ParsePeers(" 10.1.10.201, 10.244.0.0/16 ,")
	if err != nil || len(peers) != 2 {
		t.Fatalf("peers=%v err=%v", peers, err)
	}
	s := &Server{AllowedPeers: peers}
	for ip, want := range map[string]bool{
		"10.1.10.201": true, "10.1.10.202": false, "10.244.3.7": true, "::ffff:10.1.10.201": true, "192.168.1.1": false,
	} {
		if got := s.Allowed(&net.TCPAddr{IP: net.ParseIP(ip), Port: 4000}); got != want {
			t.Errorf("Allowed(%s) = %v, want %v", ip, got, want)
		}
	}
	if (&Server{}).Allowed(&net.TCPAddr{IP: net.ParseIP("127.0.0.1")}) {
		t.Error("an empty allowlist must reject every peer")
	}
	for _, bad := range []string{"10.1.10", "10.0.0.0/33", "host.example"} {
		if _, err := ParsePeers(bad); err == nil {
			t.Errorf("ParsePeers(%q) must fail", bad)
		}
	}
}

func TestServerDropsDisallowedPeer(t *testing.T) {
	m := newMetrics(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = (&Server{Metrics: m, AllowedPeers: []netip.Prefix{netip.MustParsePrefix("10.1.10.201/32")}}).Serve(ctx, l)
	}()
	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintf(c, "%s\n", good)
	_ = c.Close()
	deadline := time.Now().Add(2 * time.Second)
	for testutil.ToFloat64(m.Rejected) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("connection from a disallowed peer was not rejected")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if p := m.TakePending(); len(p) != 0 {
		t.Errorf("usage from a disallowed peer was counted: %v", p)
	}
}

func TestNoModelRecordCreatesNoTokenSeries(t *testing.T) {
	m := newMetrics(t)
	m.Observe(Record{Project: "tenant-b", Department: "default", Code: 429, Count: 7})
	if n := testutil.CollectAndCount(m.Tokens); n != 0 {
		t.Errorf("a 429 without a model created %d token series", n)
	}
	if v := testutil.ToFloat64(m.Requests.WithLabelValues("tenant-b", "default", NoModel, "429")); v != 7 {
		t.Errorf("429s counted = %v, want 7", v)
	}
}
