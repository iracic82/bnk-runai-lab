package tenant

import (
	"testing"

	kaiv1alpha1 "github.com/kai-scheduler/kai-resource-management-api/kai/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func i32(v int32) *int32 { return &v }

func runaiProject(ann map[string]string) *kaiv1alpha1.Project {
	return &kaiv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "tenant-a", Annotations: ann},
		Spec: kaiv1alpha1.ProjectSpec{
			Parent: "acme",
			Queues: []kaiv1alpha1.QueueConfig{
				{Name: "tenant-a", Nodepool: "default", Priority: i32(50),
					Resources: &kaiv1alpha1.QueueResourcesConfig{GPU: kaiv1alpha1.SystemResource{Deserved: 0.5}}},
				{Name: "tenant-a-gpu", Nodepool: "gpu", Priority: i32(150),
					Resources: &kaiv1alpha1.QueueResourcesConfig{GPU: kaiv1alpha1.SystemResource{Deserved: 1.5}}},
				{Name: "tenant-a-cpu", Nodepool: "cpu"},
			},
		},
		// Run:ai picked the namespace; it is deliberately unrelated to the project name.
		Status: kaiv1alpha1.ProjectStatus{Namespace: "kai-tenant-a-x7", Phase: kaiv1alpha1.Ready},
	}
}

func TestFromProject(t *testing.T) {
	s, err := FromProject(runaiProject(nil))
	if err != nil {
		t.Fatal(err)
	}
	if s.Project != "tenant-a" || s.Department != "acme" {
		t.Errorf("identity = %q/%q", s.Project, s.Department)
	}
	if s.Namespace != "kai-tenant-a-x7" {
		t.Errorf("namespace must come from status.namespace, got %q", s.Namespace)
	}
	if s.GPUDeserved != 2 {
		t.Errorf("GPUDeserved = %v, want 2 (summed across node-pool queues)", s.GPUDeserved)
	}
	if s.Priority == nil || *s.Priority != 150 {
		t.Errorf("Priority = %v, want highest queue priority 150", s.Priority)
	}
	if s.RPSOverride != nil || s.CriticalityOverride != nil || s.Skip {
		t.Errorf("unexpected overrides: %+v", s)
	}
}

func TestFromProjectAnnotations(t *testing.T) {
	s, err := FromProject(runaiProject(map[string]string{
		AnnotationRPS: "12.5", AnnotationCriticality: "Sheddable", AnnotationSkip: "TRUE",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if s.RPSOverride == nil || *s.RPSOverride != 12.5 {
		t.Errorf("RPSOverride = %v", s.RPSOverride)
	}
	if s.CriticalityOverride == nil || *s.CriticalityOverride != Sheddable {
		t.Errorf("CriticalityOverride = %v", s.CriticalityOverride)
	}
	if !s.Skip {
		t.Error("Skip should be true")
	}
}

func TestFromProjectBadAnnotations(t *testing.T) {
	for _, ann := range []map[string]string{
		{AnnotationRPS: "lots"},
		{AnnotationRPS: "-3"},
		{AnnotationCriticality: "urgent"},
	} {
		if _, err := FromProject(runaiProject(ann)); err == nil {
			t.Errorf("expected error for %v", ann)
		}
	}
}

func TestReady(t *testing.T) {
	p := runaiProject(nil)
	if !Ready(p) {
		t.Error("Ready project with namespace should be ready")
	}
	p.Status.Namespace = ""
	if Ready(p) {
		t.Error("no namespace yet: not ready")
	}
	p.Status.Namespace = "ns"
	p.Status.Phase = kaiv1alpha1.NotReady
	if Ready(p) {
		t.Error("NotReady phase: not ready")
	}
}

func TestWithNamespace(t *testing.T) {
	base, _ := FromProject(runaiProject(nil))
	if base.Engine != EngineVLLM {
		t.Fatalf("default engine = %q", base.Engine)
	}
	cases := []struct {
		ann         map[string]string
		engine, dyn string
		wantErr     bool
	}{
		{nil, EngineVLLM, "", false},
		{map[string]string{AnnotationEngine: "vllm"}, EngineVLLM, "", false},
		{map[string]string{AnnotationEngine: "dynamo", AnnotationDynamoGraph: "mocker"}, EngineDynamo, "mocker", false},
		{map[string]string{AnnotationEngine: "dynamo"}, "", "", true},
		{map[string]string{AnnotationEngine: "dynamo", AnnotationDynamoGraph: "Bad Name"}, "", "", true},
		{map[string]string{AnnotationEngine: "sglang"}, "", "", true},
	}
	for _, c := range cases {
		s, err := WithNamespace(base, c.ann)
		if (err != nil) != c.wantErr {
			t.Errorf("%v: err = %v", c.ann, err)
			continue
		}
		if !c.wantErr && (s.Engine != c.engine || s.DynamoGraph != c.dyn) {
			t.Errorf("%v: got %q/%q", c.ann, s.Engine, s.DynamoGraph)
		}
	}
}
