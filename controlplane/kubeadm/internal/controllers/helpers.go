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
	"strings"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bootstrapv1 "sigs.k8s.io/cluster-api/api/bootstrap/kubeadm/v1beta2"
	controlplanev1 "sigs.k8s.io/cluster-api/api/controlplane/kubeadm/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/controllers/external"
	"sigs.k8s.io/cluster-api/controlplane/kubeadm/internal"
	"sigs.k8s.io/cluster-api/internal/contract"
	topologynames "sigs.k8s.io/cluster-api/internal/topology/names"
	"sigs.k8s.io/cluster-api/internal/util/ssa"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/certs"
	v1beta1conditions "sigs.k8s.io/cluster-api/util/conditions/deprecated/v1beta1"
	"sigs.k8s.io/cluster-api/util/kubeconfig"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/cluster-api/util/secret"
)

// mandatoryMachineReadinessGates are readinessGates KCP enforces to be set on machine it owns.
var mandatoryMachineReadinessGates = []clusterv1.MachineReadinessGate{
	{ConditionType: controlplanev1.KubeadmControlPlaneMachineAPIServerPodHealthyCondition},
	{ConditionType: controlplanev1.KubeadmControlPlaneMachineControllerManagerPodHealthyCondition},
	{ConditionType: controlplanev1.KubeadmControlPlaneMachineSchedulerPodHealthyCondition},
}

// etcdMandatoryMachineReadinessGates are readinessGates KCP enforces to be set on machine it owns if etcd is managed.
var etcdMandatoryMachineReadinessGates = []clusterv1.MachineReadinessGate{
	{ConditionType: controlplanev1.KubeadmControlPlaneMachineEtcdPodHealthyCondition},
	{ConditionType: controlplanev1.KubeadmControlPlaneMachineEtcdMemberHealthyCondition},
}

func (r *KubeadmControlPlaneReconciler) reconcileKubeconfig(ctx context.Context, controlPlane *internal.ControlPlane) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	endpoint := controlPlane.Cluster.Spec.ControlPlaneEndpoint
	if endpoint.IsZero() {
		return ctrl.Result{}, nil
	}

	controllerOwnerRef := *metav1.NewControllerRef(controlPlane.KCP, controlplanev1.GroupVersion.WithKind(kubeadmControlPlaneKind))
	clusterName := util.ObjectKey(controlPlane.Cluster)
	configSecret, err := secret.GetFromNamespacedName(ctx, r.SecretCachingClient, clusterName, secret.Kubeconfig)
	switch {
	case apierrors.IsNotFound(err):
		createErr := kubeconfig.CreateSecretWithOwner(
			ctx,
			r.SecretCachingClient,
			clusterName,
			endpoint.String(),
			controllerOwnerRef,
		)
		if errors.Is(createErr, kubeconfig.ErrDependentCertificateNotFound) {
			return ctrl.Result{RequeueAfter: dependentCertRequeueAfter}, nil
		}
		// always return if we have just created in order to skip rotation checks
		return ctrl.Result{}, createErr
	case err != nil:
		return ctrl.Result{}, errors.Wrap(err, "failed to retrieve kubeconfig Secret")
	}

	if err := r.adoptKubeconfigSecret(ctx, configSecret, controlPlane.KCP); err != nil {
		return ctrl.Result{}, err
	}

	// only do rotation on owned secrets
	if !util.IsControlledBy(configSecret, controlPlane.KCP) {
		return ctrl.Result{}, nil
	}

	needsRotation, err := kubeconfig.NeedsClientCertRotation(configSecret, certs.ClientCertificateRenewalDuration)
	if err != nil {
		return ctrl.Result{}, err
	}

	if needsRotation {
		log.Info("Rotating kubeconfig secret")
		if err := kubeconfig.RegenerateSecret(ctx, r.Client, configSecret); err != nil {
			return ctrl.Result{}, errors.Wrap(err, "failed to regenerate kubeconfig")
		}
	}

	return ctrl.Result{}, nil
}

// Ensure the KubeadmConfigSecret has an owner reference to the control plane if it is not a user-provided secret.
func (r *KubeadmControlPlaneReconciler) adoptKubeconfigSecret(ctx context.Context, configSecret *corev1.Secret, kcp *controlplanev1.KubeadmControlPlane) (reterr error) {
	patchHelper, err := patch.NewHelper(configSecret, r.Client)
	if err != nil {
		return err
	}
	defer func() {
		if err := patchHelper.Patch(ctx, configSecret); err != nil {
			reterr = kerrors.NewAggregate([]error{reterr, err})
		}
	}()
	controller := metav1.GetControllerOf(configSecret)

	// If the current controller is KCP, ensure the owner reference is up to date and return early.
	// Note: This ensures secrets created prior to v1alpha4 are updated to have the correct owner reference apiVersion.
	if controller != nil && controller.Kind == kubeadmControlPlaneKind {
		configSecret.SetOwnerReferences(util.EnsureOwnerRef(configSecret.GetOwnerReferences(), *metav1.NewControllerRef(kcp, controlplanev1.GroupVersion.WithKind(kubeadmControlPlaneKind))))
		return nil
	}

	// If secret type is a CAPI-created secret ensure the owner reference is to KCP.
	if configSecret.Type == clusterv1.ClusterSecretType {
		// Remove the current controller if one exists and ensure KCP is the controller of the secret.
		if controller != nil {
			configSecret.SetOwnerReferences(util.RemoveOwnerRef(configSecret.GetOwnerReferences(), *controller))
		}
		configSecret.SetOwnerReferences(util.EnsureOwnerRef(configSecret.GetOwnerReferences(), *metav1.NewControllerRef(kcp, controlplanev1.GroupVersion.WithKind(kubeadmControlPlaneKind))))
	}
	return nil
}

func (r *KubeadmControlPlaneReconciler) reconcileExternalReference(ctx context.Context, controlPlane *internal.ControlPlane) error {
	ref := controlPlane.KCP.Spec.MachineTemplate.Spec.InfrastructureRef
	if !strings.HasSuffix(ref.Kind, clusterv1.TemplateSuffix) {
		return nil
	}

	obj, err := external.GetObjectFromContractVersionedRef(ctx, r.Client, ref, controlPlane.KCP.Namespace)
	if err != nil {
		if apierrors.IsNotFound(err) {
			controlPlane.InfraMachineTemplateIsNotFound = true
		}
		return err
	}

	// Note: We intentionally do not handle checking for the paused label on an external template reference

	desiredOwnerRef := metav1.OwnerReference{
		APIVersion: clusterv1.GroupVersion.String(),
		Kind:       "Cluster",
		Name:       controlPlane.Cluster.Name,
		UID:        controlPlane.Cluster.UID,
	}

	if util.HasExactOwnerRef(obj.GetOwnerReferences(), desiredOwnerRef) {
		return nil
	}

	patchHelper, err := patch.NewHelper(obj, r.Client)
	if err != nil {
		return err
	}

	obj.SetOwnerReferences(util.EnsureOwnerRef(obj.GetOwnerReferences(), desiredOwnerRef))

	return patchHelper.Patch(ctx, obj)
}

func (r *KubeadmControlPlaneReconciler) cloneConfigsAndGenerateMachine(ctx context.Context, cluster *clusterv1.Cluster, kcp *controlplanev1.KubeadmControlPlane, bootstrapSpec *bootstrapv1.KubeadmConfigSpec, failureDomain string) (*clusterv1.Machine, error) {
	var errs []error

	// Compute desired Machine
	machine, err := r.computeDesiredMachine(kcp, cluster, failureDomain, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create Machine: failed to compute desired Machine")
	}

	// Since the cloned resource should eventually have a controller ref for the Machine, we create an
	// OwnerReference here without the Controller field set
	infraCloneOwner := &metav1.OwnerReference{
		APIVersion: controlplanev1.GroupVersion.String(),
		Kind:       kubeadmControlPlaneKind,
		Name:       kcp.Name,
		UID:        kcp.UID,
	}

	// Clone the infrastructure template
	apiVersion, err := contract.GetAPIVersion(ctx, r.Client, kcp.Spec.MachineTemplate.Spec.InfrastructureRef.GroupKind())
	if err != nil {
		return nil, errors.Wrap(err, "failed to clone infrastructure template")
	}
	infraMachine, infraRef, err := external.CreateFromTemplate(ctx, &external.CreateFromTemplateInput{
		Client: r.Client,
		TemplateRef: &corev1.ObjectReference{
			APIVersion: apiVersion,
			Kind:       kcp.Spec.MachineTemplate.Spec.InfrastructureRef.Kind,
			Namespace:  kcp.Namespace,
			Name:       kcp.Spec.MachineTemplate.Spec.InfrastructureRef.Name,
		},
		Namespace:   kcp.Namespace,
		Name:        machine.Name,
		OwnerRef:    infraCloneOwner,
		ClusterName: cluster.Name,
		Labels:      internal.ControlPlaneMachineLabelsForCluster(kcp, cluster.Name),
		Annotations: kcp.Spec.MachineTemplate.ObjectMeta.Annotations,
	})
	if err != nil {
		// Safe to return early here since no resources have been created yet.
		v1beta1conditions.MarkFalse(kcp, controlplanev1.MachinesCreatedV1Beta1Condition, controlplanev1.InfrastructureTemplateCloningFailedV1Beta1Reason,
			clusterv1.ConditionSeverityError, "%s", err.Error())
		return nil, errors.Wrap(err, "failed to clone infrastructure template")
	}
	machine.Spec.InfrastructureRef = infraRef

	// Clone the bootstrap configuration
	bootstrapConfig, bootstrapRef, err := r.generateKubeadmConfig(ctx, kcp, cluster, bootstrapSpec, machine.Name)
	if err != nil {
		v1beta1conditions.MarkFalse(kcp, controlplanev1.MachinesCreatedV1Beta1Condition, controlplanev1.BootstrapTemplateCloningFailedV1Beta1Reason,
			clusterv1.ConditionSeverityError, "%s", err.Error())
		errs = append(errs, errors.Wrap(err, "failed to generate bootstrap config"))
	}

	// Only proceed to generating the Machine if we haven't encountered an error
	if len(errs) == 0 {
		machine.Spec.Bootstrap.ConfigRef = bootstrapRef

		if err := r.createMachine(ctx, kcp, machine); err != nil {
			v1beta1conditions.MarkFalse(kcp, controlplanev1.MachinesCreatedV1Beta1Condition, controlplanev1.MachineGenerationFailedV1Beta1Reason,
				clusterv1.ConditionSeverityError, "%s", err.Error())
			errs = append(errs, errors.Wrap(err, "failed to create Machine"))
		}
	}

	// If we encountered any errors, attempt to clean up any dangling resources
	if len(errs) > 0 {
		if err := r.cleanupFromGeneration(ctx, infraMachine, bootstrapConfig); err != nil {
			errs = append(errs, errors.Wrap(err, "failed to cleanup generated resources"))
		}
		return nil, kerrors.NewAggregate(errs)
	}

	return machine, nil
}

func (r *KubeadmControlPlaneReconciler) cleanupFromGeneration(ctx context.Context, objects ...client.Object) error {
	var errs []error

	for _, obj := range objects {
		if obj == nil {
			continue
		}
		if err := r.Client.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, errors.Wrap(err, "failed to cleanup generated resources after error"))
		}
	}

	return kerrors.NewAggregate(errs)
}

func (r *KubeadmControlPlaneReconciler) generateKubeadmConfig(ctx context.Context, kcp *controlplanev1.KubeadmControlPlane, cluster *clusterv1.Cluster, spec *bootstrapv1.KubeadmConfigSpec, name string) (*bootstrapv1.KubeadmConfig, clusterv1.ContractVersionedObjectReference, error) {
	// Create an owner reference without a controller reference because the owning controller is the machine controller
	owner := metav1.OwnerReference{
		APIVersion: controlplanev1.GroupVersion.String(),
		Kind:       kubeadmControlPlaneKind,
		Name:       kcp.Name,
		UID:        kcp.UID,
	}

	bootstrapConfig := &bootstrapv1.KubeadmConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       kcp.Namespace,
			Labels:          internal.ControlPlaneMachineLabelsForCluster(kcp, cluster.Name),
			Annotations:     kcp.Spec.MachineTemplate.ObjectMeta.Annotations,
			OwnerReferences: []metav1.OwnerReference{owner},
		},
		Spec: *spec,
	}

	if err := r.Client.Create(ctx, bootstrapConfig); err != nil {
		return nil, clusterv1.ContractVersionedObjectReference{}, errors.Wrap(err, "failed to create bootstrap configuration")
	}

	return bootstrapConfig, clusterv1.ContractVersionedObjectReference{
		APIGroup: bootstrapv1.GroupVersion.Group,
		Kind:     "KubeadmConfig",
		Name:     bootstrapConfig.GetName(),
	}, nil
}

// updateExternalObject updates the external object with the labels and annotations from KCP.
func (r *KubeadmControlPlaneReconciler) updateExternalObject(ctx context.Context, obj client.Object, kcp *controlplanev1.KubeadmControlPlane, cluster *clusterv1.Cluster) error {
	updatedObject := &unstructured.Unstructured{}
	updatedObject.SetGroupVersionKind(obj.GetObjectKind().GroupVersionKind())
	updatedObject.SetNamespace(obj.GetNamespace())
	updatedObject.SetName(obj.GetName())
	// Set the UID to ensure that Server-Side-Apply only performs an update
	// and does not perform an accidental create.
	updatedObject.SetUID(obj.GetUID())

	// Update labels
	updatedObject.SetLabels(internal.ControlPlaneMachineLabelsForCluster(kcp, cluster.Name))
	// Update annotations
	updatedObject.SetAnnotations(kcp.Spec.MachineTemplate.ObjectMeta.Annotations)

	if err := ssa.Patch(ctx, r.Client, kcpManagerName, updatedObject, ssa.WithCachingProxy{Cache: r.ssaCache, Original: obj}); err != nil {
		return errors.Wrapf(err, "failed to update %s", obj.GetObjectKind().GroupVersionKind().Kind)
	}
	return nil
}

func (r *KubeadmControlPlaneReconciler) createMachine(ctx context.Context, kcp *controlplanev1.KubeadmControlPlane, machine *clusterv1.Machine) error {
	if err := ssa.Patch(ctx, r.Client, kcpManagerName, machine); err != nil {
		return errors.Wrap(err, "failed to create Machine")
	}
	// Remove the annotation tracking that a remediation is in progress (the remediation completed when
	// the replacement machine has been created above).
	delete(kcp.Annotations, controlplanev1.RemediationInProgressAnnotation)
	return nil
}

func (r *KubeadmControlPlaneReconciler) updateMachine(ctx context.Context, machine *clusterv1.Machine, kcp *controlplanev1.KubeadmControlPlane, cluster *clusterv1.Cluster) (*clusterv1.Machine, error) {
	updatedMachine, err := r.computeDesiredMachine(kcp, cluster, machine.Spec.FailureDomain, machine)
	if err != nil {
		return nil, errors.Wrap(err, "failed to update Machine: failed to compute desired Machine")
	}

	err = ssa.Patch(ctx, r.Client, kcpManagerName, updatedMachine, ssa.WithCachingProxy{Cache: r.ssaCache, Original: machine})
	if err != nil {
		return nil, errors.Wrap(err, "failed to update Machine")
	}
	return updatedMachine, nil
}

// computeDesiredMachine computes the desired Machine.
// This Machine will be used during reconciliation to:
// * create a new Machine
// * update an existing Machine
// Because we are using Server-Side-Apply we always have to calculate the full object.
// There are small differences in how we calculate the Machine depending on if it
// is a create or update. Example: for a new Machine we have to calculate a new name,
// while for an existing Machine we have to use the name of the existing Machine.
func (r *KubeadmControlPlaneReconciler) computeDesiredMachine(kcp *controlplanev1.KubeadmControlPlane, cluster *clusterv1.Cluster, failureDomain string, existingMachine *clusterv1.Machine) (*clusterv1.Machine, error) {
	var machineName string
	var machineUID types.UID
	var version string
	annotations := map[string]string{}
	if existingMachine == nil {
		// Creating a new machine
		nameTemplate := "{{ .kubeadmControlPlane.name }}-{{ .random }}"
		if kcp.Spec.MachineNaming.Template != "" {
			nameTemplate = kcp.Spec.MachineNaming.Template
			if !strings.Contains(nameTemplate, "{{ .random }}") {
				return nil, errors.New("cannot generate Machine name: {{ .random }} is missing in machineNaming.template")
			}
		}
		generatedMachineName, err := topologynames.KCPMachineNameGenerator(nameTemplate, cluster.Name, kcp.Name).GenerateName()
		if err != nil {
			return nil, errors.Wrap(err, "failed to generate Machine name")
		}
		machineName = generatedMachineName
		version = kcp.Spec.Version

		// Machine's bootstrap config may be missing ClusterConfiguration if it is not the first machine in the control plane.
		// We store ClusterConfiguration as annotation here to detect any changes in KCP ClusterConfiguration and rollout the machine if any.
		// Nb. This annotation is read when comparing the KubeadmConfig to check if a machine needs to be rolled out.
		clusterConfigurationAnnotation, err := internal.ClusterConfigurationToMachineAnnotationValue(&kcp.Spec.KubeadmConfigSpec.ClusterConfiguration)
		if err != nil {
			return nil, err
		}
		annotations[controlplanev1.KubeadmClusterConfigurationAnnotation] = clusterConfigurationAnnotation

		// In case this machine is being created as a consequence of a remediation, then add an annotation
		// tracking remediating data.
		// NOTE: This is required in order to track remediation retries.
		if remediationData, ok := kcp.Annotations[controlplanev1.RemediationInProgressAnnotation]; ok {
			annotations[controlplanev1.RemediationForAnnotation] = remediationData
		}
	} else {
		// Updating an existing machine
		machineName = existingMachine.Name
		machineUID = existingMachine.UID
		version = existingMachine.Spec.Version

		// For existing machine only set the ClusterConfiguration annotation if the machine already has it.
		// We should not add the annotation if it was missing in the first place because we do not have enough
		// information.
		if clusterConfigurationAnnotation, ok := existingMachine.Annotations[controlplanev1.KubeadmClusterConfigurationAnnotation]; ok {
			// In case the annotation is outdated, update it.
			if internal.ClusterConfigurationAnnotationFromMachineIsOutdated(clusterConfigurationAnnotation) {
				clusterConfiguration, err := internal.ClusterConfigurationFromMachine(existingMachine)
				if err != nil {
					return nil, err
				}

				clusterConfigurationAnnotation, err = internal.ClusterConfigurationToMachineAnnotationValue(clusterConfiguration)
				if err != nil {
					return nil, err
				}
			}
			annotations[controlplanev1.KubeadmClusterConfigurationAnnotation] = clusterConfigurationAnnotation
		}

		// If the machine already has remediation data then preserve it.
		// NOTE: This is required in order to track remediation retries.
		if remediationData, ok := existingMachine.Annotations[controlplanev1.RemediationForAnnotation]; ok {
			annotations[controlplanev1.RemediationForAnnotation] = remediationData
		}
	}
	// Setting pre-terminate hook so we can later remove the etcd member right before Machine termination
	// (i.e. before InfraMachine deletion).
	annotations[controlplanev1.PreTerminateHookCleanupAnnotation] = ""

	// Construct the basic Machine.
	desiredMachine := &clusterv1.Machine{
		TypeMeta: metav1.TypeMeta{
			APIVersion: clusterv1.GroupVersion.String(),
			Kind:       "Machine",
		},
		ObjectMeta: metav1.ObjectMeta{
			UID:       machineUID,
			Name:      machineName,
			Namespace: kcp.Namespace,
			// Note: by setting the ownerRef on creation we signal to the Machine controller that this is not a stand-alone Machine.
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(kcp, controlplanev1.GroupVersion.WithKind(kubeadmControlPlaneKind)),
			},
			Labels:      map[string]string{},
			Annotations: map[string]string{},
		},
		Spec: clusterv1.MachineSpec{
			ClusterName:   cluster.Name,
			Version:       version,
			FailureDomain: failureDomain,
		},
	}

	// Set the in-place mutable fields.
	// When we create a new Machine we will just create the Machine with those fields.
	// When we update an existing Machine will we update the fields on the existing Machine (in-place mutate).

	// Set labels
	desiredMachine.Labels = internal.ControlPlaneMachineLabelsForCluster(kcp, cluster.Name)

	// Set annotations
	// Add the annotations from the MachineTemplate.
	// Note: we intentionally don't use the map directly to ensure we don't modify the map in KCP.
	for k, v := range kcp.Spec.MachineTemplate.ObjectMeta.Annotations {
		desiredMachine.Annotations[k] = v
	}
	for k, v := range annotations {
		desiredMachine.Annotations[k] = v
	}

	// Set other in-place mutable fields
	desiredMachine.Spec.Deletion.NodeDrainTimeoutSeconds = kcp.Spec.MachineTemplate.Spec.Deletion.NodeDrainTimeoutSeconds
	desiredMachine.Spec.Deletion.NodeDeletionTimeoutSeconds = kcp.Spec.MachineTemplate.Spec.Deletion.NodeDeletionTimeoutSeconds
	desiredMachine.Spec.Deletion.NodeVolumeDetachTimeoutSeconds = kcp.Spec.MachineTemplate.Spec.Deletion.NodeVolumeDetachTimeoutSeconds

	// Note: We intentionally don't set "minReadySeconds" on Machines because we consider it enough to have machine availability driven by readiness of control plane components.
	if existingMachine != nil {
		desiredMachine.Spec.InfrastructureRef = existingMachine.Spec.InfrastructureRef
		desiredMachine.Spec.Bootstrap.ConfigRef = existingMachine.Spec.Bootstrap.ConfigRef
	}

	// Set machines readiness gates
	allReadinessGates := []clusterv1.MachineReadinessGate{}
	allReadinessGates = append(allReadinessGates, mandatoryMachineReadinessGates...)
	isEtcdManaged := !kcp.Spec.KubeadmConfigSpec.ClusterConfiguration.Etcd.External.IsDefined()
	if isEtcdManaged {
		allReadinessGates = append(allReadinessGates, etcdMandatoryMachineReadinessGates...)
	}
	allReadinessGates = append(allReadinessGates, kcp.Spec.MachineTemplate.Spec.ReadinessGates...)

	desiredMachine.Spec.ReadinessGates = []clusterv1.MachineReadinessGate{}
	knownGates := sets.Set[string]{}
	for _, gate := range allReadinessGates {
		if knownGates.Has(gate.ConditionType) {
			continue
		}
		desiredMachine.Spec.ReadinessGates = append(desiredMachine.Spec.ReadinessGates, gate)
		knownGates.Insert(gate.ConditionType)
	}

	return desiredMachine, nil
}
