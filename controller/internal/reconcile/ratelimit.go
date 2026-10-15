package reconcile

import "github.com/f5devcentral/bnk-runai-tenancy/internal/tenant"

// Policy holds the Helm-configurable knobs that turn a Run:ai quota into BNK limits.
type Policy struct {
	// BaseRPS is the request rate granted per deserved GPU.
	BaseRPS float64
	// MinGPUFactor is the floor applied to the GPU quota, so a project without a GPU quota
	// still gets a small limit instead of none or unlimited.
	MinGPUFactor float64
	// CriticalPriorityThreshold: queue priority at or above it maps to Critical.
	CriticalPriorityThreshold int32
}

// DefaultPolicy matches contracts/tenancy-controller.md.
func DefaultPolicy() Policy {
	return Policy{BaseRPS: 20, MinGPUFactor: 0.25, CriticalPriorityThreshold: 100}
}

// RateLimit is each tenant's share of the shared GPU estate, expressed as requests per
// second at the BNK front door: rps = BaseRPS × max(gpuDeserved, MinGPUFactor).
// An explicit tenancy.f5.com/rps annotation on the Run:ai project wins.
func (p Policy) RateLimit(s tenant.Spec) float64 {
	if s.RPSOverride != nil {
		return *s.RPSOverride
	}
	g := s.GPUDeserved
	if g < p.MinGPUFactor {
		g = p.MinGPUFactor
	}
	return p.BaseRPS * g
}

// Criticality maps the Run:ai queue priority to a BNK inference objective level.
// A project with no queue priority is Standard.
func (p Policy) Criticality(s tenant.Spec) tenant.Criticality {
	if s.CriticalityOverride != nil {
		return *s.CriticalityOverride
	}
	if s.Priority == nil {
		return tenant.Standard
	}
	if *s.Priority >= p.CriticalPriorityThreshold {
		return tenant.Critical
	}
	return tenant.Sheddable
}
