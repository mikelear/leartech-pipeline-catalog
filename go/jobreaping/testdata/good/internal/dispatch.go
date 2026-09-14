package internal

import batchv1 "k8s.io/api/batch/v1"

func build() *batchv1.Job {
	ttl := int32(3600)
	return &batchv1.Job{Spec: batchv1.JobSpec{TTLSecondsAfterFinished: &ttl}}
}
