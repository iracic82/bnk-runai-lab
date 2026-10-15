package reconcile

import (
	"reflect"
	"testing"

	kaiv1alpha1 "github.com/kai-scheduler/kai-resource-management-api/kai/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func project(name, ns string) kaiv1alpha1.Project {
	return kaiv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     kaiv1alpha1.ProjectStatus{Namespace: ns, Phase: kaiv1alpha1.Ready},
	}
}

func TestNamespaceConflicts(t *testing.T) {
	now := metav1.Now()
	// Deleted and already cleaned up: no longer a claimant.
	released := project("old", "runai-shared")
	released.DeletionTimestamp = &now
	// Deleted but BNK cleanup not finished: still a claimant.
	draining := project("draining", "runai-other")
	draining.DeletionTimestamp = &now
	draining.Finalizers = []string{Finalizer}

	got := NamespaceConflicts([]kaiv1alpha1.Project{
		project("tenant-a", "runai-tenant-a"),
		project("x", "runai-shared"),
		project("y", "runai-shared"),
		project("pending", ""),
		released,
		draining,
		project("newcomer", "runai-other"),
	})
	want := map[string][]string{"runai-shared": {"x", "y"}, "runai-other": {"draining", "newcomer"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NamespaceConflicts = %v, want %v", got, want)
	}
}

func TestNoConflicts(t *testing.T) {
	got := NamespaceConflicts([]kaiv1alpha1.Project{
		project("tenant-a", "runai-tenant-a"),
		project("tenant-b", "runai-tenant-b"),
	})
	if len(got) != 0 {
		t.Fatalf("expected no conflicts, got %v", got)
	}
}
