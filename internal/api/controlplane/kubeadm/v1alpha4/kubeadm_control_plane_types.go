/*
Copyright 2020 The Kubernetes Authors.

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

package v1alpha4

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"sigs.k8s.io/cluster-api/errors"
	bootstrapv1alpha4 "sigs.k8s.io/cluster-api/internal/api/bootstrap/kubeadm/v1alpha4"
	clusterv1alpha4 "sigs.k8s.io/cluster-api/internal/api/core/v1alpha4"
)

// RolloutStrategyType defines the rollout strategies for a KubeadmControlPlane.
type RolloutStrategyType string

const (
	// RollingUpdateStrategyType replaces the old control planes by new one using rolling update
	// i.e. gradually scale up or down the old control planes and scale up or down the new one.
	RollingUpdateStrategyType RolloutStrategyType = "RollingUpdate"
)

const (
	// KubeadmControlPlaneFinalizer is the finalizer applied to KubeadmControlPlane resources
	// by its managing controller.
	KubeadmControlPlaneFinalizer = "kubeadm.controlplane.cluster.x-k8s.io"

	// SkipCoreDNSAnnotation annotation explicitly skips reconciling CoreDNS if set.
	SkipCoreDNSAnnotation = "controlplane.cluster.x-k8s.io/skip-coredns"

	// SkipKubeProxyAnnotation annotation explicitly skips reconciling kube-proxy if set.
	SkipKubeProxyAnnotation = "controlplane.cluster.x-k8s.io/skip-kube-proxy"

	// KubeadmClusterConfigurationAnnotation is a machine annotation that stores the json-marshalled string of KCP ClusterConfiguration.
	// This annotation is used to detect any changes in ClusterConfiguration and trigger machine rollout in KCP.
	KubeadmClusterConfigurationAnnotation = "controlplane.cluster.x-k8s.io/kubeadm-cluster-configuration"
)

// KubeadmControlPlaneSpec defines the desired state of KubeadmControlPlane.
type KubeadmControlPlaneSpec struct {
	// replicas is the number of desired machines. Defaults to 1. When stacked etcd is used only
	// odd numbers are permitted, as per [etcd best practice](https://etcd.io/docs/v3.3.12/faq/#why-an-odd-number-of-cluster-members).
	// This is a pointer to distinguish between explicit zero and not specified.
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// version defines the desired Kubernetes version.
	Version string `json:"version"`

	// machineTemplate contains information about how machines
	// should be shaped when creating or updating a control plane.
	MachineTemplate KubeadmControlPlaneMachineTemplate `json:"machineTemplate"`

	// kubeadmConfigSpec is a KubeadmConfigSpec
	// to use for initializing and joining machines to the control plane.
	KubeadmConfigSpec bootstrapv1alpha4.KubeadmConfigSpec `json:"kubeadmConfigSpec"`

	// rolloutAfter is a field to indicate a rollout should be performed
	// after the specified time even if no changes have been made to the
	// KubeadmControlPlane.
	//
	// +optional
	RolloutAfter *metav1.Time `json:"rolloutAfter,omitempty"`

	// rolloutStrategy is the RolloutStrategy to use to replace control plane machines with
	// new ones.
	// +optional
	// +kubebuilder:default={type: "RollingUpdate", rollingUpdate: {maxSurge: 1}}
	RolloutStrategy *RolloutStrategy `json:"rolloutStrategy,omitempty"`
}

// KubeadmControlPlaneMachineTemplate defines the template for Machines
// in a KubeadmControlPlane object.
type KubeadmControlPlaneMachineTemplate struct {
	// metadata is the standard object's metadata.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata
	// +optional
	ObjectMeta clusterv1alpha4.ObjectMeta `json:"metadata,omitempty"`

	// infrastructureRef is a required reference to a custom resource
	// offered by an infrastructure provider.
	InfrastructureRef corev1.ObjectReference `json:"infrastructureRef"`

	// nodeDrainTimeout is the total amount of time that the controller will spend on draining a controlplane node
	// The default value is 0, meaning that the node can be drained without any time limitations.
	// NOTE: NodeDrainTimeout is different from `kubectl drain --timeout`
	// +optional
	NodeDrainTimeout *metav1.Duration `json:"nodeDrainTimeout,omitempty"`
}

// RolloutStrategy describes how to replace existing machines
// with new ones.
type RolloutStrategy struct {
	// type of rollout. Currently the only supported strategy is
	// "RollingUpdate".
	// Default is RollingUpdate.
	// +optional
	Type RolloutStrategyType `json:"type,omitempty"`

	// rollingUpdate is the rolling update config params. Present only if
	// RolloutStrategyType = RollingUpdate.
	// +optional
	RollingUpdate *RollingUpdate `json:"rollingUpdate,omitempty"`
}

// RollingUpdate is used to control the desired behavior of rolling update.
type RollingUpdate struct {
	// maxSurge is the maximum number of control planes that can be scheduled above or under the
	// desired number of control planes.
	// Value can be an absolute number 1 or 0.
	// Defaults to 1.
	// Example: when this is set to 1, the control plane can be scaled
	// up immediately when the rolling update starts.
	// +optional
	MaxSurge *intstr.IntOrString `json:"maxSurge,omitempty"`
}

// KubeadmControlPlaneStatus defines the observed state of KubeadmControlPlane.
type KubeadmControlPlaneStatus struct {
	// selector is the label selector in string format to avoid introspection
	// by clients, and is used to provide the CRD-based integration for the
	// scale subresource and additional integrations for things like kubectl
	// describe.. The string will be in the same format as the query-param syntax.
	// More info about label selectors: http://kubernetes.io/docs/user-guide/labels#label-selectors
	// +optional
	Selector string `json:"selector,omitempty"`

	// replicas is the total number of non-terminated machines targeted by this control plane
	// (their labels match the selector).
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// version represents the minimum Kubernetes version for the control plane machines
	// in the cluster.
	// +optional
	Version *string `json:"version,omitempty"`

	// updatedReplicas is the total number of non-terminated machines targeted by this control plane
	// that have the desired template spec.
	// +optional
	UpdatedReplicas int32 `json:"updatedReplicas,omitempty"`

	// readyReplicas is the total number of fully running and ready control plane machines.
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// unavailableReplicas is the total number of unavailable machines targeted by this control plane.
	// This is the total number of machines that are still required for
	// the deployment to have 100% available capacity. They may either
	// be machines that are running but not yet ready or machines
	// that still have not been created.
	// +optional
	UnavailableReplicas int32 `json:"unavailableReplicas,omitempty"`

	// initialized denotes whether or not the control plane has the
	// uploaded kubeadm-config configmap.
	// +optional
	Initialized bool `json:"initialized"`

	// ready denotes that the KubeadmControlPlane API Server is ready to
	// receive requests.
	// +optional
	Ready bool `json:"ready"`

	// failureReason indicates that there is a terminal problem reconciling the
	// state, and will be set to a token value suitable for
	// programmatic interpretation.
	// +optional
	FailureReason errors.KubeadmControlPlaneStatusError `json:"failureReason,omitempty"`

	// failureMessage indicates that there is a terminal problem reconciling the
	// state, and will be set to a descriptive error message.
	// +optional
	FailureMessage *string `json:"failureMessage,omitempty"`

	// observedGeneration is the latest generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions defines current service state of the KubeadmControlPlane.
	// +optional
	Conditions clusterv1alpha4.Conditions `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:unservedversion
// +kubebuilder:deprecatedversion
// +kubebuilder:resource:path=kubeadmcontrolplanes,shortName=kcp,scope=Namespaced,categories=cluster-api
// +kubebuilder:subresource:status
// +kubebuilder:subresource:scale:specpath=.spec.replicas,statuspath=.status.replicas,selectorpath=.status.selector
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp",description="Time duration since creation of KubeadmControlPlane"
// +kubebuilder:printcolumn:name="Initialized",type=boolean,JSONPath=".status.initialized",description="This denotes whether or not the control plane has the uploaded kubeadm-config configmap"
// +kubebuilder:printcolumn:name="API Server Available",type=boolean,JSONPath=".status.ready",description="KubeadmControlPlane API Server is ready to receive requests"
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=".spec.version",description="Kubernetes version associated with this control plane"
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=".status.replicas",description="Total number of non-terminated machines targeted by this control plane"
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=".status.readyReplicas",description="Total number of fully running and ready control plane machines"
// +kubebuilder:printcolumn:name="Updated",type=integer,JSONPath=".status.updatedReplicas",description="Total number of non-terminated machines targeted by this control plane that have the desired template spec"
// +kubebuilder:printcolumn:name="Unavailable",type=integer,JSONPath=".status.unavailableReplicas",description="Total number of unavailable machines targeted by this control plane"

// KubeadmControlPlane is the Schema for the KubeadmControlPlane API.
//
// Deprecated: This type will be removed in one of the next releases.
type KubeadmControlPlane struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is the standard object's metadata.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec is the desired state of KubeadmControlPlane.
	Spec KubeadmControlPlaneSpec `json:"spec,omitempty"`
	// status is the observed state of KubeadmControlPlane.
	Status KubeadmControlPlaneStatus `json:"status,omitempty"`
}

// GetConditions returns the set of conditions for this object.
func (in *KubeadmControlPlane) GetConditions() clusterv1alpha4.Conditions {
	return in.Status.Conditions
}

// SetConditions sets the conditions on this object.
func (in *KubeadmControlPlane) SetConditions(conditions clusterv1alpha4.Conditions) {
	in.Status.Conditions = conditions
}

// +kubebuilder:object:root=true

// KubeadmControlPlaneList contains a list of KubeadmControlPlane.
//
// Deprecated: This type will be removed in one of the next releases.
type KubeadmControlPlaneList struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is the standard list's metadata.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#lists-and-simple-kinds
	metav1.ListMeta `json:"metadata,omitempty"`
	// items is the list of KubeadmControlPlanes.
	Items []KubeadmControlPlane `json:"items"`
}

func init() {
	objectTypes = append(objectTypes, &KubeadmControlPlane{}, &KubeadmControlPlaneList{})
}
