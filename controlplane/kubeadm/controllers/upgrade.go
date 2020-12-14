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
	"context"

	"github.com/blang/semver"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1alpha4"
	controlplanev1 "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1alpha4"
	"sigs.k8s.io/cluster-api/controlplane/kubeadm/internal"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
)

func (r *KubeadmControlPlaneReconciler) upgradeControlPlane(
	ctx context.Context,
	cluster *clusterv1.Cluster,
	kcp *controlplanev1.KubeadmControlPlane,
	controlPlane *internal.ControlPlane,
	machinesRequireUpgrade internal.FilterableMachineCollection,
) (ctrl.Result, error) {
	logger := controlPlane.Logger()

	// TODO: handle reconciliation of etcd members and kubeadm config in case they get out of sync with cluster

	workloadCluster, err := r.managementCluster.GetWorkloadCluster(ctx, util.ObjectKey(cluster))
	if err != nil {
		logger.Error(err, "failed to get remote client for workload cluster", "cluster key", util.ObjectKey(cluster))
		return ctrl.Result{}, err
	}

	parsedVersion, err := semver.ParseTolerant(kcp.Spec.Version)
	if err != nil {
		return ctrl.Result{}, errors.Wrapf(err, "failed to parse kubernetes version %q", kcp.Spec.Version)
	}

	if err := workloadCluster.ReconcileKubeletRBACRole(ctx, parsedVersion); err != nil {
		return ctrl.Result{}, errors.Wrap(err, "failed to reconcile the remote kubelet RBAC role")
	}

	if err := workloadCluster.ReconcileKubeletRBACBinding(ctx, parsedVersion); err != nil {
		return ctrl.Result{}, errors.Wrap(err, "failed to reconcile the remote kubelet RBAC binding")
	}

	// Ensure kubeadm cluster role  & bindings for v1.18+
	// as per https://github.com/kubernetes/kubernetes/commit/b117a928a6c3f650931bdac02a41fca6680548c4
	if err := workloadCluster.AllowBootstrapTokensToGetNodes(ctx); err != nil {
		return ctrl.Result{}, errors.Wrap(err, "failed to set role and role binding for kubeadm")
	}

	if err := workloadCluster.UpdateKubernetesVersionInKubeadmConfigMap(ctx, parsedVersion); err != nil {
		return ctrl.Result{}, errors.Wrap(err, "failed to update the kubernetes version in the kubeadm config map")
	}

	if kcp.Spec.KubeadmConfigSpec.ClusterConfiguration != nil {
		imageRepository := kcp.Spec.KubeadmConfigSpec.ClusterConfiguration.ImageRepository
		if err := workloadCluster.UpdateImageRepositoryInKubeadmConfigMap(ctx, imageRepository); err != nil {
			return ctrl.Result{}, errors.Wrap(err, "failed to update the image repository in the kubeadm config map")
		}
	}

	if kcp.Spec.KubeadmConfigSpec.ClusterConfiguration != nil && kcp.Spec.KubeadmConfigSpec.ClusterConfiguration.Etcd.Local != nil {
		meta := kcp.Spec.KubeadmConfigSpec.ClusterConfiguration.Etcd.Local.ImageMeta
		if err := workloadCluster.UpdateEtcdVersionInKubeadmConfigMap(ctx, meta.ImageRepository, meta.ImageTag); err != nil {
			return ctrl.Result{}, errors.Wrap(err, "failed to update the etcd version in the kubeadm config map")
		}
	}

	if err := workloadCluster.UpdateKubeletConfigMap(ctx, parsedVersion); err != nil {
		return ctrl.Result{}, errors.Wrap(err, "failed to upgrade kubelet config map")
	}

	status, err := workloadCluster.ClusterStatus(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	if rolloutStrategyIsSet(kcp) && isRollingUpgradeStrategy(kcp) {

		if controlPlaneIsSetToScaleDown(kcp) {
			// Scale down
			if *kcp.Spec.Replicas < 3 {
				logger.Info("Unable to scale down the controlplane")
				conditions.MarkFalse(kcp, controlplanev1.MachinesSpecUpToDateCondition, controlplanev1.UnableToScaleDownReason, clusterv1.ConditionSeverityWarning, "Unable to scale down the control plane")
				return ctrl.Result{}, nil
			}

			if status.Nodes >= *kcp.Spec.Replicas {
				return r.scaleDownControlPlane(ctx, cluster, kcp, controlPlane, machinesRequireUpgrade)
			}

			if status.Nodes < *kcp.Spec.Replicas {
				return r.scaleUpControlPlane(ctx, cluster, kcp, controlPlane)
			}
		} else {
			// Scale up
			if status.Nodes <= *kcp.Spec.Replicas {
				return r.scaleUpControlPlane(ctx, cluster, kcp, controlPlane)
			}
			return r.scaleDownControlPlane(ctx, cluster, kcp, controlPlane, machinesRequireUpgrade)
		}
	}

	if status.Nodes <= *kcp.Spec.Replicas {
		// scaleUp ensures that we don't continue scaling up while waiting for Machines to have NodeRefs
		return r.scaleUpControlPlane(ctx, cluster, kcp, controlPlane)

	}
	return r.scaleDownControlPlane(ctx, cluster, kcp, controlPlane, machinesRequireUpgrade)
}

func isRollingUpgradeStrategy(kcp *controlplanev1.KubeadmControlPlane) bool {
	if kcp.Spec.RolloutStrategy.Type == controlplanev1.RollingUpdateStrategyType {
		return true
	}
	return false
}

func rolloutStrategyIsSet(kcp *controlplanev1.KubeadmControlPlane) bool {
	if kcp.Spec.RolloutStrategy != nil {
		return true
	}
	return false
}

func controlPlaneIsSetToScaleDown(kcp *controlplanev1.KubeadmControlPlane) bool {
	ios1, ios0 := getIntValues()
	if *kcp.Spec.RolloutStrategy.RollingUpdate.MaxUnavailable == ios1 && *kcp.Spec.RolloutStrategy.RollingUpdate.MaxSurge == ios0 {
		return true
	}
	return false
}

func getIntValues() (intstr.IntOrString, intstr.IntOrString) {
	ios1 := intstr.FromInt(1)
	ios0 := intstr.FromInt(0)
	return ios1, ios0
}
