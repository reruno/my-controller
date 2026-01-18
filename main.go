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
	CheckTimeStartAnnotation  = "mycontrollercheck/checktimestart"
	CheckTimeEndAnnotation    = "mycontrollercheck/checktimeend"
	CheckTimeUpdateAnnotation = "mycontrollercheck/checktimeupdate"
	CheckNumAnnotaion         = "mycontrollercheck/checknum"
)

type PodReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.Log.WithName("reconciler").WithValues("pod", req.NamespacedName)

	log.Info("Starting reconcile loop")

	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		if client.IgnoreNotFound(err) != nil {
			log.Error(err, "Failed to fetch Pod")
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if pod.Annotations == nil {
		log.Info("Annotations were nil, initializing map")
		pod.Annotations = map[string]string{}
	}

	if _, ok := pod.Annotations[CheckTimeEndAnnotation]; ok {
		log.Info("Skipping reconcile: CheckTimeEndAnnotation present")
		return ctrl.Result{}, nil
	}

	patch := client.MergeFrom(pod.DeepCopy())

	timeStr, ok := pod.Annotations[CheckTimeUpdateAnnotation]
	if !ok {
		now := time.Now()
		log.Info("First run detected. Initializing annotations.", "start_time", now.Format(time.RFC3339))

		pod.Annotations[CheckTimeUpdateAnnotation] = now.Format(time.RFC3339)
		pod.Annotations[CheckTimeStartAnnotation] = now.Format(time.RFC3339)
		pod.Annotations[CheckNumAnnotaion] = "0"

		if err := r.Patch(ctx, &pod, patch); err != nil {
			log.Error(err, "Failed to patch initial annotations")
			return ctrl.Result{}, err
		}
		log.Info("Successfully initialized pod")
		return ctrl.Result{}, nil
	}

	updateTime, err := time.Parse(time.RFC3339, timeStr)
	if err != nil {
		log.Error(err, "Failed to parse update time", "malformed_value", timeStr)
		return ctrl.Result{}, err
	}

	nextUpdateTime := updateTime.Add(time.Second)
	now := time.Now()
	delta := nextUpdateTime.Sub(now)

	log.Info("Calculated timing",
		"last_update", updateTime.Format(time.RFC3339),
		"next_target", nextUpdateTime.Format(time.RFC3339),
		"current_time", now.Format(time.RFC3339),
		"delta_ms", delta.Milliseconds(),
	)

	if delta <= 0 {
		numStr, ok := pod.Annotations[CheckNumAnnotaion]
		if ok {
			checknum, err := strconv.Atoi(numStr)
			if err != nil {
				log.Error(err, "Failed to parse checknum", "malformed_value", numStr)
				return ctrl.Result{}, err
			}

			if checknum >= 120 {
				log.Info("Target count reached (120). Marking complete.")
				pod.Annotations[CheckTimeEndAnnotation] = now.Format(time.RFC3339)

				if err := r.Patch(ctx, &pod, patch); err != nil {
					log.Error(err, "Failed to patch completion annotation")
					return ctrl.Result{}, err
				}
				return ctrl.Result{}, nil
			}

			oldNum := checknum
			checknum++
			log.Info("Incrementing counter", "from", oldNum, "to", checknum)

			pod.Annotations[CheckNumAnnotaion] = fmt.Sprintf("%d", checknum)
			pod.Annotations[CheckTimeUpdateAnnotation] = now.Format(time.RFC3339)

			if err = r.Patch(ctx, &pod, patch); err != nil {
				log.Error(err, "Failed to patch update")
				return ctrl.Result{}, err
			}
			log.Info("Successfully patched update")
			return ctrl.Result{}, nil

		} else {
			err := fmt.Errorf("inconsistent state: time exists but number missing")
			log.Error(err, "Cannot proceed")
			return ctrl.Result{}, nil
		}
	} else {
		log.Info("Throttling: Waiting for next slot", "requeue_after", delta.String())
		return ctrl.Result{RequeueAfter: delta}, nil
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

	podReconciler := &PodReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}
	err = podReconciler.SetupWithManager(mgr)
	if err != nil {
		panic(err)
	}
	fmt.Println("Starting controller in namespace:", namespace)
	err = mgr.Start(ctrl.SetupSignalHandler())
	if err != nil {
		panic(err)
	}
}
