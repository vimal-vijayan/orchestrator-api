package scalr

import (
	"context"
	"fmt"
	"net/http"
	"time"

	infrav1alpha1 "infra.essity.com/orchstrator-api/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	errMissingCredentials      = "Missing credentials secret reference in TfRun spec"
	errSecretNotFound          = "Credentials secret not found for remote workspaces"
	errEnvironmentNotFound     = "Scalr environment not found"
	errWorkspaceNotFound       = "Scalr workspace not found"
	errWorkspaceCreationFailed = "Failed to create Scalr workspace"
	errWorkspaceDeletionFailed = "Failed to delete Scalr workspace"
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
		logger.Error(fmt.Errorf(errMissingCredentials), "No tofu credentials are provided for remote workspaces, The operator will fail to connect to the remote workspace")
		return "", fmt.Errorf(errMissingCredentials)
	}

	secret := &corev1.Secret{}
	key := types.NamespacedName{
		Name:      secretName,
		Namespace: tfRun.Namespace,
	}

	if err := s.K8s.Get(ctx, key, secret); err != nil {
		logger.Error(err, "Failed to get remote state provider credentials from secret", "secret", key)
		return "", fmt.Errorf("Failed to get remote state provider credentials from %s of secret %s with error %w", key.Namespace, key.Name, err)
	}

	token, ok := secret.Data["token"]
	if !ok || len(token) == 0 {
		logger.Error(fmt.Errorf(errSecretNotFound), "Token not found in secret", "secret", key)
		return "", fmt.Errorf("Token not found in secret %s/%s", key.Namespace, key.Name)
	}

	logger.V(1).Info("Successfully retrieved Scalr token from secret", "secret", key)
	return string(token), nil
}
