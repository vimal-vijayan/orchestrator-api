package backend

import (
	"testing"

	"infra.essity.com/orchestrator-api/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newTestTfRun() *v1alpha1.TfRun {
	return &v1alpha1.TfRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-tfrun",
			Namespace: "default",
		},
		Spec: v1alpha1.TfRunSpec{
			ForProvider: v1alpha1.TfProviderSpec{
				CredentialsSecretRef: "scalr-credentials",
			},
			Backend: v1alpha1.TfBackend{
				Cloud: &v1alpha1.CloudBackend{
					Provider:     "scalr",
					Hostname:     "example.scalr.io",
					Organization: "my-org",
					Workspace:    "my-workspace",
				},
			},
		},
	}
}

func TestScalrBackendEnvVars(t *testing.T) {
	s := &ScalrBackend{}

	t.Run("builds cloud backend env vars", func(t *testing.T) {
		tfRun := newTestTfRun()
		envVars, err := s.BackendEnvVars(tfRun)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		envMap := make(map[string]string)
		secretRefMap := make(map[string]string)
		for _, env := range envVars {
			if env.ValueFrom != nil && env.ValueFrom.SecretKeyRef != nil {
				secretRefMap[env.Name] = env.ValueFrom.SecretKeyRef.Name
			} else {
				envMap[env.Name] = env.Value
			}
		}

		if envMap["TF_CLOUD_HOSTNAME"] != "example.scalr.io" {
			t.Errorf("expected TF_CLOUD_HOSTNAME=example.scalr.io, got %q", envMap["TF_CLOUD_HOSTNAME"])
		}
		if envMap["TF_CLOUD_ORGANIZATION"] != "my-org" {
			t.Errorf("expected TF_CLOUD_ORGANIZATION=my-org, got %q", envMap["TF_CLOUD_ORGANIZATION"])
		}
		if envMap["TF_WORKSPACE"] != "my-workspace" {
			t.Errorf("expected TF_WORKSPACE=my-workspace, got %q", envMap["TF_WORKSPACE"])
		}

		tokenKey := "TF_TOKEN_example_scalr_io"
		secretName, ok := secretRefMap[tokenKey]
		if !ok {
			t.Fatalf("expected secret ref for %s", tokenKey)
		}
		if secretName != "scalr-credentials" {
			t.Errorf("expected secret name scalr-credentials, got %q", secretName)
		}
	})

	t.Run("no credentials secret ref skips token env var", func(t *testing.T) {
		tfRun := newTestTfRun()
		tfRun.Spec.ForProvider.CredentialsSecretRef = ""
		envVars, err := s.BackendEnvVars(tfRun)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(envVars) != 3 {
			t.Errorf("expected 3 env vars without token, got %d", len(envVars))
		}
		for _, env := range envVars {
			if env.ValueFrom != nil {
				t.Errorf("did not expect secret ref env var, got %s", env.Name)
			}
		}
	})

	t.Run("nil cloud backend returns error", func(t *testing.T) {
		tfRun := newTestTfRun()
		tfRun.Spec.Backend.Cloud = nil
		_, err := s.BackendEnvVars(tfRun)
		if err == nil {
			t.Fatal("expected error for nil cloud backend")
		}
	})
}

func TestScalrBackendType(t *testing.T) {
	s := &ScalrBackend{}
	if s.Type() != BackendScalr {
		t.Errorf("expected type %q, got %q", BackendScalr, s.Type())
	}
}
