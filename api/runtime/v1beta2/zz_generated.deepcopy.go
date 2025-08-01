//go:build !ignore_autogenerated

/*
Copyright The Kubernetes Authors.

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

// Code generated by controller-gen. DO NOT EDIT.

package v1beta2

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	corev1beta2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ClientConfig) DeepCopyInto(out *ClientConfig) {
	*out = *in
	in.Service.DeepCopyInto(&out.Service)
	if in.CABundle != nil {
		in, out := &in.CABundle, &out.CABundle
		*out = make([]byte, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ClientConfig.
func (in *ClientConfig) DeepCopy() *ClientConfig {
	if in == nil {
		return nil
	}
	out := new(ClientConfig)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ExtensionConfig) DeepCopyInto(out *ExtensionConfig) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ExtensionConfig.
func (in *ExtensionConfig) DeepCopy() *ExtensionConfig {
	if in == nil {
		return nil
	}
	out := new(ExtensionConfig)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ExtensionConfig) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ExtensionConfigDeprecatedStatus) DeepCopyInto(out *ExtensionConfigDeprecatedStatus) {
	*out = *in
	if in.V1Beta1 != nil {
		in, out := &in.V1Beta1, &out.V1Beta1
		*out = new(ExtensionConfigV1Beta1DeprecatedStatus)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ExtensionConfigDeprecatedStatus.
func (in *ExtensionConfigDeprecatedStatus) DeepCopy() *ExtensionConfigDeprecatedStatus {
	if in == nil {
		return nil
	}
	out := new(ExtensionConfigDeprecatedStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ExtensionConfigList) DeepCopyInto(out *ExtensionConfigList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]ExtensionConfig, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ExtensionConfigList.
func (in *ExtensionConfigList) DeepCopy() *ExtensionConfigList {
	if in == nil {
		return nil
	}
	out := new(ExtensionConfigList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ExtensionConfigList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ExtensionConfigSpec) DeepCopyInto(out *ExtensionConfigSpec) {
	*out = *in
	in.ClientConfig.DeepCopyInto(&out.ClientConfig)
	if in.NamespaceSelector != nil {
		in, out := &in.NamespaceSelector, &out.NamespaceSelector
		*out = new(v1.LabelSelector)
		(*in).DeepCopyInto(*out)
	}
	if in.Settings != nil {
		in, out := &in.Settings, &out.Settings
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ExtensionConfigSpec.
func (in *ExtensionConfigSpec) DeepCopy() *ExtensionConfigSpec {
	if in == nil {
		return nil
	}
	out := new(ExtensionConfigSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ExtensionConfigStatus) DeepCopyInto(out *ExtensionConfigStatus) {
	*out = *in
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]v1.Condition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.Handlers != nil {
		in, out := &in.Handlers, &out.Handlers
		*out = make([]ExtensionHandler, len(*in))
		copy(*out, *in)
	}
	if in.Deprecated != nil {
		in, out := &in.Deprecated, &out.Deprecated
		*out = new(ExtensionConfigDeprecatedStatus)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ExtensionConfigStatus.
func (in *ExtensionConfigStatus) DeepCopy() *ExtensionConfigStatus {
	if in == nil {
		return nil
	}
	out := new(ExtensionConfigStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ExtensionConfigV1Beta1DeprecatedStatus) DeepCopyInto(out *ExtensionConfigV1Beta1DeprecatedStatus) {
	*out = *in
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make(corev1beta2.Conditions, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ExtensionConfigV1Beta1DeprecatedStatus.
func (in *ExtensionConfigV1Beta1DeprecatedStatus) DeepCopy() *ExtensionConfigV1Beta1DeprecatedStatus {
	if in == nil {
		return nil
	}
	out := new(ExtensionConfigV1Beta1DeprecatedStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ExtensionHandler) DeepCopyInto(out *ExtensionHandler) {
	*out = *in
	out.RequestHook = in.RequestHook
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ExtensionHandler.
func (in *ExtensionHandler) DeepCopy() *ExtensionHandler {
	if in == nil {
		return nil
	}
	out := new(ExtensionHandler)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *GroupVersionHook) DeepCopyInto(out *GroupVersionHook) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new GroupVersionHook.
func (in *GroupVersionHook) DeepCopy() *GroupVersionHook {
	if in == nil {
		return nil
	}
	out := new(GroupVersionHook)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ServiceReference) DeepCopyInto(out *ServiceReference) {
	*out = *in
	if in.Port != nil {
		in, out := &in.Port, &out.Port
		*out = new(int32)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ServiceReference.
func (in *ServiceReference) DeepCopy() *ServiceReference {
	if in == nil {
		return nil
	}
	out := new(ServiceReference)
	in.DeepCopyInto(out)
	return out
}
