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

package unversioned

import (
	api "k8s.io/kubernetes/pkg/api"
	watch "k8s.io/kubernetes/pkg/watch"
)

// NetworksGetter has a method to return a NetworkInterface.
// A group's client should implement this interface.
type NetworksGetter interface {
	Networks() NetworkInterface
}

// NetworkInterface has methods to work with Network resources.
type NetworkInterface interface {
	Create(*api.Network) (*api.Network, error)
	Update(*api.Network) (*api.Network, error)
	UpdateStatus(*api.Network) (*api.Network, error)
	Delete(name string, options *api.DeleteOptions) error
	DeleteCollection(options *api.DeleteOptions, listOptions api.ListOptions) error
	Get(name string) (*api.Network, error)
	List(opts api.ListOptions) (*api.NetworkList, error)
	Watch(opts api.ListOptions) (watch.Interface, error)
	NetworkExpansion
}

// networks implements NetworkInterface
type networks struct {
	client *CoreClient
}

// newNetworks returns a Networks
func newNetworks(c *CoreClient) *networks {
	return &networks{
		client: c,
	}
}

// Create takes the representation of a network and creates it.  Returns the server's representation of the network, and an error, if there is any.
func (c *networks) Create(network *api.Network) (result *api.Network, err error) {
	result = &api.Network{}
	err = c.client.Post().
		Resource("networks").
		Body(network).
		Do().
		Into(result)
	return
}

// Update takes the representation of a network and updates it. Returns the server's representation of the network, and an error, if there is any.
func (c *networks) Update(network *api.Network) (result *api.Network, err error) {
	result = &api.Network{}
	err = c.client.Put().
		Resource("networks").
		Name(network.Name).
		Body(network).
		Do().
		Into(result)
	return
}

func (c *networks) UpdateStatus(network *api.Network) (result *api.Network, err error) {
	result = &api.Network{}
	err = c.client.Put().
		Resource("networks").
		Name(network.Name).
		SubResource("status").
		Body(network).
		Do().
		Into(result)
	return
}

// Delete takes name of the network and deletes it. Returns an error if one occurs.
func (c *networks) Delete(name string, options *api.DeleteOptions) error {
	return c.client.Delete().
		Resource("networks").
		Name(name).
		Body(options).
		Do().
		Error()
}

// DeleteCollection deletes a collection of objects.
func (c *networks) DeleteCollection(options *api.DeleteOptions, listOptions api.ListOptions) error {
	return c.client.Delete().
		Resource("networks").
		VersionedParams(&listOptions, api.ParameterCodec).
		Body(options).
		Do().
		Error()
}

// Get takes name of the network, and returns the corresponding network object, and an error if there is any.
func (c *networks) Get(name string) (result *api.Network, err error) {
	result = &api.Network{}
	err = c.client.Get().
		Resource("networks").
		Name(name).
		Do().
		Into(result)
	return
}

// List takes label and field selectors, and returns the list of Networks that match those selectors.
func (c *networks) List(opts api.ListOptions) (result *api.NetworkList, err error) {
	result = &api.NetworkList{}
	err = c.client.Get().
		Resource("networks").
		VersionedParams(&opts, api.ParameterCodec).
		Do().
		Into(result)
	return
}

// Watch returns a watch.Interface that watches the requested networks.
func (c *networks) Watch(opts api.ListOptions) (watch.Interface, error) {
	return c.client.Get().
		Prefix("watch").
		Resource("networks").
		VersionedParams(&opts, api.ParameterCodec).
		Watch()
}
