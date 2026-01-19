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
	"fmt"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
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
	"infra.essity.com/orchstrator-api/internal/bootstrapjob"
)

const (
	// Finalizer name
	finalizerName = "infra.essity.com/tfrun-finalizer"

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
// 2. Handle creation and deletion of remote workspaces ( for cloud backends / azure / aws )
// 3. Handles deletion by creating destroy Jobs
// 4. Computes spec hash to detect changes
// 5. Creates and tracks tofu Jobs (init/plan/apply) ( TTL based jobs )
// 6. Updates status based on Job lifecycle 
// 7. Implements idempotency - does not recreate Jobs unnecessarily ( Based on spec has and Interval )
// TODO: 
// 1. Import existing state handling
// 2. workspace idempotency handling
// 3. workspace delete lock handling ( only for cloud backends )

func (r *TfRunReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("=== Reconciliation Started ===", "namespace", req.Namespace, "name", req.Name)

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

	// Ensure finalizer is added, requeue if not present
	if err := r.ensureFinalizer(ctx, tfRun); err != nil {
		logger.Error(err, "failed to ensure finalizer")
		return ctrl.Result{
			Requeue: true,
		}, err
	}

	// Handle deletion
	if !tfRun.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, tfRun)
	}

	// Compute current spec hash
	logger.V(1).Info("computing spec hash")
	currentSpecHash, err := r.computeSpecHash(tfRun)
	if err != nil {
		logger.Error(err, "failed to compute spec hash")
		return ctrl.Result{}, err
	}

	logger.Info("computed spec hash", "hash", currentSpecHash, "previousHash", tfRun.Status.LastSpecHash)

	// Reconcile backend workspace if not already done
	if tfRun.Status.WorkspaceID == "" && tfRun.Spec.Backend.Cloud != nil {
		if result, err := r.reconcileWorkspace(ctx, tfRun); err != nil {
			logger.Error(err, "failed to reconcile backend workspace")
			return result, err
		}
	}

	logger.Info("backend workspace already exists", "workspaceID", tfRun.Status.WorkspaceID)

	// Check if there's an active Job
	logger.Info("active job name check", "activeJobName", tfRun.Status.ActiveJobName)
	if tfRun.Status.ActiveJobName != "" {
		return r.updateJobStatus(ctx, tfRun)
	}

	// Check if spec has changed
	specChanged := tfRun.Status.LastSpecHash != currentSpecHash ||
		tfRun.Status.ObservedGeneration != tfRun.Generation

	// If spec changed, create new job immediately
	if specChanged {
		logger.Info("Spec changed, creating new job immediately",
			"hashChanged", tfRun.Status.LastSpecHash != currentSpecHash,
			"generationChanged", tfRun.Status.ObservedGeneration != tfRun.Generation)
		return r.createNewJob(ctx, tfRun, currentSpecHash)
	}

	// Spec hasn't changed - check if we need to handle interval-based runs
	if tfRun.Spec.RunInterval != nil {
		return r.handleIntervalBasedRun(ctx, tfRun, currentSpecHash)
	}

	// No interval configured and spec unchanged - nothing to do
	logger.Info("Spec unchanged and no run interval configured, skipping reconciliation",
		"phase", tfRun.Status.Phase,
		"generation", tfRun.Generation,
		"observedGeneration", tfRun.Status.ObservedGeneration)

	return ctrl.Result{}, nil
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
		// Add finalizer and update
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
			//TODO: I commented this to test requeueing of destroy job on failure
			// return r.removeFinalizer(ctx, tfRun)
			return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
		}

		// Job is still running
		logger.Info("Destroy Job is still running", "jobName", job.Name)
		return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
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

	return ctrl.Result{RequeueAfter: 10 * time.Minute}, nil
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

// handleIntervalBasedRun handles periodic runs based on runInterval
func (r *TfRunReconciler) handleIntervalBasedRun(ctx context.Context, tfRun *infrav1alpha1.TfRun, currentSpecHash string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// If this is the first run (no LastRunTime), set NextRunTime and return
	if tfRun.Status.LastRunTime == nil {
		logger.Info("First run - no LastRunTime set yet")
		return ctrl.Result{}, nil
	}

	// Calculate next run time
	nextRunTime := tfRun.Status.LastRunTime.Add(tfRun.Spec.RunInterval.Time.Duration)

	// Update NextRunTime in status if not set or different
	if tfRun.Status.NextRunTime == nil || !tfRun.Status.NextRunTime.Time.Equal(nextRunTime) {
		logger.Info("Updating NextRunTime in status", "nextRunTime", nextRunTime)
		tfRun.Status.NextRunTime = &metav1.Time{Time: nextRunTime}
		if err := r.Status().Update(ctx, tfRun); err != nil {
			logger.Error(err, "Failed to update NextRunTime in status")
			return ctrl.Result{}, err
		}
	}

	// Check if it's time to run
	now := time.Now()
	if now.Before(nextRunTime) {
		timeUntilNext := time.Until(nextRunTime)
		logger.Info("Run interval not yet elapsed, requeuing",
			"nextRunTime", nextRunTime,
			"timeUntilNext", timeUntilNext)
		return ctrl.Result{RequeueAfter: timeUntilNext}, nil
	}

	// Time to create a new job
	logger.Info("Run interval elapsed, creating new job",
		"lastRunTime", tfRun.Status.LastRunTime,
		"nextRunTime", nextRunTime,
		"currentTime", now)

	return r.createNewJob(ctx, tfRun, currentSpecHash)
}

// createNewJob creates a new apply job for the TfRun
func (r *TfRunReconciler) createNewJob(ctx context.Context, tfRun *infrav1alpha1.TfRun, currentSpecHash string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Creating new job")

	applyJob, err := bootstrapjob.ForEngine(r.Client, strings.ToLower(tfRun.Spec.Engine.Type), []string{})
	if err != nil {
		logger.Error(err, "Failed to get engine Job builder for apply")
		return ctrl.Result{}, err
	}

	job, err := applyJob.BuildJob(ctx, tfRun, jobTypeApply)
	if err != nil {
		logger.Error(err, "Failed to build job template for tfrun")
		tfRun.Status.Phase = PhaseFailed
		tfRun.Status.Message = fmt.Sprintf("failed to build Job: %v", err)
		_, _ = r.updateStatus(ctx, tfRun)
		return ctrl.Result{}, err
	}

	// Set TfRun as owner of the Job
	if err := controllerutil.SetControllerReference(tfRun, job, r.Scheme); err != nil {
		logger.Error(err, "Failed to set controller reference")
		return ctrl.Result{}, err
	}

	// Create the Job
	return r.createJobAndUpdateStatus(ctx, tfRun, job, currentSpecHash)
}

// create job and update status
func (r *TfRunReconciler) createJobAndUpdateStatus(ctx context.Context, tfRun *infrav1alpha1.TfRun, job *batchv1.Job, currentSpecHash string) (ctrl.Result, error) {

	logger := log.FromContext(ctx)

	// Create the Job
	if err := r.Create(ctx, job); err != nil {
		if apierrors.IsAlreadyExists(err) {
			logger.Info("job already exists", "jobName", job.Name)
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

	logger.Info("created tfrun job", "jobName", job.Name)
	tfRun.Status.ActiveJobName = job.Name
	tfRun.Status.LastSpecHash = currentSpecHash
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

	if err := r.Status().Update(ctx, tfRun); err != nil {
		logger.Error(err, "failed to update TfRun status after job creation")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}
