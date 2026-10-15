// Package bnk renders and applies the BNK 2.4 objects for one Run:ai project.
//
// Every kind, apiVersion and field here is copied from manifests that run on BNK 2.4 today
// (iracic82/bnk-ai-kv-lab, see docs/verified-names.md). Nothing is guessed.
package bnk

import (
	"fmt"
	"math"
	"net/netip"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/f5devcentral/bnk-runai-tenancy/internal/reconcile"
	"github.com/f5devcentral/bnk-runai-tenancy/internal/tenant"
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
	// PullSecret is BNK's registry credential in Namespace, copied into each tenant namespace so the
	// tenant's Endpoint Picker can pull from repo.f5.com. Empty disables the copy.
	PullSecret string
	// MeteringSink is the "IP:port" the tenant iRules send one usage line per response to (the meter,
	// cmd/meter). Empty turns metering off. TMM connects from its own network, so this must be an
	// address TMM can reach, not a ClusterIP.
	MeteringSink string
	// MeteringToken guards the iRule's /bnk/flush path, which the meter polls to collect usage lines.
	MeteringToken string
	// DynamoNATS is the NATS URL Dynamo workers publish KV-cache events on (the dynamo-platform chart's
	// NATS). A Dynamo tenant's Endpoint Picker subscribes there (F5EPP spec.natsURL, required).
	DynamoNATS string
	// DynamoTokenizer is the model the Dynamo workers serve; the picker tokenizes prompts with it.
	DynamoTokenizer string
}

// DefaultConfig matches the lab cluster.
func DefaultConfig() Config {
	return Config{Namespace: "f5-bnk", GatewayClass: "f5-gateway-class",
		Tokenizer: "NousResearch/Meta-Llama-3.1-8B-Instruct", ListenPort: 8000,
		PullSecret:      "far-pull-secret",
		DynamoNATS:      "nats://dynamo-platform-nats.dynamo-system.svc.cluster.local:4222",
		DynamoTokenizer: "Qwen/Qwen3-0.6B"}
}

// Names of a tenant's objects. The Gateway, iRule and NetPolicy live in the BNK namespace, so they
// carry the project name; the pool, picker and route live in the tenant's own namespace.
func GatewayName(project string) string { return project }
func PoolName() string                  { return "model-pool" }
func EPPName() string                   { return "model-pool-epp" }
func RouteName() string                 { return "tenant-route" }
func IRuleName(project string) string   { return project + "-limit" }
func PolicyName(project string) string  { return project + "-policy" }

// Render returns the BNK objects for one Run:ai project, served at vip. It fails when a name that is
// rendered into the iRule's Tcl is not a plain [a-z0-9-] label.
func Render(cfg Config, d reconcile.Desired, vip string) ([]*unstructured.Unstructured, error) {
	t := d.Tenant
	rule, err := TenantIRule(t.Project, t.Department, d.RateLimit, cfg.MeteringSink, cfg.MeteringToken)
	if err != nil {
		return nil, err
	}
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
		"selector":    map[string]any{"matchLabels": poolSelector(t)},
		"targetPorts": []any{map[string]any{"number": int64(modelPort)}},
		"endpointPickerRef": map[string]any{
			"name": EPPName(), "port": map[string]any{"number": int64(eppPort)},
			// Abort on picker errors; FailOpen would silently fall back to round-robin.
			"failureMode": "FailClose",
		},
	}

	epp := obj(EPPGVK, t.Namespace, EPPName(), d)
	if t.Engine == tenant.EngineDynamo {
		if cfg.DynamoNATS == "" {
			return nil, fmt.Errorf("project %s runs NVIDIA Dynamo but no Dynamo NATS URL is configured (--dynamo-nats)", t.Project)
		}
		epp.Object["spec"] = dynamoEPPSpec(cfg)
	} else {
		epp.Object["spec"] = map[string]any{
			"poolRef": map[string]any{"name": PoolName()},
			"engine":  tenant.EngineVLLM,
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
	}

	route := obj(RouteGVK, t.Namespace, RouteName(), d)
	route.Object["spec"] = map[string]any{
		"parentRefs": []any{map[string]any{"name": GatewayName(t.Project), "namespace": cfg.Namespace, "sectionName": "http"}},
		"rules": []any{map[string]any{
			"matches": routeMatches(cfg),
			"backendRefs": []any{map[string]any{
				"group": "inference.networking.k8s.io", "kind": "InferencePool", "name": PoolName(), "port": int64(modelPort),
			}},
		}},
	}

	irule := obj(IRuleGVK, cfg.Namespace, IRuleName(t.Project), d)
	irule.Object["spec"] = map[string]any{"iRule": rule}

	policy := obj(PolicyGVK, cfg.Namespace, PolicyName(t.Project), d)
	policy.Object["spec"] = map[string]any{
		"extensionRefs": []any{map[string]any{"kind": "F5BigCneIrule", "name": IRuleName(t.Project), "group": "k8s.f5net.com"}},
		"targetRefs": []any{map[string]any{
			"kind": "Gateway", "name": GatewayName(t.Project), "group": "gateway.networking.k8s.io", "sectionName": "http",
		}},
	}

	// Creation order: the picker before the pool that points at it, the gateway before its route.
	return []*unstructured.Unstructured{gw, epp, pool, route, irule, policy}, nil
}

// dnsLabel is what a Run:ai project or department name must look like before it is rendered into Tcl:
// lower-case letters, digits and inner dashes, so nothing in it can open a Tcl quote, command or variable.
var dnsLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// meteringToken guards the iRule's /bnk/flush path; it is rendered into Tcl, so it is a plain word.
var meteringToken = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

// IsTenantName reports whether s is a name that may be rendered into a tenant iRule.
func IsTenantName(s string) bool { return dnsLabel.MatchString(s) }

// ValidateTenantNames refuses project and department names that are not plain DNS labels. The
// department may be empty (a project without a parent).
func ValidateTenantNames(project, department string) error {
	if !dnsLabel.MatchString(project) {
		return fmt.Errorf("project name %q must match [a-z0-9-] (DNS label) to be rendered into an iRule", project)
	}
	if department != "" && !dnsLabel.MatchString(department) {
		return fmt.Errorf("department name %q must match [a-z0-9-] (DNS label) to be rendered into an iRule", department)
	}
	return nil
}

// ValidateMetering checks the metering settings: both empty (metering off), or a literal IP:port sink
// (iRules cannot resolve DNS) and a 16-128 character [A-Za-z0-9_-] flush token.
func ValidateMetering(sink, token string) error {
	if sink == "" {
		return nil
	}
	if _, err := netip.ParseAddrPort(sink); err != nil {
		return fmt.Errorf("metering sink %q must be IP:port: %w", sink, err)
	}
	if !meteringToken.MatchString(token) {
		return fmt.Errorf("metering needs a flush token of 16-128 characters [A-Za-z0-9_-]")
	}
	return nil
}

// Metering limits, as proven in the Lambda lab.
const (
	// Response bodies up to this size are collected whole and scanned for usage.
	maxMeteredBody = 131072
	// Records one /bnk/flush sends at most.
	FlushBatch = 100
	// Seconds a parked record waits for a flush before BNK drops it.
	pendingLifetime = 600
	// FlushPath is the iRule's own control path the meter polls; FlushHeader carries the token.
	FlushPath   = "/bnk/flush"
	FlushHeader = "X-BNK-Flush"
)

// MeteringSubtable is the BNK session table a tenant's usage records are parked in.
func MeteringSubtable(project string) string { return "tm_" + strings.ReplaceAll(project, "-", "_") }

// TenantIRule is the per-tenant iRule: rate limiting, plus token metering when sink is set.
//
// Rate limit: at most rps requests per second at the BNK front door, refused with 429 before anything
// reaches a GPU. Every answer is tagged with the Run:ai project (X-BNK-Tenant).
//
// Metering records one line per completed response:
//
//	project=<p> dept=<d> model=<m> prompt=<n> completion=<n> total=<n> code=<status>[ n=<count>]
//
// Usage comes from the response's usage object (the last one in an SSE stream); a stream without one is
// counted by its content deltas. 429 refusals are counted in a table counter and leave as one line with
// n=<count> per flush, so a flooding tenant costs one table op per refused request and nothing more.
//
// The response events only parse the body and park the line in a session table: on BNK 2.4 a sideband
// connect from HTTP_RESPONSE* wedged TMM, and one inside a request that then went to the backend broke that
// request (Lambda lab, live). The lines leave on GET /bnk/flush, a request the iRule answers itself (204):
// the meter polls it with the flush token, and the iRule sends the parked lines over TCP to sink.
// Everything metering does is inside catch, so a dead meter never breaks a response.
//
// BNK 2.4 rules carried over from the lab: `table incr` only adds, table values are read back through
// regexp + scan, JSON with [ ] sits in braces, and bodies are read only with string last / string range /
// plain regexp.
func TenantIRule(project, department string, rps float64, sink, token string) (string, error) {
	if err := ValidateTenantNames(project, department); err != nil {
		return "", err
	}
	if err := ValidateMetering(sink, token); err != nil {
		return "", err
	}
	limit := int64(math.Ceil(rps))
	if limit < 1 {
		limit = 1
	}
	r := strings.NewReplacer(
		"@RL@", "rl_"+strings.ReplaceAll(project, "-", "_"),
		"@LIMIT@", fmt.Sprint(limit),
		"@PROJECT@", project,
		"@LABEL@", "project="+project+" dept="+department,
		"@PEND@", MeteringSubtable(project),
		"@SINK@", sink,
		"@TOKEN@", token,
		"@BATCH@", fmt.Sprint(FlushBatch),
		"@LIFE@", fmt.Sprint(pendingLifetime),
		"@BODY@", fmt.Sprint(maxMeteredBody),
		"@FLUSH@", FlushPath,
		"@HDR@", FlushHeader,
	)
	if sink == "" {
		return r.Replace(limitOnlyIRule), nil
	}
	return r.Replace(meteredIRule), nil
}

const limitOnlyIRule = `when HTTP_REQUEST {
  set rl_key [clock seconds]
  set rl_n [table incr -subtable "@RL@" $rl_key]
  table timeout -subtable "@RL@" $rl_key 2
  if { [regexp {(\d+)$} $rl_n -> rl_d] } { scan $rl_d %d rl_n }
  if { $rl_n > @LIMIT@ } {
    HTTP::respond 429 content {{"error":{"code":"tenant/rate-limited","message":"Run:ai project @PROJECT@ is over its share of the GPU estate (@LIMIT@ req/s)."}}} "Content-Type" "application/json" "Retry-After" "1" "X-BNK-Tenant" "@PROJECT@"
    return
  }
}
when HTTP_RESPONSE {
  HTTP::header insert X-BNK-Tenant "@PROJECT@"
}
`

const meteredIRule = `when HTTP_REQUEST {
  if { [string first "/bnk/" [HTTP::path]] == 0 } {
    if { !([HTTP::header value "@HDR@"] eq "@TOKEN@") } {
      HTTP::respond 403 content {{"error":"forbidden"}} "Content-Type" "application/json"
      return
    }
    if { [HTTP::path] ne "@FLUSH@" || [HTTP::method] ne "GET" } {
      HTTP::respond 404 content {{"error":"not found"}} "Content-Type" "application/json"
      return
    }
    set tm_lines ""
    set tm_n 0
    set tm_me [clock clicks]
    set tm_lock [expr { [table add -subtable "@PEND@" lock $tm_me 2] eq $tm_me }]
    if { $tm_lock } {
      set tm_tail 0; catch { set tm_tail [table lookup -notouch -subtable "@PEND@" tail] }
      if { [regexp {(\d+)$} $tm_tail -> tm_d] } { scan $tm_d %d tm_tail } else { set tm_tail 0 }
      set tm_head 0; catch { set tm_head [table lookup -notouch -subtable "@PEND@" head] }
      if { [regexp {(\d+)$} $tm_head -> tm_d] } { scan $tm_d %d tm_head } else { set tm_head 0 }
      while { $tm_head < $tm_tail && $tm_n < @BATCH@ } {
        incr tm_head
        incr tm_n
        set tm_v [table lookup -notouch -subtable "@PEND@" "r$tm_head"]
        table delete -subtable "@PEND@" "r$tm_head"
        set tm_i [string first "project=" $tm_v]
        if { $tm_i >= 0 } { append tm_lines [string range $tm_v $tm_i end] "\n" }
      }
      if { $tm_n > 0 } {
        table incr -subtable "@PEND@" head $tm_n
        table timeout -subtable "@PEND@" head indefinite
      }
      set tm_rej 0; catch { set tm_rej [table lookup -notouch -subtable "@PEND@" rej] }
      if { [regexp {(\d+)$} $tm_rej -> tm_d] } { scan $tm_d %d tm_rej } else { set tm_rej 0 }
      set tm_rejs 0; catch { set tm_rejs [table lookup -notouch -subtable "@PEND@" rejsent] }
      if { [regexp {(\d+)$} $tm_rejs -> tm_d] } { scan $tm_d %d tm_rejs } else { set tm_rejs 0 }
      if { $tm_rej > $tm_rejs } {
        set tm_k [expr { $tm_rej - $tm_rejs }]
        append tm_lines "@LABEL@ model= prompt=0 completion=0 total=0 code=429 n=$tm_k\n"
        table incr -subtable "@PEND@" rejsent $tm_k
        table timeout -subtable "@PEND@" rejsent indefinite
      }
      table delete -subtable "@PEND@" lock
    }
    if { $tm_lines ne "" } {
      if { [catch {
        set tm_c [connect -protocol TCP "@SINK@"]
        send -timeout 2000 $tm_c "auth @TOKEN@\n$tm_lines"
        close $tm_c
      } tm_err] } { log local0. "BNK usage flush failed: $tm_err" }
    }
    HTTP::respond 204 "X-BNK-Flushed" $tm_n
    return
  }
  set rl_key [clock seconds]
  set rl_n [table incr -subtable "@RL@" $rl_key]
  table timeout -subtable "@RL@" $rl_key 2
  if { [regexp {(\d+)$} $rl_n -> rl_d] } { scan $rl_d %d rl_n }
  if { $rl_n > @LIMIT@ } {
    catch {
      table incr -subtable "@PEND@" rej
      table timeout -subtable "@PEND@" rej indefinite
    }
    HTTP::respond 429 content {{"error":{"code":"tenant/rate-limited","message":"Run:ai project @PROJECT@ is over its share of the GPU estate (@LIMIT@ req/s)."}}} "Content-Type" "application/json" "Retry-After" "1" "X-BNK-Tenant" "@PROJECT@"
    return
  }
}
when HTTP_RESPONSE {
  HTTP::header insert X-BNK-Tenant "@PROJECT@"
  set tm_status [HTTP::status]
  set tm_p 0
  set tm_cp 0
  set tm_t 0
  set tm_m ""
  set tm_parked 0
  set tm_deltas 0
  set tm_tail ""
  set tm_stream [expr { [string first "event-stream" [HTTP::header value "Content-Type"]] >= 0 }]
  set tm_collect 0
  if { $tm_stream } {
    set tm_collect 1
  } elseif { [HTTP::header exists "Content-Length"] } {
    set tm_rlen [HTTP::header value "Content-Length"]
    if { [string is integer -strict $tm_rlen] && $tm_rlen > 0 && $tm_rlen <= @BODY@ } { set tm_collect $tm_rlen }
  } else {
    set tm_collect @BODY@
  }
  if { $tm_collect > 0 } {
    HTTP::collect $tm_collect
  } else {
    if { [catch {
      set tm_parked 1
      set tm_seq [table incr -subtable "@PEND@" tail]
      table timeout -subtable "@PEND@" tail indefinite
      if { [regexp {(\d+)$} $tm_seq -> tm_d] } { scan $tm_d %d tm_seq }
      table set -subtable "@PEND@" "r$tm_seq" "@LABEL@ model= prompt=0 completion=0 total=0 code=$tm_status" @LIFE@ @LIFE@
    } tm_err] } { log local0. "BNK metering failed: $tm_err" }
  }
}
when HTTP_RESPONSE_DATA {
  if { [catch {
    if { $tm_stream } {
      set tm_rb $tm_tail
      append tm_rb [HTTP::payload]
      incr tm_deltas [regexp -all {"delta"\s*:\s*\{\s*"} [HTTP::payload]]
      set tm_tail [string range $tm_rb end-1023 end]
      set tm_done [regexp {\ndata: ?\[DONE\]\s*(\r?\n|$)} $tm_rb]
      HTTP::release
      if { !$tm_done } { HTTP::collect 1 }
    } else {
      set tm_rb [HTTP::payload]
      set tm_done 1
    }
    set tm_i [string last {"prompt_tokens"} $tm_rb]
    if { $tm_i >= 0 && [regexp {"prompt_tokens"\s*:\s*(\d+)} [string range $tm_rb $tm_i end] -> tm_v] && $tm_v > $tm_p } { set tm_p $tm_v }
    set tm_i [string last {"completion_tokens"} $tm_rb]
    if { $tm_i >= 0 && [regexp {"completion_tokens"\s*:\s*(\d+)} [string range $tm_rb $tm_i end] -> tm_v] && $tm_v > $tm_cp } { set tm_cp $tm_v }
    set tm_i [string last {"total_tokens"} $tm_rb]
    if { $tm_i >= 0 && [regexp {"total_tokens"\s*:\s*(\d+)} [string range $tm_rb $tm_i end] -> tm_v] && $tm_v > $tm_t } { set tm_t $tm_v }
    if { $tm_m eq "" } { regexp {"model"\s*:\s*"([A-Za-z0-9._/:-]{1,128})"} $tm_rb -> tm_m }
    if { $tm_done && !$tm_parked } {
      set tm_parked 1
      if { $tm_stream && $tm_t == 0 && $tm_deltas > 0 } {
        set tm_cp $tm_deltas
        set tm_t [expr { $tm_p + $tm_deltas }]
      }
      if { $tm_t == 0 } { set tm_t [expr { $tm_p + $tm_cp }] }
      set tm_seq [table incr -subtable "@PEND@" tail]
      table timeout -subtable "@PEND@" tail indefinite
      if { [regexp {(\d+)$} $tm_seq -> tm_d] } { scan $tm_d %d tm_seq }
      table set -subtable "@PEND@" "r$tm_seq" "@LABEL@ model=$tm_m prompt=$tm_p completion=$tm_cp total=$tm_t code=$tm_status" @LIFE@ @LIFE@
    }
  } tm_err] } { log local0. "BNK metering failed: $tm_err" }
}
when CLIENT_CLOSED {
  if { [info exists tm_parked] && !$tm_parked } {
    if { [catch {
      set tm_parked 1
      if { $tm_t == 0 } {
        set tm_cp $tm_deltas
        set tm_t [expr { $tm_p + $tm_deltas }]
      }
      set tm_seq [table incr -subtable "@PEND@" tail]
      table timeout -subtable "@PEND@" tail indefinite
      if { [regexp {(\d+)$} $tm_seq -> tm_d] } { scan $tm_d %d tm_seq }
      table set -subtable "@PEND@" "r$tm_seq" "@LABEL@ model=$tm_m prompt=$tm_p completion=$tm_cp total=$tm_t code=$tm_status" @LIFE@ @LIFE@
    } tm_err] } { log local0. "BNK metering on close failed: $tm_err" }
  }
}
`

// routeMatches is /v1, plus /bnk when metering is on: /bnk is the iRule's own flush path and never
// reaches the backend, but it needs a route match or BNK answers 404 before the iRule runs (Lambda lab).
// Dynamo labels the operator (dynamo-platform) puts on every pod of a DynamoGraphDeployment.
const (
	DynamoGraphLabel     = "nvidia.com/dynamo-graph-deployment-name"
	DynamoComponentLabel = "nvidia.com/dynamo-component-type"
	// dynamoMetricsPort is DYN_SYSTEM_PORT, which the operator sets on every worker.
	dynamoMetricsPort = 9090
)

// poolSelector picks the pods BNK sends a tenant's requests to. For Dynamo that is the graph's decode
// workers; each carries a Dynamo frontend sidecar in direct mode on port 8000, which serves the worker the
// picker named. The Dynamo frontend itself is never in the pool: BNK, not Dynamo's router, picks the worker.
func poolSelector(t tenant.Spec) map[string]any {
	if t.Engine == tenant.EngineDynamo {
		return map[string]any{DynamoGraphLabel: t.DynamoGraph, DynamoComponentLabel: "decode"}
	}
	return map[string]any{ModelServerLabel: ModelServerValue}
}

// dynamoEPPSpec is the F5EPP for a Dynamo pool, as documented for BNK 2.4 (engine dynamo, natsURL).
//
// The dynamo-wire topics are overridden through the documented eppConfig plugin merge: BNK 2.4 subscribes
// to namespace.*.component.backend.kv-events by default, while Dynamo 1.5.1 publishes per endpoint,
// namespace.<ns>.component.backend.endpoint.generate.kv-events (seen live with nats sub ">"). Without the
// override routing still works, but the picker never receives Dynamo's KV-cache events.
func dynamoEPPSpec(cfg Config) map[string]any {
	return map[string]any{
		"poolRef": map[string]any{"name": PoolName()},
		"engine":  tenant.EngineDynamo,
		"natsURL": cfg.DynamoNATS,
		"server": map[string]any{
			"modelServerMetricsPort": int64(dynamoMetricsPort), "secureServing": false, "metricsEndpointAuth": false,
		},
		"tokenizer": map[string]any{"name": cfg.DynamoTokenizer},
		"eppConfig": map[string]any{"config": map[string]any{"plugins": []any{map[string]any{
			"name": "dynamo-wire", "type": "dynamo-wire",
			"parameters": map[string]any{
				"kv_events_topic":  "component.backend.endpoint.generate.kv-events",
				"kv_metrics_topic": "component.backend.endpoint.generate.kv_metrics",
			},
		}}}},
	}
}

func routeMatches(cfg Config) []any {
	m := []any{map[string]any{"path": map[string]any{"type": "PathPrefix", "value": "/v1"}}}
	if cfg.MeteringSink != "" {
		m = append(m, map[string]any{"path": map[string]any{"type": "PathPrefix", "value": "/bnk"}})
	}
	return m
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
