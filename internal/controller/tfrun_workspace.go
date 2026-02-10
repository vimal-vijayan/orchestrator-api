// remote state target creation and deletion logic
package controller

import (
	"context"
	"fmt"

	infrav1alpha1 "infra.essity.com/orchestrator-api/api/v1alpha1"
	"infra.essity.com/orchestrator-api/internal/backend"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	RemoteBackendFailed       = "failed to get remote backend implementation"
	StateTargetFailed         = "failed to reconcile remote state target"
	StateTargetPending        = "remote state target creation pending"
	StateTargetCreated        = "remote state target created successfully"
	StateTargetImported       = "remote state target imported successfully"
	WorkspaceImportAnnotation = "tfruns.infra.essity.com/scalr-workspace-id"
)

func (r *TfRunReconciler) getRemoteBackend(ctx context.Context, tfRun *infrav1alpha1.TfRun) (backend.RemoteBackend, error) {
	logger := log.FromContext(ctx)

	if tfRun.Spec.Backend.Cloud == nil {
		return nil, fmt.Errorf("cloud backend configuration is required")
	}

	provider := tfRun.Spec.Backend.Cloud.Provider
	if provider == "" {
		logger.V(1).Info("No cloud backend provider specified, defaulting to 'scalr'")
		provider = backend.BackendScalr
	}

	logger.V(1).Info("Getting remote backend implementation", "provider", provider)
	return backend.ForProvider(r.Client, provider)
}

// getBackendEnvVars resolves the remote backend and returns its env vars.
// Returns empty slice if no backend is configured (e.g., local state).
func (r *TfRunReconciler) getBackendEnvVars(ctx context.Context, tfRun *infrav1alpha1.TfRun) ([]corev1.EnvVar, error) {
	// No cloud backend configured — return empty (future: check storageAccount, s3, etc.)
	if tfRun.Spec.Backend.Cloud == nil {
		return []corev1.EnvVar{}, nil
	}

	be, err := r.getRemoteBackend(ctx, tfRun)
	if err != nil {
		return nil, err
	}

	return be.BackendEnvVars(tfRun)
}

func (r *TfRunReconciler) reconcileWorkspace(ctx context.Context, tfRun *infrav1alpha1.TfRun) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling remote state target for TfRun", "tfRun", tfRun.Name)

	be, err := r.getRemoteBackend(ctx, tfRun)
	if err != nil {
		logger.Error(err, RemoteBackendFailed)
		tfRun.Status.Phase = PhaseFailed
		tfRun.Status.Message = fmt.Sprintf("%s: %v", RemoteBackendFailed, err)
		tfRun.Status.WorkspaceReady = false
		return r.updateStatus(ctx, tfRun)
	}

	// import workspace if annotation is present
	if tfRun.Annotations != nil && tfRun.Annotations[WorkspaceImportAnnotation] != "" {
		return r.importScalrWorkspace(ctx, tfRun)
	}

	// Ensure the state target exists
	stateTargetID, err := be.EnsureStateTarget(ctx, tfRun)
	if err != nil {
		logger.Info(StateTargetFailed, "error", err)
		logger.Error(err, StateTargetFailed)
		tfRun.Status.Phase = PhaseFailed
		tfRun.Status.Message = fmt.Sprintf("%s: %v", StateTargetFailed, err)
		tfRun.Status.WorkspaceReady = false
		return r.updateStatus(ctx, tfRun)
	}

	// If stateTargetID is empty, the target is still being created
	if stateTargetID == "" {
		logger.Info(StateTargetPending)
		tfRun.Status.Phase = PhasePending
		tfRun.Status.Message = StateTargetPending
		tfRun.Status.WorkspaceReady = false
		tfRun.Status.ObservedGeneration = tfRun.Generation
		return r.updateStatus(ctx, tfRun)
	}

	logger.Info(StateTargetCreated, "workspaceID", stateTargetID)

	// Update TfRun status with workspace details
	tfRun.Status.WorkspaceID = stateTargetID
	tfRun.Status.WorkspaceReady = true
	tfRun.Status.Phase = PhasePending
	tfRun.Status.Message = StateTargetPending
	tfRun.Status.ObservedGeneration = tfRun.Generation
	logger.V(1).Info("tfrun updated for state target", "workspaceID", stateTargetID)

	return r.updateStatus(ctx, tfRun)
}

func (r *TfRunReconciler) importScalrWorkspace(ctx context.Context, tfRun *infrav1alpha1.TfRun) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	be, err := r.getRemoteBackend(ctx, tfRun)
	if err != nil {
		logger.Error(err, "failed to get remote backend for workspace import")
		tfRun.Status.Phase = PhaseFailed
		tfRun.Status.Message = fmt.Sprintf("failed to get remote backend for workspace import: %v", err)
		tfRun.Status.WorkspaceReady = false
		return r.updateStatus(ctx, tfRun)
	}

	workspaceId := tfRun.Annotations[WorkspaceImportAnnotation]
	logger.Info("found workspace import annotation", "workspaceID", workspaceId)
	logger.Info("importing existing workspace based on annotation", "workspaceID", workspaceId)

	// check if the workspace actually exists in the backend
	existingWorkspaceId, err := be.GetStateTarget(ctx, tfRun, workspaceId)
	if err != nil {
		logger.Error(err, "failed to get existing workspace from backend", "workspaceID", workspaceId)
		tfRun.Status.Phase = PhaseFailed
		tfRun.Status.Message = fmt.Sprintf("failed to get existing workspace from backend: %v", err)
		tfRun.Status.WorkspaceReady = false
		return r.updateStatus(ctx, tfRun)
	}

	logger.Info("successfully retrieved existing workspace from backend", "workspaceID", existingWorkspaceId)
	tfRun.Status.WorkspaceID = existingWorkspaceId
	tfRun.Status.WorkspaceReady = true
	tfRun.Status.WorkspaceImport = true
	tfRun.Status.Phase = PhasePending
	tfRun.Status.Message = StateTargetImported
	tfRun.Status.ObservedGeneration = tfRun.Generation
	logger.V(1).Info("tfrun updated for imported workspace", "workspaceID", existingWorkspaceId)

	return r.updateStatus(ctx, tfRun)
}
