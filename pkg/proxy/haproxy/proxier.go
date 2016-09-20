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

package haproxy

import (
	"fmt"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/golang/glog"
	"k8s.io/kubernetes/pkg/api"
	kubeclient "k8s.io/kubernetes/pkg/client/unversioned"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet/container"
	"k8s.io/kubernetes/pkg/kubelet/hyper"
	grpctypes "k8s.io/kubernetes/pkg/kubelet/hyper/types"
	"k8s.io/kubernetes/pkg/proxy"
	"k8s.io/kubernetes/pkg/types"

	"k8s.io/kubernetes/pkg/util/slice"
)

// internal struct for string service information
type serviceInfo struct {
	clusterIP           net.IP
	namespace           string
	port                int32
	protocol            api.Protocol
	nodePort            int32
	loadBalancerStatus  api.LoadBalancerStatus
	sessionAffinityType api.ServiceAffinity
	endpoints           []string
}

// returns a new serviceInfo struct
func newServiceInfo(service proxy.ServicePortName) *serviceInfo {
	return &serviceInfo{
		sessionAffinityType: api.ServiceAffinityNone, // default
	}
}

// Proxier is an pod-buildin-haproxy proxy for connections between a localhost:lport
// and services that provide the actual backends.
type Proxier struct {
	mu                          sync.Mutex // protects the following fields
	serviceMap                  map[proxy.ServicePortName]*serviceInfo
	portsMap                    map[localPort]closeable
	hyperClient                 *hyper.HyperClient
	kubeClient                  *kubeclient.Client
	haveReceivedServiceUpdate   bool // true once we've seen an OnServiceUpdate event
	haveReceivedEndpointsUpdate bool // true once we've seen an OnEndpointsUpdate event

	// These are effectively const and do not need the mutex to be held.
	syncPeriod                  time.Duration
	masqueradeAll               bool
	disableHyperInternalService bool
}

type localPort struct {
	desc     string
	ip       string
	port     int
	protocol string
}

func (lp *localPort) String() string {
	return fmt.Sprintf("%q (%s:%d/%s)", lp.desc, lp.ip, lp.port, lp.protocol)
}

type closeable interface {
	Close() error
}

// Proxier implements ProxyProvider
var _ proxy.ProxyProvider = &Proxier{}

// NewProxier returns a new Proxier given an pod-buildin-haproxy Interface instance.
func NewProxier(syncPeriod time.Duration, kubeClient *kubeclient.Client, disableHyperInternalService bool) (*Proxier, error) {
	client, err := hyper.NewHyperClient()
	if err != nil {
		return nil, err
	}
	_, err = client.Version()
	if err != nil {
		glog.Errorf("Can not get hyper version: %v", err)
		return nil, err
	}

	return &Proxier{
		serviceMap:                  make(map[proxy.ServicePortName]*serviceInfo),
		portsMap:                    make(map[localPort]closeable),
		syncPeriod:                  syncPeriod,
		hyperClient:                 client,
		kubeClient:                  kubeClient,
		disableHyperInternalService: disableHyperInternalService,
	}, nil
}

func (proxier *Proxier) sameConfig(info *serviceInfo, service *api.Service, port *api.ServicePort) bool {
	if info.protocol != port.Protocol || info.port != port.Port || info.nodePort != port.NodePort {
		return false
	}
	if !info.clusterIP.Equal(net.ParseIP(service.Spec.ClusterIP)) {
		return false
	}
	if !api.LoadBalancerStatusEqual(&info.loadBalancerStatus, &service.Status.LoadBalancer) {
		return false
	}
	if info.namespace != service.Namespace {
		return false
	}
	if info.sessionAffinityType != service.Spec.SessionAffinity {
		return false
	}
	return true
}

func ipsEqual(lhs, rhs []string) bool {
	if len(lhs) != len(rhs) {
		return false
	}
	for i := range lhs {
		if lhs[i] != rhs[i] {
			return false
		}
	}
	return true
}

// Sync is called to immediately synchronize the proxier state to iptables
func (proxier *Proxier) Sync() {
	proxier.mu.Lock()
	defer proxier.mu.Unlock()
	proxier.syncProxyRules()
}

// SyncLoop runs periodic work.  This is expected to run as a goroutine or
// as the main loop of the app.  It does not return.
func (proxier *Proxier) SyncLoop() {
	t := time.NewTicker(proxier.syncPeriod)
	defer t.Stop()
	for {
		<-t.C
		glog.V(6).Infof("Periodic sync")
		proxier.Sync()
	}
}

// OnServiceUpdate tracks the active set of service proxies.
// They will be synchronized using syncProxyRules()
func (proxier *Proxier) OnServiceUpdate(allServices []api.Service) {
	proxier.mu.Lock()
	defer proxier.mu.Unlock()
	proxier.haveReceivedServiceUpdate = true

	activeServices := make(map[proxy.ServicePortName]bool) // use a map as a set

	for i := range allServices {
		service := &allServices[i]

		// Check if namespace is configured with network
		namespace, err := proxier.kubeClient.Namespaces().Get(service.Namespace)
		if err != nil {
			glog.Warningf("Get namespace error: %v", err)
			continue
		}
		if namespace.Spec.Network == "" {
			// Only process namespaces with network
			// Namespaces without network will be processed by userspace proxier
			continue
		}

		svcName := types.NamespacedName{
			Namespace: service.Namespace,
			Name:      service.Name,
		}

		// if ClusterIP is "None" or empty, skip proxying
		if !api.IsServiceIPSet(service) {
			glog.V(3).Infof("Skipping service %s due to clusterIP = %q", svcName, service.Spec.ClusterIP)
			continue
		}

		for i := range service.Spec.Ports {
			servicePort := &service.Spec.Ports[i]

			serviceName := proxy.ServicePortName{
				NamespacedName: svcName,
				Port:           servicePort.Name,
			}
			activeServices[serviceName] = true
			info, exists := proxier.serviceMap[serviceName]
			if exists && proxier.sameConfig(info, service, servicePort) {
				// Nothing changed.
				continue
			}
			if exists {
				//Something changed.
				glog.V(3).Infof("Something changed for service %q: removing it", serviceName)
				delete(proxier.serviceMap, serviceName)
			}

			serviceIP := net.ParseIP(service.Spec.ClusterIP)
			glog.V(1).Infof("Adding new service %q at %s:%d/%s", serviceName, serviceIP, servicePort.Port, servicePort.Protocol)
			info = newServiceInfo(serviceName)
			info.clusterIP = serviceIP
			info.namespace = service.Namespace
			info.port = servicePort.Port
			info.protocol = servicePort.Protocol
			info.nodePort = servicePort.NodePort
			// Deep-copy in case the service instance changes
			info.loadBalancerStatus = *api.LoadBalancerStatusDeepCopy(&service.Status.LoadBalancer)
			info.sessionAffinityType = service.Spec.SessionAffinity
			proxier.serviceMap[serviceName] = info

			glog.V(4).Infof("added serviceInfo(%s): %s", serviceName, spew.Sdump(info))
		}
	}

	for name, info := range proxier.serviceMap {
		// Check for servicePorts that were not in this update and have no endpoints.
		// This helps prevent unnecessarily removing and adding services.
		if !activeServices[name] && info.endpoints == nil {
			glog.V(1).Infof("Removing service %q", name)
			delete(proxier.serviceMap, name)
		}
	}

	proxier.syncProxyRules()
}

// OnEndpointsUpdate takes in a slice of updated endpoints.
func (proxier *Proxier) OnEndpointsUpdate(allEndpoints []api.Endpoints) {
	proxier.mu.Lock()
	defer proxier.mu.Unlock()
	proxier.haveReceivedEndpointsUpdate = true

	registeredEndpoints := make(map[proxy.ServicePortName]bool) // use a map as a set

	// Update endpoints for services.
	for i := range allEndpoints {
		svcEndpoints := &allEndpoints[i]

		// Check is namespace is configured with network
		namespace, err := proxier.kubeClient.Namespaces().Get(svcEndpoints.Namespace)
		if err != nil {
			glog.Warningf("Get namespace error: %v", err)
			continue
		}
		if namespace.Spec.Network == "" {
			// Only process namespaces with network
			// Namespaces without network will be processed by userspace proxier
			continue
		}

		// We need to build a map of portname -> all ip:ports for that
		// portname.  Explode Endpoints.Subsets[*] into this structure.
		portsToEndpoints := map[string][]hostPortPair{}
		for i := range svcEndpoints.Subsets {
			ss := &svcEndpoints.Subsets[i]
			for i := range ss.Ports {
				port := &ss.Ports[i]
				for i := range ss.Addresses {
					addr := &ss.Addresses[i]
					portsToEndpoints[port.Name] = append(portsToEndpoints[port.Name], hostPortPair{addr.IP, port.Port})
				}
			}
		}

		for portname := range portsToEndpoints {
			svcPort := proxy.ServicePortName{NamespacedName: types.NamespacedName{Namespace: svcEndpoints.Namespace, Name: svcEndpoints.Name}, Port: portname}
			state, exists := proxier.serviceMap[svcPort]
			if !exists || state == nil {
				state = newServiceInfo(svcPort)
				proxier.serviceMap[svcPort] = state
			}
			curEndpoints := []string{}
			if state != nil {
				curEndpoints = state.endpoints
			}
			newEndpoints := flattenValidEndpoints(portsToEndpoints[portname])

			if len(curEndpoints) != len(newEndpoints) || !slicesEquiv(slice.CopyStrings(curEndpoints), newEndpoints) {
				glog.V(1).Infof("Setting endpoints for %s to %+v", svcPort, newEndpoints)
				state.endpoints = newEndpoints
			}
			registeredEndpoints[svcPort] = true
		}
	}
	// Remove endpoints missing from the update.
	for service, info := range proxier.serviceMap {
		// if missing from update and not already set by previous endpoints event
		if _, exists := registeredEndpoints[service]; !exists && info.endpoints != nil {
			glog.V(2).Infof("Removing endpoints for %s", service)
			// Set the endpoints to nil, we will check for this in OnServiceUpdate so that we
			// only remove ServicePorts that have no endpoints and were not in the service update,
			// that way we only remove ServicePorts that were not in both.
			proxier.serviceMap[service].endpoints = nil
		}
	}

	proxier.syncProxyRules()
}

// used in OnEndpointsUpdate
type hostPortPair struct {
	host string
	port int32
}

func isValidEndpoint(hpp *hostPortPair) bool {
	return hpp.host != "" && hpp.port > 0
}

// Tests whether two slices are equivalent.  This sorts both slices in-place.
func slicesEquiv(lhs, rhs []string) bool {
	if len(lhs) != len(rhs) {
		return false
	}
	if reflect.DeepEqual(slice.SortStrings(lhs), slice.SortStrings(rhs)) {
		return true
	}
	return false
}

func flattenValidEndpoints(endpoints []hostPortPair) []string {
	// Convert Endpoint objects into strings for easier use later.
	var result []string
	for i := range endpoints {
		hpp := &endpoints[i]
		if isValidEndpoint(hpp) {
			result = append(result, net.JoinHostPort(hpp.host, strconv.Itoa(int(hpp.port))))
		} else {
			glog.Warningf("got invalid endpoint: %+v", *hpp)
		}
	}
	return result
}

// This is where all of haproxy-setting calls happen.
// assumes proxier.mu is held
func (proxier *Proxier) syncProxyRules() {
	if proxier.disableHyperInternalService {
		return
	}

	// don't sync rules till we've received services and endpoints
	if !proxier.haveReceivedEndpointsUpdate || !proxier.haveReceivedServiceUpdate {
		glog.V(2).Info("Not syncing proxy rules until Services and Endpoints have been received from master")
		return
	}
	glog.V(3).Infof("Syncing proxy rules")

	// Get existing pods
	podList, err := proxier.hyperClient.ListPods()
	if err != nil {
		glog.Warningf("Can not get pod list: %v", err)
		return
	}

	// setup services with pod's same namespace for each pod
	for _, podInfo := range podList {
		_, podNamespace, err := kubecontainer.ParsePodFullName(podInfo.PodName)
		if err != nil {
			glog.Warningf("Pod %s is not managed by kubernetes", podInfo.PodName)
			continue
		}

		// Build services of same namespace (assume all services within same
		// namespace will be consumed)
		consumedServices := make([]*grpctypes.UserService, 0, 1)
		for _, svcInfo := range proxier.serviceMap {
			if svcInfo.namespace != podNamespace {
				continue
			}

			svc := &grpctypes.UserService{
				ServicePort: svcInfo.port,
				ServiceIP:   svcInfo.clusterIP.String(),
				Protocol:    strings.ToLower(string(svcInfo.protocol)),
			}

			hosts := make([]*grpctypes.UserServiceBackend, 0, 1)
			for _, ep := range svcInfo.endpoints {
				hostport := strings.Split(ep, ":")
				port, _ := strconv.ParseInt(hostport[1], 10, 0)
				hosts = append(hosts, &grpctypes.UserServiceBackend{
					HostIP:   hostport[0],
					HostPort: int32(port),
				})
			}
			svc.Hosts = hosts

			consumedServices = append(consumedServices, svc)
		}
		glog.V(4).Infof("Services of pod %s should consumed: %v", podInfo.PodName, consumedServices)

		// update existing services
		if len(consumedServices) == 0 {
			// services can't be null for kubernetes, so fake one if it is null
			consumedServices = append(consumedServices, &grpctypes.UserService{
				ServiceIP:   "127.0.0.2",
				ServicePort: 65534,
			})
		}

		err = proxier.hyperClient.UpdateServices(podInfo.PodID, consumedServices)
		if err != nil {
			glog.Warningf("Updating service for hyper pod %s failed: %v", podInfo.PodName, err)
		}
	}
}
