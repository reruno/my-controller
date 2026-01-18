package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

const (
	StartCheckTimeAnnotation = "mycontrollercheck/startchecktime"
	EndCheckTimeAnnotation   = "mycontrollercheck/endchecktime"
	CheckNumAnnotaions       = "mycontrollercheck/checknum"
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
	var ok bool
	if _, ok = pod.Annotations[EndCheckTimeAnnotation]; ok {
		return ctrl.Result{}, nil
	}
	var checknum int = 0
	if strNum, ok := pod.Annotations[CheckNumAnnotaions]; ok {
		num, err := strconv.Atoi(strNum)
		if err != nil {
			return ctrl.Result{}, err
		}
		checknum = num
		checknum++
		pod.Annotations[CheckNumAnnotaions] = fmt.Sprintf("%d", checknum)
	} else {
		pod.Annotations[StartCheckTimeAnnotation] = time.Now().Format(time.RFC3339)
		checknum = 1
		pod.Annotations[CheckNumAnnotaions] = fmt.Sprintf("%d", checknum)
	}

	if checknum >= 100 {
		pod.Annotations[EndCheckTimeAnnotation] = time.Now().Format(time.RFC3339)
		r.Update(ctx, &pod)
		return ctrl.Result{}, nil
	} else {
		log.Info("Checks are done", "pod", pod.Name)
		r.Update(ctx, &pod)
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}
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
