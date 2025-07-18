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

package v1beta1

import (
	apiconversion "k8s.io/apimachinery/pkg/conversion"
	"sigs.k8s.io/controller-runtime/pkg/conversion"

	clusterv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	infraexpv1 "sigs.k8s.io/cluster-api/test/infrastructure/docker/exp/api/v1beta2"
	utilconversion "sigs.k8s.io/cluster-api/util/conversion"
)

func (src *DockerMachinePool) ConvertTo(dstRaw conversion.Hub) error {
	dst := dstRaw.(*infraexpv1.DockerMachinePool)

	if err := Convert_v1beta1_DockerMachinePool_To_v1beta2_DockerMachinePool(src, dst, nil); err != nil {
		return err
	}

	// Reset conditions from autogenerated conversions
	// NOTE: v1beta1 conditions should not be automatically be converted into v1beta2 conditions.
	dst.Status.Conditions = nil
	if src.Status.Conditions != nil {
		dst.Status.Deprecated = &infraexpv1.DockerMachinePoolDeprecatedStatus{}
		dst.Status.Deprecated.V1Beta1 = &infraexpv1.DockerMachinePoolV1Beta1DeprecatedStatus{}
		clusterv1beta1.Convert_v1beta1_Conditions_To_v1beta2_Deprecated_V1Beta1_Conditions(&src.Status.Conditions, &dst.Status.Deprecated.V1Beta1.Conditions)
	}

	restored := &infraexpv1.DockerMachinePool{}
	if ok, err := utilconversion.UnmarshalData(src, restored); err != nil || !ok {
		return err
	}

	dst.Status.Conditions = restored.Status.Conditions

	return nil
}

func (dst *DockerMachinePool) ConvertFrom(srcRaw conversion.Hub) error {
	src := srcRaw.(*infraexpv1.DockerMachinePool)

	if err := Convert_v1beta2_DockerMachinePool_To_v1beta1_DockerMachinePool(src, dst, nil); err != nil {
		return err
	}

	dst.Status.Conditions = nil
	if src.Status.Deprecated != nil && src.Status.Deprecated.V1Beta1 != nil && src.Status.Deprecated.V1Beta1.Conditions != nil {
		clusterv1beta1.Convert_v1beta2_Deprecated_V1Beta1_Conditions_To_v1beta1_Conditions(&src.Status.Deprecated.V1Beta1.Conditions, &dst.Status.Conditions)
	}

	return utilconversion.MarshalData(src, dst)
}

func (src *DockerMachinePoolTemplate) ConvertTo(dstRaw conversion.Hub) error {
	dst := dstRaw.(*infraexpv1.DockerMachinePoolTemplate)

	return Convert_v1beta1_DockerMachinePoolTemplate_To_v1beta2_DockerMachinePoolTemplate(src, dst, nil)
}

func (dst *DockerMachinePoolTemplate) ConvertFrom(srcRaw conversion.Hub) error {
	src := srcRaw.(*infraexpv1.DockerMachinePoolTemplate)

	return Convert_v1beta2_DockerMachinePoolTemplate_To_v1beta1_DockerMachinePoolTemplate(src, dst, nil)
}

func Convert_v1beta2_DockerMachinePoolStatus_To_v1beta1_DockerMachinePoolStatus(in *infraexpv1.DockerMachinePoolStatus, out *DockerMachinePoolStatus, s apiconversion.Scope) error {
	return autoConvert_v1beta2_DockerMachinePoolStatus_To_v1beta1_DockerMachinePoolStatus(in, out, s)
}
