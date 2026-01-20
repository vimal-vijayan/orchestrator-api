// backend package defines interfaces and types for different backend implementations.

package backend

import (
	"context"
	"fmt"

	"infra.essity.com/orchstrator-api/api/v1alpha1"
	scalr "infra.essity.com/orchstrator-api/internal/scalr"
)

type ScalrBackend struct {
	Scalr *scalr.Service
}

func (s *ScalrBackend) EnsureWorkspace(ctx context.Context, tfRun *v1alpha1.TfRun) (string, error) {

	environmentID, err := s.getEnvironmentId(ctx, tfRun)
	if err != nil {
		return "", err
	}

	return s.Scalr.CreateScalrWorkspace(ctx, tfRun, environmentID)
}

func (s *ScalrBackend) DeleteWorkspace(ctx context.Context, tfRun *v1alpha1.TfRun, workspaceID string) error {
	return s.Scalr.DeleteScalrWorkspace(ctx, tfRun, workspaceID)
}

func (s *ScalrBackend) GetWorkspace(ctx context.Context, tfRun *v1alpha1.TfRun, workspaceID string) (string, error) {

	environmentID, err := s.getEnvironmentId(ctx, tfRun)
	if err != nil {
		return "", err
	}

	return s.Scalr.GetWorkspace(ctx, tfRun, workspaceID, environmentID)
}

func (s *ScalrBackend) getEnvironmentId(ctx context.Context, tfRun *v1alpha1.TfRun) (string, error) {

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

	return environmentID, nil
}
