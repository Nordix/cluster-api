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

// Package annotations implements annotation helper functions.
package annotations

import (
	"regexp"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

// IsPaused returns true if the Cluster is paused or the object has the `paused` annotation.
func IsPaused(cluster *clusterv1.Cluster, o metav1.Object) bool {
	if ptr.Deref(cluster.Spec.Paused, false) {
		return true
	}
	return HasPaused(o)
}

// IsExternallyManaged returns true if the object has the `managed-by` annotation.
func IsExternallyManaged(o metav1.Object) bool {
	return hasAnnotation(o, clusterv1.ManagedByAnnotation)
}

// HasPaused returns true if the object has the `paused` annotation.
func HasPaused(o metav1.Object) bool {
	return hasAnnotation(o, clusterv1.PausedAnnotation)
}

// HasSkipRemediation returns true if the object has the `skip-remediation` annotation.
func HasSkipRemediation(o metav1.Object) bool {
	return hasAnnotation(o, clusterv1.MachineSkipRemediationAnnotation)
}

// HasRemediateMachine returns true if the object has the `remediate-machine` annotation.
func HasRemediateMachine(o metav1.Object) bool {
	return hasAnnotation(o, clusterv1.RemediateMachineAnnotation)
}

// HasWithPrefix returns true if at least one of the annotations has the prefix specified.
func HasWithPrefix(prefix string, annotations map[string]string) bool {
	for key := range annotations {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// ReplicasManagedByExternalAutoscaler returns true if the standard annotation for external autoscaler is present.
func ReplicasManagedByExternalAutoscaler(o metav1.Object) bool {
	return hasTruthyAnnotationValue(o, clusterv1.ReplicasManagedByAnnotation)
}

// AddAnnotations sets the desired annotations on the object and returns true if the annotations have changed.
func AddAnnotations(o metav1.Object, desired map[string]string) bool {
	if len(desired) == 0 {
		return false
	}
	annotations := o.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	hasChanged := false
	for k, v := range desired {
		if cur, ok := annotations[k]; !ok || cur != v {
			annotations[k] = v
			hasChanged = true
		}
	}
	o.SetAnnotations(annotations)
	return hasChanged
}

// GetManagedAnnotations filters out and returns the CAPI-managed annotations for a Machine, with an optional list of regex patterns for user-specified annotations.
func GetManagedAnnotations(m *clusterv1.Machine, additionalSyncMachineAnnotations ...*regexp.Regexp) map[string]string {
	// Always sync CAPI's bookkeeping annotations
	managedAnnotations := map[string]string{
		clusterv1.ClusterNameAnnotation:      m.Spec.ClusterName,
		clusterv1.ClusterNamespaceAnnotation: m.GetNamespace(),
		clusterv1.MachineAnnotation:          m.Name,
	}
	if owner := metav1.GetControllerOfNoCopy(m); owner != nil {
		managedAnnotations[clusterv1.OwnerKindAnnotation] = owner.Kind
		managedAnnotations[clusterv1.OwnerNameAnnotation] = owner.Name
	}
	for key, value := range m.GetAnnotations() {
		// Always sync CAPI's default annotation node domain
		dnsSubdomainOrName := strings.Split(key, "/")[0]
		if dnsSubdomainOrName == clusterv1.ManagedNodeAnnotationDomain || strings.HasSuffix(dnsSubdomainOrName, "."+clusterv1.ManagedNodeAnnotationDomain) {
			managedAnnotations[key] = value
			continue
		}
		// Sync if the annotations matches at least one user provided regex
		for _, regex := range additionalSyncMachineAnnotations {
			if regex.MatchString(key) {
				managedAnnotations[key] = value
				break
			}
		}
	}
	return managedAnnotations
}

// hasAnnotation returns true if the object has the specified annotation.
func hasAnnotation(o metav1.Object, annotation string) bool {
	annotations := o.GetAnnotations()
	if annotations == nil {
		return false
	}
	_, ok := annotations[annotation]
	return ok
}

// hasTruthyAnnotationValue returns true if the object has an annotation with a value that is not "false".
func hasTruthyAnnotationValue(o metav1.Object, annotation string) bool {
	annotations := o.GetAnnotations()
	if annotations == nil {
		return false
	}
	if val, ok := annotations[annotation]; ok {
		return val != "false"
	}
	return false
}
