/*
Copyright 2015 The Kubernetes Authors All rights reserved.

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

package etcd

import (
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/rest"
	"k8s.io/kubernetes/pkg/fields"
	"k8s.io/kubernetes/pkg/labels"
	"k8s.io/kubernetes/pkg/registry/cachesize"
	"k8s.io/kubernetes/pkg/registry/generic"
	"k8s.io/kubernetes/pkg/registry/generic/registry"
	"k8s.io/kubernetes/pkg/registry/network"
	"k8s.io/kubernetes/pkg/runtime"
)

// rest implements a RESTStorage for networks against etcd
type REST struct {
	*registry.Store
	status *registry.Store
}

// StatusREST implements the REST endpoint for changing the status of a network.
type StatusREST struct {
	store *registry.Store
}

// NewREST returns a RESTStorage object that will work against networks.
func NewREST(opts generic.RESTOptions) (*REST, *StatusREST) {
	prefix := "/networks"

	newListFunc := func() runtime.Object { return &api.NetworkList{} }
	storageInterface := opts.Decorator(
		opts.Storage, cachesize.GetWatchCacheSizeByResource(cachesize.Namespaces), &api.Network{}, prefix, network.Strategy, newListFunc)

	store := &registry.Store{
		NewFunc:     func() runtime.Object { return &api.Network{} },
		NewListFunc: newListFunc,
		KeyRootFunc: func(ctx api.Context) string {
			return prefix
		},
		KeyFunc: func(ctx api.Context, name string) (string, error) {
			return prefix + "/" + name, nil
		},
		ObjectNameFunc: func(obj runtime.Object) (string, error) {
			return obj.(*api.Network).Name, nil
		},
		PredicateFunc: func(label labels.Selector, field fields.Selector) generic.Matcher {
			return network.MatchNetwork(label, field)
		},
		QualifiedResource: api.Resource("networks"),

		CreateStrategy:      network.Strategy,
		UpdateStrategy:      network.Strategy,
		DeleteStrategy:      network.Strategy,
		ReturnDeletedObject: true,

		Storage: storageInterface,
	}

	statusStore := *store
	statusStore.UpdateStrategy = network.StatusStrategy

	return &REST{Store: store, status: &statusStore}, &StatusREST{store: &statusStore}
}

// Delete enforces life-cycle rules for network termination
func (r *REST) Delete(ctx api.Context, name string, options *api.DeleteOptions) (runtime.Object, error) {
	return r.Store.Delete(ctx, name, nil)
}

func (r *StatusREST) New() runtime.Object {
	return r.store.New()
}

// Update alters the status subset of an object.
func (r *StatusREST) Update(ctx api.Context, name string, objInfo rest.UpdatedObjectInfo) (runtime.Object, bool, error) {
	return r.store.Update(ctx, name, objInfo)
}
