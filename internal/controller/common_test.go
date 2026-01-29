/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	infrav1alpha1 "infra.essity.com/orchestrator-api/api/v1alpha1"
)

const (
	unExpectedError = "an unexpected error occurred: %v"
	sampleModule    = "github.com/example/module"
)

// TestComputeSpecHash tests the computeSpecHash function
func TestComputeSpecHash(t *testing.T) {
	reconciler := &TfRunReconciler{}

	t.Run("same spec produces same hash", func(t *testing.T) {
		tfRun1 := &infrav1alpha1.TfRun{
			Spec: infrav1alpha1.TfRunSpec{
				Source: infrav1alpha1.TfSource{
					Module: sampleModule,
					Ref:    "main",
					Path:   "./config",
				},
			},
		}

		hash1, err := reconciler.computeSpecHash(tfRun1)
		if err != nil {
			t.Fatalf(unExpectedError, err)
		}

		hash2, err := reconciler.computeSpecHash(tfRun1)
		if err != nil {
			t.Fatalf(unExpectedError, err)
		}

		if hash1 != hash2 {
			t.Errorf("expected same hash for same spec, got %s and %s", hash1, hash2)
		}
	})

	t.Run("different specs produce different hashes", func(t *testing.T) {
		tfRun1 := &infrav1alpha1.TfRun{
			Spec: infrav1alpha1.TfRunSpec{
				Source: infrav1alpha1.TfSource{
					Module: "github.com/example/module1",
				},
			},
		}

		tfRun2 := &infrav1alpha1.TfRun{
			Spec: infrav1alpha1.TfRunSpec{
				Source: infrav1alpha1.TfSource{
					Module: "github.com/example/module2",
				},
			},
		}

		hash1, err := reconciler.computeSpecHash(tfRun1)
		if err != nil {
			t.Fatalf(unExpectedError, err)
		}

		hash2, err := reconciler.computeSpecHash(tfRun2)
		if err != nil {
			t.Fatalf(unExpectedError, err)
		}

		if hash1 == hash2 {
			t.Errorf("expected different hashes for different specs, got same: %s", hash1)
		}
	})

	t.Run("hash changes when vars change", func(t *testing.T) {
		tfRun1 := &infrav1alpha1.TfRun{
			Spec: infrav1alpha1.TfRunSpec{
				Source: infrav1alpha1.TfSource{
					Module: sampleModule,
				},
				Vars: map[string]*apiextensionsv1.JSON{
					"env": {Raw: []byte(`"dev"`)},
				},
			},
		}

		tfRun2 := &infrav1alpha1.TfRun{
			Spec: infrav1alpha1.TfRunSpec{
				Source: infrav1alpha1.TfSource{
					Module: sampleModule,
				},
				Vars: map[string]*apiextensionsv1.JSON{
					"env": {Raw: []byte(`"prod"`)},
				},
			},
		}

		hash1, err := reconciler.computeSpecHash(tfRun1)
		if err != nil {
			t.Fatalf(unExpectedError, err)
		}

		hash2, err := reconciler.computeSpecHash(tfRun2)
		if err != nil {
			t.Fatalf(unExpectedError, err)
		}

		if hash1 == hash2 {
			t.Errorf("expected different hashes when vars change, got same: %s", hash1)
		}
	})
}

// TestIsJobActive tests the isJobActive function
func TestIsJobActive(t *testing.T) {
	reconciler := &TfRunReconciler{}

	tests := []struct {
		name     string
		job      *batchv1.Job
		expected bool
	}{
		{
			name: "job with active pods",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Active: 1,
				},
			},
			expected: true,
		},
		{
			name: "job with no active pods",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Active: 0,
				},
			},
			expected: false,
		},
		{
			name: "job with multiple active pods",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Active: 3,
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := reconciler.isJobActive(tt.job)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// TestIsJobSucceeded tests the isJobSucceeded function
func TestIsJobSucceeded(t *testing.T) {
	reconciler := &TfRunReconciler{}

	tests := []struct {
		name     string
		job      *batchv1.Job
		expected bool
	}{
		{
			name: "job succeeded",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Succeeded: 1,
				},
			},
			expected: true,
		},
		{
			name: "job not succeeded",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Succeeded: 0,
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := reconciler.isJobSucceeded(tt.job)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// TestIsJobFailed tests the isJobFailed function
func TestIsJobFailed(t *testing.T) {
	reconciler := &TfRunReconciler{}

	tests := []struct {
		name     string
		job      *batchv1.Job
		expected bool
	}{
		{
			name: "job failed",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Failed: 1,
				},
			},
			expected: true,
		},
		{
			name: "job not failed",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Failed: 0,
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := reconciler.isJobFailed(tt.job)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// Example of a table-driven test pattern for Job status combinations
func TestJobStatusScenarios(t *testing.T) {
	reconciler := &TfRunReconciler{}

	scenarios := []struct {
		name          string
		active        int32
		succeeded     int32
		failed        int32
		wantActive    bool
		wantSucceeded bool
		wantFailed    bool
	}{
		{
			name:          "job is running",
			active:        1,
			succeeded:     0,
			failed:        0,
			wantActive:    true,
			wantSucceeded: false,
			wantFailed:    false,
		},
		{
			name:          "job completed successfully",
			active:        0,
			succeeded:     1,
			failed:        0,
			wantActive:    false,
			wantSucceeded: true,
			wantFailed:    false,
		},
		{
			name:          "job failed",
			active:        0,
			succeeded:     0,
			failed:        1,
			wantActive:    false,
			wantSucceeded: false,
			wantFailed:    true,
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			job := &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-job",
				},
				Status: batchv1.JobStatus{
					Active:    scenario.active,
					Succeeded: scenario.succeeded,
					Failed:    scenario.failed,
				},
			}

			if got := reconciler.isJobActive(job); got != scenario.wantActive {
				t.Errorf("isJobActive() = %v, want %v", got, scenario.wantActive)
			}
			if got := reconciler.isJobSucceeded(job); got != scenario.wantSucceeded {
				t.Errorf("isJobSucceeded() = %v, want %v", got, scenario.wantSucceeded)
			}
			if got := reconciler.isJobFailed(job); got != scenario.wantFailed {
				t.Errorf("isJobFailed() = %v, want %v", got, scenario.wantFailed)
			}
		})
	}
}
