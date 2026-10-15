package tenantmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const testToken = "test-token-0123456789abcdef"

func gateway(name, dept, statusVIP, specVIP, programmed string, managed bool) *unstructured.Unstructured {
	gw := &unstructured.Unstructured{}
	gw.SetGroupVersionKind(gatewayGVK)
	gw.SetNamespace("f5-bnk")
	gw.SetName(name)
	labels := map[string]string{labelDepartment: dept}
	if managed {
		labels[labelManagedBy] = managedBy
	}
	gw.SetLabels(labels)
	gw.Object["spec"] = map[string]any{
		"gatewayClassName": "f5-gateway-class",
		"addresses":        []any{map[string]any{"type": "IPAddress", "value": specVIP}},
		"listeners":        []any{map[string]any{"name": "http", "protocol": "HTTP", "port": int64(8000)}},
	}
	status := map[string]any{}
	if statusVIP != "" {
		status["addresses"] = []any{map[string]any{"type": "IPAddress", "value": statusVIP}}
	}
	if programmed != "" {
		status["conditions"] = []any{
			map[string]any{"type": "Accepted", "status": "True", "reason": "Accepted"},
			map[string]any{"type": "Programmed", "status": programmed, "reason": "Programmed"},
		}
	}
	gw.Object["status"] = status
	return gw
}

func irule(project string, limit int) *unstructured.Unstructured {
	ir := &unstructured.Unstructured{}
	ir.SetGroupVersionKind(iruleGVK)
	ir.SetNamespace("f5-bnk")
	ir.SetName(project + "-limit")
	ir.Object["spec"] = map[string]any{"iRule": "when HTTP_REQUEST {\n  if { $rl_n > " + itoa(limit) + " } {\n    HTTP::respond 429\n  }\n}\n"}
	return ir
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func fixture(t *testing.T) *Tools {
	t.Helper()
	ledger := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "bnk-runai-tenancy", Name: ledgerName},
		Data:       map[string]string{"project.team-a": "1234", "department.research": "5000", "project.team-b": "garbage"},
	}
	objs := []client.Object{
		gateway("team-a", "research", "172.16.201.110", "172.16.201.110", "True", true),
		irule("team-a", 40),
		gateway("team-b", "research", "", "172.16.201.111", "", true),
		gateway("not-ours", "x", "172.16.201.200", "172.16.201.200", "True", false),
		ledger,
	}
	c := fake.NewClientBuilder().WithObjects(objs...).Build()
	return &Tools{Reader: c, Config: Config{BNKNamespace: "f5-bnk", LedgerNamespace: "bnk-runai-tenancy"}}
}

func TestFrontDoorReady(t *testing.T) {
	fd, err := fixture(t).FrontDoor(context.Background(), "team-a")
	if err != nil {
		t.Fatal(err)
	}
	if fd.VIP != "172.16.201.110" || fd.VIPSource != "status" || fd.Port != 8000 || !fd.Programmed || !fd.Ready {
		t.Errorf("front door = %+v", fd)
	}
	if fd.Endpoint != "http://172.16.201.110:8000/v1" || fd.RateLimitRPS != 40 || fd.Department != "research" {
		t.Errorf("front door = %+v", fd)
	}
	if fd.TenantHeader != "X-BNK-Tenant: team-a" || fd.Gateway != "f5-bnk/team-a" {
		t.Errorf("front door = %+v", fd)
	}
}

func TestFrontDoorNotYetProgrammed(t *testing.T) {
	fd, err := fixture(t).FrontDoor(context.Background(), "team-b")
	if err != nil {
		t.Fatal(err)
	}
	if fd.Ready || fd.Programmed || fd.VIPSource != "spec" || fd.VIP != "172.16.201.111" || fd.RateLimitRPS != 0 || fd.ProgrammedState != "Unknown" {
		t.Errorf("front door = %+v", fd)
	}
}

func TestFrontDoorRefusesUnmanagedAndMissing(t *testing.T) {
	tools := fixture(t)
	for _, p := range []string{"not-ours", "nope"} {
		if _, err := tools.FrontDoor(context.Background(), p); err == nil || !strings.Contains(err.Error(), "no BNK tenant") {
			t.Errorf("%s: err = %v", p, err)
		}
	}
}

func TestProjectNameValidation(t *testing.T) {
	tools := fixture(t)
	for _, p := range []string{"", "Team-A", "-a", "a-", "a_b", "a/b", "../x", strings.Repeat("a", 64)} {
		if _, err := tools.FrontDoor(context.Background(), p); err == nil {
			t.Errorf("%q accepted", p)
		}
		if _, err := tools.Usage(context.Background(), p); err == nil {
			t.Errorf("%q accepted", p)
		}
	}
	if err := validProject(strings.Repeat("a", 63)); err != nil {
		t.Error(err)
	}
}

func TestUsage(t *testing.T) {
	tools := fixture(t)
	u, err := tools.Usage(context.Background(), "team-a")
	if err != nil {
		t.Fatal(err)
	}
	if !u.Metered || u.ProjectTokens != 1234 || u.DepartmentTokens == nil || *u.DepartmentTokens != 5000 || u.Department != "research" {
		t.Errorf("usage = %+v", u)
	}
	u, err = tools.Usage(context.Background(), "team-b")
	if err != nil || !u.Metered || u.ProjectTokens != 0 {
		t.Errorf("unreadable total must read as 0: %+v %v", u, err)
	}
}

func TestList(t *testing.T) {
	l, err := fixture(t).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Tenants) != 2 || l.Tenants[0].Project != "team-a" || l.Tenants[1].Project != "team-b" || !l.Tenants[0].Programmed {
		t.Errorf("tenants = %+v", l.Tenants)
	}
}

// --- over the wire: JSON-RPC on the stateless Streamable HTTP handler ---

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := NewServer(fixture(t), "test")
	ts := httptest.NewServer(Handler(srv, []byte(testToken), slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(ts.Close)
	return ts
}

func post(t *testing.T, url, token, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url+"/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func rpc(t *testing.T, resp *http.Response) rpcResponse {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type %q, want application/json", ct)
	}
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.Fatalf("stateless server issued session id %q", sid)
	}
	var r rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		t.Fatal(err)
	}
	if r.Error != nil {
		t.Fatalf("rpc error %d: %s", r.Error.Code, r.Error.Message)
	}
	return r
}

func TestAuth(t *testing.T) {
	ts := newTestServer(t)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	for _, tok := range []string{"", "wrong", testToken + "x", testToken[:len(testToken)-1]} {
		resp := post(t, ts.URL, tok, body)
		if resp.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Bearer") {
			t.Errorf("token %q: status %d", tok, resp.StatusCode)
		}
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(body))
	req.Header.Set("Authorization", "Basic "+testToken)
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("basic scheme: status %d", resp.StatusCode)
	}
	h, err := http.Get(ts.URL + "/healthz")
	if err != nil || h.StatusCode != http.StatusOK {
		t.Errorf("healthz must not need auth: %v %v", h, err)
	}
	h.Body.Close()
}

func TestStatelessToolsListAndCall(t *testing.T) {
	ts := newTestServer(t)

	// initialize is accepted but establishes nothing: no session id comes back.
	rpc(t, post(t, ts.URL, testToken, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`))

	// tools/list straight away, with no session id: every request stands alone.
	r := rpc(t, post(t, ts.URL, testToken, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	var list struct {
		Tools []struct {
			Name        string `json:"name"`
			Annotations struct {
				ReadOnly bool `json:"readOnlyHint"`
			} `json:"annotations"`
			InputSchema struct {
				Required []string `json:"required"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(r.Result, &list); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range list.Tools {
		names[tl.Name] = true
		if !tl.Annotations.ReadOnly {
			t.Errorf("%s is not marked read-only", tl.Name)
		}
		if tl.Name != "list_tenants" && (len(tl.InputSchema.Required) != 1 || tl.InputSchema.Required[0] != "project") {
			t.Errorf("%s required = %v", tl.Name, tl.InputSchema.Required)
		}
	}
	for _, want := range []string{"get_tenant_front_door", "get_tenant_usage", "list_tenants"} {
		if !names[want] {
			t.Errorf("missing tool %s (have %v)", want, names)
		}
	}

	r = rpc(t, post(t, ts.URL, testToken, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_tenant_front_door","arguments":{"project":"team-a"}}}`))
	var call struct {
		IsError           bool      `json:"isError"`
		StructuredContent FrontDoor `json:"structuredContent"`
	}
	if err := json.Unmarshal(r.Result, &call); err != nil {
		t.Fatal(err)
	}
	if call.IsError || call.StructuredContent.VIP != "172.16.201.110" || !call.StructuredContent.Ready {
		t.Errorf("call = %s", r.Result)
	}

	// A tool error is a tool result with isError, not a transport failure.
	r = rpc(t, post(t, ts.URL, testToken, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get_tenant_front_door","arguments":{"project":"Bad_Name"}}}`))
	if !bytes.Contains(r.Result, []byte(`"isError":true`)) {
		t.Errorf("invalid project must be a tool error: %s", r.Result)
	}

	// Stateless: there is no server-to-client stream to open and no session to delete.
	for _, m := range []string{http.MethodGet, http.MethodDelete} {
		req, _ := http.NewRequest(m, ts.URL+"/mcp", nil)
		req.Header.Set("Authorization", "Bearer "+testToken)
		req.Header.Set("Accept", "text/event-stream")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s: status %d, want 405", m, resp.StatusCode)
		}
	}
}

// bearerTransport adds the token to every request, as a remote MCP client configured with a header does.
type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func TestSDKClient(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()
	c := mcp.NewClient(&mcp.Implementation{Name: "test-agent", Version: "1"}, nil)
	sess, err := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearerTransport{testToken}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "get_tenant_usage", Arguments: map[string]any{"project": "team-a"}})
	if err != nil || res.IsError {
		t.Fatalf("call: %v %+v", err, res)
	}
	b, _ := json.Marshal(res.StructuredContent)
	var u Usage
	_ = json.Unmarshal(b, &u)
	if u.ProjectTokens != 1234 {
		t.Errorf("usage = %s", b)
	}
	res, err = sess.CallTool(ctx, &mcp.CallToolParams{Name: "list_tenants", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("list: %v %+v", err, res)
	}
}
