package scalr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	infrav1alpha1 "infra.essity.com/orchstrator-api/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	errMissingCredentials       = "missing credentials secret reference in TfRun spec"
	errSecretNotFound           = "credentials secret not found for remote workspaces"
	errGetEnvironmentFailed     = "failed to get Scalr environment"
	errGetEnvironmentListFailed = "failed to get Scalr environment list"
	errEnvironmentNotFound      = "scalr environment not found"
	errWorkspaceNotFound        = "scalr workspace not found"
	errWorkspaceCreationFailed  = "failed to create scalr workspace"
	errWorkspaceDeletionFailed  = "failed to delete scalr workspace"
	errFormatWithCause          = "%s: %w"

	requestHeaderAccept = "application/vnd.api+json"

	// IacPlatform for Scalr workspace
	IacPlatform = "opentofu"
)

type Service struct {
	K8s  client.Client
	HTTP *http.Client
}

func NewService(k8s client.Client) *Service {
	return &Service{
		K8s: k8s,
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Get scalr token
func (s *Service) GetScalrToken(ctx context.Context, tfRun *infrav1alpha1.TfRun) (string, error) {
	logger := log.FromContext(ctx)
	secretName := tfRun.Spec.ForProvider.CredentialsSecretRef

	if secretName == "" {
		logger.Error(fmt.Errorf(errMissingCredentials), "no tofu credentials are provided for remote workspaces, The operator will fail to connect to the remote workspace")
		return "", fmt.Errorf(errMissingCredentials)
	}

	secret := &corev1.Secret{}
	key := types.NamespacedName{
		Name:      secretName,
		Namespace: tfRun.Namespace,
	}

	if err := s.K8s.Get(ctx, key, secret); err != nil {
		logger.Error(err, "failed to get remote state provider credentials from secret", "secret", key)
		return "", fmt.Errorf("failed to get remote state provider credentials from %s of secret %s with error %w", key.Namespace, key.Name, err)
	}

	token, ok := secret.Data["token"]
	if !ok || len(token) == 0 {
		logger.Error(fmt.Errorf(errSecretNotFound), "token not found in secret", "secret", key)
		return "", fmt.Errorf("token not found in secret %s/%s", key.Namespace, key.Name)
	}

	logger.V(1).Info("successfully retrieved Scalr token from secret")
	return string(token), nil
}

// scalr environment list response
type ScalrEnvironmentListResponse struct {
	Data []struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Attributes struct {
			Name string `json:"name"`
		} `json:"attributes"`
	} `json:"data"`
}

func (s *Service) GetWorkspace(ctx context.Context, tfRun *infrav1alpha1.TfRun, workspaceId string, environmentId string) (string, error) {
	logger := log.FromContext(ctx)
	backend := tfRun.Spec.Backend.Cloud
	// apiUrl := fmt.Sprintf("https://%s/api/iacp/v3/workspaces/%s",&backend.Hostname, workspaceId)
	apiUrl := fmt.Sprintf("https://%s/api/iacp/v3/workspaces?filter[environment]=%s&filter[workspace]=%s", backend.Hostname, environmentId, workspaceId)
	req, err := http.NewRequestWithContext(ctx, "GET", apiUrl, nil)
	if err != nil {
		return "", fmt.Errorf(errFormatWithCause, "failed to create HTTP request for Scalr workspace", err)
	}

	req.Header.Set("Accept", requestHeaderAccept)
	token, err := s.GetScalrToken(ctx, tfRun)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf(errFormatWithCause, "failed to perform HTTP request for Scalr workspace", err)
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf(errFormatWithCause, "failed to read HTTP response body for Scalr workspace", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET: scalr workspace retrieval failed with status code %d: %s", resp.StatusCode, string(body))
	}

	var workspaceResponse ScalrEnvironmentListResponse
	if err := json.Unmarshal(body, &workspaceResponse); err != nil {
		return "", fmt.Errorf(errFormatWithCause, "failed to unmarshal Scalr workspace response", err)
	}

	if len(workspaceResponse.Data) == 0 {
		logger.Error(fmt.Errorf(errEnvironmentNotFound), "scalr environment not found", "environment", backend.Organization)
		return "", fmt.Errorf(errEnvironmentNotFound)
	}

	workspaceID := workspaceResponse.Data[0].ID
	logger.V(1).Info("successfully retrieved scalr workspace id", "workspaceID", workspaceID)

	return workspaceID, nil
}

// Get scalr evnvironment ID
func (s *Service) GetScalrEnvironmentID(ctx context.Context, tfRun *infrav1alpha1.TfRun) (string, error) {
	logger := log.FromContext(ctx)
	backend := tfRun.Spec.Backend.Cloud
	apiUrl := fmt.Sprintf("https://%s/api/iacp/v3/environments?filter[name]=%s", backend.Hostname, backend.Organization)
	req, err := http.NewRequestWithContext(ctx, "GET", apiUrl, nil)
	if err != nil {
		logger.Error(err, "failed to create HTTP request for Scalr environment")
		return "", fmt.Errorf(errFormatWithCause, errGetEnvironmentFailed, err)
	}

	req.Header.Set("Accept", requestHeaderAccept)
	token, err := s.GetScalrToken(ctx, tfRun)

	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := s.HTTP.Do(req)
	if err != nil {
		logger.Error(err, "failed to perform HTTP request for Scalr environment")
		return "", fmt.Errorf(errFormatWithCause, errGetEnvironmentListFailed, err)
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error(err, "failed to read HTTP response body for Scalr environment")
		return "", fmt.Errorf(errFormatWithCause, errGetEnvironmentFailed, err)
	}

	if resp.StatusCode != http.StatusOK {
		logger.Error(fmt.Errorf("scalr API returned status code %d: %s", resp.StatusCode, string(body)), "failed to get Scalr environment")
		return "", fmt.Errorf("GET: scalr environment id failed with status code %d: %s", resp.StatusCode, string(body))
	}

	var envListResponse ScalrEnvironmentListResponse
	if err := json.Unmarshal(body, &envListResponse); err != nil {
		logger.Error(err, "failed to unmarshal scalr environment response")
		return "", fmt.Errorf(errFormatWithCause, errGetEnvironmentFailed, err)
	}

	if len(envListResponse.Data) == 0 {
		logger.Error(fmt.Errorf(errEnvironmentNotFound), "scalr environment not found", "environment", backend.Organization)
		return "", fmt.Errorf(errEnvironmentNotFound)
	}

	environmentID := envListResponse.Data[0].ID
	logger.V(1).Info("successfully retrieved scalr environment id", "environmentID", environmentID)
	return environmentID, nil
}

// Scalr workspace response structure
type ScalrWorkspaceResponse struct {
	Data struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Attributes struct {
			Name string `json:"name"`
		} `json:"attributes"`
	} `json:"data"`
}

// Scalr workspace creation
func (s *Service) CreateScalrWorkspace(ctx context.Context, tfRun *infrav1alpha1.TfRun, environmentID string) (string, error) {
	logger := log.FromContext(ctx)

	backend := tfRun.Spec.Backend.Cloud
	apiUrl := fmt.Sprintf("https://%s/api/iacp/v3/workspaces", backend.Hostname)
	workspaceName := backend.Workspace
	agentPoolId := backend.AgentPoolID
	scalrAutoApply := tfRun.Spec.Arguments
	autoApprove, exists := scalrAutoApply["autoApprove"]
	iacPlatform := tfRun.Spec.Engine.Type

	if iacPlatform == "" {
		iacPlatform = IacPlatform
	}

	IacVersion := tfRun.Spec.Engine.Version
	if IacVersion == "" {
		IacVersion = "latest"
	}

	if !exists {
		autoApprove = "true"
	}

	if environmentID == "" {
		logger.Error(fmt.Errorf(errEnvironmentNotFound), "Cannot create workspace without valid environment ID, Null or empty environment ID provided")
		return "", fmt.Errorf(errEnvironmentNotFound)
	}

	payload := ScalrWorkspaceRequest{
		Data: ScalrWorkspaceData{
			Type: "workspaces",
			Attributes: ScalrWorkspaceAttributes{
				Name:        workspaceName,
				AutoApply:   autoApprove == "true",
				IacPlatform: iacPlatform,
				IacVersion:  IacVersion,
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

	// if agent pool id is provided, add it to the payload
	if agentPoolId != "" {
		logger.V(1).Info("using agent pool id", "agentPoolId", agentPoolId)
		payload.Data.Relationships.AgentPool = ScalrAgentPoolRelation{
			Data: ScalrAgentPoolData{
				Type: "agent-pools",
				ID:   agentPoolId,
			},
		}
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		logger.Error(err, "failed to marshal Scalr workspace payload")
		return "", fmt.Errorf(errFormatWithCause, errWorkspaceCreationFailed, err)
	}

	logger.V(1).Info("Creating Scalr workspace",
		"scalr workspace", workspaceName,
		"scalr environment id", environmentID,
		"iac platform", iacPlatform,
		"version", IacVersion,
	)

	req, err := http.NewRequestWithContext(ctx, "POST", apiUrl, bytes.NewReader(payloadBytes))
	if err != nil {
		logger.Error(err, "failed to create HTTP request for Scalr workspace")
		return "", fmt.Errorf(errFormatWithCause, errWorkspaceCreationFailed, err)
	}

	req.Header.Set("Content-Type", requestHeaderAccept)
	req.Header.Set("Accept", requestHeaderAccept)
	token, err := s.GetScalrToken(ctx, tfRun)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := s.HTTP.Do(req)
	if err != nil {
		logger.Error(err, "failed to perform HTTP request for Scalr workspace")
		return "", fmt.Errorf(errFormatWithCause, errWorkspaceCreationFailed, err)
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error(err, "failed to read HTTP response body for Scalr workspace")
		return "", fmt.Errorf(errFormatWithCause, errWorkspaceCreationFailed, err)
	}

	if resp.StatusCode != http.StatusCreated {
		logger.Error(fmt.Errorf("Scalr API returned status code %d: %s", resp.StatusCode, string(body)), "failed to create Scalr workspace")
		return "", fmt.Errorf("POST: scalr workspace creation failed with status code %d: %s", resp.StatusCode, string(body))
	}

	var workspaceResponse ScalrWorkspaceResponse
	if err := json.Unmarshal(body, &workspaceResponse); err != nil {
		logger.Error(err, "failed to unmarshal Scalr workspace response")
		return "", fmt.Errorf(errFormatWithCause, errWorkspaceCreationFailed, err)
	}

	workspaceID := workspaceResponse.Data.ID
	logger.V(1).Info("successfully created Scalr workspace", "workspaceID", workspaceID)
	return workspaceID, nil
}

// Delete Scalr workspace
func (s *Service) DeleteScalrWorkspace(ctx context.Context, tfRun *infrav1alpha1.TfRun, workspaceID string) error {
	logger := log.FromContext(ctx)

	if workspaceID == "" {
		logger.Error(fmt.Errorf(errWorkspaceNotFound), "Cannot delete workspace without valid workspace ID, Null or empty workspace ID provided")
		return fmt.Errorf(errWorkspaceNotFound)
	}

	backend := tfRun.Spec.Backend.Cloud
	hostname := backend.Hostname
	token, err := s.GetScalrToken(ctx, tfRun)

	if err != nil {
		return err
	}

	//Construct API URL
	apiURL := fmt.Sprintf("https://%s/api/iacp/v3/workspaces/%s", hostname, workspaceID)
	logger.V(1).Info("deleting Scalr workspace", "url", apiURL, "workspaceID", workspaceID)

	// create DELETE request
	req, err := http.NewRequestWithContext(ctx, "DELETE", apiURL, nil)
	if err != nil {
		logger.Error(err, "failed to create HTTP request for Scalr workspace deletion")
		return fmt.Errorf(errFormatWithCause, errWorkspaceDeletionFailed, err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := s.HTTP.Do(req)
	if err != nil {
		logger.Error(err, "failed to perform HTTP request for Scalr workspace deletion")
		return fmt.Errorf(errFormatWithCause, errWorkspaceDeletionFailed, err)
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error(err, "failed to read HTTP response body for Scalr workspace deletion")
		return fmt.Errorf(errFormatWithCause, errWorkspaceDeletionFailed, err)
	}

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		logger.Error(fmt.Errorf("scalr API returned status code %d: %s", resp.StatusCode, string(body)), "failed to delete Scalr workspace")
		return fmt.Errorf("DELETE: scalr workspace deletion failed with status code %d: %s", resp.StatusCode, string(body))
	}

	if resp.StatusCode == http.StatusNotFound {
		logger.V(1).Info("scalr workspace already deleted or not found", "workspace ID : ", workspaceID)
	} else {
		logger.V(1).Info("successfully deleted Scalr workspace", "workspaceID", workspaceID)
	}

	return nil
}
