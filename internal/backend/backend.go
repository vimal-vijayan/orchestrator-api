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

type CloudBackend interface {
	EnsureWorkspace(ctx context.Context, tfRun *v1alpha1.TfRun) (workspaceID string, err error)
	DeleteWorkspace(ctx context.Context, tfRun *v1alpha1.TfRun, workspaceID string) error
	GetWorkspace(ctx context.Context, tfRun *v1alpha1.TfRun, workspaceID string) (string, error)
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
