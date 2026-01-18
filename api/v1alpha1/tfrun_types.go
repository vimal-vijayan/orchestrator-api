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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// TfRunSpec defines the desired state of TfRun
type TfRunSpec struct {
	//+kubebuilder:validation:Required
	ForProvider TfProviderSpec `json:"forProvider"`
	//+kubebuilder:validation:Optional
	RunInterval *metav1.Duration `json:"runInterval,omitempty"`
	//+kubebuilder:validation:Optional
	Engine TfEngine `json:"engine"`
	//+kubebuilder:validation:Required
	Source TfSource `json:"source"`
	//+kubebuilder:validation:Optional
	Backend TfBackend `json:"backend,omitempty"`
	//+kubebuilder:validation:Optional
	Arguments map[string]string `json:"arguments,omitempty"`
	//+kubebuilder:validation:Optional
	//+kubebuilder:pruning:PreserveUnknownFields
	//+kubebuilder:validation:Schemaless
	Vars map[string]*apiextensionsv1.JSON `json:"vars,omitempty"`
}

// TfRunStatus defines the observed state of TfRun
type TfRunStatus struct {
	// ObservedGeneration reflects the generation of the most recently observed TfRun
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Phase represents the current phase of the TerraformRun
	// +kubebuilder:validation:Enum=Pending;Running;Succeeded;Failed
	Phase string `json:"phase,omitempty"`

	// ActiveJobName is the name of the currently running Job
	ActiveJobName string `json:"activeJobName,omitempty"`

	// LastSuccessfulJobName is the name of the last successful Job
	LastSuccessfulJobName string `json:"lastSuccessfulJobName,omitempty"`

	// WorkspaceID is the Scalr workspace ID created for this TfRun
	WorkspaceID string `json:"workspaceID,omitempty"`

	// WorkspaceReady indicates whether the backend workspace is ready
	WorkspaceReady bool `json:"workspaceReady,omitempty"`

	// LastSpecHash is the hash of the spec from the last successful run
	LastSpecHash string `json:"lastSpecHash,omitempty"`

	// LastRunTime is the timestamp of the last run
	LastRunTime *metav1.Time `json:"lastRunTime,omitempty"`

	// Message provides additional information about the current state
	Message string `json:"message,omitempty"`

	// Conditions represent the latest available observations of the TfRun's state
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type TfEngine struct {
	//+kubebuilder:validation:Optional
	//+kubebuilder:default:="opentofu"
	//+kubebuilder:validation:Enum=terraform;opentofu
	Type string `json:"type,omitempty"`
	//+kubebuilder:validation:Optional
	//+kubebuilder:default:="latest"
	Version string `json:"version,omitempty"`
}

type TfBackend struct {
	// Cloud backend configuration
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:={provider:"scalr", hostname:"essity.scalr.io", organization:"my-org", workspace:"my-workspace"}
	Cloud *CloudBackend `json:"cloud,omitempty"`
	// +kubebuilder:validation:Optional
	StorageAccount *StorageAccountBackend `json:"storageAccount,omitempty"`
}

type CloudBackend struct {
	//+kubebuilder:validation:Required
	//+kubebuilder:validation:Enum=scalr;terraformCloud
	//+kubebuilder:default:="scalr"
	Provider string `json:"provider"`
	//+kubebuilder:validation:Required
	Hostname string `json:"hostname"`
	//+kubebuilder:validation:Required
	Organization string `json:"organization"`
	//+kubebuilder:validation:Required
	Workspace string `json:"workspace"`
	//+kubebuilder:validation:Optional
	EnvironmentID string `json:"environmentId,omitempty"`
	//+kubebuilder:validation:Optional
	AgentPoolID string `json:"agentPoolId,omitempty"`
	//+kubebuilder:validation:Optional
	DeleteProtection bool `json:"deleteProtection,omitempty"`
}

type StorageAccountBackend struct {
	//+kubebuilder:validation:Required
	AccountName string `json:"accountName"`
	//+kubebuilder:validation:Required
	ContainerName string `json:"containerName"`
	//+kubebuilder:validation:Required
	Key string `json:"key"`
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
	// CredentialsSecretRef is the name of the secret containing git credentials
	// +kubebuilder:validation:Optional
	CredentialsSecretRef string `json:"credentialsSecretRef,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Workspace",type=string,JSONPath=".status.workspaceID"
// +kubebuilder:printcolumn:name="WorkspaceReady",type=boolean,JSONPath=".status.workspaceReady"
// +kubebuilder:printcolumn:name="Job",type=string,JSONPath=".status.activeJobName",priority=1
// +kubebuilder:printcolumn:name="LastRun",type=date,JSONPath=".status.lastRunTime"
// +kubebuilder:printcolumn:name="Message",type=string,JSONPath=".status.message",priority=1
//
// TfRun is the Schema for the tfruns API
type TfRun struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TfRunSpec   `json:"spec,omitempty"`
	Status TfRunStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
//
// TfRunList contains a list of TfRun
type TfRunList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TfRun `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TfRun{}, &TfRunList{})
}
