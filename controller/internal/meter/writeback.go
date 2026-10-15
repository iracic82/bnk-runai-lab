package meter

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	kaiv1alpha1 "github.com/kai-scheduler/kai-resource-management-api/kai/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// LedgerName is the ConfigMap holding durable token totals:
//
//	data["project.<name>"]    = total tokens metered for that Run:ai project
//	data["department.<name>"] = sum over the projects whose spec.parent names it
//
// Run:ai's admission webhook lets only Run:ai's own service accounts write Projects and Departments,
// so totals cannot live on those objects. Each pass also records a TokensMetered Event against the
// Run:ai Project and Department, which `kubectl describe` shows beside them.
const LedgerName = "bnk-runai-usage"

// Writeback keeps project and department token totals in the ledger. Each pass adds what was metered
// since the last one, so a meter restart loses at most one interval of unwritten tokens and never
// resets a total.
type Writeback struct {
	Client    client.Client
	Namespace string // where the ledger lives
	Recorder  record.EventRecorder
	Metrics   *Metrics
	Log       *slog.Logger
	Now       func() time.Time
}

// Run writes back every interval until ctx is done, with a final pass on the way out.
func (w *Writeback) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			w.Once(c)
			cancel()
			return
		case <-t.C:
			w.Once(ctx)
		}
	}
}

// Once adds pending deltas to the ledger, then rolls projects up into departments.
func (w *Writeback) Once(ctx context.Context) {
	pending := w.Metrics.TakePending()
	var projects kaiv1alpha1.ProjectList
	if err := w.Client.List(ctx, &projects); err != nil {
		w.Log.Warn("list Run:ai projects, retrying next pass", "err", err)
		w.requeue(pending)
		return
	}
	byName := map[string]*kaiv1alpha1.Project{}
	for i := range projects.Items {
		byName[projects.Items[i].Name] = &projects.Items[i]
	}

	var totals map[string]string
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cm, err := w.ledger(ctx)
		if err != nil {
			return err
		}
		if cm.Data == nil {
			cm.Data = map[string]string{}
		}
		for project, delta := range pending {
			if _, ok := byName[project]; !ok {
				w.Log.Info("usage for unknown Run:ai project dropped", "project", project, "tokens", delta)
				continue
			}
			cm.Data["project."+project] = strconv.FormatInt(Tokens(cm.Data["project."+project])+delta, 10)
		}
		depts := map[string]int64{}
		for name, p := range byName {
			if p.Spec.Parent != "" {
				depts[p.Spec.Parent] += Tokens(cm.Data["project."+name])
			}
		}
		for d, total := range depts {
			cm.Data["department."+d] = strconv.FormatInt(total, 10)
		}
		cm.Data["updated"] = w.now().UTC().Format(time.RFC3339)
		totals = cm.Data
		return w.Client.Update(ctx, cm)
	})
	if err != nil {
		w.Log.Warn("ledger write failed, retrying next pass", "err", err)
		w.requeue(pending)
		return
	}

	for key, v := range totals {
		if d, ok := strings.CutPrefix(key, "department."); ok {
			w.Metrics.Departments.WithLabelValues(d).Set(float64(Tokens(v)))
		}
	}
	if w.Recorder == nil {
		return
	}
	for project := range pending {
		p, ok := byName[project]
		if !ok {
			continue
		}
		w.Recorder.Event(p, corev1.EventTypeNormal, "TokensMetered",
			fmt.Sprintf("%s tokens metered at the BNK edge in total", totals["project."+project]))
		if p.Spec.Parent != "" {
			d := &kaiv1alpha1.Department{}
			if err := w.Client.Get(ctx, client.ObjectKey{Name: p.Spec.Parent}, d); err == nil {
				w.Recorder.Event(d, corev1.EventTypeNormal, "TokensMetered",
					fmt.Sprintf("%s tokens metered at the BNK edge across the department's projects", totals["department."+p.Spec.Parent]))
			}
		}
	}
}

func (w *Writeback) requeue(pending map[string]int64) {
	for p, d := range pending {
		w.Metrics.Requeue(p, d)
	}
}

// ledger returns the ledger ConfigMap, creating it on first use.
func (w *Writeback) ledger(ctx context.Context) (*corev1.ConfigMap, error) {
	cm := &corev1.ConfigMap{}
	key := client.ObjectKey{Namespace: w.Namespace, Name: LedgerName}
	err := w.Client.Get(ctx, key, cm)
	if apierrors.IsNotFound(err) {
		cm = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name,
			Labels: map[string]string{"app.kubernetes.io/managed-by": "bnk-runai-tenancy"}}}
		if err := w.Client.Create(ctx, cm); err != nil && !apierrors.IsAlreadyExists(err) {
			return nil, err
		}
		err = w.Client.Get(ctx, key, cm)
	}
	return cm, err
}

func (w *Writeback) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// Tokens parses a stored total; missing or unreadable is 0.
func Tokens(v string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
