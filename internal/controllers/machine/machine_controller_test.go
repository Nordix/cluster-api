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

package machine

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/record"
	utilfeature "k8s.io/component-base/featuregate/testing"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache/informertest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/api/core/v1beta2/index"
	"sigs.k8s.io/cluster-api/controllers/clustercache"
	"sigs.k8s.io/cluster-api/controllers/external"
	externalfake "sigs.k8s.io/cluster-api/controllers/external/fake"
	"sigs.k8s.io/cluster-api/feature"
	"sigs.k8s.io/cluster-api/internal/contract"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/cache"
	"sigs.k8s.io/cluster-api/util/conditions"
	v1beta1conditions "sigs.k8s.io/cluster-api/util/conditions/deprecated/v1beta1"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/cluster-api/util/test/builder"
)

func TestWatches(t *testing.T) {
	g := NewWithT(t)
	ns, err := env.CreateNamespace(ctx, "test-machine-watches")
	g.Expect(err).ToNot(HaveOccurred())

	infraMachine := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind":       "GenericInfrastructureMachine",
			"apiVersion": clusterv1.GroupVersionInfrastructure.String(),
			"metadata": map[string]interface{}{
				"name":      "infra-config1",
				"namespace": ns.Name,
			},
			"spec": map[string]interface{}{
				"providerID": "test://id-1",
			},
			"status": map[string]interface{}{
				"ready": true,
				"addresses": []interface{}{
					map[string]interface{}{
						"type":    "InternalIP",
						"address": "10.0.0.1",
					},
				},
			},
		},
	}

	defaultBootstrap := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind":       "GenericBootstrapConfig",
			"apiVersion": clusterv1.GroupVersionBootstrap.String(),
			"metadata": map[string]interface{}{
				"name":      "bootstrap-config-machinereconcile",
				"namespace": ns.Name,
			},
			"spec":   map[string]interface{}{},
			"status": map[string]interface{}{},
		},
	}

	testCluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "machine-reconcile-",
			Namespace:    ns.Name,
		},
		Spec: clusterv1.ClusterSpec{
			ControlPlaneRef: clusterv1.ContractVersionedObjectReference{
				APIGroup: builder.ControlPlaneGroupVersion.Group,
				Kind:     builder.GenericControlPlaneKind,
				Name:     "cp1",
			},
		},
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "node-1",
			Namespace: ns.Name,
		},
		Spec: corev1.NodeSpec{
			ProviderID: "test://id-1",
		},
	}

	g.Expect(env.Create(ctx, testCluster)).To(Succeed())
	g.Expect(env.CreateKubeconfigSecret(ctx, testCluster)).To(Succeed())
	// Set InfrastructureReady to true so ClusterCache creates the clusterAccessor.
	testClusterOriginal := client.MergeFrom(testCluster.DeepCopy())
	testCluster.Status.Initialization.InfrastructureProvisioned = ptr.To(true)
	g.Expect(env.Status().Patch(ctx, testCluster, testClusterOriginal)).To(Succeed())

	g.Expect(env.Create(ctx, defaultBootstrap)).To(Succeed())
	g.Expect(env.Create(ctx, node)).To(Succeed())
	g.Expect(env.Create(ctx, infraMachine)).To(Succeed())

	defer func(do ...client.Object) {
		g.Expect(env.Cleanup(ctx, do...)).To(Succeed())
	}(ns, testCluster, defaultBootstrap)

	// Patch infra machine ready
	patchHelper, err := patch.NewHelper(infraMachine, env)
	g.Expect(err).ShouldNot(HaveOccurred())
	g.Expect(unstructured.SetNestedField(infraMachine.Object, true, "status", "initialization", "provisioned")).To(Succeed())
	g.Expect(patchHelper.Patch(ctx, infraMachine, patch.WithStatusObservedGeneration{})).To(Succeed())

	// Patch bootstrap ready
	patchHelper, err = patch.NewHelper(defaultBootstrap, env)
	g.Expect(err).ShouldNot(HaveOccurred())
	g.Expect(unstructured.SetNestedField(defaultBootstrap.Object, true, "status", "initialization", "dataSecretCreated")).To(Succeed())
	g.Expect(unstructured.SetNestedField(defaultBootstrap.Object, "secretData", "status", "dataSecretName")).To(Succeed())
	g.Expect(patchHelper.Patch(ctx, defaultBootstrap, patch.WithStatusObservedGeneration{})).To(Succeed())

	machine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "machine-created-",
			Namespace:    ns.Name,
			Labels: map[string]string{
				clusterv1.MachineControlPlaneLabel: "",
			},
		},
		Spec: clusterv1.MachineSpec{
			ClusterName: testCluster.Name,
			InfrastructureRef: clusterv1.ContractVersionedObjectReference{
				APIGroup: clusterv1.GroupVersionInfrastructure.Group,
				Kind:     "GenericInfrastructureMachine",
				Name:     "infra-config1",
			},
			Bootstrap: clusterv1.Bootstrap{
				ConfigRef: clusterv1.ContractVersionedObjectReference{
					APIGroup: clusterv1.GroupVersionBootstrap.Group,
					Kind:     "GenericBootstrapConfig",
					Name:     "bootstrap-config-machinereconcile",
				},
			},
		},
	}

	g.Expect(env.Create(ctx, machine)).To(Succeed())
	defer func() {
		g.Expect(env.Cleanup(ctx, machine)).To(Succeed())
	}()

	// Wait for reconciliation to happen.
	// Since infra and bootstrap objects are ready, a nodeRef will be assigned during node reconciliation.
	key := client.ObjectKey{Name: machine.Name, Namespace: machine.Namespace}
	g.Eventually(func() bool {
		if err := env.Get(ctx, key, machine); err != nil {
			return false
		}
		return machine.Status.NodeRef.IsDefined()
	}, timeout).Should(BeTrue())

	// Node deletion will trigger node watchers and a request will be added to the queue.
	g.Expect(env.Delete(ctx, node)).To(Succeed())
	// TODO: Once conditions are in place, check if node deletion triggered a reconcile.

	// Delete infra machine, external tracker will trigger reconcile
	// and machine Status.FailureReason should be non-nil after reconcileInfrastructure
	g.Expect(env.Delete(ctx, infraMachine)).To(Succeed())
	g.Eventually(func() bool {
		if err := env.Get(ctx, key, machine); err != nil {
			return false
		}
		if machine.Status.Deprecated == nil || machine.Status.Deprecated.V1Beta1 == nil {
			return false
		}
		return machine.Status.Deprecated.V1Beta1.FailureMessage != nil
	}, timeout).Should(BeTrue())
}

func TestWatchesDelete(t *testing.T) {
	g := NewWithT(t)
	ns, err := env.CreateNamespace(ctx, "test-machine-watches-delete")
	g.Expect(err).ToNot(HaveOccurred())

	infraMachine := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind":       "GenericInfrastructureMachine",
			"apiVersion": clusterv1.GroupVersionInfrastructure.String(),
			"metadata": map[string]interface{}{
				"name":      "infra-config1",
				"namespace": ns.Name,
			},
			"spec": map[string]interface{}{
				"providerID": "test://id-1",
			},
			"status": map[string]interface{}{
				"ready": true,
				"addresses": []interface{}{
					map[string]interface{}{
						"type":    "InternalIP",
						"address": "10.0.0.1",
					},
				},
			},
		},
	}
	infraMachineFinalizer := "test.infrastructure.cluster.x-k8s.io"
	controllerutil.AddFinalizer(infraMachine, infraMachineFinalizer)

	defaultBootstrap := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind":       "GenericBootstrapConfig",
			"apiVersion": clusterv1.GroupVersionBootstrap.String(),
			"metadata": map[string]interface{}{
				"name":      "bootstrap-config-machinereconcile",
				"namespace": ns.Name,
			},
			"spec":   map[string]interface{}{},
			"status": map[string]interface{}{},
		},
	}
	bootstrapFinalizer := "test.bootstrap.cluster.x-k8s.io"
	controllerutil.AddFinalizer(defaultBootstrap, bootstrapFinalizer)

	testCluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "machine-reconcile-",
			Namespace:    ns.Name,
		},
		Spec: clusterv1.ClusterSpec{
			// we create the cluster in paused state so we don't reconcile
			// the machine immediately after creation.
			// This avoids going through reconcileExternal, which adds watches
			// for the provider machine and the bootstrap config objects.
			Paused: ptr.To(true),
			InfrastructureRef: clusterv1.ContractVersionedObjectReference{
				APIGroup: builder.InfrastructureGroupVersion.Group,
				Kind:     builder.GenericInfrastructureClusterKind,
				Name:     "infracluster1",
			},
		},
	}

	g.Expect(env.Create(ctx, testCluster)).To(Succeed())
	g.Expect(env.CreateKubeconfigSecret(ctx, testCluster)).To(Succeed())
	g.Expect(env.Create(ctx, defaultBootstrap)).To(Succeed())
	g.Expect(env.Create(ctx, infraMachine)).To(Succeed())

	defer func(do ...client.Object) {
		g.Expect(env.Cleanup(ctx, do...)).To(Succeed())
	}(ns, testCluster, defaultBootstrap)

	// Patch infra machine ready
	patchHelper, err := patch.NewHelper(infraMachine, env)
	g.Expect(err).ShouldNot(HaveOccurred())
	g.Expect(unstructured.SetNestedField(infraMachine.Object, true, "status", "initialization", "provisioned")).To(Succeed())
	g.Expect(patchHelper.Patch(ctx, infraMachine, patch.WithStatusObservedGeneration{})).To(Succeed())

	// Patch bootstrap ready
	patchHelper, err = patch.NewHelper(defaultBootstrap, env)
	g.Expect(err).ShouldNot(HaveOccurred())
	g.Expect(unstructured.SetNestedField(defaultBootstrap.Object, true, "status", "initialization", "dataSecretCreated")).To(Succeed())
	g.Expect(unstructured.SetNestedField(defaultBootstrap.Object, "secretData", "status", "dataSecretName")).To(Succeed())
	g.Expect(patchHelper.Patch(ctx, defaultBootstrap, patch.WithStatusObservedGeneration{})).To(Succeed())

	machine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "machine-created-",
			Namespace:    ns.Name,
			Labels: map[string]string{
				clusterv1.MachineControlPlaneLabel: "",
			},
		},
		Spec: clusterv1.MachineSpec{
			ClusterName: testCluster.Name,
			InfrastructureRef: clusterv1.ContractVersionedObjectReference{
				APIGroup: clusterv1.GroupVersionInfrastructure.Group,
				Kind:     "GenericInfrastructureMachine",
				Name:     "infra-config1",
			},
			Bootstrap: clusterv1.Bootstrap{
				ConfigRef: clusterv1.ContractVersionedObjectReference{
					APIGroup: clusterv1.GroupVersionBootstrap.Group,
					Kind:     "GenericBootstrapConfig",
					Name:     "bootstrap-config-machinereconcile",
				},
			},
		},
	}
	// We create the machine with a finalizer so the machine is not deleted immediately.
	controllerutil.AddFinalizer(machine, clusterv1.MachineFinalizer)

	g.Expect(env.Create(ctx, machine)).To(Succeed())
	defer func() {
		g.Expect(env.Cleanup(ctx, machine)).To(Succeed())
	}()

	// We mark the machine for deletion
	g.Expect(env.Delete(ctx, machine)).To(Succeed())

	// We unpause the cluster so the machine can be reconciled.
	testCluster.Spec.Paused = ptr.To(false)
	g.Expect(env.Update(ctx, testCluster)).To(Succeed())

	// Wait for reconciliation to happen.
	// The first reconciliation should add the cluster name label.
	key := client.ObjectKey{Name: machine.Name, Namespace: machine.Namespace}
	g.Eventually(func() bool {
		if err := env.Get(ctx, key, machine); err != nil {
			return false
		}
		return machine.Labels[clusterv1.ClusterNameLabel] == testCluster.Name
	}, timeout).Should(BeTrue())

	// Deleting the machine should mark the infra machine for deletion
	infraMachineKey := client.ObjectKey{Name: infraMachine.GetName(), Namespace: infraMachine.GetNamespace()}
	g.Eventually(func() bool {
		if err := env.Get(ctx, infraMachineKey, infraMachine); err != nil {
			return false
		}
		return infraMachine.GetDeletionTimestamp() != nil
	}, timeout).Should(BeTrue(), "infra machine should be marked for deletion")

	// We wait a bit and remove the finalizer, simulating the infra machine controller.
	time.Sleep(2 * time.Second)
	infraMachine.SetFinalizers([]string{})
	g.Expect(env.Update(ctx, infraMachine)).To(Succeed())

	// This should delete the infra machine
	g.Eventually(func() bool {
		err := env.Get(ctx, infraMachineKey, infraMachine)
		return apierrors.IsNotFound(err)
	}, timeout).Should(BeTrue(), "infra machine should be deleted")

	// If the watch on infra machine works, deleting of the infra machine will trigger another
	// reconcile, which will mark the bootstrap config for deletion
	bootstrapKey := client.ObjectKey{Name: defaultBootstrap.GetName(), Namespace: defaultBootstrap.GetNamespace()}
	g.Eventually(func() bool {
		if err := env.Get(ctx, bootstrapKey, defaultBootstrap); err != nil {
			return false
		}
		return defaultBootstrap.GetDeletionTimestamp() != nil
	}, timeout).Should(BeTrue(), "bootstrap config should be marked for deletion")

	// We wait a bit a remove the finalizer, simulating the bootstrap config controller.
	time.Sleep(2 * time.Second)
	defaultBootstrap.SetFinalizers([]string{})
	g.Expect(env.Update(ctx, defaultBootstrap)).To(Succeed())

	// This should delete the bootstrap config.
	g.Eventually(func() bool {
		err := env.Get(ctx, bootstrapKey, defaultBootstrap)
		return apierrors.IsNotFound(err)
	}, timeout).Should(BeTrue(), "bootstrap config should be deleted")

	// If the watch on bootstrap config works, the deleting of the bootstrap config will trigger another
	// reconcile, which will remove the finalizer and delete the machine
	g.Eventually(func() bool {
		err := env.Get(ctx, key, machine)
		return apierrors.IsNotFound(err)
	}, timeout).Should(BeTrue(), "machine should be deleted")
}

func TestMachine_Reconcile(t *testing.T) {
	g := NewWithT(t)

	ns, err := env.CreateNamespace(ctx, "test-machine-reconcile")
	g.Expect(err).ToNot(HaveOccurred())

	infraMachine := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind":       "GenericInfrastructureMachine",
			"apiVersion": clusterv1.GroupVersionInfrastructure.String(),
			"metadata": map[string]interface{}{
				"name":      "infra-config1",
				"namespace": ns.Name,
			},
			"spec": map[string]interface{}{
				"providerID": "test://id-1",
			},
		},
	}

	defaultBootstrap := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind":       "GenericBootstrapConfig",
			"apiVersion": clusterv1.GroupVersionBootstrap.String(),
			"metadata": map[string]interface{}{
				"name":      "bootstrap-config-machinereconcile",
				"namespace": ns.Name,
			},
			"spec":   map[string]interface{}{},
			"status": map[string]interface{}{},
		},
	}

	testCluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "machine-reconcile-",
			Namespace:    ns.Name,
		},
		Spec: clusterv1.ClusterSpec{
			ControlPlaneRef: clusterv1.ContractVersionedObjectReference{
				APIGroup: builder.ControlPlaneGroupVersion.Group,
				Kind:     builder.GenericControlPlaneKind,
				Name:     "cp1",
			},
		},
	}

	g.Expect(env.Create(ctx, testCluster)).To(Succeed())
	g.Expect(env.CreateKubeconfigSecret(ctx, testCluster)).To(Succeed())
	// Set InfrastructureReady to true so ClusterCache creates the clusterAccessor.
	testClusterOriginal := client.MergeFrom(testCluster.DeepCopy())
	testCluster.Status.Initialization.InfrastructureProvisioned = ptr.To(true)
	g.Expect(env.Status().Patch(ctx, testCluster, testClusterOriginal)).To(Succeed())

	g.Expect(env.Create(ctx, infraMachine)).To(Succeed())
	g.Expect(env.Create(ctx, defaultBootstrap)).To(Succeed())

	defer func(do ...client.Object) {
		g.Expect(env.Cleanup(ctx, do...)).To(Succeed())
	}(ns, testCluster, defaultBootstrap)

	machine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "machine-created-",
			Namespace:    ns.Name,
			Finalizers:   []string{clusterv1.MachineFinalizer},
		},
		Spec: clusterv1.MachineSpec{
			ClusterName: testCluster.Name,
			InfrastructureRef: clusterv1.ContractVersionedObjectReference{
				APIGroup: clusterv1.GroupVersionInfrastructure.Group,
				Kind:     "GenericInfrastructureMachine",
				Name:     "infra-config1",
			},
			Bootstrap: clusterv1.Bootstrap{
				ConfigRef: clusterv1.ContractVersionedObjectReference{
					APIGroup: clusterv1.GroupVersionBootstrap.Group,
					Kind:     "GenericBootstrapConfig",
					Name:     "bootstrap-config-machinereconcile",
				},
			},
		},
		Status: clusterv1.MachineStatus{
			NodeRef: clusterv1.MachineNodeReference{
				Name: "test",
			},
		},
	}
	g.Expect(env.Create(ctx, machine)).To(Succeed())

	key := client.ObjectKey{Name: machine.Name, Namespace: machine.Namespace}

	// Wait for reconciliation to happen when infra and bootstrap objects are not ready.
	g.Eventually(func() bool {
		if err := env.Get(ctx, key, machine); err != nil {
			return false
		}
		return len(machine.Finalizers) > 0
	}, timeout).Should(BeTrue())

	// Set bootstrap ready.
	bootstrapPatch := client.MergeFrom(defaultBootstrap.DeepCopy())
	g.Expect(unstructured.SetNestedField(defaultBootstrap.Object, true, "status", "initialization", "dataSecretCreated")).ToNot(HaveOccurred())
	g.Expect(env.Status().Patch(ctx, defaultBootstrap, bootstrapPatch)).To(Succeed())

	// Set infrastructure ready.
	infraMachinePatch := client.MergeFrom(infraMachine.DeepCopy())
	g.Expect(unstructured.SetNestedField(infraMachine.Object, true, "status", "initialization", "provisioned")).To(Succeed())
	g.Expect(env.Status().Patch(ctx, infraMachine, infraMachinePatch)).To(Succeed())

	// Wait for Machine Ready Condition to become True.
	g.Eventually(func() bool {
		if err := env.Get(ctx, key, machine); err != nil {
			return false
		}
		if !conditions.Has(machine, clusterv1.MachineInfrastructureReadyCondition) {
			return false
		}
		readyCondition := conditions.Get(machine, clusterv1.MachineInfrastructureReadyCondition)
		return readyCondition.Status == metav1.ConditionTrue
	}, timeout).Should(BeTrue())

	g.Expect(env.Delete(ctx, machine)).ToNot(HaveOccurred())
	// Wait for Machine to be deleted.
	g.Eventually(func() bool {
		if err := env.Get(ctx, key, machine); err != nil {
			if apierrors.IsNotFound(err) {
				return true
			}
		}
		return false
	}, timeout).Should(BeTrue())

	// Check if Machine deletion successfully deleted infrastructure external reference.
	keyInfra := client.ObjectKey{Name: infraMachine.GetName(), Namespace: infraMachine.GetNamespace()}
	g.Eventually(func() bool {
		if err := env.Get(ctx, keyInfra, infraMachine); err != nil {
			if apierrors.IsNotFound(err) {
				return true
			}
		}
		return false
	}, timeout).Should(BeTrue())

	// Check if Machine deletion successfully deleted bootstrap external reference.
	keyBootstrap := client.ObjectKey{Name: defaultBootstrap.GetName(), Namespace: defaultBootstrap.GetNamespace()}
	g.Eventually(func() bool {
		if err := env.Get(ctx, keyBootstrap, defaultBootstrap); err != nil {
			if apierrors.IsNotFound(err) {
				return true
			}
		}
		return false
	}, timeout).Should(BeTrue())
}

func TestMachineFinalizer(t *testing.T) {
	bootstrapData := "some valid data"
	clusterCorrectMeta := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "valid-cluster",
			Namespace: metav1.NamespaceDefault,
		},
	}

	machineValidCluster := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "machine1",
			Namespace: metav1.NamespaceDefault,
		},
		Spec: clusterv1.MachineSpec{
			Bootstrap: clusterv1.Bootstrap{
				DataSecretName: &bootstrapData,
			},
			ClusterName: "valid-cluster",
		},
	}

	machineWithFinalizer := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "machine2",
			Namespace:  metav1.NamespaceDefault,
			Finalizers: []string{"some-other-finalizer"},
		},
		Spec: clusterv1.MachineSpec{
			Bootstrap: clusterv1.Bootstrap{
				DataSecretName: &bootstrapData,
			},
			ClusterName: "valid-cluster",
		},
	}

	testCases := []struct {
		name               string
		request            reconcile.Request
		m                  *clusterv1.Machine
		expectedFinalizers []string
	}{
		{
			name: "should add a machine finalizer to the machine if it doesn't have one",
			request: reconcile.Request{
				NamespacedName: util.ObjectKey(machineValidCluster),
			},
			m:                  machineValidCluster,
			expectedFinalizers: []string{clusterv1.MachineFinalizer},
		},
		{
			name: "should append the machine finalizer to the machine if it already has a finalizer",
			request: reconcile.Request{
				NamespacedName: util.ObjectKey(machineWithFinalizer),
			},
			m:                  machineWithFinalizer,
			expectedFinalizers: []string{"some-other-finalizer", clusterv1.MachineFinalizer},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			c := fake.NewClientBuilder().WithObjects(
				clusterCorrectMeta,
				machineValidCluster,
				machineWithFinalizer,
			).Build()
			mr := &Reconciler{
				Client: c,
			}

			_, _ = mr.Reconcile(ctx, tc.request)

			key := client.ObjectKey{Namespace: tc.m.Namespace, Name: tc.m.Name}
			var actual clusterv1.Machine
			if len(tc.expectedFinalizers) > 0 {
				g.Expect(mr.Client.Get(ctx, key, &actual)).To(Succeed())
				g.Expect(actual.Finalizers).ToNot(BeEmpty())
				g.Expect(actual.Finalizers).To(Equal(tc.expectedFinalizers))
			} else {
				g.Expect(actual.Finalizers).To(BeEmpty())
			}
		})
	}
}

func TestMachineOwnerReference(t *testing.T) {
	bootstrapData := "some valid data"
	testCluster := &clusterv1.Cluster{
		TypeMeta:   metav1.TypeMeta{Kind: "Cluster", APIVersion: clusterv1.GroupVersion.String()},
		ObjectMeta: metav1.ObjectMeta{Namespace: metav1.NamespaceDefault, Name: "test-cluster"},
	}

	machineInvalidCluster := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "machine1",
			Namespace: metav1.NamespaceDefault,
		},
		Spec: clusterv1.MachineSpec{
			ClusterName: "invalid",
		},
	}

	machineValidCluster := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "machine2",
			Namespace: metav1.NamespaceDefault,
		},
		Spec: clusterv1.MachineSpec{
			Bootstrap: clusterv1.Bootstrap{
				DataSecretName: &bootstrapData,
			},
			ClusterName: "test-cluster",
		},
		Status: clusterv1.MachineStatus{
			Conditions: []metav1.Condition{{
				Type:   clusterv1.PausedCondition,
				Status: metav1.ConditionFalse,
				Reason: clusterv1.NotPausedReason,
			}},
		},
	}

	machineValidMachine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "machine3",
			Namespace: metav1.NamespaceDefault,
			Labels: map[string]string{
				clusterv1.ClusterNameLabel: "valid-cluster",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: clusterv1.GroupVersion.String(),
					Kind:       "MachineSet",
					Name:       "valid-machineset",
					Controller: ptr.To(true),
				},
			},
		},
		Spec: clusterv1.MachineSpec{
			Bootstrap: clusterv1.Bootstrap{
				DataSecretName: &bootstrapData,
			},
			ClusterName: "test-cluster",
		},
		Status: clusterv1.MachineStatus{
			Conditions: []metav1.Condition{{
				Type:   clusterv1.PausedCondition,
				Status: metav1.ConditionFalse,
				Reason: clusterv1.NotPausedReason,
			}},
		},
	}

	machineValidControlled := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "machine4",
			Namespace: metav1.NamespaceDefault,
			Labels: map[string]string{
				clusterv1.ClusterNameLabel:         "valid-cluster",
				clusterv1.MachineControlPlaneLabel: "",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "test.group",
					Kind:       "KubeadmControlPlane",
					Name:       "valid-controlplane",
					Controller: ptr.To(true),
				},
			},
		},
		Spec: clusterv1.MachineSpec{
			Bootstrap: clusterv1.Bootstrap{
				DataSecretName: &bootstrapData,
			},
			ClusterName: "test-cluster",
		},
	}

	testCases := []struct {
		name       string
		request    reconcile.Request
		m          *clusterv1.Machine
		expectedOR []metav1.OwnerReference
	}{
		{
			name: "should add owner reference to machine referencing a cluster with correct type meta",
			request: reconcile.Request{
				NamespacedName: util.ObjectKey(machineValidCluster),
			},
			m: machineValidCluster,
			expectedOR: []metav1.OwnerReference{
				{
					APIVersion: testCluster.APIVersion,
					Kind:       testCluster.Kind,
					Name:       testCluster.Name,
					UID:        testCluster.UID,
				},
			},
		},
		{
			name: "should not add cluster owner reference if machine is owned by a machine set",
			request: reconcile.Request{
				NamespacedName: util.ObjectKey(machineValidMachine),
			},
			m: machineValidMachine,
			expectedOR: []metav1.OwnerReference{
				{
					APIVersion: clusterv1.GroupVersion.String(),
					Kind:       "MachineSet",
					Name:       "valid-machineset",
					Controller: ptr.To(true),
				},
			},
		},
		{
			name: "should not add cluster owner reference if machine has a controller owner",
			request: reconcile.Request{
				NamespacedName: util.ObjectKey(machineValidControlled),
			},
			m: machineValidControlled,
			expectedOR: []metav1.OwnerReference{
				{
					APIVersion: "test.group",
					Kind:       "KubeadmControlPlane",
					Name:       "valid-controlplane",
					Controller: ptr.To(true),
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			c := fake.NewClientBuilder().WithObjects(
				testCluster,
				machineInvalidCluster,
				machineValidCluster,
				machineValidMachine,
				machineValidControlled,
			).WithStatusSubresource(&clusterv1.Machine{}).Build()
			mr := &Reconciler{
				Client:       c,
				APIReader:    c,
				ClusterCache: clustercache.NewFakeClusterCache(c, client.ObjectKeyFromObject(testCluster)),
			}

			key := client.ObjectKey{Namespace: tc.m.Namespace, Name: tc.m.Name}
			var actual clusterv1.Machine

			// this first requeue is to add finalizer
			result, err := mr.Reconcile(ctx, tc.request)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(BeComparableTo(ctrl.Result{}))
			g.Expect(mr.Client.Get(ctx, key, &actual)).To(Succeed())
			g.Expect(actual.Finalizers).To(ContainElement(clusterv1.MachineFinalizer))

			_, _ = mr.Reconcile(ctx, tc.request)

			if len(tc.expectedOR) > 0 {
				g.Expect(mr.Client.Get(ctx, key, &actual)).To(Succeed())
				g.Expect(actual.OwnerReferences).To(BeComparableTo(tc.expectedOR))
			} else {
				g.Expect(actual.OwnerReferences).To(BeEmpty())
			}
		})
	}
}

func TestReconcileRequest(t *testing.T) {
	infraConfig := unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind":       "GenericInfrastructureMachine",
			"apiVersion": clusterv1.GroupVersionInfrastructure.String(),
			"metadata": map[string]interface{}{
				"name":      "infra-config1",
				"namespace": metav1.NamespaceDefault,
			},
			"spec": map[string]interface{}{
				"providerID": "test://id-1",
			},
			"status": map[string]interface{}{
				"ready": true,
				"addresses": []interface{}{
					map[string]interface{}{
						"type":    "InternalIP",
						"address": "10.0.0.1",
					},
				},
			},
		},
	}

	testCluster := clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: metav1.NamespaceDefault,
		},
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
		Spec: corev1.NodeSpec{ProviderID: "test://id-1"},
	}

	type expected struct {
		result reconcile.Result
		err    bool
	}
	testCases := []struct {
		name     string
		machine  clusterv1.Machine
		expected expected
	}{
		{
			name: "Machine should be created",
			machine: clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "created",
					Namespace:  metav1.NamespaceDefault,
					Finalizers: []string{clusterv1.MachineFinalizer},
				},
				Spec: clusterv1.MachineSpec{
					ClusterName: "test-cluster",
					InfrastructureRef: clusterv1.ContractVersionedObjectReference{
						APIGroup: clusterv1.GroupVersionInfrastructure.Group,
						Kind:     "GenericInfrastructureMachine",
						Name:     "infra-config1",
					},
					Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
				},
				Status: clusterv1.MachineStatus{
					NodeRef: clusterv1.MachineNodeReference{
						Name: "test",
					},
					ObservedGeneration: 1,
				},
			},
			expected: expected{
				result: reconcile.Result{},
				err:    false,
			},
		},
		{
			name: "Machine should be updated",
			machine: clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "updated",
					Namespace:  metav1.NamespaceDefault,
					Finalizers: []string{clusterv1.MachineFinalizer},
				},
				Spec: clusterv1.MachineSpec{
					ClusterName: "test-cluster",
					InfrastructureRef: clusterv1.ContractVersionedObjectReference{
						APIGroup: clusterv1.GroupVersionInfrastructure.Group,
						Kind:     "GenericInfrastructureMachine",
						Name:     "infra-config1",
					},
					Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
				},
				Status: clusterv1.MachineStatus{
					NodeRef: clusterv1.MachineNodeReference{
						Name: "test",
					},
					ObservedGeneration: 1,
				},
			},
			expected: expected{
				result: reconcile.Result{},
				err:    false,
			},
		},
		{
			name: "Machine should be deleted",
			machine: clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deleted",
					Namespace: metav1.NamespaceDefault,
					Labels: map[string]string{
						clusterv1.MachineControlPlaneLabel: "",
					},
					Finalizers:        []string{clusterv1.MachineFinalizer},
					DeletionTimestamp: ptr.To(metav1.Now()),
				},
				Spec: clusterv1.MachineSpec{
					ClusterName: "test-cluster",
					InfrastructureRef: clusterv1.ContractVersionedObjectReference{
						APIGroup: clusterv1.GroupVersionInfrastructure.Group,
						Kind:     "GenericInfrastructureMachine",
						Name:     "infra-config1",
					},
					Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
				},
			},
			expected: expected{
				result: reconcile.Result{},
				err:    false,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			clientFake := fake.NewClientBuilder().WithObjects(
				node,
				&testCluster,
				&tc.machine,
				builder.GenericInfrastructureMachineCRD.DeepCopy(),
				&infraConfig,
			).WithStatusSubresource(&clusterv1.Machine{}).WithIndex(&corev1.Node{}, index.NodeProviderIDField, index.NodeByProviderID).Build()

			r := &Reconciler{
				Client:               clientFake,
				ClusterCache:         clustercache.NewFakeClusterCache(clientFake, client.ObjectKey{Name: testCluster.Name, Namespace: testCluster.Namespace}),
				recorder:             record.NewFakeRecorder(10),
				reconcileDeleteCache: cache.New[cache.ReconcileEntry](cache.DefaultTTL),
				externalTracker: external.ObjectTracker{
					Controller:      externalfake.Controller{},
					Cache:           &informertest.FakeInformers{},
					Scheme:          clientFake.Scheme(),
					PredicateLogger: ptr.To(logr.New(log.NullLogSink{})),
				},
			}

			result, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: util.ObjectKey(&tc.machine)})
			if tc.expected.err {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}

			g.Expect(result).To(BeComparableTo(tc.expected.result))
		})
	}
}

func TestMachineV1Beta1Conditions(t *testing.T) {
	infraConfig := func(provisioned bool) *unstructured.Unstructured {
		return &unstructured.Unstructured{
			Object: map[string]interface{}{
				"kind":       "GenericInfrastructureMachine",
				"apiVersion": clusterv1.GroupVersionInfrastructure.String(),
				"metadata": map[string]interface{}{
					"name":      "infra-config1",
					"namespace": metav1.NamespaceDefault,
				},
				"spec": map[string]interface{}{
					"providerID": "test://id-1",
				},
				"status": map[string]interface{}{
					"initialization": map[string]interface{}{
						"provisioned": provisioned,
					},
					"addresses": []interface{}{
						map[string]interface{}{
							"type":    "InternalIP",
							"address": "10.0.0.1",
						},
					},
				},
			},
		}
	}

	boostrapConfig := func(dataSecretCreated bool) *unstructured.Unstructured {
		status := map[string]interface{}{
			"initialization": map[string]interface{}{
				"dataSecretCreated": dataSecretCreated,
			},
		}
		if dataSecretCreated {
			status["dataSecretName"] = "data"
		}
		return &unstructured.Unstructured{
			Object: map[string]interface{}{
				"kind":       "GenericBootstrapConfig",
				"apiVersion": clusterv1.GroupVersionBootstrap.String(),
				"metadata": map[string]interface{}{
					"name":      "bootstrap-config1",
					"namespace": metav1.NamespaceDefault,
				},
				"status": status,
			},
		}
	}

	testCluster := clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: metav1.NamespaceDefault,
		},
		Status: clusterv1.ClusterStatus{
			Initialization: clusterv1.ClusterInitializationStatus{
				InfrastructureProvisioned: ptr.To(true),
			},
			Conditions: []metav1.Condition{
				{
					Type:   clusterv1.ClusterControlPlaneInitializedCondition,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	machine := clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "blah",
			Namespace: metav1.NamespaceDefault,
			Labels: map[string]string{
				clusterv1.MachineControlPlaneLabel: "",
			},
			Finalizers: []string{clusterv1.MachineFinalizer},
		},
		Spec: clusterv1.MachineSpec{
			ProviderID:  "test://id-1",
			ClusterName: "test-cluster",
			InfrastructureRef: clusterv1.ContractVersionedObjectReference{
				APIGroup: clusterv1.GroupVersionInfrastructure.Group,
				Kind:     "GenericInfrastructureMachine",
				Name:     "infra-config1",
			},
			Bootstrap: clusterv1.Bootstrap{
				ConfigRef: clusterv1.ContractVersionedObjectReference{
					APIGroup: clusterv1.GroupVersionBootstrap.Group,
					Kind:     "GenericBootstrapConfig",
					Name:     "bootstrap-config1",
				},
			},
		},
		Status: clusterv1.MachineStatus{
			NodeRef: clusterv1.MachineNodeReference{
				Name: "test",
			},
			ObservedGeneration: 1,
			Conditions: []metav1.Condition{{
				Type:   clusterv1.PausedCondition,
				Status: metav1.ConditionFalse,
				Reason: clusterv1.NotPausedReason,
			}},
		},
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
		Spec: corev1.NodeSpec{ProviderID: "test://id-1"},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
				{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionFalse},
				{Type: corev1.NodeDiskPressure, Status: corev1.ConditionFalse},
				{Type: corev1.NodePIDPressure, Status: corev1.ConditionFalse},
			},
		},
	}

	testcases := []struct {
		name                       string
		infraProvisioned           bool
		bootstrapDataSecretCreated bool
		beforeFunc                 func(bootstrap, infra *unstructured.Unstructured, m *clusterv1.Machine)
		additionalObjects          []client.Object
		conditionsToAssert         []metav1.Condition
		v1beta1ConditionsToAssert  []*clusterv1.Condition
		wantErr                    bool
	}{
		{
			name:                       "all conditions true",
			infraProvisioned:           true,
			bootstrapDataSecretCreated: true,
			beforeFunc: func(_, _ *unstructured.Unstructured, m *clusterv1.Machine) {
				// since these conditions are set by an external controller
				v1beta1conditions.MarkTrue(m, clusterv1.MachineHealthCheckSucceededV1Beta1Condition)
				v1beta1conditions.MarkTrue(m, clusterv1.MachineOwnerRemediatedV1Beta1Condition)
			},
			conditionsToAssert: []metav1.Condition{
				{Type: clusterv1.MachineReadyCondition, Status: metav1.ConditionTrue, Reason: clusterv1.MachineReadyReason, Message: ""},
				{Type: clusterv1.MachineBootstrapConfigReadyCondition, Status: metav1.ConditionTrue, Reason: clusterv1.MachineBootstrapConfigReadyReason, Message: ""},
				{Type: clusterv1.MachineInfrastructureReadyCondition, Status: metav1.ConditionTrue, Reason: clusterv1.MachineInfrastructureReadyReason, Message: ""},
			},
			v1beta1ConditionsToAssert: []*clusterv1.Condition{
				v1beta1conditions.TrueCondition(clusterv1.InfrastructureReadyV1Beta1Condition),
				v1beta1conditions.TrueCondition(clusterv1.BootstrapReadyV1Beta1Condition),
				v1beta1conditions.TrueCondition(clusterv1.MachineOwnerRemediatedV1Beta1Condition),
				v1beta1conditions.TrueCondition(clusterv1.MachineHealthCheckSucceededV1Beta1Condition),
				v1beta1conditions.TrueCondition(clusterv1.ReadyV1Beta1Condition),
			},
		},
		{
			name:                       "infra condition consumes reason from the infra config",
			infraProvisioned:           false,
			bootstrapDataSecretCreated: true,
			beforeFunc: func(_, infra *unstructured.Unstructured, _ *clusterv1.Machine) {
				addConditionToExternal(infra, metav1.Condition{
					Type:   contract.InfrastructureMachine().ReadyConditionType(),
					Status: metav1.ConditionFalse,
					Reason: "Custom reason",
				})
			},
			conditionsToAssert: []metav1.Condition{
				{Type: clusterv1.MachineInfrastructureReadyCondition, Status: metav1.ConditionFalse, Reason: "Custom reason", Message: ""},
			},
			v1beta1ConditionsToAssert: []*clusterv1.Condition{
				v1beta1conditions.FalseCondition(clusterv1.InfrastructureReadyV1Beta1Condition, "Custom reason", "", ""),
			},
		},
		{
			name:                       "infra condition consumes the fallback reason",
			infraProvisioned:           false,
			bootstrapDataSecretCreated: true,
			conditionsToAssert: []metav1.Condition{
				{Type: clusterv1.MachineInfrastructureReadyCondition, Status: metav1.ConditionFalse, Reason: clusterv1.MachineInfrastructureNotReadyReason, Message: "GenericInfrastructureMachine status.initialization.provisioned is false"},
				{Type: clusterv1.MachineReadyCondition, Status: metav1.ConditionFalse, Reason: clusterv1.MachineNotReadyReason, Message: "* InfrastructureReady: GenericInfrastructureMachine status.initialization.provisioned is false"},
			},
			v1beta1ConditionsToAssert: []*clusterv1.Condition{
				v1beta1conditions.FalseCondition(clusterv1.InfrastructureReadyV1Beta1Condition, clusterv1.WaitingForInfrastructureFallbackV1Beta1Reason, clusterv1.ConditionSeverityInfo, ""),
				v1beta1conditions.FalseCondition(clusterv1.ReadyV1Beta1Condition, clusterv1.WaitingForInfrastructureFallbackV1Beta1Reason, clusterv1.ConditionSeverityInfo, ""),
			},
		},
		{
			name:                       "bootstrap condition consumes reason from the bootstrap config",
			infraProvisioned:           true,
			bootstrapDataSecretCreated: false,
			beforeFunc: func(bootstrap, _ *unstructured.Unstructured, _ *clusterv1.Machine) {
				addConditionToExternal(bootstrap, metav1.Condition{
					Type:   contract.Bootstrap().ReadyConditionType(),
					Status: metav1.ConditionFalse,
					Reason: "Custom reason",
				})
			},
			conditionsToAssert: []metav1.Condition{
				{Type: clusterv1.MachineBootstrapConfigReadyCondition, Status: metav1.ConditionFalse, Reason: "Custom reason", Message: ""},
			},
			v1beta1ConditionsToAssert: []*clusterv1.Condition{
				v1beta1conditions.FalseCondition(clusterv1.BootstrapReadyV1Beta1Condition, "Custom reason", "", ""),
			},
		},
		{
			name:                       "bootstrap condition consumes the fallback reason",
			infraProvisioned:           true,
			bootstrapDataSecretCreated: false,
			conditionsToAssert: []metav1.Condition{
				{Type: clusterv1.MachineBootstrapConfigReadyCondition, Status: metav1.ConditionFalse, Reason: clusterv1.MachineBootstrapConfigNotReadyReason, Message: "GenericBootstrapConfig status.initialization.dataSecretCreated is false"},
				{Type: clusterv1.MachineReadyCondition, Status: metav1.ConditionFalse, Reason: clusterv1.MachineNotReadyReason, Message: "* BootstrapConfigReady: GenericBootstrapConfig status.initialization.dataSecretCreated is false"},
			},
			v1beta1ConditionsToAssert: []*clusterv1.Condition{
				v1beta1conditions.FalseCondition(clusterv1.BootstrapReadyV1Beta1Condition, clusterv1.WaitingForDataSecretFallbackV1Beta1Reason, clusterv1.ConditionSeverityInfo, ""),
				v1beta1conditions.FalseCondition(clusterv1.ReadyV1Beta1Condition, clusterv1.WaitingForDataSecretFallbackV1Beta1Reason, clusterv1.ConditionSeverityInfo, ""),
			},
		},
		// Assert summary conditions
		// infra condition takes precedence over bootstrap condition in generating summary
		{
			name:                       "ready condition summary use a generic reason in case of multiple issues",
			infraProvisioned:           false,
			bootstrapDataSecretCreated: false,
			conditionsToAssert: []metav1.Condition{
				{Type: clusterv1.MachineReadyCondition, Status: metav1.ConditionFalse, Reason: clusterv1.MachineNotReadyReason, Message: "* BootstrapConfigReady: GenericBootstrapConfig status.initialization.dataSecretCreated is false\n* InfrastructureReady: GenericInfrastructureMachine status.initialization.provisioned is false"},
			},
			v1beta1ConditionsToAssert: []*clusterv1.Condition{
				// in V1beta1 ready condition summary consumes reason from the infra condition
				v1beta1conditions.FalseCondition(clusterv1.ReadyV1Beta1Condition, clusterv1.WaitingForInfrastructureFallbackV1Beta1Reason, clusterv1.ConditionSeverityInfo, ""),
			},
		},
		{
			name:                       "ready condition summary consumes reason from the machine owner remediated condition",
			infraProvisioned:           true,
			bootstrapDataSecretCreated: true,
			beforeFunc: func(_, _ *unstructured.Unstructured, m *clusterv1.Machine) {
				v1beta1conditions.MarkFalse(m, clusterv1.MachineOwnerRemediatedV1Beta1Condition, clusterv1.WaitingForRemediationV1Beta1Reason, clusterv1.ConditionSeverityWarning, "MHC failed")
			},
			v1beta1ConditionsToAssert: []*clusterv1.Condition{
				v1beta1conditions.FalseCondition(clusterv1.ReadyV1Beta1Condition, clusterv1.WaitingForRemediationV1Beta1Reason, clusterv1.ConditionSeverityWarning, "MHC failed"),
			},
		},
		{
			name:                       "ready condition summary consumes reason from the MHC succeeded condition",
			infraProvisioned:           true,
			bootstrapDataSecretCreated: true,
			beforeFunc: func(_, _ *unstructured.Unstructured, m *clusterv1.Machine) {
				v1beta1conditions.MarkFalse(m, clusterv1.MachineHealthCheckSucceededV1Beta1Condition, clusterv1.NodeNotFoundV1Beta1Reason, clusterv1.ConditionSeverityWarning, "")
			},
			v1beta1ConditionsToAssert: []*clusterv1.Condition{
				v1beta1conditions.FalseCondition(clusterv1.ReadyV1Beta1Condition, clusterv1.NodeNotFoundV1Beta1Reason, clusterv1.ConditionSeverityWarning, ""),
			},
		},
		{
			name:                       "machine ready and MachineNodeHealthy unknown",
			infraProvisioned:           true,
			bootstrapDataSecretCreated: true,
			additionalObjects: []client.Object{&corev1.Node{
				// This is a duplicate node with the same providerID
				// This should lead to an error when trying to get the Node for a Machine.
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-duplicate",
				},
				Spec: corev1.NodeSpec{ProviderID: "test://id-1"},
			}},
			wantErr: true,
			conditionsToAssert: []metav1.Condition{
				{Type: clusterv1.MachineReadyCondition, Status: metav1.ConditionUnknown, Reason: clusterv1.MachineReadyUnknownReason, Message: "* NodeHealthy: Please check controller logs for errors"},
				{Type: clusterv1.MachineBootstrapConfigReadyCondition, Status: metav1.ConditionTrue, Reason: clusterv1.MachineBootstrapConfigReadyReason, Message: ""},
				{Type: clusterv1.MachineInfrastructureReadyCondition, Status: metav1.ConditionTrue, Reason: clusterv1.MachineInfrastructureReadyReason, Message: ""},
				{Type: clusterv1.MachineNodeHealthyCondition, Status: metav1.ConditionUnknown, Reason: clusterv1.MachineNodeInternalErrorReason, Message: "Please check controller logs for errors"},
			},
			v1beta1ConditionsToAssert: []*clusterv1.Condition{
				v1beta1conditions.TrueCondition(clusterv1.InfrastructureReadyV1Beta1Condition),
				v1beta1conditions.TrueCondition(clusterv1.BootstrapReadyV1Beta1Condition),
				v1beta1conditions.TrueCondition(clusterv1.ReadyV1Beta1Condition),
				v1beta1conditions.UnknownCondition(clusterv1.MachineNodeHealthyV1Beta1Condition, clusterv1.NodeInspectionFailedV1Beta1Reason, "Failed to get the Node for this Machine by ProviderID"),
			},
		},
		{
			name:                       "ready condition summary consumes reason from the draining succeeded condition",
			infraProvisioned:           true,
			bootstrapDataSecretCreated: true,
			beforeFunc: func(_, _ *unstructured.Unstructured, m *clusterv1.Machine) {
				v1beta1conditions.MarkFalse(m, clusterv1.DrainingSucceededV1Beta1Condition, clusterv1.DrainingFailedV1Beta1Reason, clusterv1.ConditionSeverityWarning, "")
			},
			// conditionsToAssert: in v1beta1  clusterv1.DrainingSucceededV1Beta1Condition has been merged into Deleting.
			v1beta1ConditionsToAssert: []*clusterv1.Condition{
				v1beta1conditions.FalseCondition(clusterv1.ReadyV1Beta1Condition, clusterv1.DrainingFailedV1Beta1Reason, clusterv1.ConditionSeverityWarning, ""),
			},
		},
	}

	for _, tt := range testcases {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			// setup objects
			bootstrap := boostrapConfig(tt.bootstrapDataSecretCreated)
			infra := infraConfig(tt.infraProvisioned)
			m := machine.DeepCopy()
			if tt.beforeFunc != nil {
				tt.beforeFunc(bootstrap, infra, m)
			}

			objs := []client.Object{
				&testCluster, m, node,
				builder.GenericInfrastructureMachineCRD.DeepCopy(), infra,
				builder.GenericBootstrapConfigCRD.DeepCopy(), bootstrap,
			}
			objs = append(objs, tt.additionalObjects...)

			clientFake := fake.NewClientBuilder().WithObjects(objs...).
				WithIndex(&corev1.Node{}, index.NodeProviderIDField, index.NodeByProviderID).
				WithStatusSubresource(&clusterv1.Machine{}).
				Build()

			r := &Reconciler{
				Client:       clientFake,
				recorder:     record.NewFakeRecorder(10),
				ClusterCache: clustercache.NewFakeClusterCache(clientFake, client.ObjectKey{Name: testCluster.Name, Namespace: testCluster.Namespace}),
				externalTracker: external.ObjectTracker{
					Controller:      externalfake.Controller{},
					Cache:           &informertest.FakeInformers{},
					Scheme:          clientFake.Scheme(),
					PredicateLogger: ptr.To(logr.New(log.NullLogSink{})),
				},
				controller:                  &fakeController{},
				predicateLog:                ptr.To(logr.New(log.NullLogSink{})),
				RemoteConditionsGracePeriod: 50 * time.Second,
			}

			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: util.ObjectKey(&machine)})
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}

			m = &clusterv1.Machine{}
			g.Expect(r.Client.Get(ctx, client.ObjectKeyFromObject(&machine), m)).ToNot(HaveOccurred())

			assertConditions(t, m, tt.conditionsToAssert...)
			assertV1Beta1Conditions(t, m, tt.v1beta1ConditionsToAssert...)
		})
	}
}

type fakeController struct{}

func (f fakeController) Reconcile(_ context.Context, _ reconcile.Request) (reconcile.Result, error) {
	panic("implement me")
}

func (f fakeController) Watch(_ source.TypedSource[reconcile.Request]) error {
	return nil
}

func (f fakeController) Start(_ context.Context) error {
	panic("implement me")
}

func (f fakeController) GetLogger() logr.Logger {
	panic("implement me")
}

func TestRemoveMachineFinalizerAfterDeleteReconcile(t *testing.T) {
	g := NewWithT(t)

	dt := metav1.Now()

	testCluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Namespace: metav1.NamespaceDefault, Name: "test-cluster"},
	}

	m := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "delete123",
			Namespace:         metav1.NamespaceDefault,
			Finalizers:        []string{clusterv1.MachineFinalizer, "test"},
			DeletionTimestamp: &dt,
		},
		Spec: clusterv1.MachineSpec{
			ClusterName: "test-cluster",
			InfrastructureRef: clusterv1.ContractVersionedObjectReference{
				APIGroup: clusterv1.GroupVersionInfrastructure.Group,
				Kind:     "GenericInfrastructureMachine",
				Name:     "infra-config1",
			},
			Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
		},
		Status: clusterv1.MachineStatus{
			Conditions: []metav1.Condition{{
				Type:   clusterv1.PausedCondition,
				Status: metav1.ConditionFalse,
				Reason: clusterv1.NotPausedReason,
			}},
		},
	}
	key := client.ObjectKey{Namespace: m.Namespace, Name: m.Name}
	c := fake.NewClientBuilder().WithObjects(testCluster, m, builder.GenericInfrastructureMachineCRD.DeepCopy()).WithStatusSubresource(&clusterv1.Machine{}).Build()
	mr := &Reconciler{
		Client:               c,
		ClusterCache:         clustercache.NewFakeClusterCache(c, client.ObjectKeyFromObject(testCluster)),
		reconcileDeleteCache: cache.New[cache.ReconcileEntry](cache.DefaultTTL),
	}
	_, err := mr.Reconcile(ctx, reconcile.Request{NamespacedName: key})
	g.Expect(err).ToNot(HaveOccurred())

	var actual clusterv1.Machine
	g.Expect(mr.Client.Get(ctx, key, &actual)).To(Succeed())
	g.Expect(actual.ObjectMeta.Finalizers).To(Equal([]string{"test"}))
}

func TestIsNodeDrainedAllowed(t *testing.T) {
	testCluster := &clusterv1.Cluster{
		TypeMeta:   metav1.TypeMeta{Kind: "Cluster", APIVersion: clusterv1.GroupVersion.String()},
		ObjectMeta: metav1.ObjectMeta{Namespace: metav1.NamespaceDefault, Name: "test-cluster"},
	}

	tests := []struct {
		name     string
		machine  *clusterv1.Machine
		expected bool
	}{
		{
			name: "Exclude node draining annotation exists",
			machine: &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-machine",
					Namespace:   metav1.NamespaceDefault,
					Finalizers:  []string{clusterv1.MachineFinalizer},
					Annotations: map[string]string{clusterv1.ExcludeNodeDrainingAnnotation: "existed!!"},
				},
				Spec: clusterv1.MachineSpec{
					ClusterName: "test-cluster",
					// InfrastructureRef is not defined
					Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
				},
				Status: clusterv1.MachineStatus{},
			},
			expected: false,
		},
		{
			name: "KCP machine with the pre terminate hook should drain",
			machine: &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-machine",
					Namespace:   metav1.NamespaceDefault,
					Labels:      map[string]string{clusterv1.MachineControlPlaneLabel: ""},
					Annotations: map[string]string{KubeadmControlPlanePreTerminateHookCleanupAnnotation: ""},
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: clusterv1.GroupVersionControlPlane.String(),
							Kind:       "KubeadmControlPlane",
							Name:       "Foo",
						},
					},
				},
				Spec: clusterv1.MachineSpec{
					ClusterName: "test-cluster",
					// InfrastructureRef is not defined
					Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
				},
				Status: clusterv1.MachineStatus{},
			},
			expected: true,
		},
		{
			name: "KCP machine without the pre terminate hook should stop draining",
			machine: &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: metav1.NamespaceDefault,
					Labels:    map[string]string{clusterv1.MachineControlPlaneLabel: ""},
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: clusterv1.GroupVersionControlPlane.String(),
							Kind:       "KubeadmControlPlane",
							Name:       "Foo",
						},
					},
				},
				Spec: clusterv1.MachineSpec{
					ClusterName: "test-cluster",
					// InfrastructureRef is not defined
					Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
				},
				Status: clusterv1.MachineStatus{},
			},
			expected: false,
		},
		{
			name: "Node draining timeout is over",
			machine: &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-machine",
					Namespace:  metav1.NamespaceDefault,
					Finalizers: []string{clusterv1.MachineFinalizer},
				},
				Spec: clusterv1.MachineSpec{
					ClusterName: "test-cluster",
					// InfrastructureRef is not defined
					Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
					Deletion: clusterv1.MachineDeletionSpec{
						NodeDrainTimeoutSeconds: ptr.To(int32(60)),
					},
				},

				Status: clusterv1.MachineStatus{
					Deletion: &clusterv1.MachineDeletionStatus{
						NodeDrainStartTime: metav1.Time{Time: time.Now().Add(-(time.Second * 70)).UTC()},
					},
				},
			},
			expected: false,
		},
		{
			name: "Node draining timeout is not yet over",
			machine: &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-machine",
					Namespace:  metav1.NamespaceDefault,
					Finalizers: []string{clusterv1.MachineFinalizer},
				},
				Spec: clusterv1.MachineSpec{
					ClusterName: "test-cluster",
					// InfrastructureRef is not defined
					Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
					Deletion: clusterv1.MachineDeletionSpec{
						NodeDrainTimeoutSeconds: ptr.To(int32(60)),
					},
				},
				Status: clusterv1.MachineStatus{
					Deletion: &clusterv1.MachineDeletionStatus{
						NodeDrainStartTime: metav1.Time{Time: time.Now().Add(-(time.Second * 30)).UTC()},
					},
				},
			},
			expected: true,
		},
		{
			name: "NodeDrainTimeoutSeconds option is set to its default value 0",
			machine: &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-machine",
					Namespace:  metav1.NamespaceDefault,
					Finalizers: []string{clusterv1.MachineFinalizer},
				},
				Spec: clusterv1.MachineSpec{
					ClusterName: "test-cluster",
					// InfrastructureRef is not defined
					Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
				},
				Status: clusterv1.MachineStatus{
					Deletion: &clusterv1.MachineDeletionStatus{
						NodeDrainStartTime: metav1.Time{Time: time.Now().Add(-(time.Second * 1000)).UTC()},
					},
				},
			},
			expected: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			var objs []client.Object
			objs = append(objs, testCluster, tt.machine)

			c := fake.NewClientBuilder().WithObjects(objs...).Build()
			r := &Reconciler{
				Client: c,
			}

			got := r.isNodeDrainAllowed(tt.machine)
			g.Expect(got).To(Equal(tt.expected))
		})
	}
}

func TestDrainNode(t *testing.T) {
	g := NewWithT(t)

	testCluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: metav1.NamespaceDefault,
			Name:      "test-cluster",
		},
	}
	testMachine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: metav1.NamespaceDefault,
			Name:      "test-machine",
		},
	}
	nodeDrainStartTime, err := time.Parse(time.RFC3339, "2024-10-09T16:13:59Z")
	g.Expect(err).ToNot(HaveOccurred())

	tests := []struct {
		name                 string
		nodeName             string
		node                 *corev1.Node
		pods                 []*corev1.Pod
		nodeDrainStartTime   metav1.Time
		wantV1Beta1Condition *clusterv1.Condition
		wantResult           ctrl.Result
		wantErr              string
		wantDeletingReason   string
		wantDeletingMessage  string
	}{
		{
			name:     "Node does not exist, no-op",
			nodeName: "node-does-not-exist",
		},
		{
			name:     "Node does exist, should be cordoned",
			nodeName: "node-1",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
				},
			},
		},
		{
			name:     "Node does exist, should stay cordoned",
			nodeName: "node-1",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
				},
				Spec: corev1.NodeSpec{
					Unschedulable: true,
				},
			},
		},
		{
			name:     "Node does exist, only Pods that don't have to be drained",
			nodeName: "node-1",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
				},
				Spec: corev1.NodeSpec{
					Unschedulable: true,
				},
			},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pod-1-skip-mirror-pod",
						Namespace: "test-namespace",
						Annotations: map[string]string{
							corev1.MirrorPodAnnotationKey: "some-value",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pod-4-skip-daemonset-pod",
						Namespace: "test-namespace",
						OwnerReferences: []metav1.OwnerReference{
							{
								Kind:       "DaemonSet",
								Name:       "daemonset-does-exist",
								Controller: ptr.To(true),
							},
						},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
					},
				},
			},
		},
		{
			name:     "Node does exist, some Pods have to be drained",
			nodeName: "node-1",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
				},
				Spec: corev1.NodeSpec{
					Unschedulable: true,
				},
			},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pod-1-skip-mirror-pod",
						Namespace: "test-namespace",
						Annotations: map[string]string{
							corev1.MirrorPodAnnotationKey: "some-value",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pod-2-delete-running-deployment-pod",
						Namespace: "test-namespace",
						OwnerReferences: []metav1.OwnerReference{
							{
								Kind:       "Deployment",
								Controller: ptr.To(true),
							},
						},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
					},
				},
			},
			nodeDrainStartTime: metav1.Time{Time: nodeDrainStartTime},
			wantResult:         ctrl.Result{RequeueAfter: 20 * time.Second},
			wantV1Beta1Condition: &clusterv1.Condition{
				Type:     clusterv1.DrainingSucceededV1Beta1Condition,
				Status:   corev1.ConditionFalse,
				Severity: clusterv1.ConditionSeverityInfo,
				Reason:   clusterv1.DrainingV1Beta1Reason,
				Message: `Drain not completed yet (started at 2024-10-09T16:13:59Z):
* Pod test-namespace/pod-2-delete-running-deployment-pod: deletionTimestamp set, but still not removed from the Node`,
			},
			wantDeletingReason: clusterv1.MachineDeletingDrainingNodeReason,
			wantDeletingMessage: `Drain not completed yet (started at 2024-10-09T16:13:59Z):
* Pod test-namespace/pod-2-delete-running-deployment-pod: deletionTimestamp set, but still not removed from the Node`,
		},
		{
			name:     "Node does exist but is unreachable, no Pods have to be drained because they all have old deletionTimestamps",
			nodeName: "node-1",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
				},
				Spec: corev1.NodeSpec{
					Unschedulable: true,
				},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{ // unreachable.
						{
							Type:   corev1.NodeReady,
							Status: corev1.ConditionUnknown,
						},
					},
				},
			},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "pod-1-skip-pod-old-deletionTimestamp",
						Namespace:         "test-namespace",
						DeletionTimestamp: &metav1.Time{Time: time.Now().Add(time.Duration(1) * time.Hour * -1)},
						Finalizers:        []string{"block-deletion"},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			// Setting NodeName here to avoid noise in the table above.
			for i := range tt.pods {
				tt.pods[i].Spec.NodeName = tt.nodeName
			}

			// Making a copy because drainNode will modify the Machine.
			testMachine := testMachine.DeepCopy()

			var objs []client.Object
			objs = append(objs, testCluster, testMachine)
			c := fake.NewClientBuilder().
				WithObjects(objs...).
				Build()

			var remoteObjs []client.Object
			if tt.node != nil {
				remoteObjs = append(remoteObjs, tt.node)
			}
			for _, p := range tt.pods {
				remoteObjs = append(remoteObjs, p)
			}
			remoteObjs = append(remoteObjs, &appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "daemonset-does-exist",
					Namespace: "test-namespace",
				},
			}, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-namespace",
					Labels: map[string]string{
						"kubernetes.io/metadata.name": "test-namespace",
					},
				},
			})
			remoteClient := fake.NewClientBuilder().
				WithIndex(&corev1.Pod{}, "spec.nodeName", podByNodeName).
				WithObjects(remoteObjs...).
				Build()

			r := &Reconciler{
				Client:               c,
				ClusterCache:         clustercache.NewFakeClusterCache(remoteClient, client.ObjectKeyFromObject(testCluster)),
				reconcileDeleteCache: cache.New[cache.ReconcileEntry](cache.DefaultTTL),
			}

			testMachine.Status.NodeRef = clusterv1.MachineNodeReference{
				Name: tt.nodeName,
			}
			if !tt.nodeDrainStartTime.IsZero() {
				testMachine.Status.Deletion = &clusterv1.MachineDeletionStatus{
					NodeDrainStartTime: tt.nodeDrainStartTime,
				}
			}

			s := &scope{
				cluster: testCluster,
				machine: testMachine,
			}

			res, err := r.drainNode(ctx, s)
			g.Expect(res).To(BeComparableTo(tt.wantResult))
			if tt.wantErr == "" {
				g.Expect(err).ToNot(HaveOccurred())
			} else {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(BeComparableTo(tt.wantErr))
			}

			gotCondition := v1beta1conditions.Get(testMachine, clusterv1.DrainingSucceededV1Beta1Condition)
			if tt.wantV1Beta1Condition == nil {
				g.Expect(gotCondition).To(BeNil())
			} else {
				g.Expect(gotCondition).ToNot(BeNil())
				// Cleanup for easier comparison
				gotCondition.LastTransitionTime = metav1.Time{}
				g.Expect(gotCondition).To(BeComparableTo(tt.wantV1Beta1Condition))
			}

			g.Expect(s.deletingReason).To(Equal(tt.wantDeletingReason))
			g.Expect(s.deletingMessage).To(Equal(tt.wantDeletingMessage))

			// If there is a Node it should be cordoned.
			if tt.node != nil {
				gotNode := &corev1.Node{}
				g.Expect(remoteClient.Get(ctx, client.ObjectKeyFromObject(tt.node), gotNode)).To(Succeed())
				g.Expect(gotNode.Spec.Unschedulable).To(BeTrue())
			}
		})
	}
}

func TestDrainNode_withCaching(t *testing.T) {
	g := NewWithT(t)

	testCluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: metav1.NamespaceDefault,
			Name:      "test-cluster",
		},
	}
	nodeDrainStartTime, err := time.Parse(time.RFC3339, "2024-10-09T16:13:59Z")
	g.Expect(err).ToNot(HaveOccurred())
	testMachine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: metav1.NamespaceDefault,
			Name:      "test-machine",
		},
		Status: clusterv1.MachineStatus{
			NodeRef: clusterv1.MachineNodeReference{
				Name: "node-1",
			},
			Deletion: &clusterv1.MachineDeletionStatus{
				NodeDrainStartTime: metav1.Time{Time: nodeDrainStartTime},
			},
		},
	}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-1",
		},
		Spec: corev1.NodeSpec{
			Unschedulable: true,
		},
	}

	pods := []*corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pod-delete-running-deployment-pod",
				Namespace: "test-namespace",
				Finalizers: []string{
					// Add a finalizer so the Pod doesn't go away after eviction.
					"cluster.x-k8s.io/block",
				},
				OwnerReferences: []metav1.OwnerReference{
					{
						Kind:       "Deployment",
						Controller: ptr.To(true),
					},
				},
			},
			Spec: corev1.PodSpec{
				NodeName: "node-1",
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
			},
		},
	}
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-namespace",
			Labels: map[string]string{
				"kubernetes.io/metadata.name": "test-namespace",
			},
		},
	}

	var objs []client.Object
	objs = append(objs, testCluster, testMachine)
	c := fake.NewClientBuilder().
		WithObjects(objs...).
		Build()

	remoteObjs := []client.Object{node, ns}
	for _, p := range pods {
		remoteObjs = append(remoteObjs, p)
	}
	remoteClient := fake.NewClientBuilder().
		WithIndex(&corev1.Pod{}, "spec.nodeName", podByNodeName).
		WithObjects(remoteObjs...).
		Build()

	reconcileDeleteCache := cache.New[cache.ReconcileEntry](cache.DefaultTTL)
	r := &Reconciler{
		Client:               c,
		ClusterCache:         clustercache.NewFakeClusterCache(remoteClient, client.ObjectKeyFromObject(testCluster)),
		reconcileDeleteCache: reconcileDeleteCache,
	}

	s := &scope{
		cluster: testCluster,
		machine: testMachine,
	}

	// The first reconcile will cordon the Node, evict the one Pod running on the Node and then requeue.
	res, err := r.drainNode(ctx, s)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(res).To(BeComparableTo(ctrl.Result{RequeueAfter: drainRetryInterval}))
	// Condition should report the one Pod that has been evicted.
	gotCondition := v1beta1conditions.Get(testMachine, clusterv1.DrainingSucceededV1Beta1Condition)
	g.Expect(gotCondition).ToNot(BeNil())
	// Cleanup for easier comparison
	gotCondition.LastTransitionTime = metav1.Time{}
	g.Expect(gotCondition).To(BeComparableTo(&clusterv1.Condition{
		Type:     clusterv1.DrainingSucceededV1Beta1Condition,
		Status:   corev1.ConditionFalse,
		Severity: clusterv1.ConditionSeverityInfo,
		Reason:   clusterv1.DrainingV1Beta1Reason,
		Message: `Drain not completed yet (started at 2024-10-09T16:13:59Z):
* Pod test-namespace/pod-delete-running-deployment-pod: deletionTimestamp set, but still not removed from the Node`,
	}))
	g.Expect(s.deletingReason).To(Equal(clusterv1.MachineDeletingDrainingNodeReason))
	g.Expect(s.deletingMessage).To(Equal(`Drain not completed yet (started at 2024-10-09T16:13:59Z):
* Pod test-namespace/pod-delete-running-deployment-pod: deletionTimestamp set, but still not removed from the Node`))

	// Node should be cordoned.
	gotNode := &corev1.Node{}
	g.Expect(remoteClient.Get(ctx, client.ObjectKeyFromObject(node), gotNode)).To(Succeed())
	g.Expect(gotNode.Spec.Unschedulable).To(BeTrue())

	// Drain cache should have an entry for the Machine
	gotEntry, ok := reconcileDeleteCache.Has(cache.NewReconcileEntryKey(testMachine))
	g.Expect(ok).To(BeTrue())
	g.Expect(gotEntry.Request.Namespace).To(Equal(testMachine.Namespace))
	g.Expect(gotEntry.Request.Name).To(Equal(testMachine.Name))
}

func TestIsNodeVolumeDetachingAllowed(t *testing.T) {
	testCluster := &clusterv1.Cluster{
		TypeMeta:   metav1.TypeMeta{Kind: "Cluster", APIVersion: clusterv1.GroupVersion.String()},
		ObjectMeta: metav1.ObjectMeta{Namespace: metav1.NamespaceDefault, Name: "test-cluster"},
	}

	tests := []struct {
		name     string
		machine  *clusterv1.Machine
		expected bool
	}{
		{
			name: "Exclude wait node volume detaching annotation exists",
			machine: &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-machine",
					Namespace:   metav1.NamespaceDefault,
					Finalizers:  []string{clusterv1.MachineFinalizer},
					Annotations: map[string]string{clusterv1.ExcludeWaitForNodeVolumeDetachAnnotation: "existed!!"},
				},
				Spec: clusterv1.MachineSpec{
					ClusterName: "test-cluster",
					// InfrastructureRef is not defined
					Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
				},
				Status: clusterv1.MachineStatus{},
			},
			expected: false,
		},
		{
			name: "KCP machine with the pre terminate hook should wait",
			machine: &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-machine",
					Namespace:   metav1.NamespaceDefault,
					Labels:      map[string]string{clusterv1.MachineControlPlaneLabel: ""},
					Annotations: map[string]string{KubeadmControlPlanePreTerminateHookCleanupAnnotation: ""},
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: clusterv1.GroupVersionControlPlane.String(),
							Kind:       "KubeadmControlPlane",
							Name:       "Foo",
						},
					},
				},
				Spec: clusterv1.MachineSpec{
					ClusterName: "test-cluster",
					// InfrastructureRef is not defined
					Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
				},
				Status: clusterv1.MachineStatus{},
			},
			expected: true,
		},
		{
			name: "KCP machine without the pre terminate hook should stop waiting",
			machine: &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machine",
					Namespace: metav1.NamespaceDefault,
					Labels:    map[string]string{clusterv1.MachineControlPlaneLabel: ""},
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: clusterv1.GroupVersionControlPlane.String(),
							Kind:       "KubeadmControlPlane",
							Name:       "Foo",
						},
					},
				},
				Spec: clusterv1.MachineSpec{
					ClusterName: "test-cluster",
					// InfrastructureRef is not defined
					Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
				},
				Status: clusterv1.MachineStatus{},
			},
			expected: false,
		},
		{
			name: "Volume detach timeout is over",
			machine: &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-machine",
					Namespace:  metav1.NamespaceDefault,
					Finalizers: []string{clusterv1.MachineFinalizer},
				},
				Spec: clusterv1.MachineSpec{
					ClusterName: "test-cluster",
					// InfrastructureRef is not defined
					Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
					Deletion: clusterv1.MachineDeletionSpec{
						NodeVolumeDetachTimeoutSeconds: ptr.To(int32(30)),
					},
				},

				Status: clusterv1.MachineStatus{
					Deletion: &clusterv1.MachineDeletionStatus{
						WaitForNodeVolumeDetachStartTime: metav1.Time{Time: time.Now().Add(-(time.Second * 60)).UTC()},
					},
				},
			},
			expected: false,
		},
		{
			name: "Volume detach timeout is not yet over",
			machine: &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-machine",
					Namespace:  metav1.NamespaceDefault,
					Finalizers: []string{clusterv1.MachineFinalizer},
				},
				Spec: clusterv1.MachineSpec{
					ClusterName: "test-cluster",
					// InfrastructureRef is not defined
					Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
					Deletion: clusterv1.MachineDeletionSpec{
						NodeVolumeDetachTimeoutSeconds: ptr.To(int32(60)),
					},
				},
				Status: clusterv1.MachineStatus{
					Deletion: &clusterv1.MachineDeletionStatus{
						WaitForNodeVolumeDetachStartTime: metav1.Time{Time: time.Now().Add(-(time.Second * 30)).UTC()},
					},
				},
			},
			expected: true,
		},
		{
			name: "Volume detach timeout option is set to it's default value 0",
			machine: &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-machine",
					Namespace:  metav1.NamespaceDefault,
					Finalizers: []string{clusterv1.MachineFinalizer},
				},
				Spec: clusterv1.MachineSpec{
					ClusterName: "test-cluster",
					// InfrastructureRef is not defined
					Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
				},
				Status: clusterv1.MachineStatus{
					Deletion: &clusterv1.MachineDeletionStatus{
						WaitForNodeVolumeDetachStartTime: metav1.Time{Time: time.Now().Add(-(time.Second * 1000)).UTC()},
					},
				},
			},
			expected: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			var objs []client.Object
			objs = append(objs, testCluster, tt.machine)

			c := fake.NewClientBuilder().WithObjects(objs...).Build()
			r := &Reconciler{
				Client: c,
			}

			got := r.isNodeVolumeDetachingAllowed(tt.machine)
			g.Expect(got).To(Equal(tt.expected))
		})
	}
}

func TestShouldWaitForNodeVolumes(t *testing.T) {
	g := NewWithT(t)

	waitForNodeVolumeDetachStartTime, err := time.Parse(time.RFC3339, "2024-10-09T16:13:59Z")
	g.Expect(err).ToNot(HaveOccurred())

	testCluster := &clusterv1.Cluster{
		TypeMeta:   metav1.TypeMeta{Kind: "Cluster", APIVersion: clusterv1.GroupVersion.String()},
		ObjectMeta: metav1.ObjectMeta{Namespace: metav1.NamespaceDefault, Name: "test-cluster"},
	}

	nodeName := "test-node"

	persistentVolume := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pv",
		},
		Spec: corev1.PersistentVolumeSpec{
			ClaimRef: &corev1.ObjectReference{
				Kind:      "PersistentVolumeClaim",
				Namespace: "default",
				Name:      "test-pvc",
			},
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					VolumeHandle: "foo",
					Driver:       "dummy",
				},
			},
		},
	}

	persistentVolumeWithoutClaim := persistentVolume.DeepCopy()
	persistentVolumeWithoutClaim.Spec.ClaimRef.Kind = "NotAPVC"

	volumeAttachment := &storagev1.VolumeAttachment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-va",
		},
		Spec: storagev1.VolumeAttachmentSpec{
			NodeName: nodeName,
			Source: storagev1.VolumeAttachmentSource{
				PersistentVolumeName: &persistentVolume.Name,
			},
		},
		Status: storagev1.VolumeAttachmentStatus{
			Attached: true,
		},
	}
	testMachine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: metav1.NamespaceDefault,
		},
		Status: clusterv1.MachineStatus{
			Deletion: &clusterv1.MachineDeletionStatus{
				WaitForNodeVolumeDetachStartTime: metav1.Time{Time: waitForNodeVolumeDetachStartTime},
			},
		},
	}

	attachedVolumes := []corev1.AttachedVolume{
		{
			Name:       corev1.UniqueVolumeName(fmt.Sprintf("kubernetes.io/csi/%s^%s", persistentVolume.Spec.CSI.Driver, persistentVolume.Spec.CSI.VolumeHandle)),
			DevicePath: "test-path",
		},
	}

	tests := []struct {
		name                    string
		node                    *corev1.Node
		remoteObjects           []client.Object
		featureGateDisabled     bool
		expected                ctrl.Result
		expectedDeletingReason  string
		expectedDeletingMessage string
	}{
		{
			name: "Node has volumes attached according to node status",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{
							Type:   corev1.NodeReady,
							Status: corev1.ConditionTrue,
						},
					},
					VolumesAttached: attachedVolumes,
				},
			},
			remoteObjects: []client.Object{
				persistentVolume,
			},
			expected:               ctrl.Result{RequeueAfter: waitForVolumeDetachRetryInterval},
			expectedDeletingReason: clusterv1.MachineDeletingWaitingForVolumeDetachReason,
			expectedDeletingMessage: `Waiting for Node volumes to be detached (started at 2024-10-09T16:13:59Z)
* PersistentVolumeClaims: default/test-pvc`,
		},
		{
			name: "Node has volumes attached according to node status but the pv does not reference a PersistentVolumeClaim",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{
							Type:   corev1.NodeReady,
							Status: corev1.ConditionTrue,
						},
					},
					VolumesAttached: attachedVolumes,
				},
			},
			remoteObjects: []client.Object{
				persistentVolumeWithoutClaim,
			},
			expected:               ctrl.Result{RequeueAfter: waitForVolumeDetachRetryInterval},
			expectedDeletingReason: clusterv1.MachineDeletingWaitingForVolumeDetachReason,
			expectedDeletingMessage: `Waiting for Node volumes to be detached (started at 2024-10-09T16:13:59Z)
* PersistentVolumes without a .spec.claimRef to a PersistentVolumeClaim: test-pv`,
		},
		{
			name: "Node has volumes attached according to node status but without a pv",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{
							Type:   corev1.NodeReady,
							Status: corev1.ConditionTrue,
						},
					},
					VolumesAttached: attachedVolumes,
				},
			},
			remoteObjects:          []client.Object{},
			expected:               ctrl.Result{RequeueAfter: waitForVolumeDetachRetryInterval},
			expectedDeletingReason: clusterv1.MachineDeletingWaitingForVolumeDetachReason,
			expectedDeletingMessage: `Waiting for Node volumes to be detached (started at 2024-10-09T16:13:59Z)
* Node with .status.volumesAttached entries not matching a PersistentVolume: kubernetes.io/csi/dummy^foo`,
		},
		{
			name: "Node has volumes attached according to node status but its from a daemonset pod which gets ignored",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{
							Type:   corev1.NodeReady,
							Status: corev1.ConditionTrue,
						},
					},
					VolumesAttached: attachedVolumes,
				},
			},
			remoteObjects: []client.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
						OwnerReferences: []metav1.OwnerReference{
							{
								Kind:       appsv1.SchemeGroupVersion.WithKind("DaemonSet").Kind,
								Name:       "test-ds",
								Controller: ptr.To(true),
							},
						},
					},
					Spec: corev1.PodSpec{
						NodeName: nodeName,
						Volumes: []corev1.Volume{
							{
								Name: "test-pvc",
								VolumeSource: corev1.VolumeSource{
									PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
										ClaimName: "test-pvc",
									},
								},
							},
						},
					},
				},
				&appsv1.DaemonSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-ds",
						Namespace: "default",
					},
				},
				persistentVolume,
			},
			expected: ctrl.Result{},
		},
		{
			name: "Node has volumes attached according to volumeattachments",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{
							Type:   corev1.NodeReady,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			remoteObjects: []client.Object{
				volumeAttachment,
				persistentVolume,
			},
			expected:               ctrl.Result{RequeueAfter: waitForVolumeDetachRetryInterval},
			expectedDeletingReason: clusterv1.MachineDeletingWaitingForVolumeDetachReason,
			expectedDeletingMessage: `Waiting for Node volumes to be detached (started at 2024-10-09T16:13:59Z)
* PersistentVolumeClaims: default/test-pvc`,
		},
		{
			name: "Node has volumes attached according to volumeattachments (but ignored because feature gate is disabled)",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{
							Type:   corev1.NodeReady,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			remoteObjects: []client.Object{
				volumeAttachment,
				persistentVolume,
			},
			featureGateDisabled: true,
			expected:            ctrl.Result{},
		},
		{
			name: "Node has volumes attached according to volumeattachments but without a pv",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{
							Type:   corev1.NodeReady,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			remoteObjects: []client.Object{
				volumeAttachment,
			},
			expected:               ctrl.Result{RequeueAfter: waitForVolumeDetachRetryInterval},
			expectedDeletingReason: clusterv1.MachineDeletingWaitingForVolumeDetachReason,
			expectedDeletingMessage: `Waiting for Node volumes to be detached (started at 2024-10-09T16:13:59Z)
* VolumeAttachment with .spec.source.persistentVolumeName not matching a PersistentVolume: test-pv`,
		},
		{
			name: "Node has volumes attached according to volumeattachments but its from a daemonset pod which gets ignored",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{
							Type:   corev1.NodeReady,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			remoteObjects: []client.Object{
				volumeAttachment,
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pod",
						Namespace: "default",
						OwnerReferences: []metav1.OwnerReference{
							{
								Kind:       appsv1.SchemeGroupVersion.WithKind("DaemonSet").Kind,
								Name:       "test-ds",
								Controller: ptr.To(true),
							},
						},
					},
					Spec: corev1.PodSpec{
						NodeName: nodeName,
						Volumes: []corev1.Volume{
							{
								Name: "test-pvc",
								VolumeSource: corev1.VolumeSource{
									PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
										ClaimName: "test-pvc",
									},
								},
							},
						},
					},
				},
				&appsv1.DaemonSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-ds",
						Namespace: "default",
					},
				},
				persistentVolume,
			},
			expected: ctrl.Result{},
		},
		{
			name: "Node has volumes attached from a Pod which is in deletion",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{
							Type:   corev1.NodeReady,
							Status: corev1.ConditionTrue,
						},
					},
					VolumesAttached: attachedVolumes,
				},
			},
			remoteObjects: []client.Object{
				volumeAttachment,
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "test-pod",
						Namespace:         "default",
						DeletionTimestamp: ptr.To(metav1.NewTime(time.Now().Add(time.Hour * 24 * -1))),
						Finalizers: []string{
							"prevent-removal",
						},
					},
					Spec: corev1.PodSpec{
						NodeName: nodeName,
						Volumes: []corev1.Volume{
							{
								Name: "test-pvc",
								VolumeSource: corev1.VolumeSource{
									PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
										ClaimName: "test-pvc",
									},
								},
							},
						},
					},
				},
				persistentVolume,
			},
			expected:               ctrl.Result{RequeueAfter: waitForVolumeDetachRetryInterval},
			expectedDeletingReason: clusterv1.MachineDeletingWaitingForVolumeDetachReason,
			expectedDeletingMessage: `Waiting for Node volumes to be detached (started at 2024-10-09T16:13:59Z)
* PersistentVolumeClaims: default/test-pvc`,
		},
		{
			name: "Node has no volumes attached",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{
							Type:   corev1.NodeReady,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			expected: ctrl.Result{},
		},
		{
			name: "Node is unreachable and has volumes attached",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "unreachable-node",
				},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{
							Type:   corev1.NodeReady,
							Status: corev1.ConditionUnknown,
						},
					},
					VolumesAttached: attachedVolumes,
				},
			},
			expected: ctrl.Result{},
		},
		{
			name: "Node is unreachable and has no volumes attached",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "unreachable-node",
				},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{
							Type:   corev1.NodeReady,
							Status: corev1.ConditionUnknown,
						},
					},
				},
			},
			expected: ctrl.Result{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			if tt.featureGateDisabled {
				utilfeature.SetFeatureGateDuringTest(t, feature.Gates, feature.MachineWaitForVolumeDetachConsiderVolumeAttachments, false)
			}

			fakeClient := fake.NewClientBuilder().WithObjects(testCluster).Build()

			var remoteObjects []client.Object
			remoteObjects = append(remoteObjects, tt.node)
			remoteObjects = append(remoteObjects, tt.remoteObjects...)
			remoteObjects = append(remoteObjects, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "default",
					Labels: map[string]string{
						"kubernetes.io/metadata.name": "default",
					},
				},
			})
			remoteFakeClient := fake.NewClientBuilder().WithIndex(&corev1.Pod{}, "spec.nodeName", nodeNameIndex).
				WithObjects(remoteObjects...).Build()

			r := &Reconciler{
				Client:               fakeClient,
				ClusterCache:         clustercache.NewFakeClusterCache(remoteFakeClient, client.ObjectKeyFromObject(testCluster)),
				reconcileDeleteCache: cache.New[cache.ReconcileEntry](cache.DefaultTTL),
			}

			testMachine.Status.NodeRef = clusterv1.MachineNodeReference{
				Name: tt.node.GetName(),
			}

			s := &scope{
				cluster: testCluster,
				machine: testMachine,
			}

			got, err := r.shouldWaitForNodeVolumes(ctx, s)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(got).To(BeEquivalentTo(tt.expected))
			g.Expect(s.deletingReason).To(BeEquivalentTo(tt.expectedDeletingReason))
			g.Expect(s.deletingMessage).To(BeEquivalentTo(tt.expectedDeletingMessage))
		})
	}
}

func nodeNameIndex(o client.Object) []string {
	return []string{o.(*corev1.Pod).Spec.NodeName}
}

func TestIsDeleteNodeAllowed(t *testing.T) {
	deletionts := metav1.Now()

	testCases := []struct {
		name          string
		cluster       *clusterv1.Cluster
		machine       *clusterv1.Machine
		infraMachine  *unstructured.Unstructured
		expectedError error
	}{
		{
			name: "machine without nodeRef",
			cluster: &clusterv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-cluster",
				},
			},
			machine: &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "created",
					Namespace: "test-cluster",
					Labels: map[string]string{
						clusterv1.ClusterNameLabel: "test-cluster",
					},
					Finalizers: []string{clusterv1.MachineFinalizer},
				},
				Spec: clusterv1.MachineSpec{
					ClusterName: "test-cluster",
					// InfrastructureRef is not defined
					Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
				},
				Status: clusterv1.MachineStatus{},
			},
			infraMachine:  nil,
			expectedError: errNilNodeRef,
		},
		{
			name: "no control plane members",
			cluster: &clusterv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-cluster",
				},
			},
			machine: &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "created",
					Namespace: "test-cluster",
					Labels: map[string]string{
						clusterv1.ClusterNameLabel: "test-cluster",
					},
					Finalizers: []string{clusterv1.MachineFinalizer},
				},
				Spec: clusterv1.MachineSpec{
					ClusterName: "test-cluster",
					// InfrastructureRef is not defined
					Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
				},
				Status: clusterv1.MachineStatus{
					NodeRef: clusterv1.MachineNodeReference{
						Name: "test",
					},
				},
			},
			infraMachine:  nil,
			expectedError: errNoControlPlaneNodes,
		},
		{
			name: "is last control plane member",
			cluster: &clusterv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-cluster",
				},
			},
			machine: &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "created",
					Namespace: "test-cluster",
					Labels: map[string]string{
						clusterv1.ClusterNameLabel:         "test-cluster",
						clusterv1.MachineControlPlaneLabel: "",
					},
					Finalizers:        []string{clusterv1.MachineFinalizer},
					DeletionTimestamp: &metav1.Time{Time: time.Now().UTC()},
				},
				Spec: clusterv1.MachineSpec{
					ClusterName: "test-cluster",
					// InfrastructureRef is not defined
					Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
				},
				Status: clusterv1.MachineStatus{
					NodeRef: clusterv1.MachineNodeReference{
						Name: "test",
					},
				},
			},
			infraMachine:  nil,
			expectedError: errNoControlPlaneNodes,
		},
		{
			name: "has nodeRef and control plane is healthy",
			cluster: &clusterv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-cluster",
				},
			},
			machine: &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "created",
					Namespace: "test-cluster",
					Labels: map[string]string{
						clusterv1.ClusterNameLabel: "test-cluster",
					},
					Finalizers: []string{clusterv1.MachineFinalizer},
				},
				Spec: clusterv1.MachineSpec{
					ClusterName: "test-cluster",
					// InfrastructureRef is not defined
					Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
				},
				Status: clusterv1.MachineStatus{
					NodeRef: clusterv1.MachineNodeReference{
						Name: "test",
					},
				},
			},
			infraMachine:  nil,
			expectedError: nil,
		},
		{
			name: "has nodeRef and cluster is being deleted",
			cluster: &clusterv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-cluster",
					Namespace:         "test-cluster",
					DeletionTimestamp: &deletionts,
					Finalizers:        []string{clusterv1.ClusterFinalizer},
				},
			},
			machine:       &clusterv1.Machine{},
			infraMachine:  nil,
			expectedError: errClusterIsBeingDeleted,
		},
		{
			name: "has nodeRef and control plane is healthy and externally managed",
			cluster: &clusterv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-cluster",
				},
				Spec: clusterv1.ClusterSpec{
					ControlPlaneRef: clusterv1.ContractVersionedObjectReference{
						APIGroup: clusterv1.GroupVersionControlPlane.Group,
						Kind:     builder.GenericControlPlaneKind,
						Name:     "test-cluster",
					},
				},
			},
			machine: &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "created",
					Namespace: "test-cluster",
					Labels: map[string]string{
						clusterv1.ClusterNameLabel: "test-cluster",
					},
					Finalizers: []string{clusterv1.MachineFinalizer},
				},
				Spec: clusterv1.MachineSpec{
					ClusterName: "test-cluster",
					// InfrastructureRef is not defined
					Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
				},
				Status: clusterv1.MachineStatus{
					NodeRef: clusterv1.MachineNodeReference{
						Name: "test",
					},
				},
			},
			infraMachine:  nil,
			expectedError: nil,
		},
		{
			name: "has nodeRef, control plane is being deleted and not externally managed",
			cluster: &clusterv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-cluster",
				},
				Spec: clusterv1.ClusterSpec{
					ControlPlaneRef: clusterv1.ContractVersionedObjectReference{
						APIGroup: builder.ControlPlaneGroupVersion.Group,
						Kind:     builder.GenericControlPlaneKind,
						Name:     "test-cluster-2",
					},
				},
			},
			machine: &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "created",
					Namespace: "test-cluster",
					Labels: map[string]string{
						clusterv1.ClusterNameLabel: "test-cluster",
					},
					Finalizers: []string{clusterv1.MachineFinalizer},
				},
				Spec: clusterv1.MachineSpec{
					ClusterName: "test-cluster",
					// InfrastructureRef is not defined
					Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
				},
				Status: clusterv1.MachineStatus{
					NodeRef: clusterv1.MachineNodeReference{
						Name: "test",
					},
				},
			},
			infraMachine:  nil,
			expectedError: errControlPlaneIsBeingDeleted,
		},
		{
			name: "has nodeRef, control plane is being deleted and is externally managed",
			cluster: &clusterv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-cluster",
				},
				Spec: clusterv1.ClusterSpec{
					ControlPlaneRef: clusterv1.ContractVersionedObjectReference{
						APIGroup: clusterv1.GroupVersionControlPlane.Group,
						Kind:     builder.GenericControlPlaneKind,
						Name:     "test-cluster-3",
					},
				},
			},
			machine: &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "created",
					Namespace: "test-cluster",
					Labels: map[string]string{
						clusterv1.ClusterNameLabel: "test-cluster",
					},
					Finalizers: []string{clusterv1.MachineFinalizer},
				},
				Spec: clusterv1.MachineSpec{
					ClusterName: "test-cluster",
					// InfrastructureRef is not defined
					Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
				},
				Status: clusterv1.MachineStatus{
					NodeRef: clusterv1.MachineNodeReference{
						Name: "test",
					},
				},
			},
			infraMachine:  nil,
			expectedError: errControlPlaneIsBeingDeleted,
		},
		{
			name: "no nodeRef, infrastructure machine has providerID",
			cluster: &clusterv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-cluster",
				},
			},
			machine: &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "created",
					Namespace: "test-cluster",
					Labels: map[string]string{
						clusterv1.ClusterNameLabel: "test-cluster",
					},
					Finalizers: []string{clusterv1.MachineFinalizer},
				},
				Spec: clusterv1.MachineSpec{
					ClusterName: "test-cluster",
					// InfrastructureRef is not defined
					Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
				},
				Status: clusterv1.MachineStatus{},
			},
			infraMachine: &unstructured.Unstructured{Object: map[string]interface{}{
				"spec": map[string]interface{}{
					"providerID": "test-node-1",
				},
			}},
			expectedError: nil,
		},
	}

	emp := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"status": map[string]interface{}{
				"externalManagedControlPlane": true,
			},
		},
	}
	emp.SetAPIVersion(clusterv1.GroupVersionControlPlane.String())
	emp.SetKind(builder.GenericControlPlaneKind)
	emp.SetName("test-cluster")
	emp.SetNamespace("test-cluster")

	mcpBeingDeleted := &unstructured.Unstructured{
		Object: map[string]interface{}{},
	}
	mcpBeingDeleted.SetAPIVersion(clusterv1.GroupVersionControlPlane.String())
	mcpBeingDeleted.SetKind(builder.GenericControlPlaneKind)
	mcpBeingDeleted.SetName("test-cluster-2")
	mcpBeingDeleted.SetNamespace("test-cluster")
	mcpBeingDeleted.SetDeletionTimestamp(&metav1.Time{Time: time.Now()})
	mcpBeingDeleted.SetFinalizers([]string{"block-deletion"})

	empBeingDeleted := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"status": map[string]interface{}{
				"externalManagedControlPlane": true,
			},
		},
	}
	empBeingDeleted.SetAPIVersion(clusterv1.GroupVersionControlPlane.String())
	empBeingDeleted.SetKind(builder.GenericControlPlaneKind)
	empBeingDeleted.SetName("test-cluster-3")
	empBeingDeleted.SetNamespace("test-cluster")
	empBeingDeleted.SetDeletionTimestamp(&metav1.Time{Time: time.Now()})
	empBeingDeleted.SetFinalizers([]string{"block-deletion"})

	testNodeA := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-1",
		},
		Spec: corev1.NodeSpec{
			ProviderID: "test-node-1",
		},
	}
	remoteClient := fake.NewClientBuilder().WithIndex(&corev1.Node{}, "spec.providerID", index.NodeByProviderID).WithObjects(testNodeA).Build()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			m1 := &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cp1",
					Namespace: "test-cluster",
					Labels: map[string]string{
						clusterv1.ClusterNameLabel: "test-cluster",
					},
					Finalizers: []string{clusterv1.MachineFinalizer},
				},
				Spec: clusterv1.MachineSpec{
					ClusterName: "test-cluster",
					// InfrastructureRef is not defined
					Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
				},
				Status: clusterv1.MachineStatus{
					NodeRef: clusterv1.MachineNodeReference{
						Name: "test1",
					},
				},
			}
			m2 := &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cp2",
					Namespace: "test-cluster",
					Labels: map[string]string{
						clusterv1.ClusterNameLabel: "test-cluster",
					},
					Finalizers: []string{clusterv1.MachineFinalizer},
				},
				Spec: clusterv1.MachineSpec{
					ClusterName: "test-cluster",
					// InfrastructureRef is not defined
					Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
				},
				Status: clusterv1.MachineStatus{
					NodeRef: clusterv1.MachineNodeReference{
						Name: "test2",
					},
				},
			}
			// For isDeleteNodeAllowed to be true we assume a healthy control plane.
			if tc.expectedError == nil {
				m1.Labels[clusterv1.MachineControlPlaneLabel] = ""
				m2.Labels[clusterv1.MachineControlPlaneLabel] = ""
			}

			c := fake.NewClientBuilder().WithObjects(
				tc.cluster,
				tc.machine,
				m1,
				m2,
				emp,
				mcpBeingDeleted,
				empBeingDeleted,
				// Note: CRD is needed to look up the apiVersion from contract labels.
				builder.GenericControlPlaneCRD,
			).Build()
			mr := &Reconciler{
				Client:       c,
				ClusterCache: clustercache.NewFakeClusterCache(remoteClient, client.ObjectKeyFromObject(tc.cluster)),
			}

			err := mr.isDeleteNodeAllowed(ctx, tc.cluster, tc.machine, tc.infraMachine)
			if tc.expectedError == nil {
				g.Expect(err).ToNot(HaveOccurred())
			} else {
				g.Expect(err).To(Equal(tc.expectedError))
			}
		})
	}
}

func TestNodeToMachine(t *testing.T) {
	g := NewWithT(t)
	ns, err := env.CreateNamespace(ctx, "test-node-to-machine")
	g.Expect(err).ToNot(HaveOccurred())

	// Set up cluster, machines and nodes to test against.
	infraMachine := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind":       "GenericInfrastructureMachine",
			"apiVersion": clusterv1.GroupVersionInfrastructure.String(),
			"metadata": map[string]interface{}{
				"name":      "infra-config1",
				"namespace": ns.Name,
			},
			"spec": map[string]interface{}{
				"providerID": "test://id-1",
			},
			"status": map[string]interface{}{
				"ready": true,
				"addresses": []interface{}{
					map[string]interface{}{
						"type":    "InternalIP",
						"address": "10.0.0.1",
					},
				},
			},
		},
	}

	infraMachine2 := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind":       "GenericInfrastructureMachine",
			"apiVersion": clusterv1.GroupVersionInfrastructure.String(),
			"metadata": map[string]interface{}{
				"name":      "infra-config2",
				"namespace": ns.Name,
			},
			"spec": map[string]interface{}{
				"providerID": "test://id-2",
			},
			"status": map[string]interface{}{
				"ready": true,
				"addresses": []interface{}{
					map[string]interface{}{
						"type":    "InternalIP",
						"address": "10.0.0.1",
					},
				},
			},
		},
	}

	defaultBootstrap := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"kind":       "GenericBootstrapConfig",
			"apiVersion": clusterv1.GroupVersionBootstrap.String(),
			"metadata": map[string]interface{}{
				"name":      "bootstrap-config-machinereconcile",
				"namespace": ns.Name,
			},
			"spec":   map[string]interface{}{},
			"status": map[string]interface{}{},
		},
	}

	testCluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "machine-reconcile-",
			Namespace:    ns.Name,
		},
		Spec: clusterv1.ClusterSpec{
			ControlPlaneRef: clusterv1.ContractVersionedObjectReference{
				APIGroup: builder.ControlPlaneGroupVersion.Group,
				Kind:     builder.GenericControlPlaneKind,
				Name:     "cp1",
			},
		},
	}

	targetNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node-to-machine-1",
		},
		Spec: corev1.NodeSpec{
			ProviderID: "test://id-1",
		},
	}

	randomNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node-to-machine-node-2",
		},
		Spec: corev1.NodeSpec{
			ProviderID: "test://id-2",
		},
	}

	g.Expect(env.Create(ctx, testCluster)).To(Succeed())
	g.Expect(env.CreateKubeconfigSecret(ctx, testCluster)).To(Succeed())
	// Set InfrastructureReady to true so ClusterCache creates the clusterAccessor.
	testClusterOriginal := client.MergeFrom(testCluster.DeepCopy())
	testCluster.Status.Initialization.InfrastructureProvisioned = ptr.To(true)
	g.Expect(env.Status().Patch(ctx, testCluster, testClusterOriginal)).To(Succeed())

	g.Expect(env.Create(ctx, defaultBootstrap)).To(Succeed())
	g.Expect(env.Create(ctx, targetNode)).To(Succeed())
	g.Expect(env.Create(ctx, randomNode)).To(Succeed())
	g.Expect(env.Create(ctx, infraMachine)).To(Succeed())
	g.Expect(env.Create(ctx, infraMachine2)).To(Succeed())

	defer func(do ...client.Object) {
		g.Expect(env.Cleanup(ctx, do...)).To(Succeed())
	}(ns, testCluster, defaultBootstrap)

	// Patch infra expectedMachine ready
	patchHelper, err := patch.NewHelper(infraMachine, env)
	g.Expect(err).ShouldNot(HaveOccurred())
	g.Expect(unstructured.SetNestedField(infraMachine.Object, true, "status", "initialization", "provisioned")).To(Succeed())
	g.Expect(patchHelper.Patch(ctx, infraMachine, patch.WithStatusObservedGeneration{})).To(Succeed())

	// Patch infra randomMachine ready
	patchHelper, err = patch.NewHelper(infraMachine2, env)
	g.Expect(err).ShouldNot(HaveOccurred())
	g.Expect(unstructured.SetNestedField(infraMachine2.Object, true, "status", "initialization", "provisioned")).To(Succeed())
	g.Expect(patchHelper.Patch(ctx, infraMachine2, patch.WithStatusObservedGeneration{})).To(Succeed())

	// Patch bootstrap ready
	patchHelper, err = patch.NewHelper(defaultBootstrap, env)
	g.Expect(err).ShouldNot(HaveOccurred())
	g.Expect(unstructured.SetNestedField(defaultBootstrap.Object, true, "status", "initialization", "dataSecretCreated")).To(Succeed())
	g.Expect(unstructured.SetNestedField(defaultBootstrap.Object, "secretData", "status", "dataSecretName")).To(Succeed())
	g.Expect(patchHelper.Patch(ctx, defaultBootstrap, patch.WithStatusObservedGeneration{})).To(Succeed())

	expectedMachine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "machine-created-",
			Namespace:    ns.Name,
			Labels: map[string]string{
				clusterv1.MachineControlPlaneLabel: "",
			},
		},
		Spec: clusterv1.MachineSpec{
			ClusterName: testCluster.Name,
			InfrastructureRef: clusterv1.ContractVersionedObjectReference{
				APIGroup: clusterv1.GroupVersionInfrastructure.Group,
				Kind:     "GenericInfrastructureMachine",
				Name:     "infra-config1",
			},
			Bootstrap: clusterv1.Bootstrap{
				ConfigRef: clusterv1.ContractVersionedObjectReference{
					APIGroup: clusterv1.GroupVersionBootstrap.Group,
					Kind:     "GenericBootstrapConfig",
					Name:     "bootstrap-config-machinereconcile",
				},
			},
		},
	}

	g.Expect(env.Create(ctx, expectedMachine)).To(Succeed())
	defer func() {
		g.Expect(env.Cleanup(ctx, expectedMachine)).To(Succeed())
	}()

	// Wait for reconciliation to happen.
	// Since infra and bootstrap objects are ready, a nodeRef will be assigned during node reconciliation.
	key := client.ObjectKey{Name: expectedMachine.Name, Namespace: expectedMachine.Namespace}
	g.Eventually(func() bool {
		if err := env.Get(ctx, key, expectedMachine); err != nil {
			return false
		}
		return expectedMachine.Status.NodeRef.IsDefined()
	}, timeout).Should(BeTrue())

	randomMachine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "machine-created-",
			Namespace:    ns.Name,
			Labels: map[string]string{
				clusterv1.MachineControlPlaneLabel: "",
			},
		},
		Spec: clusterv1.MachineSpec{
			ClusterName: testCluster.Name,
			InfrastructureRef: clusterv1.ContractVersionedObjectReference{
				APIGroup: clusterv1.GroupVersionInfrastructure.Group,
				Kind:     "GenericInfrastructureMachine",
				Name:     "infra-config2",
			},
			Bootstrap: clusterv1.Bootstrap{
				ConfigRef: clusterv1.ContractVersionedObjectReference{
					APIGroup: clusterv1.GroupVersionBootstrap.Group,
					Kind:     "GenericBootstrapConfig",
					Name:     "bootstrap-config-machinereconcile",
				},
			},
		},
	}

	g.Expect(env.Create(ctx, randomMachine)).To(Succeed())
	defer func() {
		g.Expect(env.Cleanup(ctx, randomMachine)).To(Succeed())
	}()

	// Wait for reconciliation to happen.
	// Since infra and bootstrap objects are ready, a nodeRef will be assigned during node reconciliation.
	key = client.ObjectKey{Name: randomMachine.Name, Namespace: randomMachine.Namespace}
	g.Eventually(func() bool {
		if err := env.Get(ctx, key, randomMachine); err != nil {
			return false
		}
		return randomMachine.Status.NodeRef.IsDefined()
	}, timeout).Should(BeTrue())

	// Fake nodes for actual test of nodeToMachine.
	fakeNodes := []*corev1.Node{
		// None annotations.
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: targetNode.GetName(),
			},
			Spec: corev1.NodeSpec{
				ProviderID: targetNode.Spec.ProviderID,
			},
		},
		// ClusterNameAnnotation annotation.
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: targetNode.GetName(),
				Annotations: map[string]string{
					clusterv1.ClusterNameAnnotation: testCluster.GetName(),
				},
			},
			Spec: corev1.NodeSpec{
				ProviderID: targetNode.Spec.ProviderID,
			},
		},
		// ClusterNamespaceAnnotation annotation.
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: targetNode.GetName(),
				Annotations: map[string]string{
					clusterv1.ClusterNamespaceAnnotation: ns.GetName(),
				},
			},
			Spec: corev1.NodeSpec{
				ProviderID: targetNode.Spec.ProviderID,
			},
		},
		// Both annotations.
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: targetNode.GetName(),
				Annotations: map[string]string{
					clusterv1.ClusterNameAnnotation:      testCluster.GetName(),
					clusterv1.ClusterNamespaceAnnotation: ns.GetName(),
				},
			},
			Spec: corev1.NodeSpec{
				ProviderID: targetNode.Spec.ProviderID,
			},
		},
	}

	r := &Reconciler{
		Client: env,
	}
	for _, node := range fakeNodes {
		request := r.nodeToMachine(ctx, node)
		g.Expect(request).To(BeEquivalentTo([]reconcile.Request{
			{
				NamespacedName: client.ObjectKeyFromObject(expectedMachine),
			},
		}))
	}
}

type fakeClientWithNodeDeletionErr struct {
	client.Client
}

func (fc fakeClientWithNodeDeletionErr) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	gvk, err := apiutil.GVKForObject(obj, fakeScheme)
	if err == nil && gvk.Kind == "Node" {
		return fmt.Errorf("fake error")
	}
	return fc.Client.Delete(ctx, obj, opts...)
}

func TestNodeDeletion(t *testing.T) {
	deletionTime := metav1.Now().Add(-1 * time.Second)

	testCluster := clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: metav1.NamespaceDefault,
		},
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
		Spec: corev1.NodeSpec{ProviderID: "test://id-1"},
	}

	testMachine := clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: metav1.NamespaceDefault,
			Labels: map[string]string{
				clusterv1.MachineControlPlaneLabel: "",
			},
			Annotations: map[string]string{
				"machine.cluster.x-k8s.io/exclude-node-draining": "",
			},
			Finalizers:        []string{clusterv1.MachineFinalizer},
			DeletionTimestamp: &metav1.Time{Time: deletionTime},
		},
		Spec: clusterv1.MachineSpec{
			ClusterName: "test-cluster",
			InfrastructureRef: clusterv1.ContractVersionedObjectReference{
				APIGroup: clusterv1.GroupVersionInfrastructure.Group,
				Kind:     "GenericInfrastructureMachine",
				Name:     "infra-config1",
			},
			Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
		},
		Status: clusterv1.MachineStatus{
			NodeRef: clusterv1.MachineNodeReference{
				Name: "test",
			},
		},
	}

	cpmachine1 := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cp1",
			Namespace: metav1.NamespaceDefault,
			Labels: map[string]string{
				clusterv1.ClusterNameLabel:         "test-cluster",
				clusterv1.MachineControlPlaneLabel: "",
			},
			Finalizers: []string{clusterv1.MachineFinalizer},
		},
		Spec: clusterv1.MachineSpec{
			ClusterName: "test-cluster",
			// InfrastructureRef is not defined
			Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
		},
		Status: clusterv1.MachineStatus{
			NodeRef: clusterv1.MachineNodeReference{
				Name: "cp1",
			},
		},
	}

	testCases := []struct {
		name                   string
		deletionTimeoutSeconds *int32
		resultErr              bool
		clusterDeleted         bool
		expectNodeDeletion     bool
		expectDeletingReason   string
		createFakeClient       func(...client.Object) client.Client
	}{
		{
			name:                   "should return no error when deletion is successful",
			deletionTimeoutSeconds: ptr.To(int32(1)),
			resultErr:              false,
			expectNodeDeletion:     true,
			expectDeletingReason:   clusterv1.MachineDeletingDeletionCompletedReason,
			createFakeClient: func(initObjs ...client.Object) client.Client {
				return fake.NewClientBuilder().
					WithObjects(initObjs...).
					WithStatusSubresource(&clusterv1.Machine{}).
					Build()
			},
		},
		{
			name:                   "should return an error when timeout is not expired and node deletion fails",
			deletionTimeoutSeconds: ptr.To(int32(60 * 60)),
			resultErr:              true,
			expectNodeDeletion:     false,
			expectDeletingReason:   clusterv1.MachineDeletingDeletingNodeReason,
			createFakeClient: func(initObjs ...client.Object) client.Client {
				fc := fake.NewClientBuilder().
					WithObjects(initObjs...).
					WithStatusSubresource(&clusterv1.Machine{}).
					Build()
				return fakeClientWithNodeDeletionErr{fc}
			},
		},
		{
			name:                   "should return an error when timeout is infinite and node deletion fails",
			deletionTimeoutSeconds: ptr.To(int32(0)), // should lead to infinite timeout
			resultErr:              true,
			expectNodeDeletion:     false,
			expectDeletingReason:   clusterv1.MachineDeletingDeletingNodeReason,
			createFakeClient: func(initObjs ...client.Object) client.Client {
				fc := fake.NewClientBuilder().
					WithObjects(initObjs...).
					WithStatusSubresource(&clusterv1.Machine{}).
					Build()
				return fakeClientWithNodeDeletionErr{fc}
			},
		},
		{
			name:                   "should not return an error when timeout is expired and node deletion fails",
			deletionTimeoutSeconds: ptr.To(int32(1)),
			resultErr:              false,
			expectNodeDeletion:     false,
			expectDeletingReason:   clusterv1.DeletionCompletedReason,
			createFakeClient: func(initObjs ...client.Object) client.Client {
				fc := fake.NewClientBuilder().
					WithObjects(initObjs...).
					WithStatusSubresource(&clusterv1.Machine{}).
					Build()
				return fakeClientWithNodeDeletionErr{fc}
			},
		},
		{
			name:                   "should not delete the node or return an error when the cluster is marked for deletion",
			deletionTimeoutSeconds: nil, // should lead to infinite timeout
			resultErr:              false,
			clusterDeleted:         true,
			expectNodeDeletion:     false,
			expectDeletingReason:   clusterv1.DeletionCompletedReason,
			createFakeClient: func(initObjs ...client.Object) client.Client {
				fc := fake.NewClientBuilder().
					WithObjects(initObjs...).
					WithStatusSubresource(&clusterv1.Machine{}).
					Build()
				return fakeClientWithNodeDeletionErr{fc}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			m := testMachine.DeepCopy()
			m.Spec.Deletion.NodeDeletionTimeoutSeconds = tc.deletionTimeoutSeconds

			fakeClient := tc.createFakeClient(node, m, cpmachine1)

			r := &Reconciler{
				Client:                   fakeClient,
				ClusterCache:             clustercache.NewFakeClusterCache(fakeClient, client.ObjectKeyFromObject(&testCluster)),
				recorder:                 record.NewFakeRecorder(10),
				nodeDeletionRetryTimeout: 10 * time.Millisecond,
				reconcileDeleteCache:     cache.New[cache.ReconcileEntry](cache.DefaultTTL),
			}

			cluster := testCluster.DeepCopy()
			if tc.clusterDeleted {
				cluster.DeletionTimestamp = &metav1.Time{Time: deletionTime.Add(time.Hour)}
			}

			s := &scope{
				cluster:                   cluster,
				machine:                   m,
				infraMachineIsNotFound:    true,
				bootstrapConfigIsNotFound: true,
			}
			_, err := r.reconcileDelete(context.Background(), s)

			if tc.resultErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				if tc.expectNodeDeletion {
					n := &corev1.Node{}
					g.Expect(fakeClient.Get(context.Background(), client.ObjectKeyFromObject(node), n)).NotTo(Succeed())
				}
			}
			g.Expect(s.deletingReason).To(Equal(tc.expectDeletingReason))
		})
	}
}

func TestNodeDeletionWithoutNodeRefFallback(t *testing.T) {
	g := NewWithT(t)

	deletionTime := metav1.Now().Add(-1 * time.Second)

	testCluster := clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: metav1.NamespaceDefault,
		},
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
		Spec: corev1.NodeSpec{ProviderID: "test://id-1"},
	}

	testMachine := clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: metav1.NamespaceDefault,
			Labels: map[string]string{
				clusterv1.MachineControlPlaneLabel: "",
				clusterv1.ClusterNameLabel:         "test-cluster",
			},
			Annotations: map[string]string{
				"machine.cluster.x-k8s.io/exclude-node-draining": "",
			},
			Finalizers:        []string{clusterv1.MachineFinalizer},
			DeletionTimestamp: &metav1.Time{Time: deletionTime},
		},
		Spec: clusterv1.MachineSpec{
			ClusterName: "test-cluster",
			InfrastructureRef: clusterv1.ContractVersionedObjectReference{
				APIGroup: clusterv1.GroupVersionInfrastructure.Group,
				Kind:     "GenericInfrastructureMachine",
				Name:     "infra-config1",
			},
			Bootstrap:  clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
			ProviderID: "test://id-1",
		},
	}

	cpmachine1 := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cp1",
			Namespace: metav1.NamespaceDefault,
			Labels: map[string]string{
				clusterv1.ClusterNameLabel:         "test-cluster",
				clusterv1.MachineControlPlaneLabel: "",
			},
			Finalizers: []string{clusterv1.MachineFinalizer},
		},
		Spec: clusterv1.MachineSpec{
			ClusterName: "test-cluster",
			// InfrastructureRef is not defined
			Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("data")},
		},
		Status: clusterv1.MachineStatus{
			NodeRef: clusterv1.MachineNodeReference{
				Name: "cp1",
			},
		},
	}

	testCases := []struct {
		name                   string
		deletionTimeoutSeconds *int32
		resultErr              bool
		expectNodeDeletion     bool
		expectDeletingReason   string
		createFakeClient       func(...client.Object) client.Client
	}{
		{
			name:                   "should return no error when the node exists and matches the provider id",
			deletionTimeoutSeconds: ptr.To(int32(1)),
			resultErr:              false,
			expectNodeDeletion:     true,
			expectDeletingReason:   clusterv1.MachineDeletingDeletionCompletedReason,
			createFakeClient: func(initObjs ...client.Object) client.Client {
				return fake.NewClientBuilder().
					WithObjects(initObjs...).
					WithIndex(&corev1.Node{}, index.NodeProviderIDField, index.NodeByProviderID).
					WithStatusSubresource(&clusterv1.Machine{}).
					Build()
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(*testing.T) {
			m := testMachine.DeepCopy()
			m.Spec.Deletion.NodeDeletionTimeoutSeconds = tc.deletionTimeoutSeconds

			fakeClient := tc.createFakeClient(node, m, cpmachine1)

			r := &Reconciler{
				Client:                   fakeClient,
				ClusterCache:             clustercache.NewFakeClusterCache(fakeClient, client.ObjectKeyFromObject(&testCluster)),
				recorder:                 record.NewFakeRecorder(10),
				nodeDeletionRetryTimeout: 10 * time.Millisecond,
				reconcileDeleteCache:     cache.New[cache.ReconcileEntry](cache.DefaultTTL),
			}

			s := &scope{
				cluster:                   testCluster.DeepCopy(),
				machine:                   m,
				infraMachineIsNotFound:    true,
				bootstrapConfigIsNotFound: true,
			}
			_, err := r.reconcileDelete(context.Background(), s)

			if tc.resultErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				if tc.expectNodeDeletion {
					n := &corev1.Node{}
					g.Expect(apierrors.IsNotFound(fakeClient.Get(context.Background(), client.ObjectKeyFromObject(node), n))).To(BeTrue())
				}
			}
			g.Expect(s.deletingReason).To(Equal(tc.expectDeletingReason))
		})
	}
}

// adds a condition list to an external object.
func addConditionToExternal(u *unstructured.Unstructured, c metav1.Condition) {
	err := unstructured.SetNestedSlice(u.Object, []interface{}{
		map[string]interface{}{
			"type":    c.Type,
			"status":  string(c.Status),
			"reason":  c.Reason,
			"message": c.Message,
		},
	}, "status", "conditions")
	if err != nil {
		panic(err)
	}
}

// asserts the conditions set on the Getter object.
func assertConditions(t *testing.T, from conditions.Getter, conditionsToAssert ...metav1.Condition) {
	t.Helper()

	g := NewWithT(t)
	for _, condition := range conditionsToAssert {
		actualCondition := conditions.Get(from, condition.Type)
		g.Expect(actualCondition).ToNot(BeNil(), "condition %s is missing", condition.Type)
		g.Expect(*actualCondition).To(conditions.MatchCondition(condition, conditions.IgnoreLastTransitionTime(true)))
	}
}

// asserts the conditions set on the Getter object.
func assertV1Beta1Conditions(t *testing.T, from v1beta1conditions.Getter, conditions ...*clusterv1.Condition) {
	t.Helper()

	for _, condition := range conditions {
		assertV1Beta1Condition(t, from, condition)
	}
}

// asserts whether a condition of type is set on the Getter object
// when the condition is true, asserting the reason/severity/message
// for the condition are avoided.
func assertV1Beta1Condition(t *testing.T, from v1beta1conditions.Getter, condition *clusterv1.Condition) {
	t.Helper()

	g := NewWithT(t)
	g.Expect(v1beta1conditions.Has(from, condition.Type)).To(BeTrue())

	if condition.Status == corev1.ConditionTrue {
		g.Expect(v1beta1conditions.IsTrue(from, condition.Type)).To(BeTrue())
	} else {
		conditionToBeAsserted := v1beta1conditions.Get(from, condition.Type)
		g.Expect(conditionToBeAsserted.Status).To(Equal(condition.Status))
		g.Expect(conditionToBeAsserted.Severity).To(Equal(condition.Severity))
		g.Expect(conditionToBeAsserted.Reason).To(Equal(condition.Reason))
		if condition.Message != "" {
			g.Expect(conditionToBeAsserted.Message).To(Equal(condition.Message))
		}
	}
}

func podByNodeName(o client.Object) []string {
	pod, ok := o.(*corev1.Pod)
	if !ok {
		panic(fmt.Sprintf("Expected a Pod but got a %T", o))
	}

	if pod.Spec.NodeName == "" {
		return nil
	}

	return []string{pod.Spec.NodeName}
}
