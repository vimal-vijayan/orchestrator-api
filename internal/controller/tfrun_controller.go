/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	infrav1alpha1 "infra.essity.com/orchstrator-api/api/v1alpha1"
	"infra.essity.com/orchstrator-api/internal/backend"
	"infra.essity.com/orchstrator-api/internal/bootstrapjob"
)

const (
	// Finalizer name
	finalizerName = "tofu-operator.io/finalizer"

	// Phase constants
	PhasePending   = "Pending"
	PhaseRunning   = "Running"
	PhaseSucceeded = "Succeeded"
	PhaseFailed    = "Failed"

	// Condition types
	ConditionTypeReady     = "Ready"
	ConditionTypeApplied   = "Applied"
	ConditionTypeDestroyed = "Destroyed"

	// Job labels
	jobTypeApply   = "apply"
	jobTypeDestroy = "destroy"
)

// TfRunReconciler reconciles a TfRun object
type TfRunReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *TfRunReconciler) getCloudBackend(ctx context.Context, tfRun *infrav1alpha1.TfRun) (backend.CloudBackend, error) {
	logger := log.FromContext(ctx)

	if tfRun.Spec.Backend.Cloud == nil {
		return nil, fmt.Errorf("cloud backend configuration is required")
	}

	provider := tfRun.Spec.Backend.Cloud.Provider
	if provider == "" {
		logger.V(1).Info("No cloud backend provider specified, defaulting to 'scalr'")
		provider = backend.ProviderScalr
	}

	logger.V(1).Info("Getting cloud backend implementation", "provider", provider)
	return backend.ForProvider(r.Client, provider)
}

// +kubebuilder:rbac:groups=infra.essity.com,resources=tfruns,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infra.essity.com,resources=tfruns/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infra.essity.com,resources=tfruns/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// The controller:
// 1. Ensures finalizer is present for proper cleanup
// 2. Handles deletion by creating destroy Jobs
// 3. Computes spec hash to detect changes
// 4. Creates and tracks tofu Jobs (init/plan/apply)
// 5. Updates status based on Job lifecycle
// 6. Implements idempotency - does not recreate Jobs unnecessarily
func (r *TfRunReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("=== Reconciliation started ===", "namespace", req.Namespace, "name", req.Name)

	// Fetch the TfRun instance
	tfRun := &infrav1alpha1.TfRun{}
	if err := r.Get(ctx, req.NamespacedName, tfRun); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("TfRun instance deleted, skipping reconciliation")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get TfRun")
		return ctrl.Result{}, err
	}

	logger.Info("TfRun fetched", "generation", tfRun.Generation, "phase", tfRun.Status.Phase, "deletionTimestamp", tfRun.DeletionTimestamp)

	// Ensure finalizer is present
	if err := r.ensureFinalizer(ctx, tfRun); err != nil {
		logger.Error(err, "Failed to ensure finalizer")
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !tfRun.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, tfRun)
	}

	// Compute current spec hash
	logger.V(1).Info("Computing spec hash")
	currentSpecHash, err := r.computeSpecHash(tfRun)
	if err != nil {
		logger.Error(err, "Failed to compute spec hash")
		return ctrl.Result{}, err
	}
	logger.Info("Computed spec hash", "hash", currentSpecHash, "previousHash", tfRun.Status.LastSpecHash)

	// Check if there's an active Job
	if tfRun.Status.ActiveJobName != "" {
		// Track the active Job
		job := &batchv1.Job{}
		jobKey := types.NamespacedName{
			Name:      tfRun.Status.ActiveJobName,
			Namespace: tfRun.Namespace,
		}

		if err := r.Get(ctx, jobKey, job); err != nil {
			if apierrors.IsNotFound(err) {
				// Job was deleted, clear active job name
				logger.Info("Active job no longer exists", "jobName", tfRun.Status.ActiveJobName)
				tfRun.Status.ActiveJobName = ""
				tfRun.Status.Phase = PhaseFailed
				tfRun.Status.Message = "Job was deleted unexpectedly"
				return r.updateStatus(ctx, tfRun)
			}
			logger.Error(err, "Failed to get active Job")
			return ctrl.Result{}, err
		}

		// Update status based on Job state
		logger.V(1).Info("Checking Job status", "active", job.Status.Active, "succeeded", job.Status.Succeeded, "failed", job.Status.Failed)
		if r.isJobActive(job) {
			logger.Info("Job is active", "jobName", job.Name)
			tfRun.Status.Phase = PhaseRunning
			tfRun.Status.Message = fmt.Sprintf("Job %s is running", job.Name)
			meta.SetStatusCondition(&tfRun.Status.Conditions, metav1.Condition{
				Type:               ConditionTypeApplied,
				Status:             metav1.ConditionFalse,
				Reason:             "InProgress",
				Message:            "tofu Job is running",
				ObservedGeneration: tfRun.Generation,
			})
		} else if r.isJobSucceeded(job) {
			logger.Info("Job succeeded", "jobName", job.Name)
			tfRun.Status.Phase = PhaseSucceeded
			tfRun.Status.ActiveJobName = ""
			tfRun.Status.LastSpecHash = currentSpecHash
			tfRun.Status.LastRunTime = &metav1.Time{Time: time.Now()}
			tfRun.Status.Message = "tofu apply succeeded"
			meta.SetStatusCondition(&tfRun.Status.Conditions, metav1.Condition{
				Type:               ConditionTypeApplied,
				Status:             metav1.ConditionTrue,
				Reason:             "ApplySucceeded",
				Message:            "tofu apply completed successfully",
				ObservedGeneration: tfRun.Generation,
			})
			meta.SetStatusCondition(&tfRun.Status.Conditions, metav1.Condition{
				Type:               ConditionTypeReady,
				Status:             metav1.ConditionTrue,
				Reason:             "Ready",
				Message:            "tofuRun is ready",
				ObservedGeneration: tfRun.Generation,
			})
		} else if r.isJobFailed(job) {
			logger.Info("Job failed", "jobName", job.Name)
			tfRun.Status.Phase = PhaseFailed
			tfRun.Status.ActiveJobName = ""
			tfRun.Status.LastSpecHash = currentSpecHash
			tfRun.Status.Message = "tofu apply failed"
			meta.SetStatusCondition(&tfRun.Status.Conditions, metav1.Condition{
				Type:               ConditionTypeApplied,
				Status:             metav1.ConditionFalse,
				Reason:             "ApplyFailed",
				Message:            "tofu apply failed",
				ObservedGeneration: tfRun.Generation,
			})
			meta.SetStatusCondition(&tfRun.Status.Conditions, metav1.Condition{
				Type:               ConditionTypeReady,
				Status:             metav1.ConditionFalse,
				Reason:             "Failed",
				Message:            "tofuRun failed",
				ObservedGeneration: tfRun.Generation,
			})
		}

		tfRun.Status.ObservedGeneration = tfRun.Generation
		return r.updateStatus(ctx, tfRun)
	}

	// No active Job - decide if we need to create one
	// Skip if spec hasn't changed and we've already processed this generation
	if tfRun.Status.LastSpecHash == currentSpecHash &&
		tfRun.Status.ObservedGeneration == tfRun.Generation {
		logger.Info("Spec unchanged and already processed, skipping reconciliation",
			"phase", tfRun.Status.Phase,
			"generation", tfRun.Generation,
			"observedGeneration", tfRun.Status.ObservedGeneration,
			"lastRunTime", tfRun.Status.LastRunTime)
		return ctrl.Result{}, nil
	}

	logger.Info("creating new Job", "reason", "spec changed or first run", "hashChanged", tfRun.Status.LastSpecHash != currentSpecHash,"previousPhase", tfRun.Status.Phase)
	logger.Info("creating new tfrun job")

	// Create Scalr workspace if not already created
	if tfRun.Status.WorkspaceID == "" && tfRun.Spec.Backend.Cloud != nil {
		logger.Info("ensuring backend workspace exists")
		be, err := r.getCloudBackend(ctx, tfRun)
		if err != nil {
			logger.Error(err, "Failed to get cloud backend")
			tfRun.Status.Phase = PhaseFailed
			tfRun.Status.Message = fmt.Sprintf("Failed to get cloud backend: %v", err)
			_, _ = r.updateStatus(ctx, tfRun)
			return ctrl.Result{}, err
		}

		workspaceID, err := be.EnsureWorkspace(ctx, tfRun)
		if err != nil {
			logger.Error(err, "Failed to create Scalr workspace")
			tfRun.Status.Phase = PhaseFailed
			tfRun.Status.Message = fmt.Sprintf("Failed to create Scalr workspace: %v", err)
			_, _ = r.updateStatus(ctx, tfRun)
			return ctrl.Result{}, err
		}

		tfRun.Status.WorkspaceID = workspaceID
		tfRun.Status.Phase = PhasePending
		tfRun.Status.Message = "backend workspace ensured"
		tfRun.Status.ObservedGeneration = tfRun.Generation

		logger.Info("backend workspace created, updating status", "workspaceID", workspaceID)
		if err := r.Status().Update(ctx, tfRun); err != nil {
			logger.Error(err, "Failed to update status with workspace ID")
			return ctrl.Result{}, err
		}
	} else if tfRun.Status.WorkspaceID != "" {
		logger.Info("Using existing backend workspace", "workspaceID", tfRun.Status.WorkspaceID)
	}

	// Create a new apply Job
	logger.Info("Creating new tofu apply Job")
	// job, err := r.buildtofuJob(ctx, tfRun, jobTypeApply)
	// FIXME: pass the jobEngine from tfRun spec
	applyJob, err := bootstrapjob.ForEngine(r.Client, "opentofu", []string{})
	if err != nil {
		logger.Error(err, "Failed to get tofu Job builder for apply")
		return ctrl.Result{}, err
	}
	job, err := applyJob.BuildJob(ctx, tfRun, jobTypeApply)
	if err != nil {
		logger.Error(err, "Failed to build tofu Job")
		tfRun.Status.Phase = PhaseFailed
		tfRun.Status.Message = fmt.Sprintf("Failed to build Job: %v", err)
		_, _ = r.updateStatus(ctx, tfRun)
		return ctrl.Result{}, err
	}

	// Set TfRun as owner of the Job
	if err := controllerutil.SetControllerReference(tfRun, job, r.Scheme); err != nil {
		logger.Error(err, "Failed to set controller reference")
		return ctrl.Result{}, err
	}

	// Create the Job
	if err := r.Create(ctx, job); err != nil {
		if apierrors.IsAlreadyExists(err) {
			logger.Info("Job already exists", "jobName", job.Name)
			tfRun.Status.ActiveJobName = job.Name
			tfRun.Status.Phase = PhaseRunning
			tfRun.Status.ObservedGeneration = tfRun.Generation
			return r.updateStatus(ctx, tfRun)
		}
		logger.Error(err, "Failed to create Job")
		tfRun.Status.Phase = PhaseFailed
		tfRun.Status.Message = fmt.Sprintf("Failed to create Job: %v", err)
		_, _ = r.updateStatus(ctx, tfRun)
		return ctrl.Result{}, err
	}

	logger.Info("Created tofu Job", "jobName", job.Name)
	tfRun.Status.ActiveJobName = job.Name
	tfRun.Status.Phase = PhaseRunning
	tfRun.Status.ObservedGeneration = tfRun.Generation
	tfRun.Status.Message = fmt.Sprintf("Created Job %s", job.Name)
	meta.SetStatusCondition(&tfRun.Status.Conditions, metav1.Condition{
		Type:               ConditionTypeApplied,
		Status:             metav1.ConditionFalse,
		Reason:             "JobCreated",
		Message:            "tofu Job created",
		ObservedGeneration: tfRun.Generation,
	})

	return r.updateStatus(ctx, tfRun)
}

// SetupWithManager sets up the controller with the Manager.
func (r *TfRunReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1alpha1.TfRun{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}

// ensureFinalizer ensures the finalizer is present on the TfRun
func (r *TfRunReconciler) ensureFinalizer(ctx context.Context, tfRun *infrav1alpha1.TfRun) error {
	logger := log.FromContext(ctx)
	if !controllerutil.ContainsFinalizer(tfRun, finalizerName) {
		logger.Info("Adding finalizer", "finalizer", finalizerName)
		controllerutil.AddFinalizer(tfRun, finalizerName)
		if err := r.Update(ctx, tfRun); err != nil {
			return fmt.Errorf("failed to add finalizer: %w", err)
		}
		logger.Info("Finalizer added successfully")
	} else {
		logger.V(1).Info("Finalizer already present")
	}
	return nil
}

// handleDeletion handles the deletion of a TfRun by creating a destroy Job
func (r *TfRunReconciler) handleDeletion(ctx context.Context, tfRun *infrav1alpha1.TfRun) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Handling TfRun deletion")

	// Check if there's an active destroy Job
	if tfRun.Status.ActiveJobName != "" {
		// Check Job status
		job := &batchv1.Job{}
		jobKey := types.NamespacedName{
			Name:      tfRun.Status.ActiveJobName,
			Namespace: tfRun.Namespace,
		}

		if err := r.Get(ctx, jobKey, job); err != nil {
			if apierrors.IsNotFound(err) {
				// Job was deleted, proceed with finalizer removal
				logger.Info("Destroy job no longer exists, removing finalizer")
				return r.removeFinalizer(ctx, tfRun)
			}
			return ctrl.Result{}, err
		}

		// Check if destroy Job succeeded
		if r.isJobSucceeded(job) {
			logger.Info("Destroy Job succeeded")

			// Delete backend workspace after successful destroy
			if tfRun.Status.WorkspaceID != "" && tfRun.Spec.Backend.Cloud != nil {
				logger.Info("Deleting backend workspace", "workspaceID", tfRun.Status.WorkspaceID)
				be, err := r.getCloudBackend(ctx, tfRun)
				if err != nil {
					logger.Error(err, "Failed to get cloud backend for workspace deletion")
					// Don't fail the deletion if backend retrieval fails
				} else {
					if err := be.DeleteWorkspace(ctx, tfRun, tfRun.Status.WorkspaceID); err != nil {
						logger.Error(err, "Failed to delete backend workspace", "workspaceID", tfRun.Status.WorkspaceID)
						// Don't fail the deletion if workspace deletion fails
					} else {
						logger.Info("remote workspace deleted successfully")
					}
				}
			}

			return r.removeFinalizer(ctx, tfRun)
		}

		// Check if destroy Job failed
		if r.isJobFailed(job) {
			logger.Error(nil, "Destroy Job failed, removing finalizer to prevent stuck resource")
			// Remove finalizer even on failure to prevent stuck resources
			return r.removeFinalizer(ctx, tfRun)
		}

		// Job is still running
		logger.Info("Destroy Job is still running", "jobName", job.Name)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// No active destroy Job - create one
	logger.Info("Creating tofu destroy Job")
	destroyJob, err := bootstrapjob.ForEngine(r.Client, "opentofu", []string{})
	if err != nil {
		logger.Error(err, "Failed to get tofu Job builder for destroy")
		return ctrl.Result{}, err
	}

	job, err := destroyJob.BuildJob(ctx, tfRun, jobTypeDestroy)
	if err != nil {
		logger.Error(err, "Failed to build destroy Job")
		return ctrl.Result{}, err
	}

	// Set TfRun as owner
	if err := controllerutil.SetControllerReference(tfRun, job, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}

	// Create the destroy Job
	if err := r.Create(ctx, job); err != nil {
		if apierrors.IsAlreadyExists(err) {
			logger.Info("Destroy Job already exists", "jobName", job.Name)
			// Update status to track the job
			tfRun.Status.ActiveJobName = job.Name
			tfRun.Status.Phase = PhaseRunning
			if err := r.Status().Update(ctx, tfRun); err != nil {
				logger.V(1).Info("Failed to update status (resource may be deleted)", "error", err)
			}
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
		return ctrl.Result{}, err
	}

	logger.Info("Created destroy Job", "jobName", job.Name)
	tfRun.Status.ActiveJobName = job.Name
	tfRun.Status.Phase = PhaseRunning
	tfRun.Status.Message = fmt.Sprintf("Created destroy Job %s", job.Name)
	meta.SetStatusCondition(&tfRun.Status.Conditions, metav1.Condition{
		Type:               ConditionTypeDestroyed,
		Status:             metav1.ConditionFalse,
		Reason:             "DestroyJobCreated",
		Message:            "tofu destroy Job created",
		ObservedGeneration: tfRun.Generation,
	})
	if err := r.Status().Update(ctx, tfRun); err != nil {
		logger.V(1).Info("Failed to update status (resource may be deleted)", "error", err)
	}

	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

// removeFinalizer removes the finalizer with retry logic
func (r *TfRunReconciler) removeFinalizer(ctx context.Context, tfRun *infrav1alpha1.TfRun) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Re-fetch the resource to get the latest version
	latest := &infrav1alpha1.TfRun{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(tfRun), latest); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Resource already deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Remove finalizer from the fresh copy
	if controllerutil.ContainsFinalizer(latest, finalizerName) {
		controllerutil.RemoveFinalizer(latest, finalizerName)
		if err := r.Update(ctx, latest); err != nil {
			if apierrors.IsNotFound(err) {
				logger.Info("Resource deleted during finalizer removal")
				return ctrl.Result{}, nil
			}
			if apierrors.IsConflict(err) {
				logger.Info("Conflict during finalizer removal, will retry")
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, err
		}
		logger.Info("Finalizer removed successfully")
	} else {
		logger.V(1).Info("Finalizer already removed")
	}

	return ctrl.Result{}, nil
}

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
	logger.Info("Updating status",
		"phase", tfRun.Status.Phase,
		"activeJobName", tfRun.Status.ActiveJobName,
		"message", tfRun.Status.Message,
		"observedGeneration", tfRun.Status.ObservedGeneration)
	if err := r.Status().Update(ctx, tfRun); err != nil {
		logger.Error(err, "Failed to update status")
		return ctrl.Result{}, err
	}
	logger.V(1).Info("Status updated successfully, requeuing after 10 seconds")
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}
