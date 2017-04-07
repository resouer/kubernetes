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

package fake

import (
	api "k8s.io/kubernetes/pkg/api"
	unversioned "k8s.io/kubernetes/pkg/api/unversioned"
	v1 "k8s.io/kubernetes/pkg/api/v1"
	core "k8s.io/kubernetes/pkg/client/testing/core"
	labels "k8s.io/kubernetes/pkg/labels"
	watch "k8s.io/kubernetes/pkg/watch"
)

// FakeNetworks implements NetworkInterface
type FakeNetworks struct {
	Fake *FakeCore
}

var networksResource = unversioned.GroupVersionResource{Group: "", Version: "v1", Resource: "networks"}

func (c *FakeNetworks) Create(network *v1.Network) (result *v1.Network, err error) {
	obj, err := c.Fake.
		Invokes(core.NewRootCreateAction(networksResource, network), &v1.Network{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1.Network), err
}

func (c *FakeNetworks) Update(network *v1.Network) (result *v1.Network, err error) {
	obj, err := c.Fake.
		Invokes(core.NewRootUpdateAction(networksResource, network), &v1.Network{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1.Network), err
}

func (c *FakeNetworks) UpdateStatus(network *v1.Network) (*v1.Network, error) {
	obj, err := c.Fake.
		Invokes(core.NewRootUpdateSubresourceAction(networksResource, "status", network), &v1.Network{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1.Network), err
}

func (c *FakeNetworks) Delete(name string, options *api.DeleteOptions) error {
	_, err := c.Fake.
		Invokes(core.NewRootDeleteAction(networksResource, name), &v1.Network{})
	return err
}

func (c *FakeNetworks) DeleteCollection(options *api.DeleteOptions, listOptions api.ListOptions) error {
	action := core.NewRootDeleteCollectionAction(networksResource, listOptions)

	_, err := c.Fake.Invokes(action, &v1.NetworkList{})
	return err
}

func (c *FakeNetworks) Get(name string) (result *v1.Network, err error) {
	obj, err := c.Fake.
		Invokes(core.NewRootGetAction(networksResource, name), &v1.Network{})
	if obj == nil {
		return nil, err
	}
	return obj.(*v1.Network), err
}

func (c *FakeNetworks) List(opts api.ListOptions) (result *v1.NetworkList, err error) {
	obj, err := c.Fake.
		Invokes(core.NewRootListAction(networksResource, opts), &v1.NetworkList{})
	if obj == nil {
		return nil, err
	}

	label := opts.LabelSelector
	if label == nil {
		label = labels.Everything()
	}
	list := &v1.NetworkList{}
	for _, item := range obj.(*v1.NetworkList).Items {
		if label.Matches(labels.Set(item.Labels)) {
			list.Items = append(list.Items, item)
		}
	}
	return list, err
}

// Watch returns a watch.Interface that watches the requested networks.
func (c *FakeNetworks) Watch(opts api.ListOptions) (watch.Interface, error) {
	return c.Fake.
		InvokesWatch(core.NewRootWatchAction(networksResource, opts))
}
