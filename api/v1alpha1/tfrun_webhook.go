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

package v1alpha1

import (
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var tfrunlog = logf.Log.WithName("tfrun-resource")

// SetupWebhookWithManager will setup the manager to manage the webhooks
func (r *TfRun) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-infra-essity-com-v1alpha1-tfrun,mutating=true,failurePolicy=fail,sideEffects=None,groups=infra.essity.com,resources=tfruns,verbs=create;update,versions=v1alpha1,name=mtfrun.kb.io,admissionReviewVersions=v1

var _ webhook.Defaulter = &TfRun{}

func (r *TfRun) Default() {
	tfrunlog.Info("default", "name", r.Name)
}

// +kubebuilder:webhook:path=/validate-infra-essity-com-v1alpha1-tfrun,mutating=false,failurePolicy=fail,sideEffects=None,groups=infra.essity.com,resources=tfruns,verbs=create;update,versions=v1alpha1,name=vtfrun.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &TfRun{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *TfRun) ValidateCreate() (admission.Warnings, error) {
	tfrunlog.Info("validate create", "name", r.Name)

	if r.Spec.RunInterval != nil {
		minInterval := metav1.Duration{Duration: 45 * time.Minute}
		if r.Spec.RunInterval.Time.Duration < minInterval.Duration {
			return nil, fmt.Errorf("runInterval must be at least 45 minutes, got %v", r.Spec.RunInterval.Time.Duration)
		}
	}

	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *TfRun) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	tfrunlog.Info("validate update", "name", r.Name)

	if r.Spec.RunInterval != nil {
		minInterval := metav1.Duration{Duration: 45 * time.Minute}
		if r.Spec.RunInterval.Time.Duration < minInterval.Duration {
			return nil, fmt.Errorf("runInterval must be at least 45 minutes, got %v", r.Spec.RunInterval.Time.Duration)
		}
	}

	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *TfRun) ValidateDelete() (admission.Warnings, error) {
	tfrunlog.Info("validate delete", "name", r.Name)
	return nil, nil
}
