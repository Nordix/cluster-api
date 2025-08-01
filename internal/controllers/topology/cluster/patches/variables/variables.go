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

// Package variables calculates variables for patching.
package variables

import (
	"encoding/json"
	"maps"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/ptr"

	clusterv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	runtimehooksv1 "sigs.k8s.io/cluster-api/api/runtime/hooks/v1alpha1"
	"sigs.k8s.io/cluster-api/internal/contract"
	"sigs.k8s.io/cluster-api/util/conversion"
)

// Global returns variables that apply to all the templates, including user provided variables
// and builtin variables for the Cluster object.
func Global(clusterTopology clusterv1.Topology, cluster *clusterv1.Cluster, patchVariableDefinitions map[string]bool) ([]runtimehooksv1.Variable, error) {
	variables := []runtimehooksv1.Variable{}

	// Add user defined variables from Cluster.spec.topology.variables.
	for _, variable := range clusterTopology.Variables {
		// Don't add user-defined "builtin" variable.
		if variable.Name == runtimehooksv1.BuiltinsName {
			continue
		}
		// Add the variable if it has a definition from this patch in the ClusterClass.
		if _, ok := patchVariableDefinitions[variable.Name]; ok {
			variables = append(variables, runtimehooksv1.Variable{Name: variable.Name, Value: variable.Value})
		}
	}

	// Construct builtin variable.
	builtin := runtimehooksv1.Builtins{
		Cluster: &runtimehooksv1.ClusterBuiltins{
			Name:      cluster.Name,
			Namespace: cluster.Namespace,
			UID:       cluster.UID,
			Topology: &runtimehooksv1.ClusterTopologyBuiltins{
				Version:        cluster.Spec.Topology.Version,
				Class:          cluster.GetClassKey().Name,
				ClassNamespace: cluster.GetClassKey().Namespace,
				ClassRef: runtimehooksv1.ClusterTopologyClusterClassRefBuiltins{
					Name:      cluster.GetClassKey().Name,
					Namespace: cluster.GetClassKey().Namespace,
				},
			},
		},
	}
	if cluster.Labels != nil || cluster.Annotations != nil {
		builtin.Cluster.Metadata = &clusterv1beta1.ObjectMeta{
			Labels:      cluster.Labels,
			Annotations: cleanupAnnotations(cluster.Annotations),
		}
	}
	if cluster.Spec.ClusterNetwork.ServiceDomain != "" {
		if builtin.Cluster.Network == nil {
			builtin.Cluster.Network = &runtimehooksv1.ClusterNetworkBuiltins{}
		}
		builtin.Cluster.Network.ServiceDomain = &cluster.Spec.ClusterNetwork.ServiceDomain
	}
	if cluster.Spec.ClusterNetwork.Services.CIDRBlocks != nil {
		if builtin.Cluster.Network == nil {
			builtin.Cluster.Network = &runtimehooksv1.ClusterNetworkBuiltins{}
		}
		builtin.Cluster.Network.Services = cluster.Spec.ClusterNetwork.Services.CIDRBlocks
	}
	if cluster.Spec.ClusterNetwork.Pods.CIDRBlocks != nil {
		if builtin.Cluster.Network == nil {
			builtin.Cluster.Network = &runtimehooksv1.ClusterNetworkBuiltins{}
		}
		builtin.Cluster.Network.Pods = cluster.Spec.ClusterNetwork.Pods.CIDRBlocks
	}

	// Add builtin variables derived from the cluster object.
	variable, err := toVariable(runtimehooksv1.BuiltinsName, builtin)
	if err != nil {
		return nil, err
	}
	variables = append(variables, *variable)

	return variables, nil
}

// ControlPlane returns variables that apply to templates belonging to the ControlPlane.
func ControlPlane(cpTopology *clusterv1.ControlPlaneTopology, cp, cpInfrastructureMachineTemplate *unstructured.Unstructured, patchVariableDefinitions map[string]bool) ([]runtimehooksv1.Variable, error) {
	variables := []runtimehooksv1.Variable{}

	// Add variables overrides for the ControlPlane.
	for _, variable := range cpTopology.Variables.Overrides {
		// Add the variable if it has a definition from this patch in the ClusterClass.
		if _, ok := patchVariableDefinitions[variable.Name]; ok {
			variables = append(variables, runtimehooksv1.Variable{Name: variable.Name, Value: variable.Value})
		}
	}

	// Construct builtin variable.
	builtin := runtimehooksv1.Builtins{
		ControlPlane: &runtimehooksv1.ControlPlaneBuiltins{
			Name: cp.GetName(),
		},
	}

	// If it is required to manage the number of replicas for the ControlPlane, set the corresponding variable.
	// NOTE: If the Cluster.spec.topology.controlPlane.replicas field is nil, the topology reconciler won't set
	// the replicas field on the ControlPlane. This happens either when the ControlPlane provider does
	// not implement support for this field or the default value of the ControlPlane is used.
	if cpTopology.Replicas != nil {
		replicas, err := contract.ControlPlane().Replicas().Get(cp)
		if err != nil {
			return nil, errors.Wrap(err, "failed to get spec.replicas from the ControlPlane")
		}
		builtin.ControlPlane.Replicas = replicas
	}
	if cp.GetLabels() != nil || cp.GetAnnotations() != nil {
		builtin.ControlPlane.Metadata = &clusterv1beta1.ObjectMeta{
			Annotations: cleanupAnnotations(cp.GetAnnotations()),
			Labels:      cp.GetLabels(),
		}
	}

	version, err := contract.ControlPlane().Version().Get(cp)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get spec.version from the ControlPlane")
	}
	builtin.ControlPlane.Version = *version

	if cpInfrastructureMachineTemplate != nil {
		builtin.ControlPlane.MachineTemplate = &runtimehooksv1.ControlPlaneMachineTemplateBuiltins{
			InfrastructureRef: runtimehooksv1.ControlPlaneMachineTemplateInfrastructureRefBuiltins{
				Name: cpInfrastructureMachineTemplate.GetName(),
			},
		}
	}

	variable, err := toVariable(runtimehooksv1.BuiltinsName, builtin)
	if err != nil {
		return nil, err
	}
	variables = append(variables, *variable)

	return variables, nil
}

// MachineDeployment returns variables that apply to templates belonging to a MachineDeployment.
func MachineDeployment(mdTopology *clusterv1.MachineDeploymentTopology, md *clusterv1.MachineDeployment, mdBootstrapTemplate, mdInfrastructureMachineTemplate *unstructured.Unstructured, patchVariableDefinitions map[string]bool) ([]runtimehooksv1.Variable, error) {
	variables := []runtimehooksv1.Variable{}

	// Add variables overrides for the MachineDeployment.
	for _, variable := range mdTopology.Variables.Overrides {
		// Add the variable if it has a definition from this patch in the ClusterClass.
		if _, ok := patchVariableDefinitions[variable.Name]; ok {
			variables = append(variables, runtimehooksv1.Variable{Name: variable.Name, Value: variable.Value})
		}
	}

	// Construct builtin variable.
	builtin := runtimehooksv1.Builtins{
		MachineDeployment: &runtimehooksv1.MachineDeploymentBuiltins{
			Version:      md.Spec.Template.Spec.Version,
			Class:        mdTopology.Class,
			Name:         md.Name,
			TopologyName: mdTopology.Name,
		},
	}
	if md.Spec.Replicas != nil {
		builtin.MachineDeployment.Replicas = ptr.To[int64](int64(*md.Spec.Replicas))
	}
	if md.Labels != nil || md.Annotations != nil {
		builtin.MachineDeployment.Metadata = &clusterv1beta1.ObjectMeta{
			Annotations: cleanupAnnotations(md.Annotations),
			Labels:      md.Labels,
		}
	}

	if mdBootstrapTemplate != nil {
		builtin.MachineDeployment.Bootstrap = &runtimehooksv1.MachineBootstrapBuiltins{
			ConfigRef: &runtimehooksv1.MachineBootstrapConfigRefBuiltins{
				Name: mdBootstrapTemplate.GetName(),
			},
		}
	}

	if mdInfrastructureMachineTemplate != nil {
		builtin.MachineDeployment.InfrastructureRef = &runtimehooksv1.MachineInfrastructureRefBuiltins{
			Name: mdInfrastructureMachineTemplate.GetName(),
		}
	}

	variable, err := toVariable(runtimehooksv1.BuiltinsName, builtin)
	if err != nil {
		return nil, err
	}
	variables = append(variables, *variable)

	return variables, nil
}

// MachinePool returns variables that apply to templates belonging to a MachinePool.
func MachinePool(mpTopology *clusterv1.MachinePoolTopology, mp *clusterv1.MachinePool, mpBootstrapObject, mpInfrastructureMachinePool *unstructured.Unstructured, patchVariableDefinitions map[string]bool) ([]runtimehooksv1.Variable, error) {
	variables := []runtimehooksv1.Variable{}

	// Add variables overrides for the MachinePool.
	for _, variable := range mpTopology.Variables.Overrides {
		// Add the variable if it has a definition from this patch in the ClusterClass.
		if _, ok := patchVariableDefinitions[variable.Name]; ok {
			variables = append(variables, runtimehooksv1.Variable{Name: variable.Name, Value: variable.Value})
		}
	}

	// Construct builtin variable.
	builtin := runtimehooksv1.Builtins{
		MachinePool: &runtimehooksv1.MachinePoolBuiltins{
			Version:      mp.Spec.Template.Spec.Version,
			Class:        mpTopology.Class,
			Name:         mp.Name,
			TopologyName: mpTopology.Name,
		},
	}
	if mp.Spec.Replicas != nil {
		builtin.MachinePool.Replicas = ptr.To[int64](int64(*mp.Spec.Replicas))
	}
	if mp.Labels != nil || mp.Annotations != nil {
		builtin.MachinePool.Metadata = &clusterv1beta1.ObjectMeta{
			Annotations: cleanupAnnotations(mp.Annotations),
			Labels:      mp.Labels,
		}
	}

	if mpBootstrapObject != nil {
		builtin.MachinePool.Bootstrap = &runtimehooksv1.MachineBootstrapBuiltins{
			ConfigRef: &runtimehooksv1.MachineBootstrapConfigRefBuiltins{
				Name: mpBootstrapObject.GetName(),
			},
		}
	}

	if mpInfrastructureMachinePool != nil {
		builtin.MachinePool.InfrastructureRef = &runtimehooksv1.MachineInfrastructureRefBuiltins{
			Name: mpInfrastructureMachinePool.GetName(),
		}
	}

	variable, err := toVariable(runtimehooksv1.BuiltinsName, builtin)
	if err != nil {
		return nil, err
	}
	variables = append(variables, *variable)

	return variables, nil
}

// toVariable converts name and value to a variable.
func toVariable(name string, value interface{}) (*runtimehooksv1.Variable, error) {
	marshalledValue, err := json.Marshal(value)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to set variable %q: error marshalling", name)
	}

	return &runtimehooksv1.Variable{
		Name:  name,
		Value: apiextensionsv1.JSON{Raw: marshalledValue},
	}, nil
}

func cleanupAnnotations(annotations map[string]string) map[string]string {
	if annotations == nil {
		return nil
	}

	// Optimize size of GeneratePatchesRequest and ValidateTopologyRequest by not sending the last-applied annotation.
	annotations = maps.Clone(annotations)
	delete(annotations, corev1.LastAppliedConfigAnnotation)
	delete(annotations, conversion.DataAnnotation)
	return annotations
}
