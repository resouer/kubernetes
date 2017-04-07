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
	core "k8s.io/kubernetes/pkg/client/testing/core"
	labels "k8s.io/kubernetes/pkg/labels"
	watch "k8s.io/kubernetes/pkg/watch"
)

// FakeNetworks implements NetworkInterface
type FakeNetworks struct {
	Fake *FakeCore
}

var networksResource = unversioned.GroupVersionResource{Group: "", Version: "", Resource: "networks"}

func (c *FakeNetworks) Create(network *api.Network) (result *api.Network, err error) {
	obj, err := c.Fake.
		Invokes(core.NewRootCreateAction(networksResource, network), &api.Network{})
	if obj == nil {
		return nil, err
	}
	return obj.(*api.Network), err
}

func (c *FakeNetworks) Update(network *api.Network) (result *api.Network, err error) {
	obj, err := c.Fake.
		Invokes(core.NewRootUpdateAction(networksResource, network), &api.Network{})
	if obj == nil {
		return nil, err
	}
	return obj.(*api.Network), err
}

func (c *FakeNetworks) UpdateStatus(network *api.Network) (*api.Network, error) {
	obj, err := c.Fake.
		Invokes(core.NewRootUpdateSubresourceAction(networksResource, "status", network), &api.Network{})
	if obj == nil {
		return nil, err
	}
	return obj.(*api.Network), err
}

func (c *FakeNetworks) Delete(name string, options *api.DeleteOptions) error {
	_, err := c.Fake.
		Invokes(core.NewRootDeleteAction(networksResource, name), &api.Network{})
	return err
}

func (c *FakeNetworks) DeleteCollection(options *api.DeleteOptions, listOptions api.ListOptions) error {
	action := core.NewRootDeleteCollectionAction(networksResource, listOptions)

	_, err := c.Fake.Invokes(action, &api.NetworkList{})
	return err
}

func (c *FakeNetworks) Get(name string) (result *api.Network, err error) {
	obj, err := c.Fake.
		Invokes(core.NewRootGetAction(networksResource, name), &api.Network{})
	if obj == nil {
		return nil, err
	}
	return obj.(*api.Network), err
}

func (c *FakeNetworks) List(opts api.ListOptions) (result *api.NetworkList, err error) {
	obj, err := c.Fake.
		Invokes(core.NewRootListAction(networksResource, opts), &api.NetworkList{})
	if obj == nil {
		return nil, err
	}

	label := opts.LabelSelector
	if label == nil {
		label = labels.Everything()
	}
	list := &api.NetworkList{}
	for _, item := range obj.(*api.NetworkList).Items {
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
