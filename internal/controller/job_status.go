package controller

import (
	"context"
	"fmt"

	infrav1alpha1 "infra.essity.com/orchstrator-api/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	jobNotFoundMessage = "active job not found"
	failedToGetJob     = "failed to get job"
)

func (r *TfRunReconciler) updateJobStatus(ctx context.Context, tfRun *infrav1alpha1.TfRun) (ctrl.Result, error) {

	logger := log.FromContext(ctx)

	job := &batchv1.Job{}
	jobKey := types.NamespacedName{
		Namespace: tfRun.Namespace,
		Name:      tfRun.Status.ActiveJobName,
	}

	// check if the job exists already
	if err := r.Get(ctx, jobKey, job); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info(jobNotFoundMessage, "jobName", tfRun.Status.ActiveJobName)
			// set active job name to empty
			tfRun.Status.ActiveJobName = ""
			tfRun.Status.Phase = PhaseFailed
			tfRun.Status.Message = jobNotFoundMessage
			// update the status of the tfRun
			_, _ = r.updateStatus(ctx, tfRun)
		}
		logger.Error(err, failedToGetJob, "jobName", tfRun.Status.ActiveJobName)
		return ctrl.Result{}, err
	}

	// Update the TfRun status based on the Job status
	if r.isJobActive(job) {
		logger.Info("job is still active", "jobName", job.Name)
		tfRun.Status.Phase = PhaseRunning
		tfRun.Status.Message = fmt.Sprintf("job %s is running", job.Name)
		meta.SetStatusCondition(&tfRun.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeApplied,
			Status:             metav1.ConditionFalse,
			Reason:             "InProgress",
			Message:            "tofu Job is running",
			ObservedGeneration: tfRun.Generation,
		})
	} else if r.isJobSucceeded(job) {
		logger.Info("job has succeeded", "jobName", job.Name)
		tfRun.Status.Phase = PhaseSucceeded
		tfRun.Status.Message = fmt.Sprintf("job %s has succeeded", job.Name)
		tfRun.Status.LastSuccessfulJobName = job.Name
		tfRun.Status.LastRunTime = &metav1.Time{Time: metav1.Now().Time}
		tfRun.Status.NextRunTime = nil
		tfRun.Status.ActiveJobName = ""
		meta.SetStatusCondition(&tfRun.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeApplied,
			Status:             metav1.ConditionTrue,
			Reason:             "Succeeded",
			Message:            "TfRun job has been succeeded",
			ObservedGeneration: tfRun.Generation,
		})
		meta.SetStatusCondition(&tfRun.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionTrue,
			Reason:             "Succeeded",
			Message:            "TfRun has been applied successfully",
			ObservedGeneration: tfRun.Generation,
		})
	} else if r.isJobFailed(job) {
		logger.Info("job has failed", "jobName", job.Name)
		tfRun.Status.Phase = PhaseFailed
		tfRun.Status.Message = fmt.Sprintf("job %s has failed", job.Name)
		tfRun.Status.ActiveJobName = ""
		meta.SetStatusCondition(&tfRun.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeApplied,
			Status:             metav1.ConditionFalse,
			Reason:             "Failed",
			Message:            "TfRun job has failed",
			ObservedGeneration: tfRun.Generation,
		})
		meta.SetStatusCondition(&tfRun.Status.Conditions, metav1.Condition{
			Type:               ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			Reason:             "Failed",
			Message:            "TfRun has failed",
			ObservedGeneration: tfRun.Generation,
		})
	}

	tfRun.Status.ObservedGeneration = tfRun.Generation
	logger.Info("updating tfrun status after checking job status",
		"phase", tfRun.Status.Phase,
		"activeJobName", tfRun.Status.ActiveJobName,
		"message", tfRun.Status.Message,
		"observedGeneration", tfRun.Status.ObservedGeneration)

	if err := r.Status().Update(ctx, tfRun); err != nil {
		logger.Error(err, "failed to update TfRun status after checking job status")
		return ctrl.Result{}, err
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
		Name:      tfRun.Status.ActiveJobName,
	}

	if err := r.Get(ctx, jobKey, job); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info(jobNotFoundMessage, "jobName", tfRun.Status.ActiveJobName)
			return r.cleanupWorkspaceAndRemoveFinalizer(ctx, tfRun)
		}

		return ctrl.Result{}, err
	}

	switch {
	case r.isJobActive(job):
		tfRun.Status.Phase = "Destroying"
		return ctrl.Result{}, nil

	case r.isJobSucceeded(job):
		logger.Info("destroy job has succeeded", "jobName", job.Name)
		return r.cleanupWorkspaceAndRemoveFinalizer(ctx, tfRun)

	case r.isJobFailed(job):
		tfRun.Status.Phase = PhaseFailed
		tfRun.Status.Message = fmt.Sprintf("destroy job %s has failed", job.Name)
		return ctrl.Result{}, nil

	default:
		logger.Info("destroy job is still running", "jobName", job.Name)
		return ctrl.Result{}, nil
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
