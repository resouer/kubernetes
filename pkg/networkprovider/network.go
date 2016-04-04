/*
Copyright 2014 The Kubernetes Authors All rights reserved.

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

package networkprovider

import (
	"errors"

	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/networkprovider/types"
)

const (
	namePrefix = "kube"
)

var ErrNotFound = errors.New("NotFound")
var ErrMultipleResults = errors.New("MultipleResults")

// Interface is an abstract, pluggable interface for network providers.
type Interface interface {
	// Pods returns a pod interface
	Pods() Pods
	// Networks returns a network interface
	Networks() Networks
	// LoadBalancer returns a balancer interface
	LoadBalancers() LoadBalancers
	// ProviderName returns the network provider ID.
	ProviderName() string
	// CheckTenantID
	CheckTenantID(tenantID string) (bool, error)
}

type Pods interface {
	// Setup pod
	SetupPod(podName, namespace, podInfraContainerID string, network *types.Network, containerRuntime string) error
	// Teardown pod
	TeardownPod(podName, namespace, podInfraContainerID string, network *types.Network, containerRuntime string) error
	// Status of pod
	PodStatus(podName, namespace, podInfraContainerID string, network *types.Network, containerRuntime string) (string, error)
}

// Networks is an abstract, pluggable interface for network segment
type Networks interface {
	// Get network by networkName
	GetNetwork(networkName string) (*types.Network, error)
	// Get network by networkID
	GetNetworkByID(networkID string) (*types.Network, error)
	// Create network
	CreateNetwork(network *types.Network) error
	// Update network
	UpdateNetwork(network *types.Network) error
	// Delete network by networkName
	DeleteNetwork(networkName string) error
}

// LoadBalancerType is the type of the load balancer
type LoadBalancerType string

const (
	LoadBalancerTypeTCP   LoadBalancerType = "TCP"
	LoadBalancerTypeUDP   LoadBalancerType = "UDP"
	LoadBalancerTypeHTTP  LoadBalancerType = "HTTP"
	LoadBalancerTypeHTTPS LoadBalancerType = "HTTPS"
)

type NetworkStatus string

const (
	// NetworkInitializing means the network is just accepted by system
	NetworkInitializing NetworkStatus = "Initializing"
	// NetworkActive means the network is available for use in the system
	NetworkActive NetworkStatus = "Active"
	// NetworkPending means the network is accepted by system, but it is still
	// processing by network provider
	NetworkPending NetworkStatus = "Pending"
	// NetworkFailed means the network is not available
	NetworkFailed NetworkStatus = "Failed"
	// NetworkTerminating means the network is undergoing graceful termination
	NetworkTerminating NetworkStatus = "Terminating"
)

// LoadBalancers is an abstract, pluggable interface for load balancers.
type LoadBalancers interface {
	// Get load balancer by name
	GetLoadBalancer(name string) (*types.LoadBalancer, error)
	// Create load balancer, return vip and externalIP
	CreateLoadBalancer(loadBalancer *types.LoadBalancer, affinity api.ServiceAffinity) (string, error)
	// Update load balancer, return vip and externalIP
	UpdateLoadBalancer(name string, hosts []*types.HostPort, externalIPs []string) (string, error)
	// Delete load balancer
	DeleteLoadBalancer(name string) error
}

func BuildNetworkName(name, tenantID string) string {
	return namePrefix + "_" + name + "_" + tenantID
}

func BuildLoadBalancerName(name, namespace string) string {
	return namePrefix + "_" + name + "_" + namespace
}

func ApiNetworkToProviderNetwork(net *api.Network) *types.Network {
	pdNetwork := types.Network{
		Name:     BuildNetworkName(net.Name, net.Spec.TenantID),
		TenantID: net.Spec.TenantID,
		Subnets:  make([]*types.Subnet, 0, 1),
	}

	for key, subnet := range net.Spec.Subnets {
		s := types.Subnet{
			Cidr:     subnet.CIDR,
			Gateway:  subnet.Gateway,
			Name:     BuildNetworkName(key, net.Spec.TenantID),
			Tenantid: net.Spec.TenantID,
		}
		pdNetwork.Subnets = append(pdNetwork.Subnets, &s)
	}

	return &pdNetwork
}
