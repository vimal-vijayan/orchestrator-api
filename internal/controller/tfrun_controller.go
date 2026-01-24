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

	infrav1alpha1 "infra.essity.com/orchestrator-api/api/v1alpha1"
	"infra.essity.com/orchestrator-api/internal/bootstrapjob"
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
	ConditionTypeReady   = "Ready"
	ConditionTypeApplied = "Applied"

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
// 2. workspace idempotency handling
// 3. workspace delete lock handling ( only for cloud backends )

func (r *TfRunReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("=== Reconciliation Started ===", "namespace", req.Namespace, "name", req.Name)

	tfRun := &infrav1alpha1.TfRun{}
	if err := r.Get(ctx, req.NamespacedName, tfRun); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("TfRun instance deleted, skipping reconciliation")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get TfRun")
		return ctrl.Result{}, err
	}

	if err := r.ensureFinalizer(ctx, tfRun); err != nil {
		logger.Error(err, "failed to ensure finalizer")
		return ctrl.Result{Requeue: true}, err
	}

	if !tfRun.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, tfRun)
	}

	logger.V(1).Info("computing spec hash")
	currentSpecHash, err := r.computeSpecHash(tfRun)
	if err != nil {
		logger.Error(err, "failed to compute spec hash")
		return ctrl.Result{}, err
	}
	logger.Info("computed spec hash", "hash", currentSpecHash, "previousHash", tfRun.Status.LastSpecHash)

	if result, err := r.ensureWorkspaceIfNeeded(ctx, tfRun); err != nil || result.Requeue || result.RequeueAfter > 0 {
		if err != nil {
			logger.Error(err, "failed to reconcile backend workspace")
		}
		return result, err
	}

	logger.Info("active job name check", "activeJobName", tfRun.Status.ActiveJobName)
	activeJob, activeJobRunning := r.getActiveJobIfAny(ctx, tfRun)

	if result, handled, err := r.handleSpecChange(ctx, tfRun, currentSpecHash, activeJob, activeJobRunning); handled {
		return result, err
	}

	if activeJobRunning {
		logger.Info("An active job is still running, waiting for it to complete", "activeJobName", activeJob.Name)
		return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
	}

	if !activeJobRunning && activeJob != nil {
		// Update status based on completed job
		return r.updateStatusBasedOnCompletedJob(ctx, tfRun, activeJob)
	}

	if result, handled, err := r.handlePendingExecution(ctx, tfRun); handled {
		return result, err
	}

	return r.handleIntervalRun(ctx, tfRun, currentSpecHash)
}

func (r *TfRunReconciler) updateStatusBasedOnCompletedJob(ctx context.Context, tfRun *infrav1alpha1.TfRun, job *batchv1.Job) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if r.isJobSucceeded(job) {
		logger.Info("Active job has succeeded", "jobName", job.Name)
		tfRun.Status.Phase = PhaseSucceeded
		tfRun.Status.Message = fmt.Sprintf("Job %s succeeded", job.Name)
		meta.SetStatusCondition(&tfRun.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeApplied,
			Status:             metav1.ConditionTrue,
			Reason:             "JobSucceeded",
			Message:            "tfrun job succeeded",
			ObservedGeneration: tfRun.Generation,
		})
	}

	if r.isJobFailed(job) {
		logger.Info("Active job has failed", "jobName", job.Name)
		tfRun.Status.Phase = PhaseFailed
		tfRun.Status.Message = fmt.Sprintf("Job %s failed", job.Name)
		meta.SetStatusCondition(&tfRun.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeApplied,
			Status:             metav1.ConditionFalse,
			Reason:             "JobFailed",
			Message:            "tfrun job failed",
			ObservedGeneration: tfRun.Generation,
		})
	}

	return r.updateStatus(ctx, tfRun)
}

func (r *TfRunReconciler) ensureWorkspaceIfNeeded(ctx context.Context, tfRun *infrav1alpha1.TfRun) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if tfRun.Spec.Backend.Cloud == nil {
		logger.V(1).Info("backend workspace reconciliation skipped; no cloud backend configured")
		return ctrl.Result{}, nil
	}

	if tfRun.Status.WorkspaceID != "" {
		logger.Info("backend workspace status exists", "workspaceID", tfRun.Status.WorkspaceID)
		logger.V(1).Info("verifying existing workspace remotely")
		// check if workspace exists remotely
		be, err := r.getCloudBackend(ctx, tfRun)
		if err != nil {
			logger.Error(err, CloudBackendFailed)
			tfRun.Status.Phase = PhaseFailed
			tfRun.Status.Message = fmt.Sprintf("%s: %v", CloudBackendFailed, err)
			tfRun.Status.WorkspaceReady = false
			return r.updateStatus(ctx, tfRun)
		}
		_, err = be.GetWorkspace(ctx, tfRun, tfRun.Status.WorkspaceID)
		if err != nil {
			logger.Error(err, "failed to get existing workspace remotely")
			tfRun.Status.Phase = PhaseFailed
			tfRun.Status.Message = fmt.Sprintf("failed to get existing workspace remotely: %v", err)
			tfRun.Status.WorkspaceReady = false
			return r.updateStatus(ctx, tfRun)
		}
		logger.Info("existing workspace verified remotely", "workspaceID", tfRun.Status.WorkspaceID)
		return ctrl.Result{}, nil
	}

	return r.reconcileWorkspace(ctx, tfRun)
}

func (r *TfRunReconciler) handleSpecChange(
	ctx context.Context,
	tfRun *infrav1alpha1.TfRun,
	currentSpecHash string,
	activeJob *batchv1.Job,
	activeJobRunning bool,
) (ctrl.Result, bool, error) {
	logger := log.FromContext(ctx)

	specChanged := tfRun.Status.LastSpecHash != currentSpecHash
	if !specChanged {
		return ctrl.Result{}, false, nil
	}

	if activeJobRunning {
		logger.Info("Spec changed but an active job is still running, will not create a new job", "activeJobName", activeJob.Name)
		tfRun.Status.PendingExecHash = currentSpecHash
		tfRun.Status.PendingReason = "SpecChange"

		if err := r.Status().Update(ctx, tfRun); err != nil {
			logger.Error(err, "failed to update TfRun status with pending execution hash")
			return ctrl.Result{}, true, err
		}

		// if an active apply job is running, wait for 1min and requeue, Since terrafrom/tofu lock the workspace while a plan/apply/destroy is running
		return ctrl.Result{RequeueAfter: 60 * time.Second}, true, nil
	}

	logger.Info("Spec changed, creating new job immediately", "hashChanged", true, "generationChanged", tfRun.Status.ObservedGeneration != tfRun.Generation)
	result, err := r.createNewJob(ctx, tfRun, currentSpecHash, jobTypeApply)
	return result, true, err
}

func (r *TfRunReconciler) handlePendingExecution(ctx context.Context, tfRun *infrav1alpha1.TfRun) (ctrl.Result, bool, error) {
	logger := log.FromContext(ctx)

	pendingExists := tfRun.Status.PendingExecHash != "" && tfRun.Status.PendingExecHash != tfRun.Status.LastSpecHash
	if !pendingExists {
		return ctrl.Result{}, false, nil
	}

	execHash := tfRun.Status.PendingExecHash
	logger.Info("Pending execution detected due to spec change during active job, creating new job", "execHash", execHash)
	result, err := r.createNewJob(ctx, tfRun, execHash, jobTypeApply)
	return result, true, err
}

func (r *TfRunReconciler) handleIntervalRun(ctx context.Context, tfRun *infrav1alpha1.TfRun, currentSpecHash string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if tfRun.Spec.RunInterval == nil {
		if tfRun.Status.NextRunTime != nil {
			tfRun.Status.NextRunTime = nil
			return r.updateStatus(ctx, tfRun)
		}
		logger.Info("checking for interval run skipped, no RunInterval configured")
		return ctrl.Result{}, nil
	}

	if tfRun.Status.NextRunTime != nil && time.Now().Before(tfRun.Status.NextRunTime.Time) {
		logger.Info("No action needed, TfRun is up-to-date and no interval run is due")
		return ctrl.Result{RequeueAfter: time.Until(tfRun.Status.NextRunTime.Time)}, nil
	}

	logger.Info("Interval run is due, creating new job")
	return r.createNewJob(ctx, tfRun, currentSpecHash, jobTypeApply)
}

// SetupWithManager sets up the controller with the Manager.
func (r *TfRunReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1alpha1.TfRun{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}

func BuildRunID(generation int64, specHash string) string {
	return fmt.Sprintf("gen-%d-%s", generation, specHash[0:8])
}

func (r *TfRunReconciler) getActiveJobIfAny(ctx context.Context, tfRun *infrav1alpha1.TfRun) (*batchv1.Job, bool) {
	logger := log.FromContext(ctx)

	if tfRun.Status.ActiveJobName == "" {
		logger.Info("No active job name in status")
		return nil, false
	}

	job := &batchv1.Job{}
	jobKey := types.NamespacedName{
		Namespace: tfRun.Namespace,
		Name:      tfRun.Status.ActiveJobName,
	}

	if err := r.Get(ctx, jobKey, job); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("No active job found with name", "jobName", tfRun.Status.ActiveJobName)
			return nil, false
		}
		logger.Error(err, "Failed to get active job", "jobName", tfRun.Status.ActiveJobName)
		return nil, false
	}

	if r.isJobActive(job) {
		logger.Info("Active job is still running", "jobName", job.Name)
		return job, true
	}

	logger.Info("Active job is not running", "jobName", job.Name)
	return job, false
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

	if tfRun.Status.ActiveDestroyJobName != "" {
		logger.Info("An active destroy job is already present", "jobName", tfRun.Status.ActiveDestroyJobName)
		return r.handleDestroyJob(ctx, tfRun)
	}

	logger.Info("creating destroy Job")
	logger.Info("creating new job for TfRun", "Name", tfRun.Name)
	destroyJob, err := bootstrapjob.ForEngine(r.Client, strings.ToLower(tfRun.Spec.Engine.Type), []string{})
	if err != nil {
		logger.Error(err, "Failed to get engine job builder for destroy")
		return ctrl.Result{}, err
	}

	jobName := strings.ToLower(fmt.Sprintf("%s-destroy", tfRun.Name))

	existing := &batchv1.Job{}
	jobKey := types.NamespacedName{
		Namespace: tfRun.Namespace,
		Name:      jobName,
	}

	// Check if destroy job already exists
	if err := r.Get(ctx, jobKey, existing); err == nil {
		logger.Info("found existing destroy job; adopting", "jobName", jobName)

		// Refresh the TfRun object to avoid conflicts
		if err := r.Get(ctx, client.ObjectKeyFromObject(tfRun), tfRun); err != nil {
			logger.Error(err, "failed to refresh TfRun object before status update")
			return ctrl.Result{}, err
		}

		tfRun.Status.ActiveDestroyJobName = jobName
		tfRun.Status.Phase = PhaseFailed
		tfRun.Status.Message = fmt.Sprintf("Destroy job %s already exists; waiting", jobName)
		err = r.Status().Update(ctx, tfRun)
		if err != nil {
			logger.Error(err, "failed to update TfRun status after adopting existing destroy job")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	job, err := destroyJob.BuildJob(ctx, tfRun, jobTypeDestroy, jobName)

	if err != nil {
		logger.Error(err, "Failed to build destroy job template for tfrun")
		tfRun.Status.Phase = PhaseFailed
		tfRun.Status.Message = fmt.Sprintf("failed to build destroy job: %v", err)
		_ = r.Status().Update(ctx, tfRun)
		return ctrl.Result{}, err
	}

	// Set TfRun as owner of the Job
	if err := controllerutil.SetControllerReference(tfRun, job, r.Scheme); err != nil {
		logger.Error(err, "failed to set controller reference for destroy job")
		return ctrl.Result{}, err
	}

	// Create the Job
	if err := r.Create(ctx, job); err != nil {
		if apierrors.IsAlreadyExists(err) {
			logger.Info("destroy job already exists", "jobName", job.Name)
			tfRun.Status.ActiveDestroyJobName = jobName
			tfRun.Status.Phase = "Failed"
			tfRun.Status.ObservedGeneration = tfRun.Generation
			// wait for destroy job to complete
			_ = r.Status().Update(ctx, tfRun)
			return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
		}
		logger.Error(err, "failed to create destroy Job")
		tfRun.Status.Phase = PhaseFailed
		tfRun.Status.Message = fmt.Sprintf("Failed to create destroy Job: %v", err)
		return ctrl.Result{}, err
	}

	logger.Info("destroy job created successfully", "jobName", jobName)
	tfRun.Status.ActiveDestroyJobName = jobName
	tfRun.Status.Phase = "Failed"
	tfRun.Status.ObservedGeneration = tfRun.Generation
	tfRun.Status.Message = fmt.Sprintf("Created destroy Job %s", jobName)
	err = r.Status().Update(ctx, tfRun)
	if err != nil {
		logger.Error(err, "Failed to update TfRun status after creating destroy job")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// createNewJob creates a new apply job for the TfRun
func (r *TfRunReconciler) createNewJob(ctx context.Context, tfRun *infrav1alpha1.TfRun, currentSpecHash string, jobType string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("creating new job for TfRun", "Name", tfRun.Name)
	applyJob, err := bootstrapjob.ForEngine(r.Client, strings.ToLower(tfRun.Spec.Engine.Type), []string{})
	if err != nil {
		logger.Error(err, "Failed to get engine job builder for apply")
		return ctrl.Result{}, err
	}

	runId := BuildRunID(tfRun.Generation, currentSpecHash)
	jobName := buildJobName(tfRun, runId)
	job, err := applyJob.BuildJob(ctx, tfRun, jobType, jobName)

	if err != nil {
		logger.Error(err, "failed to build job template for tfrun")
		tfRun.Status.Phase = PhaseFailed
		tfRun.Status.Message = fmt.Sprintf("failed to build job: %v", err)
		return r.updateStatus(ctx, tfRun)
	}

	// Set TfRun as owner of the Job
	if err := controllerutil.SetControllerReference(tfRun, job, r.Scheme); err != nil {
		logger.Error(err, "failed to set controller reference")
		return ctrl.Result{}, err
	}

	// Create the Job
	return r.createJobAndUpdateStatus(ctx, tfRun, job, currentSpecHash, jobName, jobType)
}

func buildJobName(tfRun *infrav1alpha1.TfRun, runID string) string {
	jobName := strings.ToLower(fmt.Sprintf("apply-%s-%s", tfRun.Name, runID))
	maxLen := 63
	if len(jobName) > maxLen {
		jobName = strings.TrimRight(jobName[:maxLen], "-")
	} else {
		jobName = strings.TrimRight(jobName, "-")
	}
	return jobName
}

// create job and update status
func (r *TfRunReconciler) createJobAndUpdateStatus(ctx context.Context, tfRun *infrav1alpha1.TfRun, job *batchv1.Job, currentSpecHash string, jobName string, jobType string) (ctrl.Result, error) {

	logger := log.FromContext(ctx)

	// Create the Job
	if err := r.Create(ctx, job); err != nil {
		if apierrors.IsAlreadyExists(err) {
			logger.Info("job already exists", "jobName", job.Name)

			// Re-fetch the latest version to avoid conflicts
			latest := &infrav1alpha1.TfRun{}
			if err := r.Get(ctx, client.ObjectKeyFromObject(tfRun), latest); err != nil {
				logger.Error(err, "Failed to re-fetch TfRun after job already exists")
				return ctrl.Result{}, err
			}

			latest.Status.ActiveJobName = jobName
			latest.Status.Phase = PhaseRunning
			latest.Status.ObservedGeneration = latest.Generation
			return r.updateStatus(ctx, latest)
		}
		logger.Error(err, "failed to create Job")
		tfRun.Status.Phase = PhaseFailed
		tfRun.Status.Message = fmt.Sprintf("Failed to create Job: %v", err)
		// return r.updateStatus(ctx, tfRun)
		_ = r.Status().Update(ctx, tfRun)
		return ctrl.Result{}, err
	}

	logger.Info("job created successfully", "jobName", jobName)
	tfRun.Status.ActiveJobName = jobName
	tfRun.Status.RunID = BuildRunID(tfRun.Generation, currentSpecHash)
	tfRun.Status.LastSpecHash = currentSpecHash
	tfRun.Status.Phase = PhaseRunning
	tfRun.Status.ObservedGeneration = tfRun.Generation
	tfRun.Status.Message = fmt.Sprintf("Created Job %s", jobName)
	tfRun.Status.PendingExecHash = ""
	tfRun.Status.PendingReason = ""
	meta.SetStatusCondition(&tfRun.Status.Conditions, metav1.Condition{
		Type:               ConditionTypeApplied,
		Status:             metav1.ConditionFalse,
		Reason:             "JobCreated",
		Message:            "tfrun job created",
		ObservedGeneration: tfRun.Generation,
	})

	if tfRun.Spec.RunInterval != nil {
		lastRuntime := time.Now()
		tfRun.Status.LastRunTime = &metav1.Time{Time: lastRuntime}
		nextRunTime := time.Now().Add(tfRun.Spec.RunInterval.Time.Duration)
		tfRun.Status.NextRunTime = &metav1.Time{Time: nextRunTime}
		logger.Info("setting next run time for interval based run", "nextRunTime", nextRunTime)
	} else {
		tfRun.Status.NextRunTime = nil
	}

	if err := r.Status().Update(ctx, tfRun); err != nil {
		logger.Error(err, "failed to update TfRun status after job creation")
		return ctrl.Result{}, err
	}

	if tfRun.Spec.RunInterval != nil {
		return ctrl.Result{RequeueAfter: time.Until(tfRun.Status.NextRunTime.Time)}, nil
	}

	return ctrl.Result{}, nil
}

func (r *TfRunReconciler) deleteRemoteWorkspace(ctx context.Context, tfRun *infrav1alpha1.TfRun) error {
	logger := log.FromContext(ctx)

	be, err := r.getCloudBackend(ctx, tfRun)
	if err != nil {
		logger.Error(err, "failed to get cloud backend for workspace cleanup")
		return err
	}

	err = be.DeleteWorkspace(ctx, tfRun, tfRun.Status.WorkspaceID)
	if err != nil {
		logger.Error(err, "failed to delete remote workspace during TfRun deletion")
		return err
	}

	logger.Info("remote workspace deleted successfully", "workspaceID", tfRun.Status.WorkspaceID)
	return nil
}

// update destroy job status
func (r *TfRunReconciler) handleDestroyJob(ctx context.Context, tfRun *infrav1alpha1.TfRun) (ctrl.Result, error) {

	logger := log.FromContext(ctx)

	job := &batchv1.Job{}
	jobKey := types.NamespacedName{
		Namespace: tfRun.Namespace,
		Name:      tfRun.Status.ActiveDestroyJobName,
	}

	if err := r.Get(ctx, jobKey, job); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info(jobNotFoundMessage, "jobName", tfRun.Status.ActiveDestroyJobName)
			tfRun.Status.ActiveDestroyJobName = ""
			_ = r.Status().Update(ctx, tfRun)
			return r.cleanupWorkspaceAndRemoveFinalizer(ctx, tfRun)
		}

		return ctrl.Result{}, err
	}

	switch {
	case r.isJobActive(job):
		tfRun.Status.Phase = "Destroying"
		return ctrl.Result{RequeueAfter: 60 * time.Second}, nil

	case r.isJobSucceeded(job):
		logger.Info("destroy job has succeeded", "jobName", job.Name)
		tfRun.Status.Message = fmt.Sprintf("Destroy job %s succeeded", job.Name)
		tfRun.Status.ActiveDestroyJobName = ""
		_ = r.Status().Update(ctx, tfRun)
		return r.cleanupWorkspaceAndRemoveFinalizer(ctx, tfRun)

	case r.isJobFailed(job):
		tfRun.Status.Phase = PhaseFailed
		tfRun.Status.Message = fmt.Sprintf("destroy job %s has failed", job.Name)

		// if v, ok := tfRun.Annotations["infra.essity.com/force-finalize"]; ok && strings.ToLower(v) == "true" {
		// 	logger.Info("Force finalize enabled; removing finalizer despite failed destroy")
		// 	return r.removeFinalizer(ctx, tfRun)
		// }

		// if tfRun.DeletionTimestamp != nil && time.Since(tfRun.DeletionTimestamp.Time) > 30*time.Minute {
		// 	logger.Error(fmt.Errorf("destroy failed"), "Timed out waiting for destroy; removing finalizer", "jobName", job.Name)
		// 	return r.removeFinalizer(ctx, tfRun)
		// }
		tfRun.Status.ActiveDestroyJobName = ""
		_ = r.Status().Update(ctx, tfRun)
		return ctrl.Result{}, nil

	default:
		logger.Info("unknown job state", "jobName", job.Name)
		return ctrl.Result{RequeueAfter: 600 * time.Second}, nil
	}
}

func (r *TfRunReconciler) cleanupWorkspaceAndRemoveFinalizer(ctx context.Context, tfRun *infrav1alpha1.TfRun) (ctrl.Result, error) {
	// cleanup remote workspace if not already done
	if tfRun.Status.WorkspaceReady && tfRun.Status.WorkspaceID != "" {
		if err := r.deleteRemoteWorkspace(ctx, tfRun); err != nil {
			return ctrl.Result{}, err
		}
	}

	return r.removeFinalizer(ctx, tfRun)
}

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
		logger.Info("finalizer removed successfully")
	} else {
		logger.V(1).Info("finalizer already removed")
	}

	return ctrl.Result{}, nil
}
