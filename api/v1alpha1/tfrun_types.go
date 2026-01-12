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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// TfRunSpec defines the desired state of TfRun
type TfRunSpec struct {
	//+kubebuilder:validation:Required
	ForProvider TfProviderSpec `json:"forProvider"`
	//+kubebuilder:validation:Required
	Source TfSource `json:"source"`
	//+kubebuilder:validation:Optional
	Backend TfBackend `json:"backend,omitempty"`
	//+kubebuilder:validation:Optional
	Arguments map[string]string `json:"arguments,omitempty"`
	//+kubebuilder:validation:Optional
	Vars map[string]string `json:"vars,omitempty"`
}

// TfRunStatus defines the observed state of TfRun
type TfRunStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	ObservedGeneration int64  `json:"observedGeneration,omitempty"`
	State              string `json:"state,omitempty"`
	Message            string `json:"message,omitempty"`
}

type TfBackend struct {
	// S3 backend configuration
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=s3;storageaccount;cloud
	Type string `json:"type,omitempty"`
}

type TfProviderSpec struct {
	CredentialsSecretRef string `json:"credentialsSecretRef"`
}

// TfSource defines the source of the Terraform configuration
type TfSource struct {
	// Module is the git URL of the Terraform module
	// +kubebuilder:validation:Required
	Module string `json:"module"`
	// Ref is the git reference (branch, tag, commit)
	// +kubebuilder:validation:Optional
	Ref string `json:"ref,omitempty"`
	// Path is the path within the repository to the Terraform configuration
	// +kubebuilder:validation:Optional
	Path string `json:"path,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// TfRun is the Schema for the tfruns API
type TfRun struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TfRunSpec   `json:"spec,omitempty"`
	Status TfRunStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TfRunList contains a list of TfRun
type TfRunList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TfRun `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TfRun{}, &TfRunList{})
}
