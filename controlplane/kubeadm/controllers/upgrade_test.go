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

package controllers

import (
	"testing"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha4"
	bootstrapv1 "sigs.k8s.io/cluster-api/bootstrap/kubeadm/api/v1alpha4"
	controlplanev1 "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1alpha4"
	"sigs.k8s.io/cluster-api/controlplane/kubeadm/internal"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"fmt"
)

func TestKubeadmControlPlaneReconciler_upgradeControlPlane(t *testing.T) {
	t.Run("upgrade control plane", func(t *testing.T) {
		g := NewWithT(t)

		cluster, kcp, genericMachineTemplate := createClusterWithControlPlane()
		cluster.Spec.ControlPlaneEndpoint.Host = "nodomain.example.com"
		cluster.Spec.ControlPlaneEndpoint.Port = 6443
		kcp.Spec.Version = "v1.17.3"
		kcp.Spec.KubeadmConfigSpec.ClusterConfiguration = nil
		kcp.Spec.Replicas = pointer.Int32Ptr(1)

		fakeClient := newFakeClient(g, cluster.DeepCopy(), kcp.DeepCopy(), genericMachineTemplate.DeepCopy())

		r := &KubeadmControlPlaneReconciler{
			Client:   fakeClient,
			recorder: record.NewFakeRecorder(32),
			managementCluster: &fakeManagementCluster{
				Management: &internal.Management{Client: fakeClient},
				Workload: fakeWorkloadCluster{
					Status:              internal.ClusterStatus{Nodes: 1},
					ControlPlaneHealthy: true,
					EtcdHealthy:         true,
				},
			},
			managementClusterUncached: &fakeManagementCluster{
				Management: &internal.Management{Client: fakeClient},
				Workload: fakeWorkloadCluster{
					Status:              internal.ClusterStatus{Nodes: 1},
					ControlPlaneHealthy: true,
					EtcdHealthy:         true,
				},
			},
		}
		controlPlane := &internal.ControlPlane{
			KCP:      kcp,
			Cluster:  cluster,
			Machines: nil,
		}

		result, err := r.initializeControlPlane(ctx, cluster, kcp, controlPlane)
		g.Expect(result).To(Equal(ctrl.Result{Requeue: true}))
		g.Expect(err).NotTo(HaveOccurred())

		// initial setup
		initialMachine := &clusterv1.MachineList{}
		g.Expect(fakeClient.List(ctx, initialMachine, client.InNamespace(cluster.Namespace))).To(Succeed())
		g.Expect(initialMachine.Items).To(HaveLen(1))

		// change the KCP spec so the machine becomes outdated
		kcp.Spec.Version = "v1.17.4"

		// run upgrade the first time, expect we scale up
		needingUpgrade := internal.NewFilterableMachineCollectionFromMachineList(initialMachine)
		controlPlane.Machines = needingUpgrade
		result, err = r.upgradeControlPlane(ctx, cluster, kcp, controlPlane, needingUpgrade)
		g.Expect(result).To(Equal(ctrl.Result{Requeue: true}))
		g.Expect(err).To(BeNil())
		bothMachines := &clusterv1.MachineList{}
		g.Expect(fakeClient.List(ctx, bothMachines, client.InNamespace(cluster.Namespace))).To(Succeed())
		g.Expect(bothMachines.Items).To(HaveLen(2))

		// run upgrade a second time, simulate that the node has not appeared yet but the machine exists
		r.managementCluster.(*fakeManagementCluster).Workload.ControlPlaneHealthy = false
		// Unhealthy control plane will be detected during reconcile loop and upgrade will never be called.
		_, err = r.reconcile(ctx, cluster, kcp)
		g.Expect(err).To(HaveOccurred())
		g.Expect(fakeClient.List(ctx, bothMachines, client.InNamespace(cluster.Namespace))).To(Succeed())
		g.Expect(bothMachines.Items).To(HaveLen(2))

		controlPlane.Machines = internal.NewFilterableMachineCollectionFromMachineList(bothMachines)

		// manually increase number of nodes, make control plane healthy again
		r.managementCluster.(*fakeManagementCluster).Workload.Status.Nodes++
		r.managementCluster.(*fakeManagementCluster).Workload.ControlPlaneHealthy = true

		// run upgrade the second time, expect we scale down
		result, err = r.upgradeControlPlane(ctx, cluster, kcp, controlPlane, controlPlane.Machines)
		g.Expect(err).To(BeNil())
		g.Expect(result).To(Equal(ctrl.Result{Requeue: true}))
		finalMachine := &clusterv1.MachineList{}
		g.Expect(fakeClient.List(ctx, finalMachine, client.InNamespace(cluster.Namespace))).To(Succeed())
		g.Expect(finalMachine.Items).To(HaveLen(1))

		// assert that the deleted machine is the oldest, initial machine
		g.Expect(finalMachine.Items[0].Name).ToNot(Equal(initialMachine.Items[0].Name))
		g.Expect(finalMachine.Items[0].CreationTimestamp.Time).To(BeTemporally(">", initialMachine.Items[0].CreationTimestamp.Time))
	})
	t.Run("upgrade control plane with scale up RolloutStrategy", func(t *testing.T) {
		g := NewWithT(t)

		cluster, kcp, genericMachineTemplate := createClusterWithControlPlane()
		cluster.Spec.ControlPlaneEndpoint.Host = "nodomain.example.com"
		cluster.Spec.ControlPlaneEndpoint.Port = 6443
		kcp.Spec.Version = "v1.17.3"
		kcp.Spec.KubeadmConfigSpec.ClusterConfiguration = nil
		kcp.Spec.RolloutStrategy.Type = controlplanev1.ScaleUpRolloutType
		kcp.Spec.Replicas = pointer.Int32Ptr(1)

		fakeClient := newFakeClient(g, cluster.DeepCopy(), kcp.DeepCopy(), genericMachineTemplate.DeepCopy())

		r := &KubeadmControlPlaneReconciler{
			Client:   fakeClient,
			recorder: record.NewFakeRecorder(32),
			managementCluster: &fakeManagementCluster{
				Management: &internal.Management{Client: fakeClient},
				Workload: fakeWorkloadCluster{
					Status:              internal.ClusterStatus{Nodes: 1},
					ControlPlaneHealthy: true,
					EtcdHealthy:         true,
				},
			},
			managementClusterUncached: &fakeManagementCluster{
				Management: &internal.Management{Client: fakeClient},
				Workload: fakeWorkloadCluster{
					Status:              internal.ClusterStatus{Nodes: 1},
					ControlPlaneHealthy: true,
					EtcdHealthy:         true,
				},
			},
		}
		controlPlane := &internal.ControlPlane{
			KCP:      kcp,
			Cluster:  cluster,
			Machines: nil,
		}

		result, err := r.initializeControlPlane(ctx, cluster, kcp, controlPlane)
		g.Expect(result).To(Equal(ctrl.Result{Requeue: true}))
		g.Expect(err).NotTo(HaveOccurred())

		// initial setup
		initialMachine := &clusterv1.MachineList{}
		g.Expect(fakeClient.List(ctx, initialMachine, client.InNamespace(cluster.Namespace))).To(Succeed())
		g.Expect(initialMachine.Items).To(HaveLen(1))

		// change the KCP spec so the machine becomes outdated
		kcp.Spec.Version = "v1.17.4"

		// run upgrade the first time, expect we scale up
		needingUpgrade := internal.NewFilterableMachineCollectionFromMachineList(initialMachine)
		controlPlane.Machines = needingUpgrade
		result, err = r.upgradeControlPlane(ctx, cluster, kcp, controlPlane, needingUpgrade)
		g.Expect(result).To(Equal(ctrl.Result{Requeue: true}))
		g.Expect(err).To(BeNil())
		bothMachines := &clusterv1.MachineList{}
		g.Expect(fakeClient.List(ctx, bothMachines, client.InNamespace(cluster.Namespace))).To(Succeed())
		g.Expect(bothMachines.Items).To(HaveLen(2))

		// run upgrade a second time, simulate that the node has not appeared yet but the machine exists
		r.managementCluster.(*fakeManagementCluster).Workload.ControlPlaneHealthy = false
		// Unhealthy control plane will be detected during reconcile loop and upgrade will never be called.
		_, err = r.reconcile(ctx, cluster, kcp)
		g.Expect(err).To(HaveOccurred())
		g.Expect(fakeClient.List(ctx, bothMachines, client.InNamespace(cluster.Namespace))).To(Succeed())
		g.Expect(bothMachines.Items).To(HaveLen(2))

		controlPlane.Machines = internal.NewFilterableMachineCollectionFromMachineList(bothMachines)

		// manually increase number of nodes, make control plane healthy again
		r.managementCluster.(*fakeManagementCluster).Workload.Status.Nodes++
		r.managementCluster.(*fakeManagementCluster).Workload.ControlPlaneHealthy = true

		// run upgrade the second time, expect we scale down
		result, err = r.upgradeControlPlane(ctx, cluster, kcp, controlPlane, controlPlane.Machines)
		g.Expect(err).To(BeNil())
		g.Expect(result).To(Equal(ctrl.Result{Requeue: true}))
		finalMachine := &clusterv1.MachineList{}
		g.Expect(fakeClient.List(ctx, finalMachine, client.InNamespace(cluster.Namespace))).To(Succeed())
		g.Expect(finalMachine.Items).To(HaveLen(1))

		// assert that the deleted machine is the oldest, initial machine
		g.Expect(finalMachine.Items[0].Name).ToNot(Equal(initialMachine.Items[0].Name))
		g.Expect(finalMachine.Items[0].CreationTimestamp.Time).To(BeTemporally(">", initialMachine.Items[0].CreationTimestamp.Time))
	})
	t.Run("upgrade control plane with scale down RolloutStrategy", func(t *testing.T) {
		version := "v1.17.3"
		g := NewWithT(t)

		cluster, kcp, tmpl := createClusterWithControlPlane()
		cluster.Spec.ControlPlaneEndpoint.Host = "nodomain.example.com1"
		cluster.Spec.ControlPlaneEndpoint.Port = 6443
		kcp.Spec.Version = version
		kcp.Spec.KubeadmConfigSpec.ClusterConfiguration = nil
		kcp.Spec.RolloutStrategy.Type = controlplanev1.ScaleDownRolloutType
		kcp.Spec.Replicas = pointer.Int32Ptr(3)

		fmc := &fakeManagementCluster{
			Machines: internal.FilterableMachineCollection{},
			Workload: fakeWorkloadCluster{
				Status:              internal.ClusterStatus{Nodes: 3},
				ControlPlaneHealthy: true,
				EtcdHealthy:         true,
			},
		}
		objs := []client.Object{cluster.DeepCopy(), kcp.DeepCopy(), tmpl.DeepCopy()}
		for i := 0; i < 3; i++ {
			name := fmt.Sprintf("test-%d", i)
			m := &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: cluster.Namespace,
					Name:      name,
					Labels:    internal.ControlPlaneLabelsForCluster(cluster.Name),
				},
				Spec: clusterv1.MachineSpec{
					Bootstrap: clusterv1.Bootstrap{
						ConfigRef: &corev1.ObjectReference{
							APIVersion: bootstrapv1.GroupVersion.String(),
							Kind:       "KubeadmConfig",
							Name:       name,
						},
					},
					Version: &version,
				},
			}
			cfg := &bootstrapv1.KubeadmConfig{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: cluster.Namespace,
					Name:      name,
				},
			}
			objs = append(objs, m, cfg)
			fmc.Machines.Insert(m)
		}
		fakeClient := newFakeClient(g, objs...)
		fmc.Reader = fakeClient
		r := &KubeadmControlPlaneReconciler{
			Client:                    fakeClient,
			managementCluster:         fmc,
			managementClusterUncached: fmc,
		}

		controlPlane := &internal.ControlPlane{
			KCP:      kcp,
			Cluster:  cluster,
			Machines: nil,
		}

		g.Expect(r.reconcile(ctx, cluster, kcp)).To(Equal(ctrl.Result{}))
		machineList := &clusterv1.MachineList{}
		g.Expect(fakeClient.List(ctx, machineList, client.InNamespace(cluster.Namespace))).To(Succeed())
		g.Expect(machineList.Items).To(HaveLen(3))

		// change the KCP spec so the machine becomes outdated
		kcp.Spec.Version = "v1.17.4"

		// run upgrade the first time, expect we scale down
		needingUpgrade := internal.NewFilterableMachineCollectionFromMachineList(machineList)
		controlPlane.Machines = needingUpgrade
		result, err := r.upgradeControlPlane(ctx, cluster, kcp, controlPlane, needingUpgrade)
		g.Expect(result).To(Equal(ctrl.Result{Requeue: true}))
		g.Expect(err).To(BeNil())
		bothMachines := &clusterv1.MachineList{}
		g.Expect(fakeClient.List(ctx, bothMachines, client.InNamespace(cluster.Namespace))).To(Succeed())
		g.Expect(bothMachines.Items).To(HaveLen(2))

		controlPlane.Machines = internal.NewFilterableMachineCollectionFromMachineList(bothMachines)
		r.managementCluster.(*fakeManagementCluster).Workload.Status.Nodes--

		// run upgrade the second time, expect we scale up and new machine has a upgraded Version
		result, err = r.upgradeControlPlane(ctx, cluster, kcp, controlPlane, controlPlane.Machines)
		g.Expect(err).To(BeNil())
		g.Expect(result).To(Equal(ctrl.Result{Requeue: true}))
		finalMachines := &clusterv1.MachineList{}
		g.Expect(fakeClient.List(ctx, finalMachines, client.InNamespace(cluster.Namespace))).To(Succeed())
		g.Expect(finalMachines.Items).To(HaveLen(3))
		g.Expect(*finalMachines.Items[0].Spec.Version).To(ContainSubstring("v1.17.4"))
	})
	t.Run("Scale down should fail if Replica count is less than 3", func(t *testing.T) {
		g := NewWithT(t)

		cluster, kcp, genericMachineTemplate := createClusterWithControlPlane()
		cluster.Spec.ControlPlaneEndpoint.Host = "nodomain.example.com"
		cluster.Spec.ControlPlaneEndpoint.Port = 6443
		kcp.Spec.Version = "v1.17.3"
		kcp.Spec.RolloutStrategy.Type = controlplanev1.ScaleDownRolloutType
		kcp.Spec.KubeadmConfigSpec.ClusterConfiguration = nil
		kcp.Spec.Replicas = pointer.Int32Ptr(1)

		fakeClient := newFakeClient(g, cluster.DeepCopy(), kcp.DeepCopy(), genericMachineTemplate.DeepCopy())

		r := &KubeadmControlPlaneReconciler{
			Client:   fakeClient,
			recorder: record.NewFakeRecorder(32),
			managementCluster: &fakeManagementCluster{
				Management: &internal.Management{Client: fakeClient},
				Workload: fakeWorkloadCluster{
					Status:              internal.ClusterStatus{Nodes: 1},
					ControlPlaneHealthy: true,
					EtcdHealthy:         true,
				},
			},
			managementClusterUncached: &fakeManagementCluster{
				Management: &internal.Management{Client: fakeClient},
				Workload: fakeWorkloadCluster{
					Status:              internal.ClusterStatus{Nodes: 1},
					ControlPlaneHealthy: true,
					EtcdHealthy:         true,
				},
			},
		}
		controlPlane := &internal.ControlPlane{
			KCP:      kcp,
			Cluster:  cluster,
			Machines: nil,
		}

		result, err := r.initializeControlPlane(ctx, cluster, kcp, controlPlane)
		g.Expect(result).To(Equal(ctrl.Result{Requeue: true}))
		g.Expect(err).NotTo(HaveOccurred())

		// initial setup
		initialMachine := &clusterv1.MachineList{}
		g.Expect(fakeClient.List(ctx, initialMachine, client.InNamespace(cluster.Namespace))).To(Succeed())
		g.Expect(initialMachine.Items).To(HaveLen(1))

		// change the KCP spec so the machine becomes outdated
		kcp.Spec.Version = "v1.17.4"

		// run upgrade, expect not to be unable to scale down
		needingUpgrade := internal.NewFilterableMachineCollectionFromMachineList(initialMachine)
		controlPlane.Machines = needingUpgrade
		result, err = r.upgradeControlPlane(ctx, cluster, kcp, controlPlane, needingUpgrade)
		g.Expect(result).To(Equal(ctrl.Result{}))
		g.Expect(err).To(BeNil())

		// verify that controlplanev1.MachinesSpecUpToDateCondition is set to false
		g.Expect(conditions.IsFalse(kcp, controlplanev1.MachinesSpecUpToDateCondition)).To(BeTrue())

		// make sure that kcp does not scale
		machines := &clusterv1.MachineList{}
		g.Expect(fakeClient.List(ctx, machines, client.InNamespace(cluster.Namespace))).To(Succeed())
		g.Expect(machines.Items).To(HaveLen(1))
	})
}

type machineOpt func(*clusterv1.Machine)

func machine(name string, opts ...machineOpt) *clusterv1.Machine {
	m := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}
