package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	cnatv1alpha1 "cnat/api/v1alpha1"
	"cnat/pkg/schedule"
	"cnat/pkg/spawn"

	corev1 "k8s.io/api/core/v1"
)

// AtReconciler reconciles a At object
type AtReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logr.Logger
}

//+kubebuilder:rbac:groups=cnat.ishankhare.dev,resources=ats,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=cnat.ishankhare.dev,resources=ats/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=cnat.ishankhare.dev,resources=ats/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the At object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.2/pkg/reconcile
func (r *AtReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// reqLogger := r.Log.WithValues("at", req.NamespacedName)
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()
	reqLogger := logger.Sugar()

	reqLogger.Info("=== Reconciling At")

	// your logic here

	instance := &cnatv1alpha1.At{}
	err := r.Get(context.TODO(), req.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// object not found, could have been deleted after
			// reconcile request, hence don't requeue
			return ctrl.Result{}, nil
		}

		// error reading the object, requeue the request
		return ctrl.Result{}, err
	}

	// if no phase set, default to Pending
	if instance.Status.Phase == "" {
		instance.Status.Phase = cnatv1alpha1.PhasePending
	}

	// state transition PENDING -> RUNNING -> DONE
	switch instance.Status.Phase {
	case cnatv1alpha1.PhasePending:
		reqLogger.Info("Phase: PENDING")

		diff, err := schedule.TimeUntilSchedule(instance.Spec.Schedule)
		if err != nil {
			reqLogger.Error(err, "Schedule parsing failure")

			return ctrl.Result{}, err
		}

		reqLogger.Info("Schedule parsing done", " Result", fmt.Sprintf("%v", diff))

		if diff > 0 {
			// not yet time to execute, wait until scheduled time
			// return ctrl.Result{RequeueAfter: diff * time.Second}, nil
			return ctrl.Result{RequeueAfter: diff}, nil
		}

		reqLogger.Info("It's time!", "Ready to execute", instance.Spec.Command)
		// change state
		instance.Status.Phase = cnatv1alpha1.PhaseRunning
	case cnatv1alpha1.PhaseRunning:
		reqLogger.Info("Phase: RUNNING")

		pod := spawn.NewPodForCR(instance)
		err := ctrl.SetControllerReference(instance, pod, r.Scheme)
		if err != nil {
			// requeue with error
			return ctrl.Result{}, err
		}

		query := &corev1.Pod{}
		// try to see if the pod already exists
		err = r.Get(context.TODO(), req.NamespacedName, query)

		reqLogger.Info("Query Phase status ", query.Status)

		if err != nil && errors.IsNotFound(err) {
			// does not exist, create a pod
			err = r.Create(context.TODO(), pod)
			if err != nil {
				return ctrl.Result{}, err
			}

			// Successfully created a Pod
			reqLogger.Info("Pod Created successfully", "name", pod.Name)
			return ctrl.Result{}, nil
		} else if err != nil {
			// requeue with err
			reqLogger.Error(err, "cannot create pod")
			return ctrl.Result{}, err
		} else if query.Status.Phase == corev1.PodFailed ||
			query.Status.Phase == corev1.PodSucceeded {
			// pod already finished or errored out`
			reqLogger.Info("Container terminated", "reason", query.Status.Reason,
				"message", query.Status.Message)
			instance.Status.Phase = cnatv1alpha1.PhaseDone
		} else {
			reqLogger.Info("Fell into else", query.Status)
			// don't requeue, it will happen automatically when
			// pod status changes
			return ctrl.Result{}, nil
		}
	case cnatv1alpha1.PhaseDone:
		reqLogger.Info("Phase: DONE")
		// reconcile without requeuing
		return ctrl.Result{}, nil
	default:
		reqLogger.Info("NOP")
		return ctrl.Result{}, nil
	}

	// update status
	err = r.Status().Update(context.TODO(), instance)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AtReconciler) SetupWithManager(mgr ctrl.Manager) error {
	err := ctrl.NewControllerManagedBy(mgr).
		For(&cnatv1alpha1.At{}).
		Owns(&corev1.Pod{}).
		Complete(r)

	if err != nil {
		return err
	}
	return nil
}
