package reconcile

import (
	"sort"

	kaiv1alpha1 "github.com/kai-scheduler/kai-resource-management-api/kai/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// NamespaceConflicts returns, for every namespace claimed by more than one Run:ai project,
// the sorted names of the projects claiming it. Projects in a conflict get no BNK config at
// all: a tenant bundle bound to the wrong project would leak one tenant's traffic to another.
// A project that is being deleted still counts while it holds the finalizer, because its BNK
// config may still exist until the cleanup finishes.
func NamespaceConflicts(projects []kaiv1alpha1.Project) map[string][]string {
	byNS := map[string][]string{}
	for _, p := range projects {
		if p.Status.Namespace == "" {
			continue
		}
		if !p.DeletionTimestamp.IsZero() && !controllerutil.ContainsFinalizer(&p, Finalizer) {
			continue
		}
		byNS[p.Status.Namespace] = append(byNS[p.Status.Namespace], p.Name)
	}
	out := map[string][]string{}
	for ns, names := range byNS {
		if len(names) > 1 {
			sort.Strings(names)
			out[ns] = names
		}
	}
	return out
}
