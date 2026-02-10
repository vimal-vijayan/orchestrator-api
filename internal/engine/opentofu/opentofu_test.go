package opentofu

import (
	"strings"
	"testing"
)

func TestNewOpentofu(t *testing.T) {
	t.Run("empty args get defaulted to -auto-approve", func(t *testing.T) {
		ot := NewOpentofu([]string{})
		if ot == nil {
			t.Fatal("expected non-nil Opentofu")
		}
		if len(ot.Args) != 1 || ot.Args[0] != "-auto-approve" {
			t.Errorf("expected default args [-auto-approve], got %v", ot.Args)
		}
	})

	t.Run("nil args get defaulted to -auto-approve", func(t *testing.T) {
		ot := NewOpentofu(nil)
		if ot == nil {
			t.Fatal("expected non-nil Opentofu")
		}
		if len(ot.Args) != 1 || ot.Args[0] != "-auto-approve" {
			t.Errorf("expected default args [-auto-approve], got %v", ot.Args)
		}
	})

	t.Run("provided args are preserved", func(t *testing.T) {
		args := []string{"-no-color", "-input=false"}
		ot := NewOpentofu(args)
		if len(ot.Args) != 2 {
			t.Fatalf("expected 2 args, got %d", len(ot.Args))
		}
		if ot.Args[0] != "-no-color" || ot.Args[1] != "-input=false" {
			t.Errorf("expected args [-no-color -input=false], got %v", ot.Args)
		}
	})
}

func TestOpentofuCommand(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		jobType        string
		expectContains []string
	}{
		{
			name:           "apply job type",
			args:           nil,
			jobType:        "apply",
			expectContains: []string{"tofu init", "tofu apply"},
		},
		{
			name:           "apply with custom args",
			args:           []string{"-no-color", "-auto-approve"},
			jobType:        "apply",
			expectContains: []string{"tofu init", "tofu apply -no-color -auto-approve"},
		},
		{
			name:           "destroy job type",
			args:           nil,
			jobType:        "destroy",
			expectContains: []string{"tofu init", "tofu plan -destroy", "tofu destroy"},
		},
		{
			name:           "unknown job type returns default command",
			args:           nil,
			jobType:        "plan",
			expectContains: []string{"tofu init && tofu apply -auto-approve"},
		},
		{
			name:           "empty job type returns default command",
			args:           nil,
			jobType:        "",
			expectContains: []string{"tofu init && tofu apply -auto-approve"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ot := NewOpentofu(tt.args)
			cmd := ot.Command(tt.jobType)

			for _, expected := range tt.expectContains {
				if !strings.Contains(cmd, expected) {
					t.Errorf("expected command to contain %q, got %q", expected, cmd)
				}
			}
		})
	}
}
