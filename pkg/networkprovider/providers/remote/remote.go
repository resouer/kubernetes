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

package remote

import (
	"errors"
	"fmt"

	"github.com/golang/glog"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/networkprovider"
	"k8s.io/kubernetes/pkg/networkprovider/types"
)

type RemoteProvider struct {
	server string

	networkClient      types.NetworksClient
	podClient          types.PodsClient
	loadbalancerClient types.LoadBalancersClient
}

func ProbeNetworkProviders(remoteAddr string) error {
	conn, err := grpc.Dial(remoteAddr, grpc.WithInsecure())
	if err != nil {
		glog.Errorf("Connect network provider %s failed: %v", remoteAddr, err)
		return err
	}

	networkClient := types.NewNetworksClient(conn)
	podClient := types.NewPodsClient(conn)
	lbClient := types.NewLoadBalancersClient(conn)
	resp, err := networkClient.Active(
		context.Background(),
		&types.ActiveRequest{},
	)
	if err != nil || !resp.Result {
		glog.Errorf("Active network provider %s failed: %v", remoteAddr, err)
		return err
	}

	networkprovider.RegisterNetworkProvider(remoteAddr, func() (networkprovider.Interface, error) {
		return &RemoteProvider{
			server:             remoteAddr,
			podClient:          podClient,
			loadbalancerClient: lbClient,
			networkClient:      networkClient,
		}, nil
	})

	return nil
}

func (r *RemoteProvider) ProviderName() string {
	return r.server
}

func (r *RemoteProvider) CheckTenantID(tenantID string) (bool, error) {
	if tenantID == "" {
		return false, fmt.Errorf("tenantID is null")
	}

	resp, err := r.networkClient.CheckTenantID(
		context.Background(),
		&types.CheckTenantIDRequest{
			TenantID: tenantID,
		},
	)
	if err != nil || resp.Error != "" {
		if err == nil {
			err = errors.New(resp.Error)
		}
		glog.Warningf("NetworkProvider check tenant id %s failed: %v", tenantID, err)
		return false, err
	}

	return resp.Result, nil
}

// Network interface is self
func (r *RemoteProvider) Networks() networkprovider.Networks {
	return r
}

// Pods interface is self
func (r *RemoteProvider) Pods() networkprovider.Pods {
	return r
}

// LoadBalancer interface is self
func (r *RemoteProvider) LoadBalancers() networkprovider.LoadBalancers {
	return r
}

// Get network by networkName
func (r *RemoteProvider) GetNetwork(networkName string) (*types.Network, error) {
	if networkName == "" {
		return nil, errors.New("networkName is null")
	}

	resp, err := r.networkClient.GetNetwork(
		context.Background(),
		&types.GetNetworkRequest{
			Name: networkName,
		},
	)
	if err != nil || resp.Error != "" {
		if err == nil {
			err = errors.New(resp.Error)
		}
		glog.Warningf("NetworkProvider get network %s failed: %v", networkName, err)
		return nil, err
	}

	return resp.Network, nil
}

// Get network by networkID
func (r *RemoteProvider) GetNetworkByID(networkID string) (*types.Network, error) {
	if networkID == "" {
		return nil, errors.New("networkID is null")
	}

	resp, err := r.networkClient.GetNetwork(
		context.Background(),
		&types.GetNetworkRequest{
			Id: networkID,
		},
	)
	if err != nil || resp.Error != "" {
		if err == nil {
			err = errors.New(resp.Error)
		}
		glog.Warningf("NetworkProvider get network %s failed: %v", networkID, err)
		return nil, err
	}

	return resp.Network, nil
}

// Create network
func (r *RemoteProvider) CreateNetwork(network *types.Network) error {
	resp, err := r.networkClient.CreateNetwork(
		context.Background(),
		&types.CreateNetworkRequest{
			Network: network,
		},
	)
	if err != nil || resp.Error != "" {
		if err == nil {
			err = errors.New(resp.Error)
		}
		glog.Warningf("NetworkProvider create network %s failed: %v", network.Name, err)
		return err
	}

	return nil
}

// Update network
func (r *RemoteProvider) UpdateNetwork(network *types.Network) error {
	resp, err := r.networkClient.UpdateNetwork(
		context.Background(),
		&types.UpdateNetworkRequest{
			Network: network,
		},
	)
	if err != nil || resp.Error != "" {
		if err == nil {
			err = errors.New(resp.Error)
		}
		glog.Warningf("NetworkProvider update network %s failed: %v", network.Name, err)
		return err
	}

	return nil
}

// Delete network by networkName
func (r *RemoteProvider) DeleteNetwork(networkName string) error {
	if networkName == "" {
		return errors.New("networkName is null")
	}

	resp, err := r.networkClient.DeleteNetwork(
		context.Background(),
		&types.DeleteNetworkRequest{
			NetworkName: networkName,
		},
	)
	if err != nil || resp.Error != "" {
		if err == nil {
			err = errors.New(resp.Error)
		}
		glog.Warningf("NetworkProvider delete network %s failed: %v", networkName, err)
		return err
	}

	return nil
}

// Get load balancer by name
func (r *RemoteProvider) GetLoadBalancer(name string) (*types.LoadBalancer, error) {
	if name == "" {
		return nil, errors.New("LoadBalancer name is null")
	}

	resp, err := r.loadbalancerClient.GetLoadBalancer(
		context.Background(),
		&types.GetLoadBalancerRequest{
			Name: name,
		},
	)
	if err != nil || resp.Error != "" {
		if err == nil {
			err = errors.New(resp.Error)
		}
		glog.Warningf("NetworkProvider get loadbalancer %s failed: %v", name, err)
		return nil, err
	}

	return resp.LoadBalancer, nil
}

// Create load balancer, return vip
func (r *RemoteProvider) CreateLoadBalancer(loadBalancer *types.LoadBalancer, affinity api.ServiceAffinity) (string, error) {
	resp, err := r.loadbalancerClient.CreateLoadBalancer(
		context.Background(),
		&types.CreateLoadBalancerRequest{
			LoadBalancer: loadBalancer,
			Affinity:     string(affinity),
		},
	)
	if err != nil || resp.Error != "" {
		if err == nil {
			err = errors.New(resp.Error)
		}
		glog.Warningf("NetworkProvider create loadbalancer %s failed: %v", loadBalancer.Name, err)
		return "", err
	}

	return resp.Vip, nil
}

// Update load balancer, return externalIP
func (r *RemoteProvider) UpdateLoadBalancer(name string, hosts []*types.HostPort, externalIPs []string) (string, error) {
	resp, err := r.loadbalancerClient.UpdateLoadBalancer(
		context.Background(),
		&types.UpdateLoadBalancerRequest{
			Name:        name,
			Hosts:       hosts,
			ExternalIPs: externalIPs,
		},
	)
	if err != nil || resp.Error != "" {
		if err == nil {
			err = errors.New(resp.Error)
		}
		glog.Warningf("NetworkProvider update loadbalancer %s failed: %v", name, err)
		return "", err
	}

	return resp.Vip, nil
}

// Delete load balancer
func (r *RemoteProvider) DeleteLoadBalancer(name string) error {
	resp, err := r.loadbalancerClient.DeleteLoadBalancer(
		context.Background(),
		&types.DeleteLoadBalancerRequest{
			Name: name,
		},
	)
	if err != nil || resp.Error != "" {
		if err == nil {
			err = errors.New(resp.Error)
		}
		glog.Warningf("NetworkProvider delete loadbalancer %s failed: %v", name, err)
		return err
	}

	return nil
}

// Setup pod
func (r *RemoteProvider) SetupPod(podName, namespace, podInfraContainerID string, network *types.Network, containerRuntime string) error {
	resp, err := r.podClient.SetupPod(
		context.Background(),
		&types.SetupPodRequest{
			PodName:             podName,
			Namespace:           namespace,
			PodInfraContainerID: podInfraContainerID,
			ContainerRuntime:    containerRuntime,
			Network:             network,
		},
	)
	if err != nil || resp.Error != "" {
		if err == nil {
			err = errors.New(resp.Error)
		}
		glog.Warningf("NetworkProvider SetupPod %s failed: %v", podName, err)
		return err
	}

	return nil
}

// Teardown pod
func (r *RemoteProvider) TeardownPod(podName, namespace, podInfraContainerID string, network *types.Network, containerRuntime string) error {
	resp, err := r.podClient.TeardownPod(
		context.Background(),
		&types.TeardownPodRequest{
			PodName:             podName,
			Namespace:           namespace,
			PodInfraContainerID: podInfraContainerID,
			ContainerRuntime:    containerRuntime,
			Network:             network,
		},
	)
	if err != nil || resp.Error != "" {
		if err == nil {
			err = errors.New(resp.Error)
		}
		glog.Warningf("NetworkProvider TeardownPod %s failed: %v", podName, err)
		return err
	}

	return nil
}

// Status of pod
func (r *RemoteProvider) PodStatus(podName, namespace, podInfraContainerID string, network *types.Network, containerRuntime string) (string, error) {
	resp, err := r.podClient.PodStatus(
		context.Background(),
		&types.PodStatusRequest{
			PodName:             podName,
			Namespace:           namespace,
			PodInfraContainerID: podInfraContainerID,
			ContainerRuntime:    containerRuntime,
			Network:             network,
		},
	)
	if err != nil || resp.Error != "" {
		if err == nil {
			err = errors.New(resp.Error)
		}
		glog.Warningf("NetworkProvider TeardownPod %s failed: %v", podName, err)
		return "", err
	}

	return resp.Ip, nil
}
