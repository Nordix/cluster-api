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

package framework

import (
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/pointer"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha3"
	"sigs.k8s.io/cluster-api/bootstrap/kubeadm/types/v1beta1"
	"sigs.k8s.io/cluster-api/controllers/remote"
	controlplanev1 "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1alpha3"
	"sigs.k8s.io/cluster-api/test/framework/internal/log"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CreateKubeadmControlPlaneInput is the input for CreateKubeadmControlPlane.
type CreateKubeadmControlPlaneInput struct {
	Creator         Creator
	ControlPlane    *controlplanev1.KubeadmControlPlane
	MachineTemplate client.Object
}

// CreateKubeadmControlPlane creates the control plane object and necessary dependencies.
func CreateKubeadmControlPlane(ctx context.Context, input CreateKubeadmControlPlaneInput, intervals ...interface{}) {
	By("creating the machine template")
	Expect(input.Creator.Create(ctx, input.MachineTemplate)).To(Succeed())

	By("creating a KubeadmControlPlane")
	Eventually(func() error {
		err := input.Creator.Create(ctx, input.ControlPlane)
		if err != nil {
			log.Logf("Failed to create the KubeadmControlPlane: %+v", err)
		}
		return err
	}, intervals...).Should(Succeed())
}

// GetKubeadmControlPlaneByClusterInput is the input for GetKubeadmControlPlaneByCluster.
type GetKubeadmControlPlaneByClusterInput struct {
	Lister      Lister
	ClusterName string
	Namespace   string
}

// GetKubeadmControlPlaneByCluster returns the KubeadmControlPlane objects for a cluster.
// Important! this method relies on labels that are created by the CAPI controllers during the first reconciliation, so
// it is necessary to ensure this is already happened before calling it.
func GetKubeadmControlPlaneByCluster(ctx context.Context, input GetKubeadmControlPlaneByClusterInput) *controlplanev1.KubeadmControlPlane {
	controlPlaneList := &controlplanev1.KubeadmControlPlaneList{}
	Expect(input.Lister.List(ctx, controlPlaneList, byClusterOptions(input.ClusterName, input.Namespace)...)).To(Succeed(), "Failed to list KubeadmControlPlane object for Cluster %s/%s", input.Namespace, input.ClusterName)
	Expect(len(controlPlaneList.Items)).ToNot(BeNumerically(">", 1), "Cluster %s/%s should not have more than 1 KubeadmControlPlane object", input.Namespace, input.ClusterName)
	if len(controlPlaneList.Items) == 1 {
		return &controlPlaneList.Items[0]
	}
	return nil
}

// WaitForKubeadmControlPlaneMachinesToExistInput is the input for WaitForKubeadmControlPlaneMachinesToExist.
type WaitForKubeadmControlPlaneMachinesToExistInput struct {
	Lister       Lister
	Cluster      *clusterv1.Cluster
	ControlPlane *controlplanev1.KubeadmControlPlane
}

// WaitForKubeadmControlPlaneMachinesToExist will wait until all control plane machines have node refs.
func WaitForKubeadmControlPlaneMachinesToExist(ctx context.Context, input WaitForKubeadmControlPlaneMachinesToExistInput, intervals ...interface{}) {
	By("Waiting for all control plane nodes to exist")
	inClustersNamespaceListOption := client.InNamespace(input.Cluster.Namespace)
	// ControlPlane labels
	matchClusterListOption := client.MatchingLabels{
		clusterv1.MachineControlPlaneLabelName: "",
		clusterv1.ClusterLabelName:             input.Cluster.Name,
	}

	Eventually(func() (int, error) {
		machineList := &clusterv1.MachineList{}
		if err := input.Lister.List(ctx, machineList, inClustersNamespaceListOption, matchClusterListOption); err != nil {
			log.Logf("Failed to list the machines: %+v", err)
			return 0, err
		}
		count := 0
		for _, machine := range machineList.Items {
			if machine.Status.NodeRef != nil {
				count++
			}
		}
		return count, nil
	}, intervals...).Should(Equal(int(*input.ControlPlane.Spec.Replicas)))
}

// WaitForOneKubeadmControlPlaneMachinesToExistInput is the input for WaitForKubeadmControlPlaneMachinesToExist.
type WaitForOneKubeadmControlPlaneMachineToExistInput struct {
	Lister       Lister
	Cluster      *clusterv1.Cluster
	ControlPlane *controlplanev1.KubeadmControlPlane
}

// WaitForOneKubeadmControlPlaneMachineToExist will wait until all control plane machines have node refs.
func WaitForOneKubeadmControlPlaneMachineToExist(ctx context.Context, input WaitForOneKubeadmControlPlaneMachineToExistInput, intervals ...interface{}) {
	Expect(ctx).NotTo(BeNil(), "ctx is required for WaitForOneKubeadmControlPlaneMachineToExist")
	Expect(input.Lister).ToNot(BeNil(), "Invalid argument. input.Getter can't be nil when calling WaitForOneKubeadmControlPlaneMachineToExist")
	Expect(input.ControlPlane).ToNot(BeNil(), "Invalid argument. input.ControlPlane can't be nil when calling WaitForOneKubeadmControlPlaneMachineToExist")

	By("Waiting for one control plane node to exist")
	inClustersNamespaceListOption := client.InNamespace(input.Cluster.Namespace)
	// ControlPlane labels
	matchClusterListOption := client.MatchingLabels{
		clusterv1.MachineControlPlaneLabelName: "",
		clusterv1.ClusterLabelName:             input.Cluster.Name,
	}

	Eventually(func() (bool, error) {
		machineList := &clusterv1.MachineList{}
		if err := input.Lister.List(ctx, machineList, inClustersNamespaceListOption, matchClusterListOption); err != nil {
			log.Logf("Failed to list the machines: %+v", err)
			return false, err
		}
		count := 0
		for _, machine := range machineList.Items {
			if machine.Status.NodeRef != nil {
				count++
			}
		}
		return count > 0, nil
	}, intervals...).Should(BeTrue())
}

// WaitForControlPlaneToBeReadyInput is the input for WaitForControlPlaneToBeReady.
type WaitForControlPlaneToBeReadyInput struct {
	Getter       Getter
	ControlPlane *controlplanev1.KubeadmControlPlane
}

// WaitForControlPlaneToBeReady will wait for a control plane to be ready.
func WaitForControlPlaneToBeReady(ctx context.Context, input WaitForControlPlaneToBeReadyInput, intervals ...interface{}) {
	By("Waiting for the control plane to be ready")
	Eventually(func() (bool, error) {
		controlplane := &controlplanev1.KubeadmControlPlane{}
		key := client.ObjectKey{
			Namespace: input.ControlPlane.GetNamespace(),
			Name:      input.ControlPlane.GetName(),
		}
		if err := input.Getter.Get(ctx, key, controlplane); err != nil {
			return false, err
		}
		return controlplane.Status.Ready, nil
	}, intervals...).Should(BeTrue())
}

// AssertControlPlaneFailureDomainsInput is the input for AssertControlPlaneFailureDomains.
type AssertControlPlaneFailureDomainsInput struct {
	GetLister  GetLister
	ClusterKey client.ObjectKey
	// ExpectedFailureDomains is required because this function cannot (easily) infer what success looks like.
	// In theory this field is not strictly necessary and could be replaced with enough clever logic/math.
	ExpectedFailureDomains map[string]int
}

// AssertControlPlaneFailureDomains will look at all control plane machines and see what failure domains they were
// placed in. If machines were placed in unexpected or wrong failure domains the expectation will fail.
func AssertControlPlaneFailureDomains(ctx context.Context, input AssertControlPlaneFailureDomainsInput) {
	failureDomainCounts := map[string]int{}

	// Look up the cluster object to find all known failure domains.
	cluster := &clusterv1.Cluster{}
	Expect(input.GetLister.Get(ctx, input.ClusterKey, cluster)).To(Succeed())

	for fd := range cluster.Status.FailureDomains {
		failureDomainCounts[fd] = 0
	}

	// Look up all the control plane machines.
	inClustersNamespaceListOption := client.InNamespace(input.ClusterKey.Namespace)
	matchClusterListOption := client.MatchingLabels{
		clusterv1.ClusterLabelName:             input.ClusterKey.Name,
		clusterv1.MachineControlPlaneLabelName: "",
	}

	machineList := &clusterv1.MachineList{}
	Expect(input.GetLister.List(ctx, machineList, inClustersNamespaceListOption, matchClusterListOption)).
		To(Succeed(), "Couldn't list machines for the cluster %q", input.ClusterKey.Name)

	// Count all control plane machine failure domains.
	for _, machine := range machineList.Items {
		if machine.Spec.FailureDomain == nil {
			continue
		}
		failureDomainCounts[*machine.Spec.FailureDomain]++
	}
	Expect(failureDomainCounts).To(Equal(input.ExpectedFailureDomains))
}

// DiscoveryAndWaitForControlPlaneInitializedInput is the input type for DiscoveryAndWaitForControlPlaneInitialized.
type DiscoveryAndWaitForControlPlaneInitializedInput struct {
	Lister  Lister
	Cluster *clusterv1.Cluster
}

// DiscoveryAndWaitForControlPlaneInitialized discovers the KubeadmControlPlane object attached to a cluster and waits for it to be initialized.
func DiscoveryAndWaitForControlPlaneInitialized(ctx context.Context, input DiscoveryAndWaitForControlPlaneInitializedInput, intervals ...interface{}) *controlplanev1.KubeadmControlPlane {
	Expect(ctx).NotTo(BeNil(), "ctx is required for DiscoveryAndWaitForControlPlaneInitialized")
	Expect(input.Lister).ToNot(BeNil(), "Invalid argument. input.Lister can't be nil when calling DiscoveryAndWaitForControlPlaneInitialized")
	Expect(input.Cluster).ToNot(BeNil(), "Invalid argument. input.Cluster can't be nil when calling DiscoveryAndWaitForControlPlaneInitialized")

	controlPlane := GetKubeadmControlPlaneByCluster(ctx, GetKubeadmControlPlaneByClusterInput{
		Lister:      input.Lister,
		ClusterName: input.Cluster.Name,
		Namespace:   input.Cluster.Namespace,
	})
	Expect(controlPlane).ToNot(BeNil())

	log.Logf("Waiting for the first control plane machine managed by %s/%s to be provisioned", controlPlane.Namespace, controlPlane.Name)
	WaitForOneKubeadmControlPlaneMachineToExist(ctx, WaitForOneKubeadmControlPlaneMachineToExistInput{
		Lister:       input.Lister,
		Cluster:      input.Cluster,
		ControlPlane: controlPlane,
	}, intervals...)

	return controlPlane
}

// WaitForControlPlaneAndMachinesReadyInput is the input type for WaitForControlPlaneAndMachinesReady.
type WaitForControlPlaneAndMachinesReadyInput struct {
	GetLister    GetLister
	Cluster      *clusterv1.Cluster
	ControlPlane *controlplanev1.KubeadmControlPlane
}

// WaitForControlPlaneAndMachinesReady waits for a KubeadmControlPlane object to be ready (all the machine provisioned and one node ready).
func WaitForControlPlaneAndMachinesReady(ctx context.Context, input WaitForControlPlaneAndMachinesReadyInput, intervals ...interface{}) {
	Expect(ctx).NotTo(BeNil(), "ctx is required for WaitForControlPlaneReady")
	Expect(input.GetLister).ToNot(BeNil(), "Invalid argument. input.GetLister can't be nil when calling WaitForControlPlaneReady")
	Expect(input.Cluster).ToNot(BeNil(), "Invalid argument. input.Cluster can't be nil when calling WaitForControlPlaneReady")
	Expect(input.ControlPlane).ToNot(BeNil(), "Invalid argument. input.ControlPlane can't be nil when calling WaitForControlPlaneReady")

	if input.ControlPlane.Spec.Replicas != nil && int(*input.ControlPlane.Spec.Replicas) > 1 {
		log.Logf("Waiting for the remaining control plane machines managed by %s/%s to be provisioned", input.ControlPlane.Namespace, input.ControlPlane.Name)
		WaitForKubeadmControlPlaneMachinesToExist(ctx, WaitForKubeadmControlPlaneMachinesToExistInput{
			Lister:       input.GetLister,
			Cluster:      input.Cluster,
			ControlPlane: input.ControlPlane,
		}, intervals...)
	}

	log.Logf("Waiting for control plane %s/%s to be ready (implies underlying nodes to be ready as well)", input.ControlPlane.Namespace, input.ControlPlane.Name)
	waitForControlPlaneToBeReadyInput := WaitForControlPlaneToBeReadyInput{
		Getter:       input.GetLister,
		ControlPlane: input.ControlPlane,
	}
	WaitForControlPlaneToBeReady(ctx, waitForControlPlaneToBeReadyInput, intervals...)
}

// UpgradeControlPlaneAndWaitForUpgradeInput is the input type for UpgradeControlPlaneAndWaitForUpgrade.
type UpgradeControlPlaneAndWaitForUpgradeInput struct {
	ClusterProxy                ClusterProxy
	Cluster                     *clusterv1.Cluster
	ControlPlane                *controlplanev1.KubeadmControlPlane
	KubernetesUpgradeVersion    string
	EtcdImageTag                string
	DNSImageTag                 string
	WaitForMachinesToBeUpgraded []interface{}
	WaitForDNSUpgrade           []interface{}
	WaitForEtcdUpgrade          []interface{}
}

// UpgradeControlPlaneAndWaitForUpgrade upgrades a KubeadmControlPlane and waits for it to be upgraded.
func UpgradeControlPlaneAndWaitForUpgrade(ctx context.Context, input UpgradeControlPlaneAndWaitForUpgradeInput) {
	Expect(ctx).NotTo(BeNil(), "ctx is required for UpgradeControlPlaneAndWaitForUpgrade")
	Expect(input.ClusterProxy).ToNot(BeNil(), "Invalid argument. input.ClusterProxy can't be nil when calling UpgradeControlPlaneAndWaitForUpgrade")
	Expect(input.Cluster).ToNot(BeNil(), "Invalid argument. input.Cluster can't be nil when calling UpgradeControlPlaneAndWaitForUpgrade")
	Expect(input.ControlPlane).ToNot(BeNil(), "Invalid argument. input.ControlPlane can't be nil when calling UpgradeControlPlaneAndWaitForUpgrade")
	Expect(input.KubernetesUpgradeVersion).ToNot(BeNil(), "Invalid argument. input.KubernetesUpgradeVersion can't be empty when calling UpgradeControlPlaneAndWaitForUpgrade")
	Expect(input.EtcdImageTag).ToNot(BeNil(), "Invalid argument. input.EtcdImageTag can't be empty when calling UpgradeControlPlaneAndWaitForUpgrade")
	Expect(input.DNSImageTag).ToNot(BeNil(), "Invalid argument. input.DNSImageTag can't be empty when calling UpgradeControlPlaneAndWaitForUpgrade")

	mgmtClient := input.ClusterProxy.GetClient()

	log.Logf("Patching the new kubernetes version to KCP")
	patchHelper, err := patch.NewHelper(input.ControlPlane, mgmtClient)
	Expect(err).ToNot(HaveOccurred())

	input.ControlPlane.Spec.Version = input.KubernetesUpgradeVersion
	input.ControlPlane.Spec.KubeadmConfigSpec.ClusterConfiguration.Etcd = v1beta1.Etcd{
		Local: &v1beta1.LocalEtcd{
			ImageMeta: v1beta1.ImageMeta{
				ImageTag: input.EtcdImageTag,
			},
		},
	}
	input.ControlPlane.Spec.KubeadmConfigSpec.ClusterConfiguration.DNS = v1beta1.DNS{
		ImageMeta: v1beta1.ImageMeta{
			ImageTag: input.DNSImageTag,
		},
	}

	Expect(patchHelper.Patch(ctx, input.ControlPlane)).To(Succeed())

	log.Logf("Waiting for control-plane machines to have the upgraded kubernetes version")
	WaitForControlPlaneMachinesToBeUpgraded(ctx, WaitForControlPlaneMachinesToBeUpgradedInput{
		Lister:                   mgmtClient,
		Cluster:                  input.Cluster,
		MachineCount:             int(*input.ControlPlane.Spec.Replicas),
		KubernetesUpgradeVersion: input.KubernetesUpgradeVersion,
	}, input.WaitForMachinesToBeUpgraded...)

	log.Logf("Waiting for kube-proxy to have the upgraded kubernetes version")
	workloadCluster := input.ClusterProxy.GetWorkloadCluster(ctx, input.Cluster.Namespace, input.Cluster.Name)
	workloadClient := workloadCluster.GetClient()
	WaitForKubeProxyUpgrade(ctx, WaitForKubeProxyUpgradeInput{
		Getter:            workloadClient,
		KubernetesVersion: input.KubernetesUpgradeVersion,
	}, input.WaitForDNSUpgrade...)

	log.Logf("Waiting for CoreDNS to have the upgraded image tag")
	WaitForDNSUpgrade(ctx, WaitForDNSUpgradeInput{
		Getter:     workloadClient,
		DNSVersion: input.DNSImageTag,
	})

	log.Logf("Waiting for etcd to have the upgraded image tag")
	lblSelector, err := labels.Parse("component=etcd")
	Expect(err).ToNot(HaveOccurred())
	WaitForPodListCondition(ctx, WaitForPodListConditionInput{
		Lister:      workloadClient,
		ListOptions: &client.ListOptions{LabelSelector: lblSelector},
		Condition:   EtcdImageTagCondition(input.EtcdImageTag, int(*input.ControlPlane.Spec.Replicas)),
	}, input.WaitForEtcdUpgrade...)
}

// controlPlaneMachineOptions returns a set of ListOptions that allows to get all machine objects belonging to control plane.
func controlPlaneMachineOptions() []client.ListOption {
	return []client.ListOption{
		client.HasLabels{clusterv1.MachineControlPlaneLabelName},
	}
}

//UpdateControlplaneNodeDrainTimeoutInput is the input for UpdateControlplaneNodeDrainTimeout
type UpdateControlplaneNodeDrainTimeoutInput struct {
	Controlplane               *controlplanev1.KubeadmControlPlane
	NodeDrainTimeout           *metav1.Duration
	ClusterProxy               ClusterProxy
	Cluster                    *clusterv1.Cluster
	WaitForMachinesToBeUpdated []interface{}
	ScaleUpTo                  int32
}

//UpdateControlplaneNodeDrainTimeout updates the NodeDrainTimeout field of controlplane machines
func UpdateControlplaneNodeDrainTimeout(ctx context.Context, input UpdateControlplaneNodeDrainTimeoutInput) {
	Expect(ctx).NotTo(BeNil())
	Expect(input.ClusterProxy).ToNot(BeNil())
	Expect(input.Cluster).ToNot(BeNil())
	Expect(input.Controlplane).ToNot(BeNil())
	controlplane := input.Controlplane
	mgmtClient := input.ClusterProxy.GetClient()

	patchHelper, err := patch.NewHelper(controlplane, mgmtClient)
	Expect(err).ToNot(HaveOccurred())
	controlplane.Spec.NodeDrainTimeout = input.NodeDrainTimeout
	Expect(patchHelper.Patch(context.TODO(), controlplane)).To(Succeed())

	log.Logf("Manually update NodeDrainTimeout for existing controlplane machines")
	// for all machine: patch
	controlplaneMachines := GetControlPlaneMachinesByCluster(ctx, GetControlPlaneMachinesByClusterInput{

		Lister:      mgmtClient,
		ClusterName: input.Cluster.GetName(),
		Namespace:   input.Cluster.GetNamespace(),
	})
	for i := range controlplaneMachines {
		patchHelper, err := patch.NewHelper(&controlplaneMachines[i], mgmtClient)
		Expect(err).ToNot(HaveOccurred())
		controlplaneMachines[i].Spec.NodeDrainTimeout = input.NodeDrainTimeout
		Expect(patchHelper.Patch(context.TODO(), &controlplaneMachines[i])).To(Succeed())
	}

	log.Logf("Add new controlplane machine to see if new machines get new NodeDrainTimeout value")
	ScaleAndWaitControlPlane(ctx, ScaleAndWaitControlPlaneInput{
		ClusterProxy:        input.ClusterProxy,
		Cluster:             input.Cluster,
		ControlPlane:        input.Controlplane,
		Replicas:            input.ScaleUpTo,
		WaitForControlPlane: input.WaitForMachinesToBeUpdated,
	})

	controlplaneMachines = GetControlPlaneMachinesByCluster(ctx, GetControlPlaneMachinesByClusterInput{

		Lister:      mgmtClient,
		ClusterName: input.Cluster.GetName(),
		Namespace:   input.Cluster.GetNamespace(),
	})
	for _, machine := range controlplaneMachines {
		Expect(machine.Spec.NodeDrainTimeout).ToNot(BeNil())
		updated := machine.Spec.NodeDrainTimeout.Seconds() == input.NodeDrainTimeout.Seconds()
		Expect(updated).Should(Equal(true), "Expect the new machine spec to have the same NodeDrainTimeout as KCP spec")
	}
}

type DeployWorkloadOnControlplaneNodeInput struct {
	ClusterProxy                       ClusterProxy
	Cluster                            *clusterv1.Cluster
	WaitForDeploymentAvailableInterval []interface{}
	ControlPlane                       *controlplanev1.KubeadmControlPlane
}

func DeployWorkloadOnControlplaneNode(ctx context.Context, input DeployWorkloadOnControlplaneNodeInput) {
	// tain node
	// workload client
	Expect(input.ClusterProxy).NotTo(BeNil())
	Expect(input.Cluster).NotTo(BeNil())
	Expect(input.WaitForDeploymentAvailableInterval).NotTo(BeNil())

	restConfig, err := remote.RESTConfig(ctx, input.ClusterProxy.GetClient(), util.ObjectKey(input.Cluster))
	Expect(err).To(BeNil(), "Need a restconfig to create a workload client ")
	workloadClient, err := kubernetes.NewForConfig(restConfig)
	Expect(err).To(BeNil(), "Need a workload client to interact to the workload cluster")

	log.Logf("Untaint the controlplane nodes")
	labelMap := map[string]string{
		"node-role.kubernetes.io/master": "",
	}
	listOptions := metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(labelMap).String(),
	}

	controlPlaneNodes, err := workloadClient.CoreV1().Nodes().List(ctx, listOptions)
	Expect(controlPlaneNodes).ToNot(BeNil())
	Expect(err).To(BeNil())

	for i, node := range controlPlaneNodes.Items {
		node.Spec.Taints = []corev1.Taint{}
		updatedNode, err := workloadClient.CoreV1().Nodes().Update(ctx, &controlPlaneNodes.Items[i], metav1.UpdateOptions{})
		Expect(updatedNode.Spec.Taints).To(BeNil())
		Expect(err).To(BeNil())
	}

	log.Logf("Wait for the unevictable pods redeployed into the controlplane nodes")
	deployments, err := workloadClient.AppsV1().Deployments("default").List(ctx, metav1.ListOptions{})
	Expect(deployments).ToNot(BeNil())
	Expect(err).To(BeNil())
	for i := range deployments.Items {
		WaitForDeploymentsAvailableClientset(WaitForDeploymentsAvailableClientsetInput{
			ClientSet:                           workloadClient,
			Deployment:                          &deployments.Items[i],
			WaitForDeploymentsAvailableInterval: input.WaitForDeploymentAvailableInterval,
			Context:                             ctx,
		})
	}
	log.Logf("Ensure that all the unevictable pods do not run on only one controlplane node. If that is the case, and if after scaling down, that node is the only one remains, this test will become useless. Please re-run the test if it happens.")
	Expect(allPodInOneNodeOnly(ctx, workloadClient)).Should(Equal(false))

}

func allPodInOneNodeOnly(ctx context.Context, workloadClient *kubernetes.Clientset) bool {
	pods, err := workloadClient.CoreV1().Pods("default").List(ctx, metav1.ListOptions{})
	Expect(pods).ToNot(BeNil())
	Expect(err).To(BeNil())
	differentNode := 0
	currentNodeName := ""
	for _, pod := range pods.Items {
		if pod.Spec.NodeName != currentNodeName {
			differentNode++
			if differentNode > 1 {
				return false
			}
			currentNodeName = pod.Spec.NodeName
		}
	}
	return true
}

type ScaleAndWaitControlPlaneInput struct {
	ClusterProxy        ClusterProxy
	Cluster             *clusterv1.Cluster
	ControlPlane        *controlplanev1.KubeadmControlPlane
	Replicas            int32
	WaitForControlPlane []interface{}
}

// ScaleAndWaitControlPlane scales MachineDeployment and waits until all machines have node ref and equal to Replicas.
func ScaleAndWaitControlPlane(ctx context.Context, input ScaleAndWaitControlPlaneInput) {
	Expect(ctx).NotTo(BeNil(), "ctx is required for ScaleAndWaitControlPlane")
	Expect(input.ClusterProxy).ToNot(BeNil(), "Invalid argument. input.ClusterProxy can't be nil when calling ScaleAndWaitControlPlane")
	Expect(input.Cluster).ToNot(BeNil(), "Invalid argument. input.Cluster can't be nil when calling ScaleAndWaitControlPlane")

	log.Logf("Scaling controlplane %s/%s from %v to %v replicas", input.ControlPlane.Namespace, input.ControlPlane.Name, input.ControlPlane.Spec.Replicas, input.Replicas)
	patchHelper, err := patch.NewHelper(input.ControlPlane, input.ClusterProxy.GetClient())
	Expect(err).ToNot(HaveOccurred())
	input.ControlPlane.Spec.Replicas = pointer.Int32Ptr(input.Replicas)
	Expect(patchHelper.Patch(ctx, input.ControlPlane)).To(Succeed())

	log.Logf("Waiting for correct number of replicas to exist")
	Eventually(func() (int, error) {
		kcpLabelSelector, err := metav1.ParseToLabelSelector(input.ControlPlane.Status.Selector)
		if err != nil {
			return -1, err
		}

		selectorMap, err := metav1.LabelSelectorAsMap(kcpLabelSelector)
		if err != nil {
			return -1, err
		}
		machines := &clusterv1.MachineList{}
		if err := input.ClusterProxy.GetClient().List(ctx, machines, client.InNamespace(input.ControlPlane.Namespace), client.MatchingLabels(selectorMap)); err != nil {
			return -1, err
		}
		nodeRefCount := 0
		for _, machine := range machines.Items {
			if machine.Status.NodeRef != nil {
				nodeRefCount++
			}
		}
		if len(machines.Items) != nodeRefCount {
			return -1, errors.New("Machine count does not match existing nodes count")
		}
		return nodeRefCount, nil
	}, input.WaitForControlPlane...).Should(Equal(int(*input.ControlPlane.Spec.Replicas)))
}
