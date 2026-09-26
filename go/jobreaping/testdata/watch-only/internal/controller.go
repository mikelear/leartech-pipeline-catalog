package internal

import (
	batchv1 "k8s.io/api/batch/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

// The real-world shape this fixture exists for: a controller file that WATCHES
// Jobs and constructs none. Owns() declares the ownership that makes Kubernetes
// garbage-collect them, so flagging it is backwards.
func setup(mgr ctrl.Manager, r reconciler) error {
	return ctrl.NewControllerManagedBy(mgr).
		Owns(&batchv1.Job{}).
		Complete(r)
}

// An empty literal as a Get target is the other common type witness.
func fetch(r reconciler) *batchv1.Job { return &batchv1.Job{} }

type reconciler interface{}
