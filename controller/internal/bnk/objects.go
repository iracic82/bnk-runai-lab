// Package bnk renders and applies the BNK 2.4 objects for one Run:ai project.
//
// Every kind, apiVersion and field here is copied from manifests that run on BNK 2.4 today
// (iracic82/bnk-ai-kv-lab, see docs/verified-names.md). Nothing is guessed.
package bnk

import (
	"fmt"
	"math"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/f5devcentral/bnk-runai-tenancy/internal/reconcile"
)

// Labels and annotations every BNK object of a tenant carries.
const (
	LabelProject    = "tenancy.f5.com/project"
	LabelDepartment = "tenancy.f5.com/department"
	LabelManagedBy  = "app.kubernetes.io/managed-by"
	ManagedBy       = "bnk-runai-tenancy"
	AnnotSource     = "tenancy.f5.com/source"

	// ModelServerLabel selects a tenant's model pods (set by labctl / the model template).
	ModelServerLabel = "app.kubernetes.io/part-of"
	ModelServerValue = "model-server"

	modelPort = 8000
	eppPort   = 9002
)

// Kinds, as verified on BNK 2.4.
var (
	GatewayGVK = schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "Gateway"}
	RouteGVK   = schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "HTTPRoute"}
	PoolGVK    = schema.GroupVersionKind{Group: "inference.networking.k8s.io", Version: "v1", Kind: "InferencePool"}
	EPPGVK     = schema.GroupVersionKind{Group: "inference.k8s.f5.com", Version: "v1alpha1", Kind: "F5EPP"}
	IRuleGVK   = schema.GroupVersionKind{Group: "k8s.f5net.com", Version: "v1", Kind: "F5BigCneIrule"}
	PolicyGVK  = schema.GroupVersionKind{Group: "gateway.k8s.f5.com", Version: "v1alpha1", Kind: "NetPolicy"}

	// OwnedKinds is everything Delete has to clean up, in reverse dependency order.
	OwnedKinds = []schema.GroupVersionKind{PolicyGVK, IRuleGVK, RouteGVK, EPPGVK, PoolGVK, GatewayGVK}
)

// Config is the cluster-wide BNK setup the tenants share.
type Config struct {
	// Namespace where BNK Gateways, iRules and NetPolicies live (f5-bnk).
	Namespace string
	// GatewayClass bound to BNK's controller (controllerName f5.com/f5-bnk-f5-cne-controller).
	GatewayClass string
	// Tokenizer the Endpoint Picker uses for prefix-cache scoring; must match the model servers.
	Tokenizer string
	// Port each tenant's Gateway listens on.
	ListenPort int64
}

// DefaultConfig matches the lab cluster.
func DefaultConfig() Config {
	return Config{Namespace: "f5-bnk", GatewayClass: "f5-gateway-class",
		Tokenizer: "NousResearch/Meta-Llama-3.1-8B-Instruct", ListenPort: 8000}
}

// Names of a tenant's objects. The Gateway, iRule and NetPolicy live in the BNK namespace, so they
// carry the project name; the pool, picker and route live in the tenant's own namespace.
func GatewayName(project string) string { return "tenant-" + project }
func PoolName() string                  { return "model-pool" }
func EPPName() string                   { return "model-pool-epp" }
func RouteName() string                 { return "tenant-route" }
func IRuleName(project string) string   { return "tenant-" + project + "-limit" }
func PolicyName(project string) string  { return "tenant-" + project + "-policy" }

// Render returns the BNK objects for one Run:ai project, served at vip.
func Render(cfg Config, d reconcile.Desired, vip string) []*unstructured.Unstructured {
	t := d.Tenant
	gw := obj(GatewayGVK, cfg.Namespace, GatewayName(t.Project), d)
	gw.Object["spec"] = map[string]any{
		"gatewayClassName": cfg.GatewayClass,
		"addresses":        []any{map[string]any{"type": "IPAddress", "value": vip}},
		"listeners": []any{map[string]any{
			"name": "http", "protocol": "HTTP", "port": cfg.ListenPort,
			// Only the tenant's own namespace may attach routes, so one Run:ai project can never
			// publish behind another's front door. kubernetes.io/metadata.name is set on every
			// namespace by the API server.
			"allowedRoutes": map[string]any{"namespaces": map[string]any{
				"from":     "Selector",
				"selector": map[string]any{"matchLabels": map[string]any{"kubernetes.io/metadata.name": t.Namespace}},
			}},
		}},
	}

	pool := obj(PoolGVK, t.Namespace, PoolName(), d)
	pool.Object["spec"] = map[string]any{
		"selector":    map[string]any{"matchLabels": map[string]any{ModelServerLabel: ModelServerValue}},
		"targetPorts": []any{map[string]any{"number": int64(modelPort)}},
		"endpointPickerRef": map[string]any{
			"name": EPPName(), "port": map[string]any{"number": int64(eppPort)},
			// Abort on picker errors; FailOpen would silently fall back to round-robin.
			"failureMode": "FailClose",
		},
	}

	epp := obj(EPPGVK, t.Namespace, EPPName(), d)
	epp.Object["spec"] = map[string]any{
		"poolRef": map[string]any{"name": PoolName()},
		"engine":  "vllm",
		"server": map[string]any{
			"modelServerMetricsPort": int64(modelPort), "secureServing": false, "metricsEndpointAuth": false,
		},
		"tokenizer": map[string]any{"name": cfg.Tokenizer},
		"eppConfig": map[string]any{"config": map[string]any{"schedulingProfiles": []any{map[string]any{
			"name": "default",
			"plugins": []any{
				map[string]any{"pluginRef": "prefix-cache", "weight": 0.20},
				map[string]any{"pluginRef": "queue-scorer", "weight": 0.45},
			},
		}}}},
	}

	route := obj(RouteGVK, t.Namespace, RouteName(), d)
	route.Object["spec"] = map[string]any{
		"parentRefs": []any{map[string]any{"name": GatewayName(t.Project), "namespace": cfg.Namespace, "sectionName": "http"}},
		"rules": []any{map[string]any{
			"matches": []any{map[string]any{"path": map[string]any{"type": "PathPrefix", "value": "/v1"}}},
			"backendRefs": []any{map[string]any{
				"group": "inference.networking.k8s.io", "kind": "InferencePool", "name": PoolName(), "port": int64(modelPort),
			}},
		}},
	}

	irule := obj(IRuleGVK, cfg.Namespace, IRuleName(t.Project), d)
	irule.Object["spec"] = map[string]any{"iRule": LimitIRule(t.Project, d.RateLimit)}

	policy := obj(PolicyGVK, cfg.Namespace, PolicyName(t.Project), d)
	policy.Object["spec"] = map[string]any{
		"extensionRefs": []any{map[string]any{"kind": "F5BigCneIrule", "name": IRuleName(t.Project), "group": "k8s.f5net.com"}},
		"targetRefs": []any{map[string]any{
			"kind": "Gateway", "name": GatewayName(t.Project), "group": "gateway.networking.k8s.io", "sectionName": "http",
		}},
	}

	// Creation order: the picker before the pool that points at it, the gateway before its route.
	return []*unstructured.Unstructured{gw, epp, pool, route, irule, policy}
}

// LimitIRule keeps a tenant to its share of the GPU estate: at most rps requests per second at the
// BNK front door, refused with 429 before anything reaches a GPU. Every answer is tagged with the
// Run:ai project. It follows the BNK 2.4 iRule rules learned in the lab: `table incr` only adds, table
// values are read back through regexp + scan, and JSON with [ ] sits in braces.
func LimitIRule(project string, rps float64) string {
	limit := int64(math.Ceil(rps))
	if limit < 1 {
		limit = 1
	}
	sub := "rl_" + strings.ReplaceAll(project, "-", "_")
	return fmt.Sprintf(`when HTTP_REQUEST {
  set rl_key [clock seconds]
  set rl_n [table incr -subtable "%[1]s" $rl_key]
  table timeout -subtable "%[1]s" $rl_key 2
  if { [regexp {(\d+)$} $rl_n -> rl_d] } { scan $rl_d %%d rl_n }
  if { $rl_n > %[2]d } {
    HTTP::respond 429 content {{"error":{"code":"tenant/rate-limited","message":"Run:ai project %[3]s is over its share of the GPU estate (%[2]d req/s)."}}} "Content-Type" "application/json" "Retry-After" "1" "X-BNK-Tenant" "%[3]s"
    return
  }
}
when HTTP_RESPONSE {
  HTTP::header insert X-BNK-Tenant "%[3]s"
}
`, sub, limit, project)
}

func obj(gvk schema.GroupVersionKind, ns, name string, d reconcile.Desired) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	u.SetNamespace(ns)
	u.SetName(name)
	u.SetLabels(map[string]string{
		LabelProject:    d.Tenant.Project,
		LabelDepartment: d.Tenant.Department,
		LabelManagedBy:  ManagedBy,
	})
	u.SetAnnotations(map[string]string{AnnotSource: "kai.resources/v1alpha1/Project/" + d.Tenant.Project})
	return u
}
