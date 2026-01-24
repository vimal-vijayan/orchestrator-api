package controller

import (
	"context"
	"time"

	infrav1alpha1 "infra.essity.com/orchestrator-api/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"crypto/sha256"
	"encoding/json"
	"fmt"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// computeSpecHash computes a hash of the TfRun spec to detect changes
func (r *TfRunReconciler) computeSpecHash(tfRun *infrav1alpha1.TfRun) (string, error) {
	// Create a struct with fields that should trigger a new run
	hashInput := struct {
		Module    string
		Ref       string
		Path      string
		Vars      map[string]*apiextensionsv1.JSON
		Arguments map[string]string
		Backend   infrav1alpha1.TfBackend
	}{
		Module:    tfRun.Spec.Source.Module,
		Ref:       tfRun.Spec.Source.Ref,
		Path:      tfRun.Spec.Source.Path,
		Vars:      tfRun.Spec.Vars,
		Arguments: tfRun.Spec.Arguments,
		Backend:   tfRun.Spec.Backend,
	}

	data, err := json.Marshal(hashInput)
	if err != nil {
		return "", fmt.Errorf("failed to marshal spec: %w", err)
	}

	hash := sha256.Sum256(data)
	hashStr := fmt.Sprintf("%x", hash[:8])
	return hashStr, nil
}

// isJobActive checks if a Job is currently running
func (r *TfRunReconciler) isJobActive(job *batchv1.Job) bool {
	return job.Status.Active > 0
}

// isJobSucceeded checks if a Job has succeeded
func (r *TfRunReconciler) isJobSucceeded(job *batchv1.Job) bool {
	return job.Status.Succeeded > 0
}

// isJobFailed checks if a Job has failed
func (r *TfRunReconciler) isJobFailed(job *batchv1.Job) bool {
	return job.Status.Failed > 0
}

// updateStatus updates the TfRun status
func (r *TfRunReconciler) updateStatus(ctx context.Context, tfRun *infrav1alpha1.TfRun) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Updating status", "phase", tfRun.Status.Phase, "message", tfRun.Status.Message, "observedGeneration", tfRun.Status.ObservedGeneration)
	if err := r.Status().Update(ctx, tfRun); err != nil {
		logger.Error(err, "failed to update status")
		return ctrl.Result{RequeueAfter: 5 * time.Second}, err
	}
	logger.V(1).Info("status updated successfully")
	return ctrl.Result{}, nil
}
