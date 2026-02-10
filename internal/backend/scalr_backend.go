package backend

import (
	"context"
	"fmt"
	"strings"

	"infra.essity.com/orchestrator-api/api/v1alpha1"
	scalr "infra.essity.com/orchestrator-api/internal/scalr"
	corev1 "k8s.io/api/core/v1"
)

type ScalrBackend struct {
	Scalr *scalr.Service
}

func (s *ScalrBackend) EnsureStateTarget(ctx context.Context, tfRun *v1alpha1.TfRun) (string, error) {
	environmentID, err := s.getEnvironmentId(ctx, tfRun)
	if err != nil {
		return "", err
	}

	return s.Scalr.CreateScalrWorkspace(ctx, tfRun, environmentID)
}

func (s *ScalrBackend) DeleteStateTarget(ctx context.Context, tfRun *v1alpha1.TfRun, stateTargetID string) error {
	return s.Scalr.DeleteScalrWorkspace(ctx, tfRun, stateTargetID)
}

func (s *ScalrBackend) GetStateTarget(ctx context.Context, tfRun *v1alpha1.TfRun, stateTargetID string) (string, error) {
	environmentID, err := s.getEnvironmentId(ctx, tfRun)
	if err != nil {
		return "", err
	}

	return s.Scalr.GetWorkspace(ctx, tfRun, stateTargetID, environmentID)
}

func (s *ScalrBackend) BackendEnvVars(tfRun *v1alpha1.TfRun) ([]corev1.EnvVar, error) {
	cloudBackend := tfRun.Spec.Backend.Cloud
	if cloudBackend == nil {
		return nil, fmt.Errorf("cloud backend configuration is required for Scalr")
	}

	envVars := []corev1.EnvVar{
		{Name: "TF_CLOUD_HOSTNAME", Value: cloudBackend.Hostname},
		{Name: "TF_CLOUD_ORGANIZATION", Value: cloudBackend.Organization},
		{Name: "TF_WORKSPACE", Value: cloudBackend.Workspace},
	}

	if tfRun.Spec.ForProvider.CredentialsSecretRef != "" {
		envVars = append(envVars, corev1.EnvVar{
			Name: fmt.Sprintf("TF_TOKEN_%s", strings.ReplaceAll(cloudBackend.Hostname, ".", "_")),
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

	return envVars, nil
}

func (s *ScalrBackend) Type() string {
	return BackendScalr
}

func (s *ScalrBackend) getEnvironmentId(ctx context.Context, tfRun *v1alpha1.TfRun) (string, error) {
	if tfRun.Spec.Backend.Cloud == nil {
		return "", fmt.Errorf("backend configuration is nil")
	}

	backend := tfRun.Spec.Backend.Cloud
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
