package reconcile

import (
	"context"
	"fmt"
	"strings"
	"time"

	kaiv1alpha1 "github.com/kai-scheduler/kai-resource-management-api/kai/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/f5devcentral/bnk-runai-tenancy/internal/tenant"
)

// Finalizer holds a Run:ai project until its BNK tenant is gone. A finalizer is used instead
// of ownerReferences because the project is cluster-scoped and the BNK objects are namespaced.
const Finalizer = "tenancy.f5.com/bundle"

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
// +kubebuilder:rbac:groups=kai.resources,resources=projects,verbs=update;patch
// +kubebuilder:rbac:groups=scheduling.run.ai,resources=queues,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *ProjectReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("project", req.Name)

	var p kaiv1alpha1.Project
	if err := r.Get(ctx, req.NamespacedName, &p); err != nil {
		if apierrors.IsNotFound(err) {
			r.setState(req.Name, "")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Project being deleted in Run:ai: remove the BNK tenant first, then release the project.
	if !p.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&p, Finalizer) {
			return ctrl.Result{}, nil
		}
		r.setState(p.Name, StateDraining)
		if err := r.Applier.Delete(ctx, p.Name); err != nil {
			r.event(&p, corev1.EventTypeWarning, "BNKDeleteFailed", err.Error())
			return ctrl.Result{}, err
		}
		controllerutil.RemoveFinalizer(&p, Finalizer)
		if err := r.Update(ctx, &p); err != nil {
			return ctrl.Result{}, err
		}
		r.setState(p.Name, "")
		logger.Info("BNK tenant removed")
		return ctrl.Result{}, nil
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
			msg := fmt.Sprintf("namespace %s is claimed by projects %s; no BNK config written", ns, strings.Join(names, ", "))
			r.setState(p.Name, StateError)
			r.event(&p, corev1.EventTypeWarning, "NamespaceConflict", msg)
			return ctrl.Result{}, nil
		}
	}

	if !controllerutil.ContainsFinalizer(&p, Finalizer) {
		controllerutil.AddFinalizer(&p, Finalizer)
		if err := r.Update(ctx, &p); err != nil {
			return ctrl.Result{}, err
		}
	}

	r.setState(p.Name, StateProvisioning)
	d := Desired{Tenant: spec, RateLimit: r.Policy.RateLimit(spec), Criticality: r.Policy.Criticality(spec)}
	if err := r.Applier.Apply(ctx, d); err != nil {
		r.event(&p, corev1.EventTypeWarning, "BNKApplyFailed", err.Error())
		return ctrl.Result{}, err
	}
	r.setState(p.Name, StateServing)
	r.event(&p, corev1.EventTypeNormal, "BNKTenantReady",
		fmt.Sprintf("BNK tenant serving namespace %s at %.1f req/s (%s)", spec.Namespace, d.RateLimit, d.Criticality))
	return ctrl.Result{}, nil
}

// release removes the BNK tenant and the finalizer, for projects opted out with tenancy.f5.com/skip.
func (r *ProjectReconciler) release(ctx context.Context, p *kaiv1alpha1.Project) error {
	if !controllerutil.ContainsFinalizer(p, Finalizer) {
		return nil
	}
	if err := r.Applier.Delete(ctx, p.Name); err != nil {
		return err
	}
	controllerutil.RemoveFinalizer(p, Finalizer)
	return r.Update(ctx, p)
}

func (r *ProjectReconciler) requeue() time.Duration {
	if r.Requeue > 0 {
		return r.Requeue
	}
	return 5 * time.Second
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
		Named("runai-project-tenancy").
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
