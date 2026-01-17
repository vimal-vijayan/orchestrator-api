// remote workspace creation and deletion logic
package controller

import (
	"context"
	"fmt"

	infrav1alpha1 "infra.essity.com/orchstrator-api/api/v1alpha1"
	"infra.essity.com/orchstrator-api/internal/backend"
	"sigs.k8s.io/controller-runtime/pkg/log"
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
