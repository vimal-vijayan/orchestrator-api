package backend

import (
	"context"
	"fmt"
	"strings"

	"infra.essity.com/orchestrator-api/api/v1alpha1"
	scalr "infra.essity.com/orchestrator-api/internal/scalr"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	BackendScalr = "scalr"
	// BackendTerraformCloud is not yet implemented
	BackendTerraformCloud = "terraformCloud"
	// BackendAzure and BackendS3 are not yet implemented
	BackendAzure = "azure"
	BackendS3    = "s3"
)

// RemoteBackend defines the interface for all remote backend implementations.
// Cloud backends (Scalr, TFC) manage workspaces; storage backends (S3, Azure)
// verify bucket/container access.
type RemoteBackend interface {
	// EnsureStateTarget ensures the remote state target is ready.
	// For workspace-based backends, creates/verifies the workspace and returns its ID.
	// For storage backends, verifies access and returns "".
	EnsureStateTarget(ctx context.Context, tfRun *v1alpha1.TfRun) (stateTargetID string, err error)

	// DeleteStateTarget cleans up the remote state target on TfRun deletion.
	// No-op for storage backends.
	DeleteStateTarget(ctx context.Context, tfRun *v1alpha1.TfRun, stateTargetID string) error

	// GetStateTarget retrieves information about the remote state target.
	// Returns the verified ID.
	GetStateTarget(ctx context.Context, tfRun *v1alpha1.TfRun, stateTargetID string) (string, error)

	// BackendEnvVars returns the environment variables needed for terraform/tofu
	// to connect to this backend. Each implementation owns its own env var logic.
	BackendEnvVars(tfRun *v1alpha1.TfRun) ([]corev1.EnvVar, error)

	// Type returns the backend type identifier (e.g., "scalr", "s3", "azure").
	Type() string
}

// ForProvider returns the appropriate RemoteBackend implementation based on the provider string.
// The controller calls this once per TfRun reconciliation to get the backend implementation.
func ForProvider(k8s client.Client, provider string) (RemoteBackend, error) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case BackendScalr:
		return &ScalrBackend{
			Scalr: scalr.NewService(k8s),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported remote backend provider: %s", provider)
	}
}
