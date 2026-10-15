package bnk

import (
	"context"
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

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
// anything someone changed or deleted by hand. It never takes over an object it did not create.
func (a *Applier) Apply(ctx context.Context, d reconcile.Desired) error {
	vip, err := a.vipFor(ctx, d.Tenant.Project)
	if err != nil {
		return err
	}
	if err := a.copyPullSecret(ctx, d.Tenant.Namespace); err != nil {
		return err
	}
	objs, err := Render(a.Config, d, vip.String())
	if err != nil {
		return err
	}
	for _, o := range objs {
		if err := a.mustOwn(ctx, o, d.Tenant.Project); err != nil {
			return err
		}
		// Forcing is safe now: the object is either new or ours for this very project.
		if err := a.Client.Patch(ctx, o, client.Apply, client.FieldOwner(FieldManager), client.ForceOwnership); err != nil {
			return fmt.Errorf("apply %s %s/%s: %w", o.GetKind(), o.GetNamespace(), o.GetName(), err)
		}
	}
	return nil
}

// copyPullSecret puts BNK's registry credential into the tenant namespace. BNK runs each tenant's
// Endpoint Picker there from repo.f5.com, and the credential ships only in the BNK namespace (the
// bnk-ai-kv-lab does the same copy by hand). Anyone allowed to read Secrets in a tenant namespace can
// read it, so it is only ever written into namespaces Run:ai created for a project, never over a
// Secret someone else owns. Skipped when PullSecret is unset.
func (a *Applier) copyPullSecret(ctx context.Context, ns string) error {
	if a.Config.PullSecret == "" {
		return nil
	}
	if !tenantNamespace.MatchString(ns) || ns == a.Config.Namespace {
		return fmt.Errorf("refusing to copy the BNK registry secret into namespace %q: not a Run:ai project namespace", ns)
	}
	cur := &corev1.Secret{}
	err := a.Client.Get(ctx, client.ObjectKey{Namespace: ns, Name: a.Config.PullSecret}, cur)
	if err == nil && cur.Labels[LabelManagedBy] != ManagedBy {
		return fmt.Errorf("secret %s/%s exists and is not managed by %s; refusing to overwrite it", ns, a.Config.PullSecret, ManagedBy)
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("get secret %s/%s: %w", ns, a.Config.PullSecret, err)
	}
	src := &corev1.Secret{}
	if err := a.Client.Get(ctx, client.ObjectKey{Namespace: a.Config.Namespace, Name: a.Config.PullSecret}, src); err != nil {
		return fmt.Errorf("read registry secret %s/%s: %w", a.Config.Namespace, a.Config.PullSecret, err)
	}
	dst := &corev1.Secret{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: a.Config.PullSecret, Labels: map[string]string{LabelManagedBy: ManagedBy}},
		Type:       src.Type,
		Data:       src.Data,
	}
	return a.Client.Patch(ctx, dst, client.Apply, client.FieldOwner(FieldManager))
}

// tenantNamespace is the shape of namespaces Run:ai creates for projects (prefix runai-).
var tenantNamespace = regexp.MustCompile(`^runai-[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// mustOwn refuses objects that exist but were not created by this controller for this project,
// so a name collision in the shared f5-bnk namespace can never hand one tenant another's config.
func (a *Applier) mustOwn(ctx context.Context, o *unstructured.Unstructured, project string) error {
	cur := &unstructured.Unstructured{}
	cur.SetGroupVersionKind(o.GroupVersionKind())
	err := a.Client.Get(ctx, client.ObjectKeyFromObject(o), cur)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get %s %s/%s: %w", o.GetKind(), o.GetNamespace(), o.GetName(), err)
	}
	l := cur.GetLabels()
	if l[LabelManagedBy] != ManagedBy || l[LabelProject] != project {
		return fmt.Errorf("%s %s/%s already exists and is not managed for Run:ai project %s; refusing to take it over",
			o.GetKind(), o.GetNamespace(), o.GetName(), project)
	}
	return nil
}

// Delete removes every object labelled with the project, in any namespace, and puts the tenant's
// VIP on hold. Kinds whose CRD is missing are skipped, so a half-installed BNK never blocks a Run:ai
// project deletion.
func (a *Applier) Delete(ctx context.Context, project string) error {
	// The pool lives in the tenant namespace; remember it so the copied registry secret goes too.
	pools := &unstructured.UnstructuredList{}
	pools.SetGroupVersionKind(PoolGVK.GroupVersion().WithKind("InferencePoolList"))
	if err := a.Client.List(ctx, pools, client.MatchingLabels{LabelProject: project, LabelManagedBy: ManagedBy}); err == nil {
		for _, p := range pools.Items {
			if err := a.DeletePullSecret(ctx, p.GetNamespace()); err != nil {
				return fmt.Errorf("delete registry secret in %s: %w", p.GetNamespace(), err)
			}
		}
	}
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
	return a.releaseVIP(ctx, project)
}

// DeletePullSecret removes the registry secret this controller copied into a tenant namespace.
func (a *Applier) DeletePullSecret(ctx context.Context, ns string) error {
	if a.Config.PullSecret == "" || !tenantNamespace.MatchString(ns) {
		return nil
	}
	cur := &corev1.Secret{}
	err := a.Client.Get(ctx, client.ObjectKey{Namespace: ns, Name: a.Config.PullSecret}, cur)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if cur.Labels[LabelManagedBy] != ManagedBy {
		return nil
	}
	if err := a.Client.Delete(ctx, cur); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// Projects returns every Run:ai project that has a managed tenant Gateway, so the reconciler can
// find tenants whose project was deleted while the controller was down.
func (a *Applier) Projects(ctx context.Context) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, gvk := range OwnedKinds {
		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(gvk.GroupVersion().WithKind(gvk.Kind + "List"))
		err := a.Client.List(ctx, list, client.MatchingLabels{LabelManagedBy: ManagedBy})
		if meta.IsNoMatchError(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", gvk.Kind, err)
		}
		for _, o := range list.Items {
			if p := o.GetLabels()[LabelProject]; p != "" && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out, nil
}

// VIP bookkeeping lives in one ConfigMap in the BNK namespace, updated with optimistic concurrency:
//
//	data["<ip>"] = "<project>"             assigned
//	data["<ip>"] = "released:<unix-secs>"  on hold until VIPHold has passed
//
// A released address is not handed out again straight away, so clients still pointing at a deleted
// tenant's VIP never land on a new tenant.
const vipConfigMap = "bnk-runai-tenancy-vips"

// VIPHold is how long a released VIP stays unassigned.
var VIPHold = 15 * time.Minute

func (a *Applier) vipFor(ctx context.Context, project string) (netip.Addr, error) {
	var out netip.Addr
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cm, err := a.vipMap(ctx)
		if err != nil {
			return err
		}
		// Live Gateways are the ground truth: if the ConfigMap was lost or edited, an address a
		// Gateway still holds is never handed to anyone else, and a project keeps its own.
		live, err := a.liveVIPs(ctx)
		if err != nil {
			return err
		}
		for ip, owner := range live {
			if owner == project {
				out = ip
				if cm.Data == nil {
					cm.Data = map[string]string{}
				}
				if cm.Data[ip.String()] != project {
					cm.Data[ip.String()] = project
					return a.Client.Update(ctx, cm)
				}
				return nil
			}
		}
		for k, v := range cm.Data {
			if v == project {
				ip, err := netip.ParseAddr(k)
				if err == nil {
					out = ip
					return nil
				}
			}
		}
		now := time.Now()
		for _, ip := range a.VIPs {
			if _, inUse := live[ip]; inUse {
				continue
			}
			v, taken := cm.Data[ip.String()]
			if taken && !releasedLongAgo(v, now) {
				continue
			}
			if cm.Data == nil {
				cm.Data = map[string]string{}
			}
			cm.Data[ip.String()] = project
			out = ip
			return a.Client.Update(ctx, cm)
		}
		return fmt.Errorf("no free tenant VIP left in the pool (%d addresses, released ones are held for %s)", len(a.VIPs), VIPHold)
	})
	return out, err
}

// liveVIPs maps every address a managed tenant Gateway serves to that Gateway's project.
func (a *Applier) liveVIPs(ctx context.Context) (map[netip.Addr]string, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(GatewayGVK.GroupVersion().WithKind("GatewayList"))
	err := a.Client.List(ctx, list, client.InNamespace(a.Config.Namespace), client.MatchingLabels{LabelManagedBy: ManagedBy})
	if meta.IsNoMatchError(err) {
		return map[netip.Addr]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list tenant gateways: %w", err)
	}
	out := map[netip.Addr]string{}
	for _, gw := range list.Items {
		addrs, _, _ := unstructured.NestedSlice(gw.Object, "spec", "addresses")
		for _, raw := range addrs {
			m, _ := raw.(map[string]any)
			v, _ := m["value"].(string)
			if ip, err := netip.ParseAddr(v); err == nil {
				out[ip] = gw.GetLabels()[LabelProject]
			}
		}
	}
	return out, nil
}

func (a *Applier) releaseVIP(ctx context.Context, project string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cm, err := a.vipMap(ctx)
		if err != nil {
			return err
		}
		changed := false
		for k, v := range cm.Data {
			if v == project {
				cm.Data[k] = fmt.Sprintf("released:%d", time.Now().Unix())
				changed = true
			}
		}
		if !changed {
			return nil
		}
		return a.Client.Update(ctx, cm)
	})
}

func releasedLongAgo(v string, now time.Time) bool {
	ts, ok := strings.CutPrefix(v, "released:")
	if !ok {
		return false
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	return err == nil && now.Sub(time.Unix(sec, 0)) >= VIPHold
}

// vipMap returns the bookkeeping ConfigMap, creating it on first use.
func (a *Applier) vipMap(ctx context.Context) (*corev1.ConfigMap, error) {
	cm := &corev1.ConfigMap{}
	key := client.ObjectKey{Namespace: a.Config.Namespace, Name: vipConfigMap}
	err := a.Client.Get(ctx, key, cm)
	if apierrors.IsNotFound(err) {
		cm = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
			Namespace: key.Namespace, Name: key.Name, Labels: map[string]string{LabelManagedBy: ManagedBy}}}
		if err := a.Client.Create(ctx, cm); err != nil && !apierrors.IsAlreadyExists(err) {
			return nil, err
		}
		err = a.Client.Get(ctx, key, cm)
	}
	return cm, err
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
