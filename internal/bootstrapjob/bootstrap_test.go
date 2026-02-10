package bootstrapjob

import (
	"context"
	"fmt"
	"os"
	"testing"

	infrav1alpha1 "infra.essity.com/orchestrator-api/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newTestTfRun() *infrav1alpha1.TfRun {
	return &infrav1alpha1.TfRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-tfrun",
			Namespace: "default",
		},
		Spec: infrav1alpha1.TfRunSpec{
			Source: infrav1alpha1.TfSource{
				Module:               "https://github.com/example/repo.git",
				Ref:                  "main",
				Path:                 "terraform/env",
				CredentialsSecretRef: "git-credentials",
			},
			Engine: infrav1alpha1.TfEngine{
				Type:    "opentofu",
				Version: "1.8.0",
			},
			ForProvider: infrav1alpha1.TfProviderSpec{
				CredentialsSecretRef: "scalr-credentials",
			},
			Backend: infrav1alpha1.TfBackend{
				Cloud: &infrav1alpha1.CloudBackend{
					Provider:     "scalr",
					Hostname:     "example.scalr.io",
					Organization: "my-org",
					Workspace:    "my-workspace",
				},
			},
			Vars: map[string]*apiextensionsv1.JSON{
				"region":      {Raw: []byte(`"eu-west-1"`)},
				"environment": {Raw: []byte(`"dev"`)},
			},
		},
	}
}

func TestGetEngineImage(t *testing.T) {
	tests := []struct {
		name       string
		engineType string
		expected   string
	}{
		{
			name:       "opentofu engine",
			engineType: "opentofu",
			expected:   "ghcr.io/opentofu/opentofu:latest",
		},
		{
			name:       "terraform engine",
			engineType: "terraform",
			expected:   "hashicorp/terraform:latest",
		},
		{
			name:       "unknown engine defaults to opentofu image",
			engineType: "unknown",
			expected:   "ghcr.io/opentofu/opentofu:latest",
		},
		{
			name:       "empty engine defaults to opentofu image",
			engineType: "",
			expected:   "ghcr.io/opentofu/opentofu:latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getEngineImage(tt.engineType)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestGetTfImageVersion(t *testing.T) {
	tests := []struct {
		name       string
		engineType string
		version    string
		expected   string
	}{
		{
			name:       "opentofu with version returns latest (hardcoded)",
			engineType: "opentofu",
			version:    "1.8.0",
			expected:   "ghcr.io/opentofu/opentofu:latest",
		},
		{
			name:       "opentofu without version returns latest",
			engineType: "opentofu",
			version:    "",
			expected:   "ghcr.io/opentofu/opentofu:latest",
		},
		{
			name:       "terraform with specific version",
			engineType: "terraform",
			version:    "1.5.7",
			expected:   "hashicorp/terraform:1.5.7",
		},
		{
			name:       "terraform without version defaults to latest",
			engineType: "terraform",
			version:    "",
			expected:   "hashicorp/terraform:latest",
		},
		{
			name:       "unknown engine defaults to opentofu image",
			engineType: "unknown",
			version:    "1.0.0",
			expected:   "ghcr.io/opentofu/opentofu:latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tfRun := &infrav1alpha1.TfRun{
				Spec: infrav1alpha1.TfRunSpec{
					Engine: infrav1alpha1.TfEngine{
						Version: tt.version,
					},
				},
			}
			result := getTfImageVersion(tt.engineType, tfRun)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestGetTTL(t *testing.T) {
	tests := []struct {
		name       string
		envKey     string
		envValue   string
		defaultTTL int32
		expected   int32
	}{
		{
			name:       "no env var set returns default",
			envKey:     "TEST_TTL_UNSET",
			envValue:   "",
			defaultTTL: 300,
			expected:   300,
		},
		{
			name:       "valid env var above minimum",
			envKey:     "TEST_TTL_VALID",
			envValue:   "900",
			defaultTTL: 300,
			expected:   900,
		},
		{
			name:       "env var at minimum threshold (600)",
			envKey:     "TEST_TTL_MIN",
			envValue:   "600",
			defaultTTL: 300,
			expected:   600,
		},
		{
			name:       "env var below minimum returns default",
			envKey:     "TEST_TTL_LOW",
			envValue:   "100",
			defaultTTL: 300,
			expected:   300,
		},
		{
			name:       "invalid env var returns default",
			envKey:     "TEST_TTL_INVALID",
			envValue:   "notanumber",
			defaultTTL: 300,
			expected:   300,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv(tt.envKey, tt.envValue)
				defer os.Unsetenv(tt.envKey)
			}
			result := getTTL(tt.envKey, tt.defaultTTL)
			if result != tt.expected {
				t.Errorf("expected %d, got %d", tt.expected, result)
			}
		})
	}
}

func TestBuildEnvVars(t *testing.T) {
	b := &BootstrapJob{}

	t.Run("builds TF_VAR env vars from spec vars", func(t *testing.T) {
		tfRun := *newTestTfRun()
		envVars := b.buildEnvVars(tfRun)

		if len(envVars) != 2 {
			t.Fatalf("expected 2 env vars, got %d", len(envVars))
		}

		envMap := make(map[string]string)
		for _, env := range envVars {
			envMap[env.Name] = env.Value
		}

		if val, ok := envMap["TF_VAR_region"]; !ok || val != `"eu-west-1"` {
			t.Errorf("expected TF_VAR_region=%q, got %q", `"eu-west-1"`, val)
		}
		if val, ok := envMap["TF_VAR_environment"]; !ok || val != `"dev"` {
			t.Errorf("expected TF_VAR_environment=%q, got %q", `"dev"`, val)
		}
	})

	t.Run("empty vars produces no env vars", func(t *testing.T) {
		tfRun := infrav1alpha1.TfRun{
			Spec: infrav1alpha1.TfRunSpec{},
		}
		envVars := b.buildEnvVars(tfRun)
		if len(envVars) != 0 {
			t.Errorf("expected 0 env vars, got %d", len(envVars))
		}
	})

	t.Run("nil var value is skipped", func(t *testing.T) {
		tfRun := infrav1alpha1.TfRun{
			Spec: infrav1alpha1.TfRunSpec{
				Vars: map[string]*apiextensionsv1.JSON{
					"valid":  {Raw: []byte(`"value"`)},
					"nilvar": nil,
				},
			},
		}
		envVars := b.buildEnvVars(tfRun)
		if len(envVars) != 1 {
			t.Errorf("expected 1 env var (nil skipped), got %d", len(envVars))
		}
		if envVars[0].Name != "TF_VAR_valid" {
			t.Errorf("expected TF_VAR_valid, got %s", envVars[0].Name)
		}
	})
}

func TestForEngine(t *testing.T) {
	scheme := runtime.NewScheme()
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	tests := []struct {
		name        string
		engine      string
		expectErr   bool
		errContains string
	}{
		{
			name:      "opentofu engine",
			engine:    "opentofu",
			expectErr: false,
		},
		{
			name:      "terraform engine",
			engine:    "terraform",
			expectErr: false,
		},
		{
			name:        "terragrunt not implemented",
			engine:      "terragrunt",
			expectErr:   true,
			errContains: "not yet implemented",
		},
		{
			name:        "unknown engine defaults to opentofu with error",
			engine:      "pulumi",
			expectErr:   true,
			errContains: "unsupported engine",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder, err := ForEngine(k8sClient, tt.engine, []string{})

			if tt.expectErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errContains != "" {
					if got := err.Error(); !contains(got, tt.errContains) {
						t.Errorf("expected error containing %q, got %q", tt.errContains, got)
					}
				}
				// terragrunt returns nil builder
				if tt.engine == "terragrunt" && builder != nil {
					t.Error("expected nil builder for terragrunt")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if builder == nil {
				t.Fatal("expected non-nil builder")
			}
		})
	}
}

func TestBuildJobTemplate(t *testing.T) {
	b := &BootstrapJob{
		EngineType: "opentofu",
	}

	t.Run("apply job has correct structure", func(t *testing.T) {
		tfRun := newTestTfRun()
		envVars := []corev1.EnvVar{
			{Name: "TF_VAR_region", Value: `"eu-west-1"`},
		}
		gitCloneCmd := "git clone --depth 1 https://github.com/example/repo.git /workdDir"

		job := b.buildJobTemplate(tfRun, "test-job-apply", "apply", "tofu init && tofu apply -auto-approve", envVars, gitCloneCmd)

		if job.Name != "test-job-apply" {
			t.Errorf("expected job name test-job-apply, got %s", job.Name)
		}
		if job.Namespace != "default" {
			t.Errorf("expected namespace default, got %s", job.Namespace)
		}

		// Check labels
		if job.Labels["opentofu"] != "test-tfrun" {
			t.Errorf("expected label opentofu=test-tfrun, got %s", job.Labels["opentofu"])
		}
		if job.Labels[labelJobType] != "apply" {
			t.Errorf("expected label job-type=apply, got %s", job.Labels[labelJobType])
		}

		// Check backoff limit
		if *job.Spec.BackoffLimit != int32(jobBackoffLimit) {
			t.Errorf("expected backoff limit %d, got %d", jobBackoffLimit, *job.Spec.BackoffLimit)
		}

		// Check TTL
		if *job.Spec.TTLSecondsAfterFinished != ttlSuccessDefault {
			t.Errorf("expected TTL %d, got %d", ttlSuccessDefault, *job.Spec.TTLSecondsAfterFinished)
		}

		// Check init container
		initContainers := job.Spec.Template.Spec.InitContainers
		if len(initContainers) != 1 {
			t.Fatalf("expected 1 init container, got %d", len(initContainers))
		}
		if initContainers[0].Name != "git-clone" {
			t.Errorf("expected init container name git-clone, got %s", initContainers[0].Name)
		}
		if initContainers[0].Image != "alpine/git:latest" {
			t.Errorf("expected git image alpine/git:latest, got %s", initContainers[0].Image)
		}

		// Check main container
		containers := job.Spec.Template.Spec.Containers
		if len(containers) != 1 {
			t.Fatalf("expected 1 container, got %d", len(containers))
		}
		if containers[0].Name != "opentofu" {
			t.Errorf("expected container name opentofu, got %s", containers[0].Name)
		}

		// Working dir should include path
		expectedWorkDir := fmt.Sprintf("%s/%s", workdDir, "terraform/env")
		if containers[0].WorkingDir != expectedWorkDir {
			t.Errorf("expected working dir %s, got %s", expectedWorkDir, containers[0].WorkingDir)
		}

		// Check env vars passed through
		if len(containers[0].Env) != 1 || containers[0].Env[0].Name != "TF_VAR_region" {
			t.Errorf("expected env var TF_VAR_region, got %v", containers[0].Env)
		}

		// Check restart policy
		if job.Spec.Template.Spec.RestartPolicy != corev1.RestartPolicyNever {
			t.Errorf("expected restart policy Never, got %s", job.Spec.Template.Spec.RestartPolicy)
		}

		// Check volumes
		if len(job.Spec.Template.Spec.Volumes) != 1 || job.Spec.Template.Spec.Volumes[0].Name != "workspace" {
			t.Error("expected workspace volume")
		}
	})

	t.Run("destroy job uses destroy TTL", func(t *testing.T) {
		tfRun := newTestTfRun()
		job := b.buildJobTemplate(tfRun, "test-job-destroy", "destroy", "tofu init && tofu destroy", []corev1.EnvVar{}, "git clone repo")

		if job.Labels[labelJobType] != "destroy" {
			t.Errorf("expected label job-type=destroy, got %s", job.Labels[labelJobType])
		}
		if *job.Spec.TTLSecondsAfterFinished != ttlFailureDefault {
			t.Errorf("expected destroy TTL %d, got %d", ttlFailureDefault, *job.Spec.TTLSecondsAfterFinished)
		}
	})

	t.Run("no source path uses default working dir", func(t *testing.T) {
		tfRun := newTestTfRun()
		tfRun.Spec.Source.Path = ""
		job := b.buildJobTemplate(tfRun, "test-job", "apply", "tofu init", []corev1.EnvVar{}, "git clone repo")

		if job.Spec.Template.Spec.Containers[0].WorkingDir != workdDir {
			t.Errorf("expected working dir %s, got %s", workdDir, job.Spec.Template.Spec.Containers[0].WorkingDir)
		}
	})
}

func TestGetGitCredentials(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	t.Run("retrieves token from secret", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "git-credentials",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"token": []byte("ghp_testtoken123"),
			},
		}

		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
		b := &BootstrapJob{K8s: k8sClient}
		tfRun := newTestTfRun()

		token, err := b.getGitCredentials(context.Background(), tfRun, "git-credentials")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if token != "ghp_testtoken123" {
			t.Errorf("expected token ghp_testtoken123, got %s", token)
		}
	})

	t.Run("uses default secret name when empty", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "git-credentials",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"token": []byte("ghp_default"),
			},
		}

		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
		b := &BootstrapJob{K8s: k8sClient}
		tfRun := newTestTfRun()

		token, err := b.getGitCredentials(context.Background(), tfRun, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if token != "ghp_default" {
			t.Errorf("expected token ghp_default, got %s", token)
		}
	})

	t.Run("returns error when secret not found", func(t *testing.T) {
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		b := &BootstrapJob{K8s: k8sClient}
		tfRun := newTestTfRun()

		_, err := b.getGitCredentials(context.Background(), tfRun, "nonexistent")
		if err == nil {
			t.Fatal("expected error for missing secret")
		}
	})

	t.Run("returns error when token key missing", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "git-credentials",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"username": []byte("user"),
			},
		}

		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
		b := &BootstrapJob{K8s: k8sClient}
		tfRun := newTestTfRun()

		_, err := b.getGitCredentials(context.Background(), tfRun, "git-credentials")
		if err == nil {
			t.Fatal("expected error for missing token key")
		}
	})

	t.Run("returns error when token is empty", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "git-credentials",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"token": []byte(""),
			},
		}

		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
		b := &BootstrapJob{K8s: k8sClient}
		tfRun := newTestTfRun()

		_, err := b.getGitCredentials(context.Background(), tfRun, "git-credentials")
		if err == nil {
			t.Fatal("expected error for empty token")
		}
	})
}

func TestBuildJob(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	t.Run("builds complete apply job", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "git-credentials",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"token": []byte("ghp_testtoken"),
			},
		}

		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
		b := &BootstrapJob{
			K8s:        k8sClient,
			EngineType: "opentofu",
			EngineArgs: []string{},
		}
		tfRun := newTestTfRun()

		// Pre-build backend env vars (previously computed internally by cloudBackend())
		backendEnvVars := []corev1.EnvVar{
			{Name: "TF_CLOUD_HOSTNAME", Value: "example.scalr.io"},
			{Name: "TF_CLOUD_ORGANIZATION", Value: "my-org"},
			{Name: "TF_WORKSPACE", Value: "my-workspace"},
		}

		job, err := b.BuildJob(context.Background(), tfRun, "apply", "test-tfrun-apply-abc123", backendEnvVars)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if job.Name != "test-tfrun-apply-abc123" {
			t.Errorf("expected job name test-tfrun-apply-abc123, got %s", job.Name)
		}

		// Verify env vars include both TF_VARs and backend vars
		mainContainer := job.Spec.Template.Spec.Containers[0]
		envNames := make(map[string]bool)
		for _, env := range mainContainer.Env {
			envNames[env.Name] = true
		}

		if !envNames["TF_CLOUD_HOSTNAME"] {
			t.Error("expected TF_CLOUD_HOSTNAME env var")
		}
		if !envNames["TF_CLOUD_ORGANIZATION"] {
			t.Error("expected TF_CLOUD_ORGANIZATION env var")
		}
		if !envNames["TF_WORKSPACE"] {
			t.Error("expected TF_WORKSPACE env var")
		}

		// Verify init container has git clone command with token
		initArgs := job.Spec.Template.Spec.InitContainers[0].Args[0]
		if !contains(initArgs, "ghp_testtoken@github.com") {
			t.Error("expected git clone command to contain authenticated URL")
		}
	})

	t.Run("builds job with public repo (no git credentials secret)", func(t *testing.T) {
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		b := &BootstrapJob{
			K8s:        k8sClient,
			EngineType: "opentofu",
			EngineArgs: []string{},
		}
		tfRun := newTestTfRun()
		// The default secret name "git-credentials" will be used but won't be found
		// This should return an error since the secret doesn't exist
		_, err := b.BuildJob(context.Background(), tfRun, "apply", "test-job", []corev1.EnvVar{})
		if err == nil {
			t.Fatal("expected error when git credentials secret not found")
		}
	})

	t.Run("succeeds with no backend env vars", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "git-credentials",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"token": []byte("ghp_testtoken"),
			},
		}

		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
		b := &BootstrapJob{
			K8s:        k8sClient,
			EngineType: "opentofu",
			EngineArgs: []string{},
		}
		tfRun := newTestTfRun()
		tfRun.Spec.Backend.Cloud = nil

		job, err := b.BuildJob(context.Background(), tfRun, "apply", "test-job", []corev1.EnvVar{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should only have TF_VAR_* env vars, no backend vars
		mainContainer := job.Spec.Template.Spec.Containers[0]
		for _, env := range mainContainer.Env {
			if env.Name == "TF_CLOUD_HOSTNAME" {
				t.Error("did not expect TF_CLOUD_HOSTNAME when no backend env vars passed")
			}
		}
	})
}

// contains checks if s contains substr
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
