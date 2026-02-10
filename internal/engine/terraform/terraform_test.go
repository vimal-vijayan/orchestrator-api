package terraform

import (
	"strings"
	"testing"
)

func TestNewTerraform(t *testing.T) {
	t.Run("nil args get defaulted to -auto-approve", func(t *testing.T) {
		tf := NewTerraform(nil)
		if tf == nil {
			t.Fatal("expected non-nil Terraform")
		}
		if len(tf.Args) != 1 || tf.Args[0] != "-auto-approve" {
			t.Errorf("expected default args [-auto-approve], got %v", tf.Args)
		}
	})

	t.Run("provided args are preserved", func(t *testing.T) {
		args := []string{"-no-color", "-input=false"}
		tf := NewTerraform(args)
		if len(tf.Args) != 2 {
			t.Fatalf("expected 2 args, got %d", len(tf.Args))
		}
		if tf.Args[0] != "-no-color" || tf.Args[1] != "-input=false" {
			t.Errorf("expected args [-no-color -input=false], got %v", tf.Args)
		}
	})
}

func TestTerraformCommand(t *testing.T) {
	tests := []struct {
		name             string
		args             []string
		jobType          string
		expectContains   []string
		expectNotContain []string
	}{
		{
			name:           "apply job type",
			args:           nil,
			jobType:        "apply",
			expectContains: []string{"terraform init", "terraform apply"},
		},
		{
			name:           "destroy job type",
			args:           nil,
			jobType:        "destroy",
			expectContains: []string{"terraform init", "terraform plan -destroy", "terraform destroy"},
		},
		{
			name:           "unknown job type returns default command",
			args:           nil,
			jobType:        "plan",
			expectContains: []string{"terraform init && terraform apply -auto-approve"},
		},
		{
			name:           "empty job type returns default command",
			args:           nil,
			jobType:        "",
			expectContains: []string{"terraform init && terraform apply -auto-approve"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tf := NewTerraform(tt.args)
			cmd := tf.Command(tt.jobType)

			for _, expected := range tt.expectContains {
				if !strings.Contains(cmd, expected) {
					t.Errorf("expected command to contain %q, got %q", expected, cmd)
				}
			}
		})
	}
}
