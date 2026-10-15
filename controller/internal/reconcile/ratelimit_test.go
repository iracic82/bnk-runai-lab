package reconcile

import (
	"testing"

	"github.com/f5devcentral/bnk-runai-tenancy/internal/tenant"
)

func f64(v float64) *float64 { return &v }
func i32(v int32) *int32     { return &v }

func TestRateLimit(t *testing.T) {
	p := DefaultPolicy()
	cases := []struct {
		name string
		spec tenant.Spec
		want float64
	}{
		{"tenant-a 2 GPU", tenant.Spec{GPUDeserved: 2}, 40},
		{"tenant-b half GPU", tenant.Spec{GPUDeserved: 0.5}, 10},
		// A project without a GPU quota gets base_rps × 0.25, never unlimited.
		{"no GPU quota", tenant.Spec{GPUDeserved: 0}, 5},
		{"below floor", tenant.Spec{GPUDeserved: 0.1}, 5},
		{"annotation override wins", tenant.Spec{GPUDeserved: 2, RPSOverride: f64(7)}, 7},
	}
	for _, c := range cases {
		if got := p.RateLimit(c.spec); got != c.want {
			t.Errorf("%s: RateLimit = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCriticality(t *testing.T) {
	p := DefaultPolicy()
	sh := tenant.Sheddable
	cases := []struct {
		name string
		spec tenant.Spec
		want tenant.Criticality
	}{
		{"high priority", tenant.Spec{Priority: i32(150)}, tenant.Critical},
		{"at threshold", tenant.Spec{Priority: i32(100)}, tenant.Critical},
		{"low priority", tenant.Spec{Priority: i32(50)}, tenant.Sheddable},
		{"no priority", tenant.Spec{}, tenant.Standard},
		{"override", tenant.Spec{Priority: i32(150), CriticalityOverride: &sh}, tenant.Sheddable},
	}
	for _, c := range cases {
		if got := p.Criticality(c.spec); got != c.want {
			t.Errorf("%s: Criticality = %v, want %v", c.name, got, c.want)
		}
	}
}
