package meter

import (
	"strconv"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// Label caps: past these, new values are folded into "other" so a bad sender can't blow up Prometheus.
const (
	MaxModels      = 64
	MaxProjects    = 1024
	MaxDepartments = 256
	Other          = "other"
	// NoModel labels records without a model (refusals, bodies without a model field).
	NoModel = "none"
)

// Metrics holds the meter's Prometheus series and the per-project token deltas not yet written back.
type Metrics struct {
	Tokens      *prometheus.CounterVec
	Requests    *prometheus.CounterVec
	Malformed   prometheus.Counter
	Rejected    prometheus.Counter
	Departments *prometheus.GaugeVec

	mu       sync.Mutex
	models   capped
	projects capped
	depts    capped
	pending  map[string]int64
}

// NewMetrics creates the series and registers them with reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		Tokens: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "bnk_tokens_total", Help: "Tokens metered at the BNK front door per Run:ai project.",
		}, []string{"project", "department", "model", "direction"}),
		Requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "bnk_requests_total", Help: "Responses metered at the BNK front door per Run:ai project and status code.",
		}, []string{"project", "department", "model", "code"}),
		Malformed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "bnk_meter_malformed_lines_total", Help: "Usage lines the meter could not parse and ignored.",
		}),
		Rejected: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "bnk_meter_rejected_connections_total", Help: "Usage connections dropped: peer not allowed or bad auth line.",
		}),
		Departments: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "bnk_department_tokens_total", Help: "Tokens of all projects of a Run:ai department, as written back to the Department.",
		}, []string{"department"}),
		models:   capped{max: MaxModels},
		projects: capped{max: MaxProjects},
		depts:    capped{max: MaxDepartments},
		pending:  map[string]int64{},
	}
	reg.MustRegister(m.Tokens, m.Requests, m.Malformed, m.Rejected, m.Departments)
	return m
}

// Observe counts one parsed record.
func (m *Metrics) Observe(r Record) {
	m.mu.Lock()
	defer m.mu.Unlock()
	model := r.Model
	if model == "" {
		model = NoModel
	} else {
		model = m.models.label(model)
	}
	project := m.projects.label(r.Project)
	dept := r.Department
	if dept != "" {
		dept = m.depts.label(dept)
	}
	// Answers without a model (429s, /v1/models, health checks) carry no tokens: count the request
	// but create no zero-valued token series for them.
	if model != NoModel || r.Prompt > 0 || r.Completion > 0 {
		m.Tokens.WithLabelValues(project, dept, model, "prompt").Add(float64(r.Prompt))
		m.Tokens.WithLabelValues(project, dept, model, "completion").Add(float64(r.Completion))
	}
	m.Requests.WithLabelValues(project, dept, model, strconv.Itoa(r.Code)).Add(float64(r.Count))
	// Write-back keys are real project names; "other" is never a Run:ai project.
	if r.Total > 0 && project != Other {
		m.pending[r.Project] += r.Total
	}
}

// TakePending returns and clears the token totals observed since the last call.
func (m *Metrics) TakePending() map[string]int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.pending
	m.pending = map[string]int64{}
	return p
}

// Requeue adds back deltas that could not be written, so they go out with the next pass.
func (m *Metrics) Requeue(project string, tokens int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pending[project] += tokens
}

// capped hands out label values until max distinct ones were seen, then Other.
type capped struct {
	max  int
	seen map[string]struct{}
}

func (c *capped) label(v string) string {
	if _, ok := c.seen[v]; ok {
		return v
	}
	if c.seen == nil {
		c.seen = map[string]struct{}{}
	}
	if len(c.seen) >= c.max {
		return Other
	}
	c.seen[v] = struct{}{}
	return v
}
