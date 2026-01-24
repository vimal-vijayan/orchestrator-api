// remote workspace creation and deletion logic
package controller

import (
	"context"
	"fmt"

	infrav1alpha1 "infra.essity.com/orchestrator-api/api/v1alpha1"
	"infra.essity.com/orchestrator-api/internal/backend"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	CloudBackendFailed        = "failed to get cloud backend implementation"
	CloudWorkspaceFailed      = "failed to reconcile cloud workspace"
	CloudWorkspacePending     = "cloud workspace creation pending"
	CloudWorkspaceCreated     = "cloud workspace created successfully"
	CloudWorkspaceImported    = "cloud workspace imported successfully"
	WorkspaceImportAnnotation = "tfruns.infra.essity.com/scalr-workspace-id"
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
		return r.updateStatus(ctx, tfRun)
	}

	// import workspace if annotation is present
	if tfRun.Annotations != nil && tfRun.Annotations[WorkspaceImportAnnotation] != "" {
		return r.importScalrWorkspace(ctx, tfRun)
	}

	// Ensure the workspace exists
	workspaceId, err := be.EnsureWorkspace(ctx, tfRun)
	if err != nil {
		logger.Info(CloudWorkspaceFailed, "workspace", err)
		logger.Error(err, CloudWorkspaceFailed)
		tfRun.Status.Phase = PhaseFailed
		tfRun.Status.Message = fmt.Sprintf("%s: %v", CloudWorkspaceFailed, err)
		tfRun.Status.WorkspaceReady = false
		return r.updateStatus(ctx, tfRun)
	}

	// If workspaceId is empty, the workspace is still being created, considering it is pending/failed
	if workspaceId == "" {
		logger.Info(CloudWorkspacePending)
		tfRun.Status.Phase = PhasePending
		tfRun.Status.Message = CloudWorkspacePending
		tfRun.Status.WorkspaceReady = false
		tfRun.Status.ObservedGeneration = tfRun.Generation
		return r.updateStatus(ctx, tfRun)
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
	return r.updateStatus(ctx, tfRun)
}

func (r *TfRunReconciler) importScalrWorkspace(ctx context.Context, tfRun *infrav1alpha1.TfRun) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	// GET the cloud backend implementation
	be, err := r.getCloudBackend(ctx, tfRun)
	workspaceId := tfRun.Annotations[WorkspaceImportAnnotation]
	logger.Info("found workspace import annotation", "workspaceID", workspaceId)
	logger.Info("importing existing workspace based on annotation", "workspaceID", workspaceId)

	// check if the workspace actually exists in the backend
	existingWorkspaceId, err := be.GetWorkspace(ctx, tfRun, workspaceId)
	if err != nil {
		logger.Error(err, "failed to get existing workspace from backend", "workspaceID", workspaceId)
		tfRun.Status.Phase = PhaseFailed
		tfRun.Status.Message = fmt.Sprintf("failed to get existing workspace from backend: %v", err)
		tfRun.Status.WorkspaceReady = false
		return r.updateStatus(ctx, tfRun)
	}

	logger.Info("successfully retrieved existing workspace from backend", "workspaceID", existingWorkspaceId)
	// update the status with the existing workspace ID
	tfRun.Status.WorkspaceID = existingWorkspaceId
	tfRun.Status.WorkspaceReady = true
	tfRun.Status.WorkspaceImport = true
	tfRun.Status.Phase = PhasePending
	tfRun.Status.Message = CloudWorkspaceImported
	tfRun.Status.ObservedGeneration = tfRun.Generation
	logger.V(1).Info("tfrun updated for imported workspace", "workspaceID", existingWorkspaceId)

	return r.updateStatus(ctx, tfRun)
}
