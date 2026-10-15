package reconcile

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	kaiv1alpha1 "github.com/kai-scheduler/kai-resource-management-api/kai/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/f5devcentral/bnk-runai-tenancy/internal/tenant"
)

// The controller never writes to Run:ai objects: Run:ai's admission webhook lets only Run:ai's own
// service accounts change projects and departments (runai-cluster/cluster/agent/pkg/validator/
// project_validator.go). So there is no finalizer. A deleted project is noticed by its absence,
// and its BNK objects are removed by label; Sweep does the same for anything orphaned while the
// controller was down.

// Desired is what the BNK side must look like for one Run:ai project.
type Desired struct {
	Tenant      tenant.Spec
	RateLimit   float64
	Criticality tenant.Criticality
}

// BundleApplier writes and removes a tenant's BNK objects. The real implementation lives in
// internal/bnk and is written only against BNK 2.4 names verified on a running cluster
// (docs/verified-names.md). Apply must be idempotent; Delete must succeed when nothing exists.
type BundleApplier interface {
	Apply(ctx context.Context, d Desired) error
	Delete(ctx context.Context, project string) error
	// Projects lists the Run:ai projects that currently have BNK objects.
	Projects(ctx context.Context) ([]string, error)
}

// ProjectReconciler keeps one BNK tenant per Run:ai project.
type ProjectReconciler struct {
	client.Client
	Applier  BundleApplier
	Policy   Policy
	Recorder record.EventRecorder
	State    StateGauge
	// Requeue is how soon a not-yet-ready project is looked at again.
	Requeue time.Duration

	// served remembers what each project was last served with, so BNKTenantReady is posted once per
	// change instead of on every resync.
	mu     sync.Mutex
	served map[string]Desired
}

// StateGauge exposes bnk_tenant_state{project,state}. A nil gauge is fine.
type StateGauge interface {
	Set(project, state string)
	Forget(project string)
}

// Tenant states, as reported by bnk_tenant_state and project Events.
const (
	StatePending      = "Pending"
	StateProvisioning = "Provisioning"
	StateServing      = "Serving"
	StateDraining     = "Draining"
	StateError        = "Error"
	StateSkipped      = "Skipped"
)

// +kubebuilder:rbac:groups=kai.resources,resources=projects;departments,verbs=get;list;watch
// +kubebuilder:rbac:groups=scheduling.run.ai,resources=queues,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *ProjectReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var p kaiv1alpha1.Project
	if err := r.Get(ctx, req.NamespacedName, &p); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, r.remove(ctx, req.Name)
		}
		return ctrl.Result{}, err
	}

	// Being deleted in Run:ai: take the BNK tenant down now rather than when the object is gone.
	if !p.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, r.remove(ctx, p.Name)
	}

	if !tenant.Ready(&p) {
		r.setState(p.Name, StatePending)
		return ctrl.Result{RequeueAfter: r.requeue()}, nil
	}

	spec, err := tenant.FromProject(&p)
	if err != nil {
		r.setState(p.Name, StateError)
		r.event(&p, corev1.EventTypeWarning, "InvalidAnnotation", err.Error())
		return ctrl.Result{}, nil // the user fixes the annotation; that update re-triggers us
	}

	// The engine (vLLM or NVIDIA Dynamo) is declared on the namespace Run:ai created for the project,
	// by whoever deploys the workload there; the controller never guesses it from pods.
	var ns corev1.Namespace
	if err := r.Get(ctx, client.ObjectKey{Name: spec.Namespace}, &ns); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		r.setState(p.Name, StatePending)
		return ctrl.Result{RequeueAfter: r.requeue()}, nil
	}
	if spec, err = tenant.WithNamespace(spec, ns.Annotations); err != nil {
		r.setState(p.Name, StateError)
		r.event(&p, corev1.EventTypeWarning, "InvalidAnnotation", err.Error())
		return ctrl.Result{}, nil
	}

	if spec.Skip {
		if err := r.release(ctx, &p); err != nil {
			return ctrl.Result{}, err
		}
		r.setState(p.Name, StateSkipped)
		return ctrl.Result{}, nil
	}

	var all kaiv1alpha1.ProjectList
	if err := r.List(ctx, &all); err != nil {
		return ctrl.Result{}, err
	}
	for ns, names := range NamespaceConflicts(all.Items) {
		if ns == spec.Namespace {
			// Fail closed: a tenant that was already serving must lose its BNK config, or
			// pods of the other project in the same namespace would receive its traffic.
			if err := r.Applier.Delete(ctx, p.Name); err != nil {
				r.event(&p, corev1.EventTypeWarning, "BNKDeleteFailed", err.Error())
				return ctrl.Result{}, err
			}
			msg := fmt.Sprintf("namespace %s is claimed by projects %s; BNK config removed", ns, strings.Join(names, ", "))
			r.setState(p.Name, StateError)
			r.event(&p, corev1.EventTypeWarning, "NamespaceConflict", msg)
			return ctrl.Result{}, nil
		}
	}

	r.setState(p.Name, StateProvisioning)
	d := Desired{Tenant: spec, RateLimit: r.Policy.RateLimit(spec), Criticality: r.Policy.Criticality(spec)}
	if err := r.Applier.Apply(ctx, d); err != nil {
		r.event(&p, corev1.EventTypeWarning, "BNKApplyFailed", err.Error())
		return ctrl.Result{}, err
	}
	r.setState(p.Name, StateServing)
	if r.changed(p.Name, d) {
		r.event(&p, corev1.EventTypeNormal, "BNKTenantReady",
			fmt.Sprintf("BNK tenant serving namespace %s at %.1f req/s (%s), endpoint picker engine %s",
				spec.Namespace, d.RateLimit, d.Criticality, spec.Engine))
	}
	return ctrl.Result{}, nil
}

// release removes the BNK tenant of a project opted out with tenancy.f5.com/skip.
func (r *ProjectReconciler) release(ctx context.Context, p *kaiv1alpha1.Project) error {
	return r.Applier.Delete(ctx, p.Name)
}

// remove takes a project's BNK tenant down. Delete is a no-op when nothing exists.
func (r *ProjectReconciler) remove(ctx context.Context, project string) error {
	r.setState(project, StateDraining)
	if err := r.Applier.Delete(ctx, project); err != nil {
		return err
	}
	r.setState(project, "")
	r.mu.Lock()
	delete(r.served, project)
	r.mu.Unlock()
	log.FromContext(ctx).Info("BNK tenant removed", "project", project)
	return nil
}

// Sweep removes BNK tenants whose Run:ai project no longer exists. It runs once at start-up,
// covering deletes that happened while the controller was down; after that, watch events do it.
func (r *ProjectReconciler) Sweep(ctx context.Context) error {
	owned, err := r.Applier.Projects(ctx)
	if err != nil {
		return err
	}
	for _, name := range owned {
		var p kaiv1alpha1.Project
		err := r.Get(ctx, client.ObjectKey{Name: name}, &p)
		if apierrors.IsNotFound(err) {
			if err := r.remove(ctx, name); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *ProjectReconciler) requeue() time.Duration {
	if r.Requeue > 0 {
		return r.Requeue
	}
	return 5 * time.Second
}

// changed records d as what project is served with and reports whether it differs from last time.
func (r *ProjectReconciler) changed(project string, d Desired) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.served == nil {
		r.served = map[string]Desired{}
	}
	if prev, ok := r.served[project]; ok && reflect.DeepEqual(prev, d) {
		return false
	}
	r.served[project] = d
	return true
}

func (r *ProjectReconciler) setState(project, state string) {
	if r.State == nil {
		return
	}
	if state == "" {
		r.State.Forget(project)
		return
	}
	r.State.Set(project, state)
}

func (r *ProjectReconciler) event(p *kaiv1alpha1.Project, kind, reason, msg string) {
	if r.Recorder != nil {
		r.Recorder.Event(p, kind, reason, msg)
	}
}

// SetupWithManager watches Run:ai projects. A change to any project re-checks every project
// that shares its namespace, so a conflict appearing or clearing is picked up on both sides.
func (r *ProjectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kaiv1alpha1.Project{}).
		Watches(&kaiv1alpha1.Project{}, handler.EnqueueRequestsFromMapFunc(r.sameNamespace)).
		// Annotating a project's namespace (the engine) re-renders that project's tenant.
		Watches(&corev1.Namespace{}, handler.EnqueueRequestsFromMapFunc(r.projectsInNamespace)).
		Named("runai-project-tenancy").
		// Failures retry quickly and never wait more than 30s, so a fixed cause (a missing CRD, a
		// secret, BNK still starting) is picked up without restarting the controller.
		WithOptions(controller.Options{RateLimiter: workqueue.NewTypedItemExponentialFailureRateLimiter[ctrl.Request](time.Second, 30*time.Second)}).
		Complete(r)
}

func (r *ProjectReconciler) sameNamespace(ctx context.Context, obj client.Object) []ctrl.Request {
	p, ok := obj.(*kaiv1alpha1.Project)
	if !ok || p.Status.Namespace == "" {
		return nil
	}
	var all kaiv1alpha1.ProjectList
	if err := r.List(ctx, &all); err != nil {
		return nil
	}
	var out []ctrl.Request
	for _, o := range all.Items {
		if o.Name != p.Name && o.Status.Namespace == p.Status.Namespace {
			out = append(out, ctrl.Request{NamespacedName: client.ObjectKey{Name: o.Name}})
		}
	}
	return out
}

func (r *ProjectReconciler) projectsInNamespace(ctx context.Context, obj client.Object) []ctrl.Request {
	var all kaiv1alpha1.ProjectList
	if err := r.List(ctx, &all); err != nil {
		return nil
	}
	var out []ctrl.Request
	for _, o := range all.Items {
		if o.Status.Namespace == obj.GetName() {
			out = append(out, ctrl.Request{NamespacedName: client.ObjectKey{Name: o.Name}})
		}
	}
	return out
}
