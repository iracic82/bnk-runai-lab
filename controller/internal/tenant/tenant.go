// Package tenant turns a Run:ai project into the tenant description the BNK side consumes.
//
// Run:ai stores projects and departments on the cluster as kai.resources/v1alpha1 objects
// (the runai-chart ships them through its resource-management dependency). The tenant's
// namespace is read from Project.status.namespace, never derived from the project name.
package tenant

import (
	"fmt"
	"strconv"
	"strings"

	kaiv1alpha1 "github.com/kai-scheduler/kai-resource-management-api/kai/v1alpha1"
)

// Annotations on the Run:ai project that override derived values.
const (
	AnnotationRPS         = "tenancy.f5.com/rps"
	AnnotationCriticality = "tenancy.f5.com/criticality"
	AnnotationSkip        = "tenancy.f5.com/skip"
)

// Criticality is the BNK inference objective level a tenant maps to.
type Criticality string

const (
	Critical  Criticality = "Critical"
	Standard  Criticality = "Standard"
	Sheddable Criticality = "Sheddable"
)

// Spec is everything the BNK bundle needs to know about one Run:ai project.
type Spec struct {
	Project     string
	Department  string
	Namespace   string
	GPUDeserved float64
	// Priority is the highest queue priority on the project; nil when no queue sets one.
	Priority *int32
	// RPSOverride and CriticalityOverride come from project annotations; nil when unset.
	RPSOverride         *float64
	CriticalityOverride *Criticality
	Skip                bool
}

// Ready reports whether Run:ai has finished setting the project up, so BNK config can be written.
func Ready(p *kaiv1alpha1.Project) bool {
	return p.Status.Phase == kaiv1alpha1.Ready && p.Status.Namespace != ""
}

// FromProject builds a Spec from a Run:ai project. GPU quota is summed over all of the
// project's queues (one per node pool), which is the project's total deserved GPU share.
func FromProject(p *kaiv1alpha1.Project) (Spec, error) {
	s := Spec{
		Project:    p.Name,
		Department: p.Spec.Parent,
		Namespace:  p.Status.Namespace,
	}
	for _, q := range p.Spec.Queues {
		if q.Resources != nil {
			s.GPUDeserved += q.Resources.GPU.Deserved
		}
		if q.Priority != nil && (s.Priority == nil || *q.Priority > *s.Priority) {
			v := *q.Priority
			s.Priority = &v
		}
	}

	ann := p.GetAnnotations()
	if v, ok := ann[AnnotationRPS]; ok {
		rps, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil || rps <= 0 {
			return s, fmt.Errorf("annotation %s=%q must be a positive number", AnnotationRPS, v)
		}
		s.RPSOverride = &rps
	}
	if v, ok := ann[AnnotationCriticality]; ok {
		c := Criticality(strings.TrimSpace(v))
		switch c {
		case Critical, Standard, Sheddable:
			s.CriticalityOverride = &c
		default:
			return s, fmt.Errorf("annotation %s=%q must be Critical, Standard or Sheddable", AnnotationCriticality, v)
		}
	}
	s.Skip = strings.EqualFold(strings.TrimSpace(ann[AnnotationSkip]), "true")
	return s, nil
}
