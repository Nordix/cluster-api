package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/pointer"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha3"
	controlplanev1 "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1alpha3"
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

		Expect(input.E2EConfig.Variables).To(HaveKey(NodeDrainTimeoutVar))

		Expect(input.E2EConfig.GetIntervals(specName, "wait-deployment-available")).ToNot(BeNil())
		Expect(input.E2EConfig.GetIntervals(specName, "wait-node-drain")).ToNot(BeNil())

		// Setup a Namespace where to host objects for this spec and create a watcher for the namespace events.
		namespace, cancelWatches = setupSpecNamespace(ctx, specName, input.BootstrapClusterProxy, input.ArtifactFolder)

	})

	It("Should forcefully remove a node if it is failing to drain in time", func() {

		By("Creating a workload cluster")
		cluster, controlplane, machineDeployments = clusterctl.ApplyClusterTemplateAndWait(ctx, clusterctl.ApplyClusterTemplateAndWaitInput{
			ClusterProxy: input.BootstrapClusterProxy,
			ConfigCluster: clusterctl.ConfigClusterInput{
				LogFolder:                filepath.Join(input.ArtifactFolder, "clusters", input.BootstrapClusterProxy.GetName()),
				ClusterctlConfigPath:     input.ClusterctlConfigPath,
				KubeconfigPath:           input.BootstrapClusterProxy.GetKubeconfigPath(),
				InfrastructureProvider:   clusterctl.DefaultInfrastructureProvider,
				Flavor:                   clusterctl.DefaultFlavor,
				Namespace:                namespace.Name,
				ClusterName:              fmt.Sprintf("%s-%s", specName, util.RandomString(6)),
				KubernetesVersion:        input.E2EConfig.GetVariable(KubernetesVersion),
				ControlPlaneMachineCount: pointer.Int64Ptr(1),
				WorkerMachineCount:       pointer.Int64Ptr(1),
			},
			WaitForClusterIntervals:      input.E2EConfig.GetIntervals(specName, "wait-cluster"),
			WaitForControlPlaneIntervals: input.E2EConfig.GetIntervals(specName, "wait-control-plane"),
			WaitForMachineDeployments:    input.E2EConfig.GetIntervals(specName, "wait-worker-nodes"),
		})

		By("Update the nodeDrainTimeout field of the machinedeployment and wait for all machines to be updated")

		nodeDrainTimeoutInt, err := strconv.ParseInt(input.E2EConfig.GetVariable(NodeDrainTimeoutVar), 10, 64)
		Expect(err).To(BeNil(), "NodeDrainTimeOut needs to be converted from string to int")

		framework.UpdateNodeDrainTimeoutInMachineDeployment(ctx, framework.UpdateNodeDrainTimeoutInMachineDeploymentInput{
			ClusterProxy:               input.BootstrapClusterProxy,
			Cluster:                    cluster,
			MachineDeployments:         machineDeployments,
			NodeDrainTimeout:           nodeDrainTimeoutInt,
			WaitForMachinesToBeUpdated: input.E2EConfig.GetIntervals(specName, "wait-machine-updated"),
		})

		By("Add deployment and podDisruptionBudget to the workload cluster")
		framework.AddUnevictablePod(ctx, framework.AddUnevictablePodInput{
			ClusterProxy:                       input.BootstrapClusterProxy,
			Cluster:                            cluster,
			MachineDeployments:                 machineDeployments,
			WaitForDeploymentAvailableInterval: input.E2EConfig.GetIntervals(specName, "wait-deployment-available"),
		})
		By("Scale the machinedeployment down")
		for _, md := range machineDeployments {
			framework.ScaleAndWaitMachineDeployment(ctx, framework.ScaleAndWaitMachineDeploymentInput{
				ClusterProxy:              input.BootstrapClusterProxy,
				Cluster:                   cluster,
				MachineDeployment:         md,
				WaitForMachineDeployments: input.E2EConfig.GetIntervals(specName, "wait-node-drain"),
			})
		}
		By("Update nodeDrainTimeout on master nodes")
		framework.UpdateControlplaneNodeDrainTimeout(ctx, framework.UpdateControlplaneNodeDrainTimeoutInput{
			Controlplane:               controlplane,
			NodeDrainTimeout:           nodeDrainTimeoutInt,
			ClusterProxy:               input.BootstrapClusterProxy,
			Cluster:                    cluster,
			WaitForMachinesToBeUpdated: input.E2EConfig.GetIntervals(specName, "wait-machine-updated"),
		})
		By("Deploy workload on the master node")
		framework.DeployWorkloadOnControlplaneNode(ctx, framework.DeployWorkloadOnControlplaneNodeInput{})
		By("Scale down the controlplane of the workload cluster and make sure that all nodes are deleted even the draining process is blocked")
		framework.ScaleAndWaitControlPlane(ctx, framework.ScaleAndWaitControlPlaneInput{
			ClusterProxy:        input.BootstrapClusterProxy,
			Cluster:             cluster,
			ControlPlane:        controlplane,
			Replicas:            0,
			WaitForControlPlane: input.E2EConfig.GetIntervals(specName, "wait-node-drain"),
		})

		By("PASSED!")
	})

	AfterEach(func() {
		// Dumps all the resources in the spec namespace, then cleanups the cluster object and the spec namespace itself.
		dumpSpecResourcesAndCleanup(ctx, specName, input.BootstrapClusterProxy, input.ArtifactFolder, namespace, cancelWatches, cluster, input.E2EConfig.GetIntervals, input.SkipCleanup)
	})
}
