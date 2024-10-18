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
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"

	"k8s.io/utils/ptr"

	"sigs.k8s.io/cluster-api/test/e2e/internal/log"
	"sigs.k8s.io/cluster-api/test/framework"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"
)

// This file contains a modified, lightweight version of the CAPI scale.go e2e test file.
// It was copied because the original functions are not publicly exported, and it was
// necessary to adapt them for use with legacy cluster templates in CAPM3.
//
// The goal is to align our scalability tests in Metal3 with the existing CAPI tests, without reinventing the wheel.
// In the future, we should work towards making CAPI e2e tests more reusable across different providers, including CAPM3.
//
// Key changes include:
// 1. Added a hook function to apply BareMetalHosts (BMH) in the correct cluster namespace after creation.
// 2. Integrated FKAS to register the cluster, obtain the appropriate host and port, and adjust the template accordingly.
//
// TODOs:
// - Align our scalability tests with CAPI’s while maintaining support for CAPM3-specific components.
// - Investigate further refactoring to fully support ClusterClass or improve legacy template handling.

type ScaleSpecInput struct {
	E2EConfig             *clusterctl.E2EConfig
	ClusterctlConfigPath  string
	BootstrapClusterProxy framework.ClusterProxy
	ArtifactFolder        string

	// InfrastructureProviders specifies the infrastructure to use for clusterctl
	// operations (Example: get cluster templates).
	// Note: In most cases this need not be specified. It only needs to be specified when
	// multiple infrastructure providers (ex: CAPD + in-memory) are installed on the cluster as clusterctl will not be
	// able to identify the default.
	InfrastructureProvider *string

	// Flavor, if specified is the template flavor used to create the cluster for testing.
	// If not specified, the default flavor for the selected infrastructure provider is used.
	// The ClusterTopology of this flavor should have exactly one MachineDeployment.
	Flavor *string

	// ClusterCount is the number of target workload clusters.
	// If unspecified, defaults to 10.
	// Can be overridden by variable CAPI_SCALE_CLUSTER_COUNT.
	ClusterCount *int64

	// DeployClusterInSeparateNamespaces defines if each cluster should be deployed into its separate namespace.
	// In this case The namespace name will be the name of the cluster.
	DeployClusterInSeparateNamespaces bool

	// Concurrency is the maximum concurrency of each of the scale operations.
	// If unspecified it defaults to 5.
	// Can be overridden by variable CAPI_SCALE_CONCURRENCY.
	Concurrency *int64

	// ControlPlaneMachineCount defines the number of control plane machines to be added to each workload cluster.
	// If not specified, 1 will be used.
	// Can be overridden by variable CAPI_SCALE_CONTROLPLANE_MACHINE_COUNT.
	ControlPlaneMachineCount *int64

	// WorkerMachineCount defines number of worker machines per machine deployment of the workload cluster.
	// If not specified, 1 will be used.
	// Can be overridden by variable CAPI_SCALE_WORKER_MACHINE_COUNT.
	// The resulting number of worker nodes for each of the workload cluster will
	// be MachineDeploymentCount*WorkerMachineCount (CAPI_SCALE_MACHINE_DEPLOYMENT_COUNT*CAPI_SCALE_WORKER_MACHINE_COUNT).
	WorkerMachineCount *int64

	// MachineDeploymentCount defines the number of MachineDeployments to be used per workload cluster.
	// If not specified, 1 will be used.
	// Can be overridden by variable CAPI_SCALE_MACHINE_DEPLOYMENT_COUNT.
	// Note: This assumes that the cluster template of the specified flavor has exactly one machine deployment.
	// It uses this machine deployment to create additional copies.
	// Names of the MachineDeployments will be overridden to "md-1", "md-2", etc.
	// The resulting number of worker nodes for each of the workload cluster will
	// be MachineDeploymentCount*WorkerMachineCount (CAPI_SCALE_MACHINE_DEPLOYMENT_COUNT*CAPI_SCALE_WORKER_MACHINE_COUNT).
	MachineDeploymentCount *int64

	// Allows to inject a function to be run after test namespace is created.
	// If not specified, this is a no-op.
	PostNamespaceCreated             func(managementClusterProxy framework.ClusterProxy, workloadClusterNamespace string)
	PostScaleClusterNamespaceCreated func(clusterProxy framework.ClusterProxy, clusterNamespace string, clusterName string, clusterTemplateYAML []byte) (template []byte)

	// FailFast if set to true will return immediately after the first cluster operation fails.
	// If set to false, the test suite will not exit immediately after the first cluster operation fails.
	// Example: When creating clusters from c1 to c20 consider c6 fails creation. If FailFast is set to true
	// the suit will exit immediately after receiving the c6 creation error. If set to false, cluster creations
	// of the other clusters will continue and all the errors are collected before the test exists.
	// Note: Please note that the test suit will still fail since c6 creation failed. FailFast will determine
	// if the test suit should fail as soon as c6 fails or if it should fail after all cluster creations are done.
	FailFast bool

	// SkipUpgrade if set to true will skip upgrading the workload clusters.
	SkipUpgrade bool

	// SkipCleanup if set to true will skip deleting the workload clusters.
	SkipCleanup bool

	// SkipWaitForCreation defines if the test should wait for the workload clusters to be fully provisioned
	// before moving on.
	// If set to true, the test will create the workload clusters and immediately continue without waiting
	// for the clusters to be fully provisioned.
	SkipWaitForCreation bool
}

// scaleSpec implements a scale test for clusters with MachineDeployments.
func ScaleSpec(ctx context.Context, inputGetter func() ScaleSpecInput) {
	clusterCreateResults := []workResult{}
	concurrency := int64(2)

	var (
		specName      = "scale"
		input         ScaleSpecInput
		namespace     *corev1.Namespace
		cancelWatches context.CancelFunc
	)

	BeforeEach(func() {
		Expect(ctx).NotTo(BeNil(), "ctx is required for %s spec", specName)
		input = inputGetter()
		Expect(input.E2EConfig).ToNot(BeNil(), "Invalid argument. input.E2EConfig can't be nil when calling %s spec", specName)
		Expect(input.ClusterctlConfigPath).To(BeAnExistingFile(), "Invalid argument. input.ClusterctlConfigPath must be an existing file when calling %s spec", specName)
		Expect(input.BootstrapClusterProxy).ToNot(BeNil(), "Invalid argument. input.BootstrapClusterProxy can't be nil when calling %s spec", specName)
		Expect(os.MkdirAll(input.ArtifactFolder, 0750)).To(Succeed(), "Invalid argument. input.ArtifactFolder can't be created for %s spec", specName)
		// Apply bmh

		// Setup a Namespace where to host objects for this spec and create a watcher for the namespace events.
		// We are pinning the namespace for the test to help with debugging and testing.
		// Example: Queries to look up state of the clusters can be re-used.
		// Since we don	// ClusterNames is the names of clusters to work on.'t run multiple instances of this test concurrently on a management cluster it is okay to pin the namespace.
		Byf("Creating a namespace for hosting the %q test spec", specName)
		namespace, cancelWatches = framework.CreateNamespaceAndWatchEvents(ctx, framework.CreateNamespaceAndWatchEventsInput{
			Creator:             input.BootstrapClusterProxy.GetClient(),
			ClientSet:           input.BootstrapClusterProxy.GetClientSet(),
			Name:                specName,
			LogFolder:           filepath.Join(input.ArtifactFolder, "clusters", input.BootstrapClusterProxy.GetName()),
			IgnoreAlreadyExists: true,
		})

		//numNodes, _ := strconv.Atoi(e2eConfig.GetVariable("NUM_NODES"))
		//batch, _ := strconv.Atoi(e2eConfig.GetVariable("BMH_BATCH_SIZE"))
	})

	It("Should create and delete scaled number of workload clusters", func() {
		infrastructureProvider := clusterctl.DefaultInfrastructureProvider
		if input.InfrastructureProvider != nil {
			infrastructureProvider = *input.InfrastructureProvider
		}

		flavor := clusterctl.DefaultFlavor
		if input.Flavor != nil {
			flavor = *input.Flavor
		}

		controlPlaneMachineCount := ptr.To[int64](1)
		if input.ControlPlaneMachineCount != nil {
			controlPlaneMachineCount = input.ControlPlaneMachineCount
		}
		// If variable is defined that will take precedence.
		if input.E2EConfig.HasVariable(scaleControlPlaneMachineCount) {
			controlPlaneMachineCountStr := input.E2EConfig.GetVariable(scaleControlPlaneMachineCount)
			controlPlaneMachineCountInt, err := strconv.Atoi(controlPlaneMachineCountStr)
			Expect(err).ToNot(HaveOccurred())
			controlPlaneMachineCount = ptr.To[int64](int64(controlPlaneMachineCountInt))
		}

		workerMachineCount := ptr.To[int64](1)
		if input.WorkerMachineCount != nil {
			workerMachineCount = input.WorkerMachineCount
		}
		// If variable is defined that will take precedence.
		if input.E2EConfig.HasVariable(scaleWorkerMachineCount) {
			workerMachineCountStr := input.E2EConfig.GetVariable(scaleWorkerMachineCount)
			workerMachineCountInt, err := strconv.Atoi(workerMachineCountStr)
			Expect(err).ToNot(HaveOccurred())
			workerMachineCount = ptr.To[int64](int64(workerMachineCountInt))
		}

		// machineDeploymentCount := ptr.To[int64](1)
		// if input.MachineDeploymentCount != nil {
		// 	machineDeploymentCount = input.MachineDeploymentCount
		// }
		// If variable is defined that will take precedence.
		// if input.E2EConfig.HasVariable(scaleMachineDeploymentCount) {
		// 	machineDeploymentCountStr := input.E2EConfig.GetVariable(scaleMachineDeploymentCount)
		// 	machineDeploymentCountInt, err := strconv.Atoi(machineDeploymentCountStr)
		// 	Expect(err).ToNot(HaveOccurred())
		// 	machineDeploymentCount = ptr.To[int64](int64(machineDeploymentCountInt))
		// }

		clusterCount := int64(2)
		if input.ClusterCount != nil {
			clusterCount = *input.ClusterCount
		}
		// If variable is defined that will take precedence.
		// if input.E2EConfig.HasVariable(scaleClusterCount) {
		// 	clusterCountStr := input.E2EConfig.GetVariable(scaleClusterCount)
		// 	var err error
		// 	clusterCount, err = strconv.ParseInt(clusterCountStr, 10, 64)
		// 	Expect(err).NotTo(HaveOccurred(), "%q value should be integer", scaleClusterCount)
		// }

		// If variable is defined that will take precedence.
		// if input.E2EConfig.HasVariable(scaleConcurrency) {
		// 	concurrencyStr := input.E2EConfig.GetVariable(scaleConcurrency)
		// 	var err error
		// 	concurrency, err = strconv.ParseInt(concurrencyStr, 10, 64)
		// 	Expect(err).NotTo(HaveOccurred(), "%q value should be integer", scaleConcurrency)
		// }

		By("Create the cluster to be used by all workload clusters")

		log.Logf("Generating YAML for base Cluster")
		os.Setenv("CLUSTER_APIENDPOINT_HOST", "CLUSTER_APIENDPOINT_HOST_HOLDER")
		os.Setenv("CLUSTER_APIENDPOINT_PORT", "CLUSTER_APIENDPOINT_PORT_HOLDER")
		baseWorkloadClusterTemplate := clusterctl.ConfigCluster(ctx, clusterctl.ConfigClusterInput{
			LogFolder:                filepath.Join(input.ArtifactFolder, "clusters", input.BootstrapClusterProxy.GetName()),
			ClusterctlConfigPath:     input.ClusterctlConfigPath,
			KubeconfigPath:           input.BootstrapClusterProxy.GetKubeconfigPath(),
			InfrastructureProvider:   infrastructureProvider,
			Flavor:                   flavor,
			Namespace:                scaleClusterNamespacePlaceholder,
			ClusterName:              scaleClusterNamePlaceholder,
			KubernetesVersion:        input.E2EConfig.GetVariable("KUBERNETES_VERSION"),
			ControlPlaneMachineCount: controlPlaneMachineCount,
			WorkerMachineCount:       workerMachineCount,
		})

		Expect(baseWorkloadClusterTemplate).ToNot(BeNil(), "Failed to get the cluster template")

		By("Create workload clusters concurrently")
		clusterNames := make([]string, 0, clusterCount)
		clusterNameDigits := 1 + int(math.Log10(float64(clusterCount)))
		for i := int64(1); i <= clusterCount; i++ {
			// This ensures we always have the right number of leading zeros in our cluster names, e.g.
			// clusterCount=1000 will lead to cluster names like scale-0001, scale-0002, ... .
			// This makes it possible to have correct ordering of clusters in diagrams in tools like Grafana.
			name := fmt.Sprintf("%s-%0*d", specName, clusterNameDigits, i)
			clusterNames = append(clusterNames, name)
		}

		// Get the cluster creator function the creator is a function the will use
		// clusterctl.ApplyCustomClusterTemplateAndWait to apply the templates generated earlier and wait for the cluster to be ready

		creator := getClusterCreateAndWaitFn(clusterctl.ApplyCustomClusterTemplateAndWaitInput{
			ClusterProxy:                 input.BootstrapClusterProxy,
			WaitForClusterIntervals:      input.E2EConfig.GetIntervals(specName, "wait-cluster"),
			WaitForControlPlaneIntervals: input.E2EConfig.GetIntervals(specName, "wait-control-plane"),
			WaitForMachineDeployments:    input.E2EConfig.GetIntervals(specName, "wait-worker-nodes"),
		})

		// Run the creation concurrenlty send clusterNames list and the maximum concurrency number and the workerFunction.
		// Concurrency is the maximum number of clusters to be created concurrently.
		// NB. This also includes waiting for the clusters to be up and running.
		// Example: If the concurrency is 2. It would create 2 clusters concurrently and wait
		// till at least one of the clusters is up and running before it starts creating another
		// cluster.
		clusterCreateResults, err := workConcurrentlyAndWait(ctx, workConcurrentlyAndWaitInput{
			ClusterNames: clusterNames,
			Concurrency:  concurrency,
			FailFast:     input.FailFast,
			WorkerFunc: func(ctx context.Context, inputChan chan string, resultChan chan workResult, wg *sync.WaitGroup) {
				createClusterWorkerm(ctx, input.BootstrapClusterProxy, inputChan, resultChan, wg, namespace.Name, input.DeployClusterInSeparateNamespaces, baseWorkloadClusterTemplate, creator, input.PostScaleClusterNamespaceCreated)
			},
		})
		if err != nil {
			// Call Fail to notify ginkgo that the suit has failed.
			// Ginkgo will print the first observed error failure in this case.
			// Example: If cluster c1, c2 and c3 failed then ginkgo will only print the first
			// observed failure among the these 3 clusters.
			// Since ginkgo only captures one failure, to help with this we are logging the error
			// that will contain the full stack trace of failure for each cluster to help with debugging.
			// TODO(ykakarap): Follow-up: Explore options for improved error reporting.
			log.Logf("Failed to create clusters. Error: %s", err.Error())
			Fail("")
		}

		log.Logf("%v", clusterCreateResults)

		By("PASSED!")
	})
	AfterEach(func() {
		clusterNamesToDelete := []string{}
		for _, result := range clusterCreateResults {
			clusterNamesToDelete = append(clusterNamesToDelete, result.clusterName)
		}

		By("Delete the workload clusters concurrently")
		// Now delete all the workload clusters.
		_, err := workConcurrentlyAndWait(ctx, workConcurrentlyAndWaitInput{
			ClusterNames: clusterNamesToDelete,
			Concurrency:  concurrency,
			FailFast:     input.FailFast,
			WorkerFunc: func(ctx context.Context, inputChan chan string, resultChan chan workResult, wg *sync.WaitGroup) {
				deleteClusterAndWaitWorker(ctx, inputChan, resultChan, wg, input.BootstrapClusterProxy.GetClient(), namespace.Name, input.DeployClusterInSeparateNamespaces)
			},
		})
		if err != nil {
			// Call Fail to notify ginkgo that the suit has failed.
			// Ginkgo will print the first observed error failure in this case.
			// Example: If cluster c1, c2 and c3 failed then ginkgo will only print the first
			// observed failure among the these 3 clusters.
			// Since ginkgo only captures one failure, to help with this we are logging the error
			// that will contain the full stack trace of failure for each cluster to help with debugging.
			// TODO(ykakarap): Follow-up: Explore options for improved error reporting.
			log.Logf("Failed to delete clusters. Error: %s", err.Error())
			Fail("")
		}
		cancelWatches()
	})
}

type PreClusterCreateCallback func(clusterProxy framework.ClusterProxy, clusterNamespace string, clusterName string, clusterTemplateYAML []byte) (template []byte)

// This function is edited to call back function where we can apply the needed number of BMH in the correct namespace and also call FKAS and edit the template accordingly
// type PreClusterCreateCallback func(managementClusterProxy framework.ClusterProxy, namespace, clusterName string, clusterTemplateYAML []byte)
func createClusterWorkerm(ctx context.Context, clusterProxy framework.ClusterProxy, inputChan <-chan string, resultChan chan<- workResult, wg *sync.WaitGroup, defaultNamespace string, deployClusterInSeparateNamespaces bool, baseClusterTemplateYAML []byte, create clusterCreator, PostNamespaceCreated PreClusterCreateCallback) {
	defer wg.Done()

	for {
		done := func() bool {
			select {
			case <-ctx.Done():
				// If the context is cancelled, return and shutdown the worker.
				return true
			case clusterName, open := <-inputChan:
				// Read the cluster name from the channel.
				// If the channel is closed it implies there is no more work to be done. Return.
				if !open {
					return true
				}
				log.Logf("Creating cluster %s", clusterName)

				// This defer will catch ginkgo failures and record them.
				// The recorded panics are then handled by the parent goroutine.
				defer func() {
					e := recover()
					resultChan <- workResult{
						clusterName: clusterName,
						err:         e,
					}
				}()

				// Calculate namespace.
				namespaceName := defaultNamespace
				if deployClusterInSeparateNamespaces {
					namespaceName = clusterName
				}

				// If every cluster should be deployed in a separate namespace:
				// * Adjust namespace in ClusterClass YAML.
				// * Create new namespace.
				// * Deploy ClusterClass in new namespace.
				if deployClusterInSeparateNamespaces {
					log.Logf("Create namespace %", namespaceName)
					_ = framework.CreateNamespace(ctx, framework.CreateNamespaceInput{
						Creator:             clusterProxy.GetClient(),
						Name:                namespaceName,
						IgnoreAlreadyExists: true,
					}, "40s", "10s")

				}

				// how I can ensure that this function is concurency safe
				FBaseClusterTemplateYAML := []byte{}
				if PostNamespaceCreated != nil {
					log.Logf("Calling PostNamespaceCreated for namespace %s", namespaceName)
					FBaseClusterTemplateYAML = PostNamespaceCreated(clusterProxy, namespaceName, clusterName, baseClusterTemplateYAML)
				}

				// Adjust namespace and name in Cluster YAML
				clusterTemplateYAML := bytes.Replace(FBaseClusterTemplateYAML, []byte(scaleClusterNamespacePlaceholder), []byte(namespaceName), -1)
				clusterTemplateYAML = bytes.Replace(clusterTemplateYAML, []byte(scaleClusterNamePlaceholder), []byte(clusterName), -1)

				// Deploy Cluster.
				create(ctx, namespaceName, clusterName, clusterTemplateYAML)
				return false
			}
		}()
		if done {
			break
		}
	}
}
