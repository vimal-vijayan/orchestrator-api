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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
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
	labeltofuRun   = "tofurun"
	labelJobType   = "job-type"
	jobTypeApply   = "apply"
	jobTypeDestroy = "destroy"
)

// TfRunReconciler reconciles a TfRun object
type TfRunReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// ScalrWorkspaceRequest represents the Scalr API request structure
type ScalrWorkspaceRequest struct {
	Data ScalrWorkspaceData `json:"data"`
}

type ScalrWorkspaceData struct {
	Type          string                      `json:"type"`
	Attributes    ScalrWorkspaceAttributes    `json:"attributes"`
	Relationships ScalrWorkspaceRelationships `json:"relationships"`
}

type ScalrWorkspaceAttributes struct {
	Name        string `json:"name"`
	AutoApply   bool   `json:"auto-apply"`
	IacPlatform string `json:"iac-platform,omitempty"`
}

type ScalrWorkspaceRelationships struct {
	AgentPool   ScalrAgentPoolRelation   `json:"agent-pool,omitempty"`
	Environment ScalrEnvironmentRelation `json:"environment"`
}

type ScalrAgentPoolRelation struct {
	Data ScalrAgentPoolData `json:"data"`
}

type ScalrAgentPoolData struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type ScalrEnvironmentRelation struct {
	Data ScalrEnvironmentData `json:"data"`
}

type ScalrEnvironmentData struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// ScalrWorkspaceResponse represents the Scalr API response structure
type ScalrWorkspaceResponse struct {
	Data struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Attributes struct {
			Name string `json:"name"`
		} `json:"attributes"`
	} `json:"data"`
}

// getScalrToken retrieves the Scalr token from the credentials secret
func (r *TfRunReconciler) getScalrToken(ctx context.Context, tfRun *infrav1alpha1.TfRun) (string, error) {
	logger := log.FromContext(ctx)

	if tfRun.Spec.ForProvider.CredentialsSecretRef == "" {
		return "", fmt.Errorf("credentials secret reference is required")
	}

	secret := &corev1.Secret{}
	secretKey := types.NamespacedName{
		Name:      tfRun.Spec.ForProvider.CredentialsSecretRef,
		Namespace: tfRun.Namespace,
	}

	if err := r.Get(ctx, secretKey, secret); err != nil {
		return "", fmt.Errorf("failed to get credentials secret: %w", err)
	}

	token, ok := secret.Data["token"]
	if !ok {
		return "", fmt.Errorf("token key not found in secret")
	}

	logger.V(1).Info("Retrieved Scalr token from secret", "secretName", tfRun.Spec.ForProvider.CredentialsSecretRef)
	return string(token), nil
}

// getGitCredentials retrieves the git PAT token from the specified secret
// If secretName is empty, it defaults to "git-credentials"
// If the secret doesn't exist, returns empty string (falls back to public clone)
func (r *TfRunReconciler) getGitCredentials(ctx context.Context, tfRun *infrav1alpha1.TfRun, secretName string) (string, error) {
	logger := log.FromContext(ctx)

	// Use default secret name if not provided
	if secretName == "" {
		secretName = "git-credentials"
		logger.V(1).Info("No git credentials secret specified, using default", "secretName", secretName)
	}

	secret := &corev1.Secret{}
	secretKey := types.NamespacedName{
		Name:      secretName,
		Namespace: tfRun.Namespace,
	}

	if err := r.Get(ctx, secretKey, secret); err != nil {
		if apierrors.IsNotFound(err) {
			// Secret not found, return empty string (will use public clone)
			logger.V(1).Info("Git credentials secret not found, using public repository access", "secretName", secretName)
			return "", nil
		}
		return "", fmt.Errorf("failed to get git-credentials secret: %w", err)
	}

	token, ok := secret.Data["token"]
	if !ok {
		return "", fmt.Errorf("token key not found in git-credentials secret")
	}

	logger.V(1).Info("Retrieved git PAT token from secret", "secretName", secretName)
	return string(token), nil
}

// createScalrWorkspace creates a workspace in Scalr
func (r *TfRunReconciler) createScalrWorkspace(ctx context.Context, tfRun *infrav1alpha1.TfRun) (string, error) {
	logger := log.FromContext(ctx)

	if tfRun.Spec.Backend.Cloud == nil {
		return "", fmt.Errorf("cloud backend configuration is required for Scalr workspace creation")
	}

	backend := tfRun.Spec.Backend.Cloud
	agentPoolId := tfRun.Spec.Backend.Cloud.AgentPoolID
	apiURL := fmt.Sprintf("https://%s/api/iacp/v3/workspaces", backend.Hostname)

	// Use CR name as workspace name to ensure uniqueness
	workspaceName := fmt.Sprintf("%s", backend.Workspace)

	// Get environment ID from spec or fetch from API
	var environmentID string
	if backend.EnvironmentID != "" {
		// Use explicitly provided environment ID
		environmentID = backend.EnvironmentID
		logger.Info("Using provided environment ID", "environmentID", environmentID)
	} else {
		// Fetch environment ID using environment name (stored in organization field)
		logger.Info("Fetching environment ID from Scalr", "environmentName", backend.Organization)
		envID, err := r.getScalrEnvironmentID(ctx, tfRun, backend.Organization)
		if err != nil {
			return "", fmt.Errorf("failed to get environment ID: %w", err)
		}
		environmentID = envID
	}

	payload := ScalrWorkspaceRequest{
		Data: ScalrWorkspaceData{
			Type: "workspaces",
			Attributes: ScalrWorkspaceAttributes{
				Name:        workspaceName,
				AutoApply:   true,
				IacPlatform: "opentofu",
			},
			Relationships: ScalrWorkspaceRelationships{
				Environment: ScalrEnvironmentRelation{
					Data: ScalrEnvironmentData{
						Type: "environments",
						ID:   environmentID,
					},
				},
			},
		},
	}

	// Only add agent pool if provided
	if agentPoolId != "" {
		logger.Info("Using provided agent pool ID", "agentPoolID", agentPoolId)
		payload.Data.Relationships.AgentPool = ScalrAgentPoolRelation{
			Data: ScalrAgentPoolData{
				Type: "agent-pools",
				ID:   agentPoolId,
			},
		}
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal workspace request: %w", err)
	}

	logger.Info("Creating Scalr workspace",
		"workspaceName", workspaceName,
		"apiURL", apiURL,
		"environmentID", environmentID)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.api+json")
	req.Header.Set("Content-Type", "application/vnd.api+json")

	// Add authentication token from secret
	token, err := r.getScalrToken(ctx, tfRun)
	if err != nil {
		return "", fmt.Errorf("failed to get Scalr token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to call Scalr API: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		logger.Error(nil, "Scalr API returned error", "statusCode", resp.StatusCode, "body", string(body))
		return "", fmt.Errorf("scalr API returned status %d: %s", resp.StatusCode, string(body))
	}

	var workspaceResp ScalrWorkspaceResponse
	if err := json.Unmarshal(body, &workspaceResp); err != nil {
		return "", fmt.Errorf("failed to unmarshal response: %w", err)
	}

	logger.Info("Scalr workspace created successfully",
		"workspaceID", workspaceResp.Data.ID,
		"workspaceName", workspaceResp.Data.Attributes.Name)

	return workspaceResp.Data.ID, nil
}

// ScalrEnvironmentResponse represents the Scalr environment API response
type ScalrEnvironmentListResponse struct {
	Data []struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Attributes struct {
			Name string `json:"name"`
		} `json:"attributes"`
	} `json:"data"`
}

// getScalrEnvironmentID retrieves the environment ID from Scalr by name
func (r *TfRunReconciler) getScalrEnvironmentID(ctx context.Context, tfRun *infrav1alpha1.TfRun, environmentName string) (string, error) {
	logger := log.FromContext(ctx)

	if tfRun.Spec.Backend.Cloud == nil {
		return "", fmt.Errorf("cloud backend configuration is required")
	}

	backend := tfRun.Spec.Backend.Cloud
	// Use list API with filter to find environment by name
	apiURL := fmt.Sprintf("https://%s/api/iacp/v3/environments?filter[name]=%s", backend.Hostname, environmentName)

	logger.Info("Fetching Scalr environment ID by name", "environmentName", environmentName, "apiURL", apiURL)

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.api+json")

	// Add authentication token from secret
	token, err := r.getScalrToken(ctx, tfRun)
	if err != nil {
		return "", fmt.Errorf("failed to get Scalr token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to call Scalr API: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		logger.Error(nil, "Scalr API returned error", "statusCode", resp.StatusCode, "body", string(body))
		return "", fmt.Errorf("scalr API returned status %d: %s", resp.StatusCode, string(body))
	}

	var envListResp ScalrEnvironmentListResponse
	if err := json.Unmarshal(body, &envListResp); err != nil {
		return "", fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// Check if any environments were found
	if len(envListResp.Data) == 0 {
		return "", fmt.Errorf("environment '%s' not found", environmentName)
	}

	// Use the first matching environment
	environmentID := envListResp.Data[0].ID
	logger.Info("Scalr environment found",
		"environmentName", envListResp.Data[0].Attributes.Name,
		"environmentID", environmentID,
		"matchCount", len(envListResp.Data))

	return environmentID, nil
}

// deleteScalrWorkspace deletes a workspace from Scalr
func (r *TfRunReconciler) deleteScalrWorkspace(ctx context.Context, tfRun *infrav1alpha1.TfRun, workspaceID string) error {
	logger := log.FromContext(ctx)

	if tfRun.Spec.Backend.Cloud == nil {
		return fmt.Errorf("cloud backend configuration is required for Scalr workspace deletion")
	}

	if workspaceID == "" {
		logger.V(1).Info("No workspace ID to delete, skipping")
		return nil
	}

	hostname := tfRun.Spec.Backend.Cloud.Hostname
	if hostname == "" {
		return fmt.Errorf("hostname is required in cloud backend configuration")
	}

	// Get authentication token
	token, err := r.getScalrToken(ctx, tfRun)
	if err != nil {
		return fmt.Errorf("failed to get Scalr token: %w", err)
	}

	// Construct the API URL
	apiURL := fmt.Sprintf("https://%s/api/iacp/v3/workspaces/%s", hostname, workspaceID)
	logger.V(1).Info("Deleting Scalr workspace", "url", apiURL, "workspaceID", workspaceID)

	// Create DELETE request
	req, err := http.NewRequestWithContext(ctx, "DELETE", apiURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)

	// Execute the request
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to call Scalr API: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	// DELETE typically returns 204 No Content on success, but also accept 200 OK
	// Also accept 404 Not Found as the workspace may have already been deleted
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		logger.Error(nil, "Scalr API returned error", "statusCode", resp.StatusCode, "body", string(body))
		return fmt.Errorf("scalr API returned status %d: %s", resp.StatusCode, string(body))
	}

	if resp.StatusCode == http.StatusNotFound {
		logger.Info("Scalr workspace already deleted or not found", "workspaceID", workspaceID)
	} else {
		logger.Info("Scalr workspace deleted successfully", "workspaceID", workspaceID)
	}

	return nil
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

	logger.Info("Reconciliation started", "namespacedName", req.NamespacedName)

	// Fetch the TfRun instance
	tfRun := &infrav1alpha1.TfRun{}
	if err := r.Get(ctx, req.NamespacedName, tfRun); err != nil {
		if apierrors.IsNotFound(err) {
			// Object not found, could have been deleted after reconcile request
			logger.Info("TfRun resource not found, likely deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get TfRun")
		return ctrl.Result{}, err
	}

	logger.Info("TfRun fetched",
		"generation", tfRun.Generation,
		"phase", tfRun.Status.Phase,
		"deletionTimestamp", tfRun.DeletionTimestamp)

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

	logger.Info("Creating new Job",
		"reason", "spec changed or first run",
		"hashChanged", tfRun.Status.LastSpecHash != currentSpecHash,
		"previousPhase", tfRun.Status.Phase)

	// Create Scalr workspace if not already created
	if tfRun.Status.WorkspaceID == "" && tfRun.Spec.Backend.Cloud != nil {
		logger.Info("Creating Scalr workspace before Job execution")
		workspaceID, err := r.createScalrWorkspace(ctx, tfRun)
		if err != nil {
			logger.Error(err, "Failed to create Scalr workspace")
			tfRun.Status.Phase = PhaseFailed
			tfRun.Status.Message = fmt.Sprintf("Failed to create Scalr workspace: %v", err)
			_, _ = r.updateStatus(ctx, tfRun)
			return ctrl.Result{}, err
		}

		tfRun.Status.WorkspaceID = workspaceID
		logger.Info("Scalr workspace created, updating status", "workspaceID", workspaceID)
		if err := r.Status().Update(ctx, tfRun); err != nil {
			logger.Error(err, "Failed to update status with workspace ID")
			return ctrl.Result{}, err
		}
	} else if tfRun.Status.WorkspaceID != "" {
		logger.Info("Using existing Scalr workspace", "workspaceID", tfRun.Status.WorkspaceID)
	}

	// Create a new apply Job
	logger.Info("Creating new tofu apply Job")
	job, err := r.buildtofuJob(ctx, tfRun, jobTypeApply)
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
				controllerutil.RemoveFinalizer(tfRun, finalizerName)
				return ctrl.Result{}, r.Update(ctx, tfRun)
			}
			return ctrl.Result{}, err
		}

		// Check if destroy Job succeeded
		if r.isJobSucceeded(job) {
			logger.Info("Destroy Job succeeded, deleting Scalr workspace")
			tfRun.Status.Phase = PhaseSucceeded
			tfRun.Status.Message = "tofu destroy succeeded"
			meta.SetStatusCondition(&tfRun.Status.Conditions, metav1.Condition{
				Type:               ConditionTypeDestroyed,
				Status:             metav1.ConditionTrue,
				Reason:             "DestroySucceeded",
				Message:            "tofu destroy completed successfully",
				ObservedGeneration: tfRun.Generation,
			})
			if err := r.Status().Update(ctx, tfRun); err != nil {
				return ctrl.Result{}, err
			}

			// Delete Scalr workspace after successful destroy
			if tfRun.Status.WorkspaceID != "" {
				logger.Info("Deleting Scalr workspace", "workspaceID", tfRun.Status.WorkspaceID)
				if err := r.deleteScalrWorkspace(ctx, tfRun, tfRun.Status.WorkspaceID); err != nil {
					logger.Error(err, "Failed to delete Scalr workspace, continuing with finalizer removal")
					// Don't fail the deletion if workspace deletion fails
				} else {
					logger.Info("Scalr workspace deleted successfully")
				}
			}

			controllerutil.RemoveFinalizer(tfRun, finalizerName)
			return ctrl.Result{}, r.Update(ctx, tfRun)
		}

		// Check if destroy Job failed
		if r.isJobFailed(job) {
			logger.Error(nil, "Destroy Job failed")
			tfRun.Status.Phase = PhaseFailed
			tfRun.Status.Message = "tofu destroy failed"
			meta.SetStatusCondition(&tfRun.Status.Conditions, metav1.Condition{
				Type:               ConditionTypeDestroyed,
				Status:             metav1.ConditionFalse,
				Reason:             "DestroyFailed",
				Message:            "tofu destroy failed",
				ObservedGeneration: tfRun.Generation,
			})
			if err := r.Status().Update(ctx, tfRun); err != nil {
				return ctrl.Result{}, err
			}

			// Remove finalizer even on failure to prevent stuck resources
			controllerutil.RemoveFinalizer(tfRun, finalizerName)
			return ctrl.Result{}, r.Update(ctx, tfRun)
		}

		// Job is still running
		logger.Info("Destroy Job is still running", "jobName", job.Name)
		tfRun.Status.Phase = PhaseRunning
		tfRun.Status.Message = "tofu destroy in progress"
		_ = r.Status().Update(ctx, tfRun)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// No active destroy Job - create one
	logger.Info("Creating tofu destroy Job")
	job, err := r.buildtofuJob(ctx, tfRun, jobTypeDestroy)
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
			tfRun.Status.ActiveJobName = job.Name
			tfRun.Status.Phase = PhaseRunning
			_ = r.Status().Update(ctx, tfRun)
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
	_ = r.Status().Update(ctx, tfRun)

	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
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

// buildtofuJob creates a Kubernetes Job for tofu operations
func (r *TfRunReconciler) buildtofuJob(ctx context.Context, tfRun *infrav1alpha1.TfRun, jobType string) (*batchv1.Job, error) {
	logger := log.FromContext(ctx)

	logger.Info("Building tofu Job", "jobType", jobType, "module", tfRun.Spec.Source.Module, "ref", tfRun.Spec.Source.Ref, "path", tfRun.Spec.Source.Path)

	// Compute a short hash for unique Job naming
	specHash, err := r.computeSpecHash(tfRun)
	if err != nil {
		return nil, err
	}

	jobName := fmt.Sprintf("%s-%s-%s", tfRun.Name, jobType, specHash)
	logger.Info("Generated Job name", "jobName", jobName, "hash", specHash)

	// Determine tofu command based on job type
	var tfCommand string
	if jobType == jobTypeDestroy {
		tfCommand = "tofu init && tofu plan -destroy && tofu destroy -auto-approve"
		logger.Info("Job will execute destroy command")
	} else {
		//TODO: Check available go packages for tofu plan output parsing and implement a two-step plan/apply with approval
		tfCommand = "tofu init && tofu apply -auto-approve"
		logger.Info("Job will execute init, plan, and apply commands")
	}

	// Build environment variables for tofu
	logger.V(1).Info("Building environment variables", "varsCount", len(tfRun.Spec.Vars))
	envVars := []corev1.EnvVar{}
	for k, v := range tfRun.Spec.Vars {
		// Convert JSON to string for environment variable
		if v != nil {
			varValue := string(v.Raw)
			logger.V(2).Info("Adding tofu variable", "key", k, "valueLength", len(varValue))
			envVars = append(envVars, corev1.EnvVar{
				Name:  fmt.Sprintf("TF_VAR_%s", k),
				Value: varValue,
			})
		}
	}

	// Add backend configuration if present
	if tfRun.Spec.Backend.Cloud != nil {
		logger.Info("Configuring cloud backend", "hostname", tfRun.Spec.Backend.Cloud.Hostname, "organization", tfRun.Spec.Backend.Cloud.Organization, "workspace", tfRun.Spec.Backend.Cloud.Workspace)
		envVars = append(envVars,
			corev1.EnvVar{Name: "TF_CLOUD_HOSTNAME", Value: tfRun.Spec.Backend.Cloud.Hostname},
			corev1.EnvVar{Name: "TF_CLOUD_ORGANIZATION", Value: tfRun.Spec.Backend.Cloud.Organization},
			corev1.EnvVar{Name: "TF_WORKSPACE", Value: tfRun.Spec.Backend.Cloud.Workspace},
		)
	} else if tfRun.Spec.Backend.StorageAccount != nil {
		logger.Info("Configuring storage account backend", "accountName", tfRun.Spec.Backend.StorageAccount.AccountName, "containerName", tfRun.Spec.Backend.StorageAccount.ContainerName)
	}

	// Add credentials from secret
	if tfRun.Spec.ForProvider.CredentialsSecretRef != "" {
		envVars = append(envVars, corev1.EnvVar{
			// Scalr token environment variable
			Name: "TF_TOKEN_essity_scalr_io",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: tfRun.Spec.ForProvider.CredentialsSecretRef,
					},
					Key: "token",
				},
			},
		})
	}

	// Get git credentials if available
	gitToken, err := r.getGitCredentials(ctx, tfRun, tfRun.Spec.Source.CredentialsSecretRef)
	if err != nil {
		return nil, fmt.Errorf("failed to get git credentials: %w", err)
	}

	// Build authenticated repository URL if token is available
	repoURL := tfRun.Spec.Source.Module
	if gitToken != "" {
		// Convert https://github.com/user/repo.git to https://token@github.com/user/repo.git
		if len(repoURL) > 8 && repoURL[:8] == "https://" {
			repoURL = fmt.Sprintf("https://%s@%s", gitToken, repoURL[8:])
			logger.Info("Using authenticated git clone for private repository")
		} else {
			logger.Info("Git credentials available but repository URL is not HTTPS, using public clone")
		}
	} else {
		logger.Info("No git credentials found, using public repository clone")
	}

	// Build git clone command
	var gitCloneCmd string
	if tfRun.Spec.Source.Ref != "" {
		// Clone the repository and then checkout the specific ref
		// This handles branches, tags, and commit SHAs uniformly
		gitCloneCmd = fmt.Sprintf("git clone --depth 5 %s /workspace && cd /workspace && git fetch --depth 5 origin %s && git checkout %s",
			repoURL, tfRun.Spec.Source.Ref, tfRun.Spec.Source.Ref)
	} else {
		// Clone default branch
		gitCloneCmd = fmt.Sprintf("git clone --depth 5 %s /workspace", repoURL)
	}

	// Working directory for tofu
	workDir := "/workspace"
	if tfRun.Spec.Source.Path != "" {
		workDir = fmt.Sprintf("/workspace/%s", tfRun.Spec.Source.Path)
	}

	logger.Info("Job configuration",
		"gitCloneCmd", gitCloneCmd,
		"tofuCmd", tfCommand,
		"workDir", workDir,
		"envVarsCount", len(envVars))

	// Define the Job
	backoffLimit := int32(0)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: tfRun.Namespace,
			Labels: map[string]string{
				labeltofuRun: tfRun.Name,
				labelJobType: jobType,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						labeltofuRun: tfRun.Name,
						labelJobType: jobType,
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					InitContainers: []corev1.Container{
						{
							Name:    "git-clone",
							Image:   "alpine/git:latest",
							Command: []string{"/bin/sh", "-c"},
							Args:    []string{gitCloneCmd},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "workspace",
									MountPath: "/workspace",
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:       "opentofu",
							Image:      "ghcr.io/opentofu/opentofu:latest",
							Command:    []string{"/bin/sh", "-c"},
							Args:       []string{tfCommand},
							WorkingDir: workDir,
							Env:        envVars,
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "workspace",
									MountPath: "/workspace",
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "workspace",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
				},
			},
		},
	}

	logger.Info("Built tofu Job", "jobName", jobName, "jobType", jobType)
	return job, nil
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
