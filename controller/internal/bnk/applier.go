package bnk

import (
	"context"
	"fmt"
	"net/netip"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/f5devcentral/bnk-runai-tenancy/internal/reconcile"
)

// FieldManager owns every field this controller writes (server-side apply).
const FieldManager = "bnk-runai-tenancy"

// Applier writes a tenant's BNK objects with server-side apply and removes them by label.
type Applier struct {
	Client client.Client
	Config Config
	// VIPs is the address pool tenant Gateways are served from (e.g. 172.16.201.110-149).
	VIPs []netip.Addr
}

var _ reconcile.BundleApplier = (*Applier)(nil)

// Apply renders and applies the tenant's objects. It is idempotent, and re-applying restores
// anything someone changed or deleted by hand.
func (a *Applier) Apply(ctx context.Context, d reconcile.Desired) error {
	vip, err := a.vipFor(ctx, d.Tenant.Project)
	if err != nil {
		return err
	}
	for _, o := range Render(a.Config, d, vip.String()) {
		if err := a.Client.Patch(ctx, o, client.Apply, client.FieldOwner(FieldManager), client.ForceOwnership); err != nil {
			return fmt.Errorf("apply %s %s/%s: %w", o.GetKind(), o.GetNamespace(), o.GetName(), err)
		}
	}
	return nil
}

// Delete removes every object labelled with the project, in any namespace. Kinds whose CRD is
// missing are skipped, so a half-installed BNK never blocks a Run:ai project deletion.
func (a *Applier) Delete(ctx context.Context, project string) error {
	for _, gvk := range OwnedKinds {
		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(gvk.GroupVersion().WithKind(gvk.Kind + "List"))
		err := a.Client.List(ctx, list, client.MatchingLabels{LabelProject: project, LabelManagedBy: ManagedBy})
		if meta.IsNoMatchError(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("list %s: %w", gvk.Kind, err)
		}
		for i := range list.Items {
			if err := a.Client.Delete(ctx, &list.Items[i]); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("delete %s %s/%s: %w", gvk.Kind, list.Items[i].GetNamespace(), list.Items[i].GetName(), err)
			}
		}
	}
	return nil
}

// vipFor keeps the address a tenant already has, otherwise takes the lowest free one in the pool.
func (a *Applier) vipFor(ctx context.Context, project string) (netip.Addr, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(GatewayGVK.GroupVersion().WithKind("GatewayList"))
	if err := a.Client.List(ctx, list, client.InNamespace(a.Config.Namespace),
		client.MatchingLabels{LabelManagedBy: ManagedBy}); err != nil {
		return netip.Addr{}, fmt.Errorf("list tenant gateways: %w", err)
	}
	used := map[netip.Addr]bool{}
	for _, gw := range list.Items {
		addrs, _, _ := unstructured.NestedSlice(gw.Object, "spec", "addresses")
		for _, raw := range addrs {
			m, _ := raw.(map[string]any)
			v, _ := m["value"].(string)
			ip, err := netip.ParseAddr(v)
			if err != nil {
				continue
			}
			if gw.GetLabels()[LabelProject] == project {
				return ip, nil
			}
			used[ip] = true
		}
	}
	for _, ip := range a.VIPs {
		if !used[ip] {
			return ip, nil
		}
	}
	return netip.Addr{}, fmt.Errorf("no free tenant VIP left in the pool (%d addresses)", len(a.VIPs))
}

// ParseVIPRange turns "172.16.201.110-172.16.201.149" into the list of addresses.
func ParseVIPRange(r string) ([]netip.Addr, error) {
	var lo, hi string
	if n, _ := fmt.Sscanf(r, "%s", &lo); n == 0 {
		return nil, fmt.Errorf("empty VIP range")
	}
	for i := 0; i < len(r); i++ {
		if r[i] == '-' {
			lo, hi = r[:i], r[i+1:]
			break
		}
	}
	if hi == "" {
		hi = lo
	}
	a, err := netip.ParseAddr(lo)
	if err != nil {
		return nil, fmt.Errorf("VIP range start %q: %w", lo, err)
	}
	b, err := netip.ParseAddr(hi)
	if err != nil {
		return nil, fmt.Errorf("VIP range end %q: %w", hi, err)
	}
	if b.Less(a) {
		return nil, fmt.Errorf("VIP range %q ends before it starts", r)
	}
	var out []netip.Addr
	for ip := a; ; ip = ip.Next() {
		out = append(out, ip)
		if ip == b || len(out) > 1024 {
			break
		}
	}
	return out, nil
}
