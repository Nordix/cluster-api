/*
Copyright 2019 The Kubernetes Authors.

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

package phases

import (
	"io"
	"strings"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/klog"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha2"
	bmh "github.com/metal3-io/baremetal-operator/pkg/apis/metal3/v1alpha1"

)

type sourceClient interface {
	Delete(string) error
	ForceDeleteSecret(string, string) error
	ForceDeleteCluster(string, string) error
	ForceDeleteMachine(string, string) error
	ForceDeleteMachineDeployment(string, string) error
	ForceDeleteMachineSet(namespace, name string) error
	ForceDeleteUnstructuredObject(*unstructured.Unstructured) error
	GetClusterSecrets(*clusterv1.Cluster) ([]*corev1.Secret, error)
	GetBMCSecrets(*bmh.BareMetalHost) ([]*corev1.Secret, error)
	GetClusters(string) ([]*clusterv1.Cluster, error)
	GetMachineDeployments(string) ([]*clusterv1.MachineDeployment, error)
	GetMachineDeploymentsForCluster(*clusterv1.Cluster) ([]*clusterv1.MachineDeployment, error)
	GetMachines(namespace string) ([]*clusterv1.Machine, error)
	GetBareMetalHosts(namespace string) ([]*bmh.BareMetalHost, error)
	GetMachineSets(namespace string) ([]*clusterv1.MachineSet, error)
	GetMachineSetsForCluster(*clusterv1.Cluster) ([]*clusterv1.MachineSet, error)
	GetMachineSetsForMachineDeployment(*clusterv1.MachineDeployment) ([]*clusterv1.MachineSet, error)
	GetMachinesForCluster(*clusterv1.Cluster) ([]*clusterv1.Machine, error)
	GetMachinesForMachineSet(*clusterv1.MachineSet) ([]*clusterv1.Machine, error)
	GetUnstructuredObject(*unstructured.Unstructured) error
	ScaleDeployment(string, string, int32) error
	WaitForClusterV1alpha2Ready() error
}

type targetClient interface {
	Apply(string) error
	CreateSecret(*corev1.Secret) error
	CreateBareMetalHosts(*bmh.BareMetalHost, string) error
	CreateClusterObject(*clusterv1.Cluster) error
	CreateMachineDeployments([]*clusterv1.MachineDeployment, string) error
	CreateMachines([]*clusterv1.Machine, string) error
	CreateMachineSets([]*clusterv1.MachineSet, string) error
	CreateUnstructuredObject(*unstructured.Unstructured) error
	GetCluster(string, string) (*clusterv1.Cluster, error)
	EnsureNamespace(string) error
	GetMachineDeployment(namespace, name string) (*clusterv1.MachineDeployment, error)
	GetMachineSet(string, string) (*clusterv1.MachineSet, error)
	WaitForClusterV1alpha2Ready() error
	SetClusterOwnerRef(runtime.Object, *clusterv1.Cluster) error
	GetBareMetalHosts(namespace string) ([]*bmh.BareMetalHost, error)
	PatchBareMetalHost(sourceHost *bmh.BareMetalHost, targetHost *bmh.BareMetalHost) error
}

// Pivot deploys the provided provider components to a target cluster and then migrates
// all cluster-api resources from the source cluster to the target cluster
func Pivot(source sourceClient, target targetClient, providerComponents string) error {
	klog.Info("Applying Cluster API Provider Components to Target Cluster")
	if err := target.Apply(providerComponents); err != nil {
		return errors.Wrap(err, "unable to apply cluster api controllers")
	}

	klog.Info("Pivoting Cluster API objects from bootstrap to target cluster.")
	if err := pivot(source, target, providerComponents); err != nil {
		return errors.Wrap(err, "unable to pivot cluster API objects")
	}

	return nil
}

func pivot(from sourceClient, to targetClient, providerComponents string) error {
	// TODO: Attempt to handle rollback in case of pivot failure

	klog.V(4).Info("Ensuring cluster v1alpha2 resources are available on the source cluster")
	if err := from.WaitForClusterV1alpha2Ready(); err != nil {
		return errors.New("cluster v1alpha2 resource not ready on source cluster")
	}

	klog.V(4).Info("Ensuring cluster v1alpha2 resources are available on the target cluster")
	if err := to.WaitForClusterV1alpha2Ready(); err != nil {
		return errors.New("cluster v1alpha2 resource not ready on target cluster")
	}

	klog.V(4).Info("Parsing list of cluster-api controllers from provider components")
	controllers, err := parseControllers(providerComponents)
	if err != nil {
		return errors.Wrap(err, "Failed to extract Cluster API Controllers from the provider components")
	}

	// Scale down the controller managers in the source cluster
	for _, controller := range controllers {
		klog.V(4).Infof("Scaling down controller %s/%s", controller.Namespace, controller.Name)
		if err := from.ScaleDeployment(controller.Namespace, controller.Name, 0); err != nil {
			return errors.Wrapf(err, "Failed to scale down %s/%s", controller.Namespace, controller.Name)
		}
	}

	klog.V(4).Info("Retrieving list of Clusters to move")
	clusters, err := from.GetClusters("")
	if err != nil {
		return err
	}

	if err := moveClusters(from, to, clusters); err != nil {
		return err
	}

	klog.V(4).Info("Retrieving list of MachineDeployments not associated with a Cluster to move")
	machineDeployments, err := from.GetMachineDeployments("")
	if err != nil {
		return err
	}
	if err := moveMachineDeployments(from, to, machineDeployments); err != nil {
		return err
	}

	klog.V(4).Info("Retrieving list of MachineSets not associated with a MachineDeployment or a Cluster to move")
	machineSets, err := from.GetMachineSets("")
	if err != nil {
		return err
	}
	if err := moveMachineSets(from, to, machineSets); err != nil {
		return err
	}

	klog.V(4).Infof("Retrieving list of Machines not associated with a MachineSet or a Cluster to move")
	machines, err := from.GetMachines("")
	if err != nil {
		return err
	}
	if err := moveMachines(from, to, machines); err != nil {
		return err
	}

	klog.V(4).Infof("Retrieving list of BareMetalHosts to move")
	bmHosts, err := from.GetBareMetalHosts("")
	if err != nil {
		return err
	}
	if err := moveBareMetalHosts(from, to, bmHosts); err != nil {
		return err
	}

	klog.V(4).Infof("Patching BareMetalHosts")
	sourceHosts, _ := from.GetBareMetalHosts("")
	targetHosts, _ := to.GetBareMetalHosts("")
	if err := PatchBareMetalHosts(from, to, sourceHosts, targetHosts); err != nil {
		return err
	}

	// klog.V(4).Infof("Deleting provider components from source cluster")
	// if err := from.Delete(providerComponents); err != nil {
	// 	klog.Warningf("Could not delete the provider components from the source cluster: %v", err)
	// }

	return nil
}

func PatchBareMetalHosts(from sourceClient, to targetClient, sourceHosts []*bmh.BareMetalHost, targetHosts []*bmh.BareMetalHost) error {
	for _, sourcehost:= range sourceHosts{
		for _, targethost:= range targetHosts {
			if targethost.Name == sourcehost.Name {
				to.PatchBareMetalHost(sourcehost,targethost)
				break
			}
		}
	}
	return nil
}

func moveClusters(from sourceClient, to targetClient, clusters []*clusterv1.Cluster) error {
	clusterNames := make([]string, 0, len(clusters))
	for _, c := range clusters {
		clusterNames = append(clusterNames, c.Name)
	}
	klog.V(4).Infof("Preparing to move Clusters: %v", clusterNames)

	for _, c := range clusters {
		if err := moveCluster(from, to, c); err != nil {
			return errors.Wrapf(err, "Failed to move cluster: %s/%s", c.Namespace, c.Name)
		}
	}
	return nil
}

func moveCluster(from sourceClient, to targetClient, cluster *clusterv1.Cluster) error {
	klog.V(4).Infof("Moving Cluster %s/%s", cluster.Namespace, cluster.Name)

	klog.V(4).Infof("Ensuring namespace %q exists on target cluster", cluster.Namespace)
	if err := to.EnsureNamespace(cluster.Namespace); err != nil {
		return errors.Wrapf(err, "unable to ensure namespace %q in target cluster", cluster.Namespace)
	}

	// New objects cannot have a specified resource version. Clear it out.
	cluster.SetResourceVersion("")
	if err := to.CreateClusterObject(cluster); err != nil {
		return errors.Wrapf(err, "error copying Cluster %s/%s to target cluster", cluster.Namespace, cluster.Name)
	}

	// Move infrastructure reference, if any.
	if cluster.Spec.InfrastructureRef != nil {
		if err := moveReference(from, to, cluster.Spec.InfrastructureRef, cluster.Namespace); err != nil {
			return errors.Wrapf(err, "error copying Cluster %s/%s infrastructure reference to target cluster",
				cluster.Namespace, cluster.Name)
		}
	}

	// Move the cluster's secrets only after the target cluster resource is created
	// since we have to update the Secret's OwnerRef
	if err := moveClusterSecrets(from, to, cluster); err != nil {
		return errors.Wrapf(err, "failed to move Secrets for Cluster %s/%s to target cluster", cluster.Namespace, cluster.Name)
	}

	klog.V(4).Infof("Retrieving list of MachineDeployments to move for Cluster %s/%s", cluster.Namespace, cluster.Name)
	machineDeployments, err := from.GetMachineDeploymentsForCluster(cluster)
	if err != nil {
		return err
	}
	if err := moveMachineDeployments(from, to, machineDeployments); err != nil {
		return err
	}

	klog.V(4).Infof("Retrieving list of MachineSets not associated with a MachineDeployment to move for Cluster %s/%s", cluster.Namespace, cluster.Name)
	machineSets, err := from.GetMachineSetsForCluster(cluster)
	if err != nil {
		return err
	}
	if err := moveMachineSets(from, to, machineSets); err != nil {
		return err
	}

	klog.V(4).Infof("Retrieving list of Machines not associated with a MachineSet to move for Cluster %s/%s", cluster.Namespace, cluster.Name)
	machines, err := from.GetMachinesForCluster(cluster)
	if err != nil {
		return err
	}
	if err := moveMachines(from, to, machines); err != nil {
		return err
	}

	if err := from.ForceDeleteCluster(cluster.Namespace, cluster.Name); err != nil {
		return errors.Wrapf(err, "error force deleting cluster %s/%s", cluster.Namespace, cluster.Name)
	}

	klog.V(4).Infof("Successfully moved Cluster %s/%s", cluster.Namespace, cluster.Name)
	return nil
}

func moveClusterSecrets(from sourceClient, to targetClient, cluster *clusterv1.Cluster) error {
	klog.V(4).Infof("Moving Secrets for Cluster %s/%s", cluster.Namespace, cluster.Name)
	secrets, err := from.GetClusterSecrets(cluster)
	if err != nil {
		return err
	}

	toCluster, err := to.GetCluster(cluster.Name, cluster.Namespace)
	if err != nil {
		return err
	}

	for _, secret := range secrets {
		if err := moveSecret(from, to, secret, toCluster); err != nil {
			return errors.Wrapf(err, "failed to move Secret %s/%s", secret.Namespace, secret.Name)
		}
	}
	return nil
}

func moveSecret(from sourceClient, to targetClient, secret *corev1.Secret, toCluster *clusterv1.Cluster) error {
	klog.V(4).Infof("Moving secret %s/%s", secret.Namespace, secret.Name)

	// New objects cannot have a specified resource version. Clear it out.
	secret.SetResourceVersion("")
	// Set the cluster owner ref based on target cluster's Cluster resource
	to.SetClusterOwnerRef(secret, toCluster)

	if err := to.CreateSecret(secret); err != nil {
		return errors.Wrapf(err, "error copying Secret %s/%s to target cluster", secret.Namespace, secret.Name)
	}

	if err := from.ForceDeleteSecret(secret.Namespace, secret.Name); err != nil {
		return errors.Wrapf(err, "error force deleting Secret %s/%s from source cluster", secret.Namespace, secret.Name)
	}
	klog.V(4).Infof("Successfully moved Secret %s/%s", secret.Namespace, secret.Name)
	return nil
}

func moveMachineDeployments(from sourceClient, to targetClient, machineDeployments []*clusterv1.MachineDeployment) error {
	machineDeploymentNames := make([]string, 0, len(machineDeployments))
	for _, md := range machineDeployments {
		machineDeploymentNames = append(machineDeploymentNames, md.Name)
	}
	klog.V(4).Infof("Preparing to move MachineDeployments: %v", machineDeploymentNames)

	for _, md := range machineDeployments {
		if err := moveMachineDeployment(from, to, md); err != nil {
			return errors.Wrapf(err, "failed to move MachineDeployment %s/%s", md.Namespace, md.Name)
		}
	}
	return nil
}

func moveMachineDeployment(from sourceClient, to targetClient, md *clusterv1.MachineDeployment) error {
	klog.V(4).Infof("Moving MachineDeployment %s/%s", md.Namespace, md.Name)

	klog.V(4).Infof("Retrieving list of MachineSets for MachineDeployment %s/%s", md.Namespace, md.Name)
	machineSets, err := from.GetMachineSetsForMachineDeployment(md)
	if err != nil {
		return err
	}
	if err := moveMachineSets(from, to, machineSets); err != nil {
		return err
	}

	// Move infrastructure reference.
	if err := moveReference(from, to, &md.Spec.Template.Spec.InfrastructureRef, md.Namespace); err != nil {
		return errors.Wrapf(err, "error copying MachineSet %s/%s infrastructure reference to target cluster",
			md.Namespace, md.Name)
	}

	// New objects cannot have a specified resource version. Clear it out.
	md.SetResourceVersion("")

	// Remove owner reference. This currently assumes that the only owner reference would be a Cluster.
	md.SetOwnerReferences(nil)

	if err := to.CreateMachineDeployments([]*clusterv1.MachineDeployment{md}, md.Namespace); err != nil {
		return errors.Wrapf(err, "error copying MachineDeployment %s/%s to target cluster", md.Namespace, md.Name)
	}

	if err := from.ForceDeleteMachineDeployment(md.Namespace, md.Name); err != nil {
		return errors.Wrapf(err, "error force deleting MachineDeployment %s/%s from source cluster", md.Namespace, md.Name)
	}
	klog.V(4).Infof("Successfully moved MachineDeployment %s/%s", md.Namespace, md.Name)
	return nil
}

func moveMachineSets(from sourceClient, to targetClient, machineSets []*clusterv1.MachineSet) error {
	machineSetNames := make([]string, 0, len(machineSets))
	for _, ms := range machineSets {
		machineSetNames = append(machineSetNames, ms.Name)
	}
	klog.V(4).Infof("Preparing to move MachineSets: %v", machineSetNames)

	for _, ms := range machineSets {
		if err := moveMachineSet(from, to, ms); err != nil {
			return errors.Wrapf(err, "failed to move MachineSet %s:%s", ms.Namespace, ms.Name)
		}
	}
	return nil
}

func moveMachineSet(from sourceClient, to targetClient, ms *clusterv1.MachineSet) error {
	klog.V(4).Infof("Moving MachineSet %s/%s", ms.Namespace, ms.Name)

	klog.V(4).Infof("Retrieving list of Machines for MachineSet %s/%s", ms.Namespace, ms.Name)
	machines, err := from.GetMachinesForMachineSet(ms)
	if err != nil {
		return err
	}
	if err := moveMachines(from, to, machines); err != nil {
		return err
	}

	// Move infrastructure reference if the MachineSet isn't linked to a MachineDeployment.
	// When a MachineSet is owned by a MachineDeployment, the referenced template has already been moved
	// by the time this function is called.
	if metav1.GetControllerOf(ms) == nil {
		if err := moveReference(from, to, &ms.Spec.Template.Spec.InfrastructureRef, ms.Namespace); err != nil {
			return errors.Wrapf(err, "error copying MachineSet %s/%s infrastructure reference to target cluster",
				ms.Namespace, ms.Name)
		}
	}

	// New objects cannot have a specified resource version. Clear it out.
	ms.SetResourceVersion("")

	// Remove owner reference. This currently assumes that the only owner references would be a MachineDeployment and/or a Cluster.
	ms.SetOwnerReferences(nil)

	if err := to.CreateMachineSets([]*clusterv1.MachineSet{ms}, ms.Namespace); err != nil {
		return errors.Wrapf(err, "error copying MachineSet %s/%s to target cluster", ms.Namespace, ms.Name)
	}
	if err := from.ForceDeleteMachineSet(ms.Namespace, ms.Name); err != nil {
		return errors.Wrapf(err, "error force deleting MachineSet %s/%s from source cluster", ms.Namespace, ms.Name)
	}
	klog.V(4).Infof("Successfully moved MachineSet %s/%s", ms.Namespace, ms.Name)
	return nil
}

func moveMachines(from sourceClient, to targetClient, machines []*clusterv1.Machine) error {
	machineNames := make([]string, 0, len(machines))
	for _, m := range machines {
		if m.DeletionTimestamp != nil {
			klog.V(4).Infof("Skipping to move deleted machine: %q", m.Name)
			continue
		}
		machineNames = append(machineNames, m.Name)
	}
	klog.V(4).Infof("Preparing to move Machines: %v", machineNames)

	for _, m := range machines {
		if !m.DeletionTimestamp.IsZero() {
			continue
		}
		if err := moveMachine(from, to, m); err != nil {
			return errors.Wrapf(err, "failed to move Machine %s:%s", m.Namespace, m.Name)
		}
	}
	return nil
}

func moveBareMetalHosts(from sourceClient, to targetClient, bmhosts []*bmh.BareMetalHost) error {
	bmhostNames := make([]string, 0, len(bmhosts))
	for _, bmhost := range bmhosts {
		if bmhost.DeletionTimestamp != nil {
			klog.V(4).Infof("Skipping to move deleted baremetalHost: %q", bmhost.Name)
			continue
		}
		bmhostNames = append(bmhostNames, bmhost.Name)
	}
	klog.V(4).Infof("Preparing to move BareMetalHosts: %v", bmhostNames)

	for _, bmhost := range bmhosts {
		if !bmhost.DeletionTimestamp.IsZero() {
			continue
		}
		if err := moveBareMetalHost(from, to, bmhost); err != nil {
			return errors.Wrapf(err, "failed to move BareMetalHost %s:%s", bmhost.Namespace, bmhost.Name)
		}
	}
	return nil
}

func moveBMCSecrets(from sourceClient, to targetClient, bmhost *bmh.BareMetalHost) error {
	klog.V(4).Infof("Moving Secrets for BareMetalHost %s/%s", bmhost.Namespace, bmhost.Name)
	secrets, err := from.GetBMCSecrets(bmhost)
	if err != nil {
		return err
	}

	for _, secret := range secrets {
		klog.V(4).Infof("BMC Secret %s/%s being moved", secret.Namespace, secret.Name)
		if err := moveBMCSecret(from, to, secret, bmhost); err != nil {
			return errors.Wrapf(err, "failed to move Secret %s/%s", secret.Namespace, secret.Name)
		}
	}
	return nil
}

func moveBMCSecret(from sourceClient, to targetClient, secret *corev1.Secret, bmhost *bmh.BareMetalHost) error {
	klog.V(4).Infof("Moving secret %s/%s", secret.Namespace, secret.Name)

	// New objects cannot have a specified resource version. Clear it out.
	secret.SetResourceVersion("")
	// // Set the cluster owner ref based on target cluster's Cluster resource
	secret.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion: "metal3.io/v1alpha1",
			Kind:       "BareMetalHost",
			Name:       bmhost.Name,
			UID:        bmhost.UID,
		},
	})

	if err := to.CreateSecret(secret); err != nil {
		return errors.Wrapf(err, "error copying Secret %s/%s to target cluster", secret.Namespace, secret.Name)
	}
	
	klog.V(4).Infof("Successfully moved Secret %s/%s", secret.Namespace, secret.Name)
	return nil
}

func moveBareMetalHost(from sourceClient, to targetClient, bmhost *bmh.BareMetalHost) error {
	klog.V(4).Infof("Moving BareMetalHost %s/%s", bmhost.Namespace, bmhost.Name)
  
	targetObject := bmhost.DeepCopy()
	klog.V(4).Infof("DeepCopy Done ")

	targetObject.SetResourceVersion("")

	if err := to.CreateBareMetalHosts(targetObject, targetObject.Namespace); err != nil {
		return errors.Wrapf(err, "error copying BareMetalHost %s/%s to target cluster", targetObject.Namespace, targetObject.Name)
	}

	if targetObject.Spec.BMC.CredentialsName != "" {
		klog.V(4).Infof("Found Secret %s", targetObject.Spec.BMC.CredentialsName)
		if err := moveBMCSecrets(from, to, targetObject); err != nil {
			return errors.Wrapf(err, "failed to move Secrets for BareMetalHost %s/%s to target cluster", targetObject.Namespace, targetObject.Name)
		}
	}

	klog.V(4).Infof("Successfully moved BareMetalHost %s/%s", targetObject.Namespace, targetObject.Name)
	return nil
}

func moveMachine(from sourceClient, to targetClient, m *clusterv1.Machine) error {
	klog.V(4).Infof("Moving Machine %s/%s", m.Namespace, m.Name)

	// Move bootstrap reference, if any.
	if m.Spec.Bootstrap.ConfigRef != nil {
		if err := moveReference(from, to, m.Spec.Bootstrap.ConfigRef, m.Namespace); err != nil {
			return errors.Wrapf(err, "error copying Machine %s/%s bootstrap reference to target cluster",
				m.Namespace, m.Name)
		}
	}

	// Move infrastructure reference.
	if err := moveReference(from, to, &m.Spec.InfrastructureRef, m.Namespace); err != nil {
		return errors.Wrapf(err, "error copying Machine %s/%s infrastructure reference to target cluster",
			m.Namespace, m.Name)
	}

	// New objects cannot have a specified resource version. Clear it out.
	m.SetResourceVersion("")

	// Remove owner reference. This currently assumes that the only owner references would be a MachineSet and/or a Cluster.
	m.SetOwnerReferences(nil)

	if err := to.CreateMachines([]*clusterv1.Machine{m}, m.Namespace); err != nil {
		return errors.Wrapf(err, "error copying Machine %s/%s to target cluster", m.Namespace, m.Name)
	}
	if err := from.ForceDeleteMachine(m.Namespace, m.Name); err != nil {
		return errors.Wrapf(err, "error force deleting Machine %s/%s from source cluster", m.Namespace, m.Name)
	}

	klog.V(4).Infof("Successfully moved Machine %s/%s", m.Namespace, m.Name)
	return nil
}

func moveReference(from sourceClient, to targetClient, ref *corev1.ObjectReference, namespace string) error {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(ref.APIVersion)
	u.SetKind(ref.Kind)
	u.SetNamespace(namespace)
	u.SetName(ref.Name)
	if err := from.GetUnstructuredObject(u); err != nil {
		return errors.Wrapf(err, "error fetching unstructured object %q %s/%s",
			u.GroupVersionKind(), u.GetNamespace(), u.GetName())
	}
	return moveUnstructured(from, to, u)
}

func moveUnstructured(from sourceClient, to targetClient, u *unstructured.Unstructured) error {
	klog.V(4).Infof("Moving unstructured object %q %s/%s", u.GroupVersionKind(), u.GetNamespace(), u.GetName())

	targetObject := u.DeepCopy()

	// New objects cannot have a specified resource version. Clear it out.
	targetObject.SetResourceVersion("")

	// Remove owner reference.
	targetObject.SetOwnerReferences(nil)

	if err := to.CreateUnstructuredObject(targetObject); err != nil {
		return errors.Wrapf(err, "error copying unstructured object %q %s/%s to target cluster",
			u.GroupVersionKind(), u.GetNamespace(), u.GetName())
	}

	if err := from.ForceDeleteUnstructuredObject(u); err != nil {
		return errors.Wrapf(err, "error force deleting unstructured object %q %s/%s to target cluster",
			u.GroupVersionKind(), u.GetNamespace(), u.GetName())
	}
	klog.V(4).Infof("Successfully moved unstructured object %q %s/%s", u.GroupVersionKind(), u.GetNamespace(), u.GetName())
	return nil
}

func parseControllers(providerComponents string) ([]*appsv1.Deployment, error) {
	decoder := yaml.NewYAMLOrJSONDecoder(strings.NewReader(providerComponents), 32)
	controllers := []*appsv1.Deployment{}
	for {
		var out appsv1.Deployment
		err := decoder.Decode(&out)
		if err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		if out.TypeMeta.Kind == "Deployment" {
			controllers = append(controllers, &out)
		}
	}
	return controllers, nil
}
