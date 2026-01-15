// backend package defines interfaces and types for different backend implementations.

package backend

import (
	"context"
	"fmt"
	"strings"

	"infra.essity.com/orchstrator-api/api/v1alpha1"
	scalr "infra.essity.com/orchstrator-api/internal/scalr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ProviderScalr = "scalr"
	// ProviderTerraformCloud is not yet implemented
	ProviderTerraformCloud = "terraformCloud"
)

type ScalrBackend struct {
	Scalr *scalr.Service
}

type CloudBackend interface {
	EnsureWorkspace(ctx context.Context, tfRun *v1alpha1.TfRun) (workspaceID string, err error)
	DeleteWorkspace(ctx context.Context, tfRun *v1alpha1.TfRun, workspaceID string) error
}

func (s *ScalrBackend) EnsureWorkspace(ctx context.Context, tfRun *v1alpha1.TfRun) (string, error) {

	if tfRun.Spec.Backend.Cloud == nil {
		return "", fmt.Errorf("backend configuration is nil")
	}

	backend := tfRun.Spec.Backend.Cloud
	if backend == nil {
		return "", fmt.Errorf("cloud backend configuration is nil")
	}

	environmentID := backend.EnvironmentID
	if environmentID == "" {
		var err error
		environmentID, err = s.Scalr.GetScalrEnvironmentID(ctx, tfRun)
		if err != nil {
			return "", err
		}
	}

	return s.Scalr.CreateScalrWorkspace(ctx, tfRun, environmentID)
}

func (s *ScalrBackend) DeleteWorkspace(ctx context.Context, tfRun *v1alpha1.TfRun, workspaceID string) error {
	return s.Scalr.DeleteScalrWorkspace(ctx, tfRun, workspaceID)
}

// ForProvider returns the appropriate CloudBackend implementation based on the provider string.
// the controller call this once per TfRun reconciliation to get the backend implementation.
func ForProvider(k8s client.Client, provider string) (CloudBackend, error) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case ProviderScalr:
		return &ScalrBackend{
			Scalr: scalr.NewService(k8s),
		}, nil
	// Terraform Cloud backend is not yet implemented
	// case ProviderTerraformCloud:
	// 	return &TerraformCloudBackend{
	// 		// Initialize Terraform Cloud backend service here
	// 	}, nil
	default:
		return nil, fmt.Errorf("unsupported cloud backend provider: %s", provider)
	}
}
