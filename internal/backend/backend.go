package backend

import (
	"context"
	"fmt"
	"strings"

	"infra.essity.com/orchestrator-api/api/v1alpha1"
	scalr "infra.essity.com/orchestrator-api/internal/scalr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	BackendScalr = "scalr"
	// BackendTerraformCloud is not yet implemented
	BackendTerraformCloud = "terraformCloud"
	// BackendAzure and BackendS3 are not yet implemented
	BackendAzure           = "azure"
	BackendS3              = "s3"
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
	case BackendScalr:
		return &ScalrBackend{
			Scalr: scalr.NewService(k8s),
		}, nil
	// Terraform Cloud backend is not yet implemented
	// case BackendTerraformCloud:
	// 	return &TerraformCloudBackend{
	// 		// Initialize Terraform Cloud backend service here
	// 	}, nil
	// Azure backend is not yet implemented
	// case BackendAzure:
	// 	return &AzureBackend{
	// 		// Initialize Azure backend service here
	// 	}, nil
	// S3 backend is not yet implemented
	// case BackendS3:
	// 	return &S3Backend{
	// 		// Initialize S3 backend service here
	// 	}, nil
	default:
		return nil, fmt.Errorf("unsupported cloud backend provider: %s", provider)
	}
}
