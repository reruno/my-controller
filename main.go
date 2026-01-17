package main

import (
	"context"
	"fmt"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

const (
	TargetLabel         = "mycontrollercheck"
	ExpiryAnnotation    = "mycontrollercheck/expiry"
	ProcessedAnnotation = "mycontrollercheck/processed" // To prevent infinite loops
)

type PodReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.Log.WithName("reconciler")

	// 1. Fetch the Pod
	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 2. Check if we already finished with this Pod to avoid infinite loop
	// (Add -> Wait -> Remove -> Add again...)
	if pod.Annotations[ProcessedAnnotation] == "true" {
		return ctrl.Result{}, nil
	}

	currentTime := time.Now()

	// SCENARIO A: Label is MISSING. We need to add it.
	if pod.Labels[TargetLabel] != "true" {
		log.Info("Adding label to pod", "pod", pod.Name)

		if pod.Labels == nil {
			pod.Labels = map[string]string{}
		}
		if pod.Annotations == nil {
			pod.Annotations = map[string]string{}
		}

		// Add Label
		pod.Labels[TargetLabel] = "true"
		// Set Expiry 15 mins from now
		expiryTime := currentTime.Add(1 * time.Minute)
		pod.Annotations[ExpiryAnnotation] = expiryTime.Format(time.RFC3339)

		if err := r.Update(ctx, &pod); err != nil {
			return ctrl.Result{}, err
		}

		// IMPORTANT: Tell K8s to trigger this loop again in 15 mins
		return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
	}

	// SCENARIO B: Label is PRESENT. Check if time is up.
	expiryStr := pod.Annotations[ExpiryAnnotation]
	expiryTime, err := time.Parse(time.RFC3339, expiryStr)
	if err != nil {
		// If date is invalid, just remove the label to be safe
		log.Error(err, "Invalid expiry time, cleaning up")
		expiryTime = currentTime
	}

	if currentTime.After(expiryTime) {
		// Time is up! Remove the label.
		log.Info("Time is up! Removing label", "pod", pod.Name)
		delete(pod.Labels, TargetLabel)
		// Mark as processed so we don't add it again immediately
		pod.Annotations[ProcessedAnnotation] = "true"

		if err := r.Update(ctx, &pod); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Time is NOT up yet (maybe the controller restarted). Requeue for remaining time.
	remaining := expiryTime.Sub(currentTime)
	return ctrl.Result{RequeueAfter: remaining}, nil
}

func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&corev1.Pod{}).Complete(r)
}

func main() {
	ctrl.SetLogger(zap.New())

	// Read namespace from Pod environment (Downward API)
	namespace := os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		namespace = "default" // Fallback
	}

	// Configure Manager to only watch the specific namespace
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: runtime.NewScheme(),
		// This restricts the cache to only this namespace
		// Note: newer versions use Cache: cache.Options{DefaultNamespaces: ...}
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{
				namespace: {}, // Watch only this namespace
			},
		},
	})
	if err != nil {
		panic(err)
	}
	err = corev1.AddToScheme(mgr.GetScheme())
	if err != nil {
		panic(err)
	}
	err = (&PodReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr)
	if err != nil {
		panic(err)
	}
	fmt.Println("Starting controller in namespace:", namespace)
	err = mgr.Start(ctrl.SetupSignalHandler())
	if err != nil {
		panic(err)
	}
}
