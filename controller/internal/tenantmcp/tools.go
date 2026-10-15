// Package tenantmcp is a small, read-only, stateless MCP server that answers "what did BNK do with my
// Run:ai project?": the tenant's front door (VIP, Gateway status, rate limit) and its metered tokens.
//
// Every tool call is answered from the Kubernetes API at request time (no cache, no session, nothing kept
// between requests), so any number of replicas can sit behind one Service.
package tenantmcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Names shared with the tenancy controller (internal/bnk) and the meter (internal/meter). They are
// repeated here, not imported, so this binary stays small and read-only by construction.
const (
	labelManagedBy  = "app.kubernetes.io/managed-by"
	managedBy       = "bnk-runai-tenancy"
	labelDepartment = "tenancy.f5.com/department"
	tenantHeader    = "X-BNK-Tenant"
	ledgerName      = "bnk-runai-usage"
)

var (
	gatewayGVK = schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "Gateway"}
	iruleGVK   = schema.GroupVersionKind{Group: "k8s.f5net.com", Version: "v1", Kind: "F5BigCneIrule"}

	// projectName is a Run:ai project name as the controller accepts it (a DNS label).
	projectName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	// rateLimit is the limit the controller renders into the tenant iRule: `if { $rl_n > N } {`.
	rateLimit = regexp.MustCompile(`rl_n > ([0-9]+)`)
)

// Config says where the BNK tenant objects and the token ledger live.
type Config struct {
	BNKNamespace    string // f5-bnk: tenant Gateways and iRules
	LedgerNamespace string // bnk-runai-tenancy: the bnk-runai-usage ConfigMap
}

// Tools answers tool calls from a read-only Kubernetes client.
type Tools struct {
	Reader client.Reader
	Config Config
}

// ProjectInput is the argument of the per-tenant tools.
type ProjectInput struct {
	Project string `json:"project" jsonschema:"Run:ai project name (lowercase letters, digits and '-', at most 63 characters)."`
}

// FrontDoor is a tenant's BNK entry point.
type FrontDoor struct {
	Project         string  `json:"project" jsonschema:"Run:ai project name."`
	Department      string  `json:"department,omitempty" jsonschema:"Run:ai department the project belongs to."`
	Gateway         string  `json:"gateway" jsonschema:"BNK Gateway object (namespace/name)."`
	VIP             string  `json:"vip,omitempty" jsonschema:"Address BNK serves the tenant on; empty until BNK assigns it."`
	Port            int64   `json:"port,omitempty" jsonschema:"Listener port on the VIP."`
	Endpoint        string  `json:"endpoint,omitempty" jsonschema:"OpenAI-compatible base URL, http://VIP:port/v1."`
	VIPSource       string  `json:"vipSource,omitempty" jsonschema:"status (assigned by BNK) or spec (requested by the controller, not yet confirmed)."`
	Programmed      bool    `json:"programmed" jsonschema:"True when the Gateway's Programmed condition is True (BNK is serving it)."`
	ProgrammedState string  `json:"programmedState" jsonschema:"Programmed condition status and reason, or Unknown."`
	RateLimitRPS    float64 `json:"rateLimitRps,omitempty" jsonschema:"Requests per second the tenant iRule admits before answering 429."`
	TenantHeader    string  `json:"tenantHeader" jsonschema:"Response header BNK adds to every answer for this tenant."`
	Ready           bool    `json:"ready" jsonschema:"True when the VIP is assigned by BNK and the Gateway is Programmed."`
}

// Usage is a tenant's metered tokens.
type Usage struct {
	Project          string `json:"project" jsonschema:"Run:ai project name."`
	Department       string `json:"department,omitempty" jsonschema:"Run:ai department the project belongs to."`
	ProjectTokens    int64  `json:"projectTokens" jsonschema:"Tokens metered at the BNK edge for this project (prompt + completion)."`
	DepartmentTokens *int64 `json:"departmentTokens,omitempty" jsonschema:"Tokens metered across the department's projects."`
	Ledger           string `json:"ledger" jsonschema:"ConfigMap the totals are read from (namespace/name)."`
	Metered          bool   `json:"metered" jsonschema:"False when the ledger has no entry for the project yet."`
}

// TenantList is every BNK tenant the controller manages.
type TenantList struct {
	Tenants []TenantSummary `json:"tenants" jsonschema:"One entry per Run:ai project with a BNK front door. Always present."`
}

// TenantSummary is one row of list_tenants.
type TenantSummary struct {
	Project    string `json:"project"`
	Department string `json:"department,omitempty"`
	VIP        string `json:"vip,omitempty"`
	Port       int64  `json:"port,omitempty"`
	Programmed bool   `json:"programmed"`
}

// ErrInvalidProject is returned for names that cannot be a Run:ai project.
var ErrInvalidProject = errors.New("project must be lowercase letters, digits and '-', start and end with a letter or digit, at most 63 characters")

func validProject(p string) error {
	if !projectName.MatchString(p) {
		return fmt.Errorf("%w (got %q)", ErrInvalidProject, p)
	}
	return nil
}

// FrontDoor reads the tenant's Gateway and iRule.
func (t *Tools) FrontDoor(ctx context.Context, project string) (FrontDoor, error) {
	if err := validProject(project); err != nil {
		return FrontDoor{}, err
	}
	gw, err := t.gateway(ctx, project)
	if err != nil {
		return FrontDoor{}, err
	}
	fd := FrontDoor{
		Project:      project,
		Department:   gw.GetLabels()[labelDepartment],
		Gateway:      t.Config.BNKNamespace + "/" + gw.GetName(),
		TenantHeader: tenantHeader + ": " + project,
	}
	fd.VIP, fd.VIPSource = address(gw)
	fd.Port = port(gw)
	fd.Programmed, fd.ProgrammedState = programmed(gw)
	if fd.VIP != "" && fd.Port > 0 {
		fd.Endpoint = "http://" + net.JoinHostPort(fd.VIP, strconv.FormatInt(fd.Port, 10)) + "/v1"
	}
	fd.Ready = fd.VIPSource == "status" && fd.Programmed

	ir := &unstructured.Unstructured{}
	ir.SetGroupVersionKind(iruleGVK)
	switch err := t.Reader.Get(ctx, client.ObjectKey{Namespace: t.Config.BNKNamespace, Name: project + "-limit"}, ir); {
	case err == nil:
		rule, _, _ := unstructured.NestedString(ir.Object, "spec", "iRule")
		if m := rateLimit.FindStringSubmatch(rule); m != nil {
			fd.RateLimitRPS, _ = strconv.ParseFloat(m[1], 64)
		}
	case apierrors.IsNotFound(err):
		// The controller writes the Gateway and the iRule together; a missing iRule leaves the rate unknown.
	default:
		return FrontDoor{}, fmt.Errorf("read iRule %s-limit: %w", project, err)
	}
	return fd, nil
}

// Usage reads the token ledger the meter maintains.
func (t *Tools) Usage(ctx context.Context, project string) (Usage, error) {
	if err := validProject(project); err != nil {
		return Usage{}, err
	}
	u := Usage{Project: project, Ledger: t.Config.LedgerNamespace + "/" + ledgerName}
	// The department comes from the tenant's Gateway label; a project without a front door has no tenant.
	gw, err := t.gateway(ctx, project)
	if err != nil {
		return Usage{}, err
	}
	u.Department = gw.GetLabels()[labelDepartment]

	cm := &corev1.ConfigMap{}
	switch err := t.Reader.Get(ctx, client.ObjectKey{Namespace: t.Config.LedgerNamespace, Name: ledgerName}, cm); {
	case apierrors.IsNotFound(err):
		return u, nil // the meter creates the ledger on its first write
	case err != nil:
		return Usage{}, fmt.Errorf("read ledger %s: %w", u.Ledger, err)
	}
	if v, ok := cm.Data["project."+project]; ok {
		u.Metered = true
		u.ProjectTokens = tokens(v)
	}
	if u.Department != "" {
		if v, ok := cm.Data["department."+u.Department]; ok {
			n := tokens(v)
			u.DepartmentTokens = &n
		}
	}
	return u, nil
}

// List returns every Gateway the tenancy controller manages, sorted by project.
func (t *Tools) List(ctx context.Context) (TenantList, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gatewayGVK.GroupVersion().WithKind("GatewayList"))
	if err := t.Reader.List(ctx, list, client.InNamespace(t.Config.BNKNamespace),
		client.MatchingLabels{labelManagedBy: managedBy}); err != nil {
		return TenantList{}, fmt.Errorf("list gateways in %s: %w", t.Config.BNKNamespace, err)
	}
	out := TenantList{Tenants: []TenantSummary{}}
	for i := range list.Items {
		gw := &list.Items[i]
		s := TenantSummary{Project: gw.GetName(), Department: gw.GetLabels()[labelDepartment], Port: port(gw)}
		s.VIP, _ = address(gw)
		s.Programmed, _ = programmed(gw)
		out.Tenants = append(out.Tenants, s)
	}
	sort.Slice(out.Tenants, func(i, j int) bool { return out.Tenants[i].Project < out.Tenants[j].Project })
	return out, nil
}

// gateway returns the tenant's Gateway, but only one the tenancy controller manages.
func (t *Tools) gateway(ctx context.Context, project string) (*unstructured.Unstructured, error) {
	gw := &unstructured.Unstructured{}
	gw.SetGroupVersionKind(gatewayGVK)
	err := t.Reader.Get(ctx, client.ObjectKey{Namespace: t.Config.BNKNamespace, Name: project}, gw)
	if apierrors.IsNotFound(err) || (err == nil && gw.GetLabels()[labelManagedBy] != managedBy) {
		return nil, fmt.Errorf("no BNK tenant for Run:ai project %q yet: the tenancy controller writes Gateway %s/%s once Run:ai reports the project Ready",
			project, t.Config.BNKNamespace, project)
	}
	if err != nil {
		return nil, fmt.Errorf("read gateway %s/%s: %w", t.Config.BNKNamespace, project, err)
	}
	return gw, nil
}

// address prefers the address BNK reports; it falls back to the one the controller requested.
func address(gw *unstructured.Unstructured) (vip, source string) {
	for _, path := range [][]string{{"status", "addresses"}, {"spec", "addresses"}} {
		addrs, _, _ := unstructured.NestedSlice(gw.Object, path...)
		for _, a := range addrs {
			if m, ok := a.(map[string]any); ok {
				if v, _ := m["value"].(string); v != "" {
					return v, path[0]
				}
			}
		}
	}
	return "", ""
}

func port(gw *unstructured.Unstructured) int64 {
	ls, _, _ := unstructured.NestedSlice(gw.Object, "spec", "listeners")
	for _, l := range ls {
		if m, ok := l.(map[string]any); ok {
			if p, ok, _ := unstructured.NestedInt64(m, "port"); ok && p > 0 {
				return p
			}
		}
	}
	return 0
}

func programmed(gw *unstructured.Unstructured) (bool, string) {
	conds, _, _ := unstructured.NestedSlice(gw.Object, "status", "conditions")
	for _, c := range conds {
		m, ok := c.(map[string]any)
		if !ok || m["type"] != "Programmed" {
			continue
		}
		status, _ := m["status"].(string)
		reason, _ := m["reason"].(string)
		state := strings.TrimSpace(status + " " + reason)
		return status == "True", state
	}
	return false, "Unknown"
}

func tokens(v string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// NewServer builds the MCP server. It holds no per-client state, so one instance serves every request.
func NewServer(t *Tools, version string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "bnk-tenancy-mcp", Title: "F5 BNK tenancy for Run:ai", Version: version},
		&mcp.ServerOptions{Instructions: "Read-only view of the F5 BIG-IP Next for Kubernetes (BNK) front door that the " +
			"tenancy controller builds for each NVIDIA Run:ai project. After creating a Run:ai project (Run:ai MCP " +
			"server, manage_org_unit), poll get_tenant_front_door until ready=true, then send OpenAI-compatible " +
			"requests to its endpoint."})
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: ptr(false)}

	mcp.AddTool(s, &mcp.Tool{
		Name:  "get_tenant_front_door",
		Title: "Get a tenant's BNK front door",
		Description: "Return the BNK front door of one Run:ai project: its VIP and OpenAI-compatible endpoint, whether " +
			"BNK has programmed the Gateway, the request-rate limit (20 req/s per deserved GPU unless overridden) and " +
			"the tenant header BNK adds. Poll this after creating a project; ready=true means traffic is being served. " +
			"An error means the tenancy controller has not built the tenant yet.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ProjectInput) (*mcp.CallToolResult, FrontDoor, error) {
		out, err := t.FrontDoor(ctx, in.Project)
		return nil, out, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:  "get_tenant_usage",
		Title: "Get a tenant's metered tokens",
		Description: "Return the tokens BNK metered at the edge for one Run:ai project, and the total for its " +
			"department, from the bnk-runai-usage ledger. Totals are durable and update every ~30s.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ProjectInput) (*mcp.CallToolResult, Usage, error) {
		out, err := t.Usage(ctx, in.Project)
		return nil, out, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_tenants",
		Title:       "List BNK tenants",
		Description: "List every Run:ai project that has a BNK front door, with its VIP, port and Programmed status.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, TenantList, error) {
		out, err := t.List(ctx)
		return nil, out, err
	})
	return s
}

func ptr[T any](v T) *T { return &v }
