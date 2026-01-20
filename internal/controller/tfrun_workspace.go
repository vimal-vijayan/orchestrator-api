// remote workspace creation and deletion logic
package controller

import (
	"context"
	"fmt"

	infrav1alpha1 "infra.essity.com/orchstrator-api/api/v1alpha1"
	"infra.essity.com/orchstrator-api/internal/backend"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	CloudBackendFailed    = "failed to get cloud backend implementation"
	CloudWorkspaceFailed  = "failed to reconcile cloud workspace"
	CloudWorkspacePending = "cloud workspace creation pending"
	CloudWorkspaceCreated = "cloud workspace created successfully"
)

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

func (r *TfRunReconciler) reconcileWorkspace(ctx context.Context, tfRun *infrav1alpha1.TfRun) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling backend workspace for TfRun", "tfRun", tfRun.Name)

	// GET the cloud backend implementation
	be, err := r.getCloudBackend(ctx, tfRun)

	if err != nil {
		logger.Error(err, CloudBackendFailed)
		tfRun.Status.Phase = PhaseFailed
		tfRun.Status.Message = fmt.Sprintf("%s: %v", CloudBackendFailed, err)
		tfRun.Status.WorkspaceReady = false
		_, _ = r.updateStatus(ctx, tfRun)
		return ctrl.Result{}, err
	}

	// Ensure the workspace exists
	workspaceId, err := be.EnsureWorkspace(ctx, tfRun)
	if err != nil {
		logger.Info(CloudWorkspaceFailed, "workspace", err)
		logger.Error(err, CloudWorkspaceFailed)
		tfRun.Status.Phase = PhaseFailed
		tfRun.Status.Message = fmt.Sprintf("%s: %v", CloudWorkspaceFailed, err)
		tfRun.Status.WorkspaceReady = false
		_, _ = r.updateStatus(ctx, tfRun)
		return ctrl.Result{}, err
	}

	// If workspaceId is empty, the workspace is still being created
	if workspaceId == "" {
		logger.Info(CloudWorkspacePending)
		tfRun.Status.Phase = PhasePending
		tfRun.Status.Message = CloudWorkspacePending
		tfRun.Status.WorkspaceReady = false
		tfRun.Status.ObservedGeneration = tfRun.Generation
		_, _ = r.updateStatus(ctx, tfRun)
		return ctrl.Result{}, err
	}

	logger.Info(CloudWorkspaceCreated, "workspaceID", workspaceId)

	// Update TfRun status with workspace details
	tfRun.Status.WorkspaceID = workspaceId
	tfRun.Status.WorkspaceReady = true
	tfRun.Status.Phase = PhasePending
	tfRun.Status.Message = CloudWorkspacePending
	tfRun.Status.ObservedGeneration = tfRun.Generation
	logger.V(1).Info("tfrun updated for workspace", "workspaceID", workspaceId)

	// Update status after successful workspace creation
	if err := r.Status().Update(ctx, tfRun); err != nil {
		logger.Error(err, "Failed to update TfRun status after workspace creation")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}
