package engine

import (
	"testing"

	"infra.essity.com/orchestrator-api/internal/engine/opentofu"
	"infra.essity.com/orchestrator-api/internal/engine/terraform"
)

func TestForEngine(t *testing.T) {
	tests := []struct {
		name         string
		engineType   string
		args         []string
		expectedType string
	}{
		{
			name:         "terraform engine",
			engineType:   "terraform",
			args:         []string{"-auto-approve"},
			expectedType: "*terraform.Terraform",
		},
		{
			name:         "opentofu engine",
			engineType:   "opentofu",
			args:         []string{"-auto-approve"},
			expectedType: "*opentofu.Opentofu",
		},
		{
			name:         "unknown engine defaults to opentofu",
			engineType:   "unknown",
			args:         []string{},
			expectedType: "*opentofu.Opentofu",
		},
		{
			name:         "empty engine defaults to opentofu",
			engineType:   "",
			args:         []string{},
			expectedType: "*opentofu.Opentofu",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := ForEngine(tt.engineType, tt.args)
			if engine == nil {
				t.Fatal("expected non-nil engine")
			}

			switch tt.expectedType {
			case "*terraform.Terraform":
				if _, ok := engine.(*terraform.Terraform); !ok {
					t.Errorf("expected *terraform.Terraform, got %T", engine)
				}
			case "*opentofu.Opentofu":
				if _, ok := engine.(*opentofu.Opentofu); !ok {
					t.Errorf("expected *opentofu.Opentofu, got %T", engine)
				}
			}
		})
	}
}
