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

package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/pointer"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha4"
	"sigs.k8s.io/cluster-api/controllers/remote"
	controlplanev1 "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1alpha4"
	"sigs.k8s.io/cluster-api/test/framework"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"
	"sigs.k8s.io/cluster-api/util"
)

// NodeDrainTimeoutInput is the input for NodeDrainTimeoutSpec.
type NodeDrainTimeoutSpecInput struct {
	E2EConfig             *clusterctl.E2EConfig
	ClusterctlConfigPath  string
	BootstrapClusterProxy framework.ClusterProxy
	ArtifactFolder        string
	SkipCleanup           bool
}

// KCPUpgradeSpec implements a test that verifies KCP to properly upgrade a control plane with 3 machines.
func NodeDrainTimeoutSpec(ctx context.Context, inputGetter func() NodeDrainTimeoutSpecInput) {
	var (
		specName           = "node-drain-timeout"
		input              NodeDrainTimeoutSpecInput
		namespace          *corev1.Namespace
		cancelWatches      context.CancelFunc
		cluster            *clusterv1.Cluster
		machineDeployments []*clusterv1.MachineDeployment
		controlplane       *controlplanev1.KubeadmControlPlane
	)

	BeforeEach(func() {
		Expect(ctx).NotTo(BeNil(), "ctx is required for %s spec", specName)
		input = inputGetter()
		Expect(input.E2EConfig).ToNot(BeNil(), "Invalid argument. input.E2EConfig can't be nil when calling %s spec", specName)
		Expect(input.ClusterctlConfigPath).To(BeAnExistingFile(), "Invalid argument. input.ClusterctlConfigPath must be an existing file when calling %s spec", specName)
		Expect(input.BootstrapClusterProxy).ToNot(BeNil(), "Invalid argument. input.BootstrapClusterProxy can't be nil when calling %s spec", specName)
		Expect(os.MkdirAll(input.ArtifactFolder, 0755)).To(Succeed(), "Invalid argument. input.ArtifactFolder can't be created for %s spec", specName)

		Expect(input.E2EConfig.GetIntervals(specName, "wait-deployment-available")).ToNot(BeNil())
		Expect(input.E2EConfig.GetIntervals(specName, "wait-node-drain")).ToNot(BeNil())

		// Setup a Namespace where to host objects for this spec and create a watcher for the namespace events.
		namespace, cancelWatches = setupSpecNamespace(ctx, specName, input.BootstrapClusterProxy, input.ArtifactFolder)

	})

	It("A note should be forcefully removed if it cannot be drained in time", func() {

		By("Creating a workload cluster")
		controlPlaneReplicas := 3
		applyClusterTemplateResult := clusterctl.ApplyClusterTemplateAndWait(ctx, clusterctl.ApplyClusterTemplateAndWaitInput{
			ClusterProxy: input.BootstrapClusterProxy,
			ConfigCluster: clusterctl.ConfigClusterInput{
				LogFolder:                filepath.Join(input.ArtifactFolder, "clusters", input.BootstrapClusterProxy.GetName()),
				ClusterctlConfigPath:     input.ClusterctlConfigPath,
				KubeconfigPath:           input.BootstrapClusterProxy.GetKubeconfigPath(),
				InfrastructureProvider:   clusterctl.DefaultInfrastructureProvider,
				Flavor:                   "node-drain",
				Namespace:                namespace.Name,
				ClusterName:              fmt.Sprintf("%s-%s", specName, util.RandomString(6)),
				KubernetesVersion:        input.E2EConfig.GetVariable(KubernetesVersion),
				ControlPlaneMachineCount: pointer.Int64Ptr(int64(controlPlaneReplicas)),
				WorkerMachineCount:       pointer.Int64Ptr(1),
			},
			WaitForClusterIntervals:      input.E2EConfig.GetIntervals(specName, "wait-cluster"),
			WaitForControlPlaneIntervals: input.E2EConfig.GetIntervals(specName, "wait-control-plane"),
			WaitForMachineDeployments:    input.E2EConfig.GetIntervals(specName, "wait-worker-nodes"),
		})
		cluster = applyClusterTemplateResult.Cluster
		controlplane = applyClusterTemplateResult.ControlPlane
		machineDeployments = applyClusterTemplateResult.MachineDeployments

		By("Add a deployment and podDisruptionBudget to the workload cluster. The deployed pods cannot be evicted in the node draining process.")
		restConfig, err := remote.RESTConfig(ctx, input.BootstrapClusterProxy.GetClient(), util.ObjectKey(cluster))
		Expect(err).To(BeNil(), "Need a restconfig to create a workload client ")
		workloadClient, err := kubernetes.NewForConfig(restConfig)
		Expect(err).To(BeNil(), "Need a workload client to interact to the workload cluster")
		framework.DeployUnevictablePod(ctx, framework.DeployUnevictablePodInput{
			WorkloadCluster:                    workloadClient,
			MachineDeployments:                 machineDeployments,
			WaitForDeploymentAvailableInterval: input.E2EConfig.GetIntervals(specName, "wait-deployment-available"),
		})

		By("Scale the machinedeployment down to zero. If we didn't have the NodeDrainTimeout duration, the node drain process would block this operator.")
		nodeDrainTimeoutMachineDeploymentInterval := convertMachineDeploymentDurationToInterval(machineDeployments[0].Spec.Template.Spec.NodeDrainTimeout)
		for _, md := range machineDeployments {
			framework.ScaleAndWaitMachineDeployment(ctx, framework.ScaleAndWaitMachineDeploymentInput{
				ClusterProxy:              input.BootstrapClusterProxy,
				Cluster:                   cluster,
				MachineDeployment:         md,
				WaitForMachineDeployments: nodeDrainTimeoutMachineDeploymentInterval,
				Replicas:                  0,
			})
		}

		By("Deploy workload on the master node. The workload is actually the pods that we deployed above.")
		framework.DeployWorkloadOnControlplaneNode(ctx, framework.DeployWorkloadOnControlplaneNodeInput{
			WorkloadCluster:                    workloadClient,
			ControlPlane:                       controlplane,
			WaitForDeploymentAvailableInterval: input.E2EConfig.GetIntervals(specName, "wait-deployment-available"),
		})

		By("Scale down the controlplane of the workload cluster and make sure that nodes running workload can be deleted even the draining process is blocked.")
		// When we scale down the KCP, controlplane machines are by default deleted one by one, so it requires more time.
		nodeDrainTimeoutKCPInterval := convertKCPDurationToInternal(controlplane.Spec.NodeDrainTimeout, controlPlaneReplicas)
		framework.ScaleAndWaitControlPlane(ctx, framework.ScaleAndWaitControlPlaneInput{
			ClusterProxy:        input.BootstrapClusterProxy,
			Cluster:             cluster,
			ControlPlane:        controlplane,
			Replicas:            1,
			WaitForControlPlane: nodeDrainTimeoutKCPInterval,
		})

		By("PASSED!")
	})

	AfterEach(func() {
		// Dumps all the resources in the spec namespace, then cleanups the cluster object and the spec namespace itself.
		dumpSpecResourcesAndCleanup(ctx, specName, input.BootstrapClusterProxy, input.ArtifactFolder, namespace, cancelWatches, cluster, input.E2EConfig.GetIntervals, input.SkipCleanup)
	})
}

func convertDurationToInterval(duration *metav1.Duration, delayRate int) []interface{} {
	minIntervalDuration := duration.Duration
	maxIntervalDuration := (duration.Duration + time.Minute*2) * time.Duration(delayRate)
	intervals := make([]interface{}, 0, 2)
	intervals = append(intervals, maxIntervalDuration.String(), minIntervalDuration.String())
	return intervals
}

func convertMachineDeploymentDurationToInterval(duration *metav1.Duration) []interface{} {
	return convertDurationToInterval(duration, 1)
}

func convertKCPDurationToInternal(duration *metav1.Duration, numKCP int) []interface{} {
	return convertDurationToInterval(duration, numKCP-1)
}
