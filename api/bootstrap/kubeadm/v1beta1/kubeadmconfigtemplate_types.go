/*
Copyright 2021 The Kubernetes Authors.

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

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	clusterv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
)

// KubeadmConfigTemplateSpec defines the desired state of KubeadmConfigTemplate.
type KubeadmConfigTemplateSpec struct {
	// template defines the desired state of KubeadmConfigTemplate.
	// +required
	Template KubeadmConfigTemplateResource `json:"template"`
}

// KubeadmConfigTemplateResource defines the Template structure.
type KubeadmConfigTemplateResource struct {
	// metadata is the standard object's metadata.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata
	// +optional
	ObjectMeta clusterv1beta1.ObjectMeta `json:"metadata,omitempty"`

	// spec is the desired state of KubeadmConfig.
	// +optional
	Spec KubeadmConfigSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=kubeadmconfigtemplates,scope=Namespaced,categories=cluster-api
// +kubebuilder:deprecatedversion
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp",description="Time duration since creation of KubeadmConfigTemplate"

// KubeadmConfigTemplate is the Schema for the kubeadmconfigtemplates API.
type KubeadmConfigTemplate struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is the standard object's metadata.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec is the desired state of KubeadmConfigTemplate.
	// +optional
	Spec KubeadmConfigTemplateSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// KubeadmConfigTemplateList contains a list of KubeadmConfigTemplate.
type KubeadmConfigTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is the standard list's metadata.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#lists-and-simple-kinds
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`
	// items is the list of KubeadmConfigTemplates.
	Items []KubeadmConfigTemplate `json:"items"`
}

func init() {
	objectTypes = append(objectTypes, &KubeadmConfigTemplate{}, &KubeadmConfigTemplateList{})
}
