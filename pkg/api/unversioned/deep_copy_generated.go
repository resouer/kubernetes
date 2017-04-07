// +build !ignore_autogenerated

/*
Copyright 2017 The Kubernetes Authors All rights reserved.

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

// This file was autogenerated by deepcopy-gen. Do not edit it manually!

package unversioned

import (
	conversion "k8s.io/kubernetes/pkg/conversion"
	time "time"
)

func DeepCopy_unversioned_APIGroup(in APIGroup, out *APIGroup, c *conversion.Cloner) error {
	if err := DeepCopy_unversioned_TypeMeta(in.TypeMeta, &out.TypeMeta, c); err != nil {
		return err
	}
	out.Name = in.Name
	if in.Versions != nil {
		in, out := in.Versions, &out.Versions
		*out = make([]GroupVersionForDiscovery, len(in))
		for i := range in {
			if err := DeepCopy_unversioned_GroupVersionForDiscovery(in[i], &(*out)[i], c); err != nil {
				return err
			}
		}
	} else {
		out.Versions = nil
	}
	if err := DeepCopy_unversioned_GroupVersionForDiscovery(in.PreferredVersion, &out.PreferredVersion, c); err != nil {
		return err
	}
	if in.ServerAddressByClientCIDRs != nil {
		in, out := in.ServerAddressByClientCIDRs, &out.ServerAddressByClientCIDRs
		*out = make([]ServerAddressByClientCIDR, len(in))
		for i := range in {
			if err := DeepCopy_unversioned_ServerAddressByClientCIDR(in[i], &(*out)[i], c); err != nil {
				return err
			}
		}
	} else {
		out.ServerAddressByClientCIDRs = nil
	}
	return nil
}

func DeepCopy_unversioned_APIGroupList(in APIGroupList, out *APIGroupList, c *conversion.Cloner) error {
	if err := DeepCopy_unversioned_TypeMeta(in.TypeMeta, &out.TypeMeta, c); err != nil {
		return err
	}
	if in.Groups != nil {
		in, out := in.Groups, &out.Groups
		*out = make([]APIGroup, len(in))
		for i := range in {
			if err := DeepCopy_unversioned_APIGroup(in[i], &(*out)[i], c); err != nil {
				return err
			}
		}
	} else {
		out.Groups = nil
	}
	return nil
}

func DeepCopy_unversioned_APIResource(in APIResource, out *APIResource, c *conversion.Cloner) error {
	out.Name = in.Name
	out.Namespaced = in.Namespaced
	out.Kind = in.Kind
	return nil
}

func DeepCopy_unversioned_APIResourceList(in APIResourceList, out *APIResourceList, c *conversion.Cloner) error {
	if err := DeepCopy_unversioned_TypeMeta(in.TypeMeta, &out.TypeMeta, c); err != nil {
		return err
	}
	out.GroupVersion = in.GroupVersion
	if in.APIResources != nil {
		in, out := in.APIResources, &out.APIResources
		*out = make([]APIResource, len(in))
		for i := range in {
			if err := DeepCopy_unversioned_APIResource(in[i], &(*out)[i], c); err != nil {
				return err
			}
		}
	} else {
		out.APIResources = nil
	}
	return nil
}

func DeepCopy_unversioned_APIVersions(in APIVersions, out *APIVersions, c *conversion.Cloner) error {
	if err := DeepCopy_unversioned_TypeMeta(in.TypeMeta, &out.TypeMeta, c); err != nil {
		return err
	}
	if in.Versions != nil {
		in, out := in.Versions, &out.Versions
		*out = make([]string, len(in))
		copy(*out, in)
	} else {
		out.Versions = nil
	}
	if in.ServerAddressByClientCIDRs != nil {
		in, out := in.ServerAddressByClientCIDRs, &out.ServerAddressByClientCIDRs
		*out = make([]ServerAddressByClientCIDR, len(in))
		for i := range in {
			if err := DeepCopy_unversioned_ServerAddressByClientCIDR(in[i], &(*out)[i], c); err != nil {
				return err
			}
		}
	} else {
		out.ServerAddressByClientCIDRs = nil
	}
	return nil
}

func DeepCopy_unversioned_Duration(in Duration, out *Duration, c *conversion.Cloner) error {
	out.Duration = in.Duration
	return nil
}

func DeepCopy_unversioned_ExportOptions(in ExportOptions, out *ExportOptions, c *conversion.Cloner) error {
	if err := DeepCopy_unversioned_TypeMeta(in.TypeMeta, &out.TypeMeta, c); err != nil {
		return err
	}
	out.Export = in.Export
	out.Exact = in.Exact
	return nil
}

func DeepCopy_unversioned_GroupKind(in GroupKind, out *GroupKind, c *conversion.Cloner) error {
	out.Group = in.Group
	out.Kind = in.Kind
	return nil
}

func DeepCopy_unversioned_GroupResource(in GroupResource, out *GroupResource, c *conversion.Cloner) error {
	out.Group = in.Group
	out.Resource = in.Resource
	return nil
}

func DeepCopy_unversioned_GroupVersion(in GroupVersion, out *GroupVersion, c *conversion.Cloner) error {
	out.Group = in.Group
	out.Version = in.Version
	return nil
}

func DeepCopy_unversioned_GroupVersionForDiscovery(in GroupVersionForDiscovery, out *GroupVersionForDiscovery, c *conversion.Cloner) error {
	out.GroupVersion = in.GroupVersion
	out.Version = in.Version
	return nil
}

func DeepCopy_unversioned_GroupVersionKind(in GroupVersionKind, out *GroupVersionKind, c *conversion.Cloner) error {
	out.Group = in.Group
	out.Version = in.Version
	out.Kind = in.Kind
	return nil
}

func DeepCopy_unversioned_GroupVersionResource(in GroupVersionResource, out *GroupVersionResource, c *conversion.Cloner) error {
	out.Group = in.Group
	out.Version = in.Version
	out.Resource = in.Resource
	return nil
}

func DeepCopy_unversioned_LabelSelector(in LabelSelector, out *LabelSelector, c *conversion.Cloner) error {
	if in.MatchLabels != nil {
		in, out := in.MatchLabels, &out.MatchLabels
		*out = make(map[string]string)
		for key, val := range in {
			(*out)[key] = val
		}
	} else {
		out.MatchLabels = nil
	}
	if in.MatchExpressions != nil {
		in, out := in.MatchExpressions, &out.MatchExpressions
		*out = make([]LabelSelectorRequirement, len(in))
		for i := range in {
			if err := DeepCopy_unversioned_LabelSelectorRequirement(in[i], &(*out)[i], c); err != nil {
				return err
			}
		}
	} else {
		out.MatchExpressions = nil
	}
	return nil
}

func DeepCopy_unversioned_LabelSelectorRequirement(in LabelSelectorRequirement, out *LabelSelectorRequirement, c *conversion.Cloner) error {
	out.Key = in.Key
	out.Operator = in.Operator
	if in.Values != nil {
		in, out := in.Values, &out.Values
		*out = make([]string, len(in))
		copy(*out, in)
	} else {
		out.Values = nil
	}
	return nil
}

func DeepCopy_unversioned_ListMeta(in ListMeta, out *ListMeta, c *conversion.Cloner) error {
	out.SelfLink = in.SelfLink
	out.ResourceVersion = in.ResourceVersion
	return nil
}

func DeepCopy_unversioned_Patch(in Patch, out *Patch, c *conversion.Cloner) error {
	return nil
}

func DeepCopy_unversioned_RootPaths(in RootPaths, out *RootPaths, c *conversion.Cloner) error {
	if in.Paths != nil {
		in, out := in.Paths, &out.Paths
		*out = make([]string, len(in))
		copy(*out, in)
	} else {
		out.Paths = nil
	}
	return nil
}

func DeepCopy_unversioned_ServerAddressByClientCIDR(in ServerAddressByClientCIDR, out *ServerAddressByClientCIDR, c *conversion.Cloner) error {
	out.ClientCIDR = in.ClientCIDR
	out.ServerAddress = in.ServerAddress
	return nil
}

func DeepCopy_unversioned_Status(in Status, out *Status, c *conversion.Cloner) error {
	if err := DeepCopy_unversioned_TypeMeta(in.TypeMeta, &out.TypeMeta, c); err != nil {
		return err
	}
	if err := DeepCopy_unversioned_ListMeta(in.ListMeta, &out.ListMeta, c); err != nil {
		return err
	}
	out.Status = in.Status
	out.Message = in.Message
	out.Reason = in.Reason
	if in.Details != nil {
		in, out := in.Details, &out.Details
		*out = new(StatusDetails)
		if err := DeepCopy_unversioned_StatusDetails(*in, *out, c); err != nil {
			return err
		}
	} else {
		out.Details = nil
	}
	out.Code = in.Code
	return nil
}

func DeepCopy_unversioned_StatusCause(in StatusCause, out *StatusCause, c *conversion.Cloner) error {
	out.Type = in.Type
	out.Message = in.Message
	out.Field = in.Field
	return nil
}

func DeepCopy_unversioned_StatusDetails(in StatusDetails, out *StatusDetails, c *conversion.Cloner) error {
	out.Name = in.Name
	out.Group = in.Group
	out.Kind = in.Kind
	if in.Causes != nil {
		in, out := in.Causes, &out.Causes
		*out = make([]StatusCause, len(in))
		for i := range in {
			if err := DeepCopy_unversioned_StatusCause(in[i], &(*out)[i], c); err != nil {
				return err
			}
		}
	} else {
		out.Causes = nil
	}
	out.RetryAfterSeconds = in.RetryAfterSeconds
	return nil
}

func DeepCopy_unversioned_Time(in Time, out *Time, c *conversion.Cloner) error {
	if newVal, err := c.DeepCopy(in.Time); err != nil {
		return err
	} else {
		out.Time = newVal.(time.Time)
	}
	return nil
}

func DeepCopy_unversioned_Timestamp(in Timestamp, out *Timestamp, c *conversion.Cloner) error {
	out.Seconds = in.Seconds
	out.Nanos = in.Nanos
	return nil
}

func DeepCopy_unversioned_TypeMeta(in TypeMeta, out *TypeMeta, c *conversion.Cloner) error {
	out.Kind = in.Kind
	out.APIVersion = in.APIVersion
	return nil
}
