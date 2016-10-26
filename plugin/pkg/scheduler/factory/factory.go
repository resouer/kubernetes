/*
Copyright 2014 The Kubernetes Authors.

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

// Package factory can set up a scheduler. This code is here instead of
// plugin/cmd/scheduler for both testability and reuse.
package factory

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/errors"
	"k8s.io/kubernetes/pkg/client/cache"
	clientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
	"k8s.io/kubernetes/pkg/controller/informers"
	"k8s.io/kubernetes/pkg/fields"
	"k8s.io/kubernetes/pkg/kubelet/qos"
	"k8s.io/kubernetes/pkg/types"
	"k8s.io/kubernetes/pkg/util/runtime"
	"k8s.io/kubernetes/pkg/util/sets"
	utilvalidation "k8s.io/kubernetes/pkg/util/validation"
	"k8s.io/kubernetes/plugin/pkg/scheduler"
	"k8s.io/kubernetes/plugin/pkg/scheduler/algorithm"
	"k8s.io/kubernetes/plugin/pkg/scheduler/algorithm/predicates"
	schedulerapi "k8s.io/kubernetes/plugin/pkg/scheduler/api"
	"k8s.io/kubernetes/plugin/pkg/scheduler/api/validation"

	"github.com/golang/glog"
	"k8s.io/kubernetes/pkg/apis/extensions"
	"k8s.io/kubernetes/plugin/pkg/scheduler/schedulercache"
)

const (
	SchedulerAnnotationKey = "scheduler.alpha.kubernetes.io/name"
	initialGetBackoff      = 100 * time.Millisecond
	maximalGetBackoff      = time.Minute
)

// ConfigFactory knows how to fill out a scheduler config with its support functions.
type ConfigFactory struct {
	Client clientset.Interface
	// queue for pods that need scheduling
	PodQueue *cache.FIFO
	// a means to list all known scheduled pods.
	ScheduledPodLister *cache.StoreToPodLister
	// a means to list all known scheduled pods and pods assumed to have been scheduled.
	PodLister algorithm.PodLister
	// a means to list all nodes
	NodeLister *cache.StoreToNodeLister
	// a means to list all PersistentVolumes
	PVLister *cache.StoreToPVFetcher
	// a means to list all PersistentVolumeClaims
	PVCLister *cache.StoreToPersistentVolumeClaimLister
	// a means to list all services
	ServiceLister *cache.StoreToServiceLister
	// a means to list all controllers
	ControllerLister *cache.StoreToReplicationControllerLister
	// a means to list all replicasets
	ReplicaSetLister *cache.StoreToReplicaSetLister

	// Close this to stop all reflectors
	StopEverything chan struct{}

	informerFactory       informers.SharedInformerFactory
	scheduledPodPopulator *cache.Controller
	nodePopulator         *cache.Controller
	pvPopulator           *cache.Controller
	pvcPopulator          cache.ControllerInterface
	// TODO(harry) my version:
	// pvcPopulator          *cache.Controller
	PodPopulator        *cache.Controller
	servicePopulator    *cache.Controller
	controllerPopulator *cache.Controller

	schedulerCache schedulercache.Cache

	// SchedulerName of a scheduler is used to select which pods will be
	// processed by this scheduler, based on pods's annotation key:
	// 'scheduler.alpha.kubernetes.io/name'
	SchedulerName string

	// RequiredDuringScheduling affinity is not symmetric, but there is an implicit PreferredDuringScheduling affinity rule
	// corresponding to every RequiredDuringScheduling affinity rule.
	// HardPodAffinitySymmetricWeight represents the weight of implicit PreferredDuringScheduling affinity rule, in the range 0-100.
	HardPodAffinitySymmetricWeight int

	// Indicate the "all topologies" set for empty topologyKey when it's used for PreferredDuringScheduling pod anti-affinity.
	FailureDomains string

	// Equivalence class cache
	EquivalencePodCache *scheduler.EquivalenceCache
}

// Initializes the factory.
func NewConfigFactory(client clientset.Interface, schedulerName string, hardPodAffinitySymmetricWeight int, failureDomains string) *ConfigFactory {
	stopEverything := make(chan struct{})
	schedulerCache := schedulercache.New(30*time.Second, stopEverything)

	// TODO: pass this in as an argument...
	informerFactory := informers.NewSharedInformerFactory(client, 0)
	pvcInformer := informerFactory.PersistentVolumeClaims()

	c := &ConfigFactory{
		Client:             client,
		PodQueue:           cache.NewFIFO(cache.MetaNamespaceKeyFunc),
		ScheduledPodLister: &cache.StoreToPodLister{},
		informerFactory:    informerFactory,
		// Only nodes in the "Ready" condition with status == "True" are schedulable
		NodeLister:                     &cache.StoreToNodeLister{},
		PVLister:                       &cache.StoreToPVFetcher{Store: cache.NewStore(cache.MetaNamespaceKeyFunc)},
		PVCLister:                      pvcInformer.Lister(),
		pvcPopulator:                   pvcInformer.Informer().GetController(),
		ServiceLister:                  &cache.StoreToServiceLister{Indexer: cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})},
		ControllerLister:               &cache.StoreToReplicationControllerLister{Indexer: cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})},
		ReplicaSetLister:               &cache.StoreToReplicaSetLister{Indexer: cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})},
		schedulerCache:                 schedulerCache,
		StopEverything:                 stopEverything,
		SchedulerName:                  schedulerName,
		HardPodAffinitySymmetricWeight: hardPodAffinitySymmetricWeight,
		FailureDomains:                 failureDomains,
	}

	c.PodLister = schedulerCache

	// On add/delete to the scheduled pods, remove from the assumed pods.
	// We construct this here instead of in CreateFromKeys because
	// ScheduledPodLister is something we provide to plug in functions that
	// they may need to call.
	c.ScheduledPodLister.Indexer, c.scheduledPodPopulator = cache.NewIndexerInformer(
		c.createAssignedNonTerminatedPodLW(),
		&api.Pod{},
		0,
		cache.ResourceEventHandlerFuncs{
			AddFunc:    c.addPodToCache,
			UpdateFunc: c.updatePodInCache,
			DeleteFunc: c.deletePodFromCache,
		},
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
	)

	c.NodeLister.Store, c.nodePopulator = cache.NewInformer(
		c.createNodeLW(),
		&api.Node{},
		0,
		cache.ResourceEventHandlerFuncs{
			AddFunc:    c.addNodeToCache,
			UpdateFunc: c.updateNodeInCache,
			DeleteFunc: c.deleteNodeFromCache,
		},
	)

	c.PVLister.Store, c.pvPopulator = cache.NewInformer(
		c.createPersistentVolumeLW(),
		&api.PersistentVolume{},
		0,
		cache.ResourceEventHandlerFuncs{
			// MaxPDVolumeCountPredicate: since it relies on the counts of PV.
			AddFunc: func(_ interface{}) {
				c.EquivalencePodCache.SendClearAllCacheReq()
			},
			DeleteFunc: func(_ interface{}) {
				c.EquivalencePodCache.SendClearAllCacheReq()
			},
			// MaxPDVolumeCountPredicate: when the PV is updated to PVC (or vice versa) which will affect counts of PV.
			UpdateFunc: func(oldObj, newObj, interface{}) {
				oldPV := oldObj.(api.PersistentVolume)
				newPV := newObj.(api.PersistentVolume)
				if (oldPV.PersistentVolumeClaim == nil && newPV.PersistentVolumeClaim != nil) ||
					(oldPV.PersistentVolumeClaim != nil && newPV.PersistentVolumeClaim == nil) {
					c.EquivalencePodCache.SendClearAllCacheReq()
				}
			},
		},
	)

	c.PVCLister.Store, c.pvcPopulator = cache.NewInformer(
		c.createPersistentVolumeClaimLW(),
		&api.PersistentVolumeClaim{},
		0,
		cache.ResourceEventHandlerFuncs{
			// MaxPDVolumeCountPredicate: add/delete PVC will affect counts of PV when it is bound.
			AddFunc: func(obj interface{}) {
				pvc := obj.(api.PersistentVolumeClaim)
				if pvc.Spec.VolumeName != "" {
					c.EquivalencePodCache.SendClearAllCacheReq()
				}
			},
			DeleteFunc: func(obj interface{}) {
				pvc := obj.(api.PersistentVolumeClaim)
				if pvc.Spec.VolumeName != "" {
					c.EquivalencePodCache.SendClearAllCacheReq()
				}
			},
		},
	)

	c.ServiceLister.Indexer, c.servicePopulator = cache.NewIndexerInformer(
		c.createServiceLW(),
		&api.Service{},
		0,
		cache.ResourceEventHandlerFuncs{
			// ServiceAffinity: it will not be affected by svc add/delete,
			// except that the selector of the service is updated. (TODO, is it true?)
			UpdateFunc: func(oldObj, newObj interface{}) {
				oldService := oldObj.(api.Service)
				newService := newObj.(api.Service)
				if !reflect.DeepEqual(oldService.Spec.Selector, newService.Spec.Selector) {
					c.EquivalencePodCache.SendClearAllCacheReq()
				}
			},
		},
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
	)

	c.ControllerLister.Indexer, c.controllerPopulator = cache.NewIndexerInformer(
		c.createControllerLW(),
		&api.ReplicationController{},
		0,
		cache.ResourceEventHandlerFuncs{
			// Pod change (including resize) will be handled by PodLister, not here.
			// Existing equivalence cache should not be affected by add/delete RC
			// but selector change should be cared.
			UpdateFunc: func(oldObj, newObj interface{}) {
				oldRC := oldObj.(api.ReplicationController)
				newRC := newObj.(api.ReplicationController)
				if !reflect.DeepEqual(oldRC.Spec.Selector, newRC.Spec.Selector) {
					c.EquivalencePodCache.SendClearAllCacheReq()
				}
			},
		},
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
	)

	return c
}

func (c *ConfigFactory) addPodToCache(obj interface{}) {
	pod, ok := obj.(*api.Pod)
	if !ok {
		glog.Errorf("cannot convert to *api.Pod: %v", obj)
		return
	}

	if pod.Spec.NodeName != "" {
		c.EquivalencePodCache.SendInvalidAlgorithmCacheReq(pod.Spec.NodeName)
	}

	if err := c.schedulerCache.AddPod(pod); err != nil {
		glog.Errorf("scheduler cache AddPod failed: %v", err)
	}
}

func (c *ConfigFactory) updatePodInCache(oldObj, newObj interface{}) {
	oldPod, ok := oldObj.(*api.Pod)
	if !ok {
		glog.Errorf("cannot convert oldObj to *api.Pod: %v", oldObj)
		return
	}
	newPod, ok := newObj.(*api.Pod)
	if !ok {
		glog.Errorf("cannot convert newObj to *api.Pod: %v", newObj)
		return
	}

	oldToleration, err := api.GetTolerationsFromPodAnnotations(oldPod.Annotations)
	newToleration, err := api.GetTolerationsFromPodAnnotations(newPod.Annotations)
	if err != nil {
		glog.Errorf("Failed to get tolerations from pod annotation for equivalence cache")
	}

	oldAffinity, err := api.GetAffinityFromPodAnnotations(oldPod.Annotations)
	newAffinity, err := api.GetAffinityFromPodAnnotations(newPod.Annotations)
	if err != nil {
		glog.Errorf("Failed to get affinity from pod annotation for equivalence cache")
	}

	// When pod is updated, in some cases we need to invalid equivalence cache of its node.
	switch {
	// PodFitsHost
	case !reflect.DeepEqual(oldPod.Spec.NodeName, newPod.Spec.NodeName),
		// PodFitsResources
		!reflect.DeepEqual(predicates.GetResourceRequest(oldPod), predicates.GetResourceRequest(newPod)),
		// PodFitsHostPorts
		!reflect.DeepEqual(predicates.GetUsedPorts(oldPod), predicates.GetUsedPorts(newPod)),
		// PodSelectorMatches
		!reflect.DeepEqual(oldPod.Spec.NodeSelector, newPod.Spec.NodeSelector),
		// CheckNodeMemoryPressure
		!reflect.DeepEqual(qos.GetPodQOS(oldPod), qos.GetPodQOS(newPod)),
		// PodToleratesNodeTaints
		!reflect.DeepEqual(oldToleration, newToleration):
		if oldPod.Spec.NodeName != "" {
			c.EquivalencePodCache.SendInvalidAlgorithmCacheReq(oldPod.Spec.NodeName)
		}
	// MatchInterPodAffinity, this case is complicated, so we'll invalid equivalence cache of all nodes
	case !reflect.DeepEqual(oldAffinity, newAffinity):
		c.EquivalencePodCache.SendClearAllCacheReq()
	}

	if err := c.schedulerCache.UpdatePod(oldPod, newPod); err != nil {
		glog.Errorf("scheduler cache UpdatePod failed: %v", err)
	}
}

func (c *ConfigFactory) deletePodFromCache(obj interface{}) {
	var pod *api.Pod
	switch t := obj.(type) {
	case *api.Pod:
		pod = t
		if pod.Spec.NodeName != "" {
			c.EquivalencePodCache.SendInvalidAlgorithmCacheReq(pod.Spec.NodeName)
		}
	case cache.DeletedFinalStateUnknown:
		var ok bool
		pod, ok = t.Obj.(*api.Pod)
		if !ok {
			glog.Errorf("cannot convert to *api.Pod: %v", t.Obj)
			return
		}
		if pod.Spec.NodeName != "" {
			c.EquivalencePodCache.SendInvalidAlgorithmCacheReq(pod.Spec.NodeName)
		}
	default:
		glog.Errorf("cannot convert to *api.Pod: %v", t)
		return
	}
	if err := c.schedulerCache.RemovePod(pod); err != nil {
		glog.Errorf("scheduler cache RemovePod failed: %v", err)
	}
}

func (c *ConfigFactory) addNodeToCache(obj interface{}) {
	node, ok := obj.(*api.Node)
	if !ok {
		glog.Errorf("cannot convert to *api.Node: %v", obj)
		return
	}

	// have to invalid all equivalence cache when new node joined
	c.EquivalencePodCache.SendClearAllCacheReq()

	if err := c.schedulerCache.AddNode(node); err != nil {
		glog.Errorf("scheduler cache AddNode failed: %v", err)
	}
}

func (c *ConfigFactory) updateNodeInCache(oldObj, newObj interface{}) {
	oldNode, ok := oldObj.(*api.Node)
	if !ok {
		glog.Errorf("cannot convert oldObj to *api.Node: %v", oldObj)
		return
	}
	newNode, ok := newObj.(*api.Node)
	if !ok {
		glog.Errorf("cannot convert newObj to *api.Node: %v", newObj)
		return
	}

	// When node is updated, we need to invalid equivalence cache of all nodes.
	// TODO(harryz) or only in some cases?
	c.EquivalencePodCache.SendClearAllCacheReq()

	if err := c.schedulerCache.UpdateNode(oldNode, newNode); err != nil {
		glog.Errorf("scheduler cache UpdateNode failed: %v", err)
	}
}

func (c *ConfigFactory) deleteNodeFromCache(obj interface{}) {
	var node *api.Node
	switch t := obj.(type) {
	case *api.Node:
		node = t
		c.EquivalencePodCache.SendInvalidAlgorithmCacheReq(node.Name)
	case cache.DeletedFinalStateUnknown:
		var ok bool
		node, ok = t.Obj.(*api.Node)
		if !ok {
			glog.Errorf("cannot convert to *api.Node: %v", t.Obj)
			return
		}
		c.EquivalencePodCache.SendInvalidAlgorithmCacheReq(node.Name)
	default:
		glog.Errorf("cannot convert to *api.Node: %v", t)
		return
	}
	if err := c.schedulerCache.RemoveNode(node); err != nil {
		glog.Errorf("scheduler cache RemoveNode failed: %v", err)
	}
}

// Create creates a scheduler with the default algorithm provider.
func (f *ConfigFactory) Create() (*scheduler.Config, error) {
	return f.CreateFromProvider(DefaultProvider)
}

// Creates a scheduler from the name of a registered algorithm provider.
func (f *ConfigFactory) CreateFromProvider(providerName string) (*scheduler.Config, error) {
	glog.V(2).Infof("Creating scheduler from algorithm provider '%v'", providerName)
	provider, err := GetAlgorithmProvider(providerName)
	if err != nil {
		return nil, err
	}

	return f.CreateFromKeys(provider.FitPredicateKeys, provider.PriorityFunctionKeys, []algorithm.SchedulerExtender{})
}

// Creates a scheduler from the configuration file
func (f *ConfigFactory) CreateFromConfig(policy schedulerapi.Policy) (*scheduler.Config, error) {
	glog.V(2).Infof("Creating scheduler from configuration: %v", policy)

	// validate the policy configuration
	if err := validation.ValidatePolicy(policy); err != nil {
		return nil, err
	}

	predicateKeys := sets.NewString()
	for _, predicate := range policy.Predicates {
		glog.V(2).Infof("Registering predicate: %s", predicate.Name)
		predicateKeys.Insert(RegisterCustomFitPredicate(predicate))
	}

	priorityKeys := sets.NewString()
	for _, priority := range policy.Priorities {
		glog.V(2).Infof("Registering priority: %s", priority.Name)
		priorityKeys.Insert(RegisterCustomPriorityFunction(priority))
	}

	extenders := make([]algorithm.SchedulerExtender, 0)
	if len(policy.ExtenderConfigs) != 0 {
		for ii := range policy.ExtenderConfigs {
			glog.V(2).Infof("Creating extender with config %+v", policy.ExtenderConfigs[ii])
			if extender, err := scheduler.NewHTTPExtender(&policy.ExtenderConfigs[ii], policy.APIVersion); err != nil {
				return nil, err
			} else {
				extenders = append(extenders, extender)
			}
		}
	}
	return f.CreateFromKeys(predicateKeys, priorityKeys, extenders)
}

// Creates a scheduler from a set of registered fit predicate keys and priority keys.
func (f *ConfigFactory) CreateFromKeys(predicateKeys, priorityKeys sets.String, extenders []algorithm.SchedulerExtender) (*scheduler.Config, error) {
	glog.V(2).Infof("creating scheduler with fit predicates '%v' and priority functions '%v", predicateKeys, priorityKeys)

	if f.HardPodAffinitySymmetricWeight < 0 || f.HardPodAffinitySymmetricWeight > 100 {
		return nil, fmt.Errorf("invalid hardPodAffinitySymmetricWeight: %d, must be in the range 0-100", f.HardPodAffinitySymmetricWeight)
	}

	predicateFuncs, err := f.GetPredicates(predicateKeys)
	if err != nil {
		return nil, err
	}

	priorityConfigs, err := f.GetPriorityFunctionConfigs(priorityKeys)
	if err != nil {
		return nil, err
	}

	priorityMetaProducer, err := f.GetPriorityMetadataProducer()
	if err != nil {
		return nil, err
	}

	predicateMetaProducer, err := f.GetPredicateMetadataProducer()
	if err != nil {
		return nil, err
	}
	// Init equivalence class cache
	if getEquivalencePodFunc != nil {
		f.EquivalencePodCache = scheduler.NewEquivalenceCache(getEquivalencePodFunc)
		glog.Info("Create equivalence class cache")
	}

	f.Run()
	algo := scheduler.NewGenericScheduler(f.schedulerCache, f.EquivalencePodCache, predicateFuncs, predicateMetaProducer, priorityConfigs, priorityMetaProducer, extenders)
	podBackoff := podBackoff{
		perPodBackoff: map[types.NamespacedName]*backoffEntry{},
		clock:         realClock{},

		defaultDuration: 1 * time.Second,
		maxDuration:     60 * time.Second,
	}

	return &scheduler.Config{
		SchedulerCache: f.schedulerCache,
		// The scheduler only needs to consider schedulable nodes.
		NodeLister:          f.NodeLister.NodeCondition(getNodeConditionPredicate()),
		Algorithm:           algo,
		Binder:              &binder{f.Client},
		PodConditionUpdater: &podConditionUpdater{f.Client},
		NextPod: func() *api.Pod {
			return f.getNextPod()
		},
		Error:          f.makeDefaultErrorFunc(&podBackoff, f.PodQueue),
		StopEverything: f.StopEverything,
	}, nil
}

func (f *ConfigFactory) GetPriorityFunctionConfigs(priorityKeys sets.String) ([]algorithm.PriorityConfig, error) {
	pluginArgs, err := f.getPluginArgs()
	if err != nil {
		return nil, err
	}

	return getPriorityFunctionConfigs(priorityKeys, *pluginArgs)
}

func (f *ConfigFactory) GetPriorityMetadataProducer() (algorithm.MetadataProducer, error) {
	pluginArgs, err := f.getPluginArgs()
	if err != nil {
		return nil, err
	}

	return getPriorityMetadataProducer(*pluginArgs)
}

func (f *ConfigFactory) GetPredicateMetadataProducer() (algorithm.MetadataProducer, error) {
	pluginArgs, err := f.getPluginArgs()
	if err != nil {
		return nil, err
	}
	return getPredicateMetadataProducer(*pluginArgs)
}

func (f *ConfigFactory) GetPredicates(predicateKeys sets.String) (map[string]algorithm.FitPredicate, error) {
	pluginArgs, err := f.getPluginArgs()
	if err != nil {
		return nil, err
	}

	return getFitPredicateFunctions(predicateKeys, *pluginArgs)
}

func (f *ConfigFactory) getPluginArgs() (*PluginFactoryArgs, error) {
	failureDomainArgs := strings.Split(f.FailureDomains, ",")
	for _, failureDomain := range failureDomainArgs {
		if errs := utilvalidation.IsQualifiedName(failureDomain); len(errs) != 0 {
			return nil, fmt.Errorf("invalid failure domain: %q: %s", failureDomain, strings.Join(errs, ";"))
		}
	}

	return &PluginFactoryArgs{
		PodLister:        f.PodLister,
		ServiceLister:    f.ServiceLister,
		ControllerLister: f.ControllerLister,
		ReplicaSetLister: f.ReplicaSetLister,
		// All fit predicates only need to consider schedulable nodes.
		NodeLister: f.NodeLister.NodeCondition(getNodeConditionPredicate()),
		NodeInfo:   &predicates.CachedNodeInfo{StoreToNodeLister: f.NodeLister},
		PVInfo:     f.PVLister,
		PVCInfo:    &predicates.CachedPersistentVolumeClaimInfo{StoreToPersistentVolumeClaimLister: f.PVCLister},
		HardPodAffinitySymmetricWeight: f.HardPodAffinitySymmetricWeight,
		FailureDomains:                 sets.NewString(failureDomainArgs...).List(),
	}, nil
}

func (f *ConfigFactory) Run() {
	// Watch and queue pods that need scheduling.
	cache.NewReflector(f.createUnassignedNonTerminatedPodLW(), &api.Pod{}, f.PodQueue, 0).RunUntil(f.StopEverything)

	// Begin populating scheduled pods.
	go f.scheduledPodPopulator.Run(f.StopEverything)

	// Begin populating nodes.
	go f.nodePopulator.Run(f.StopEverything)

	// Begin populating pv & pvc
	go f.pvPopulator.Run(f.StopEverything)
	go f.pvcPopulator.Run(f.StopEverything)

	// Begin populating services
	go f.servicePopulator.Run(f.StopEverything)

	// Begin populating controllers
	go f.controllerPopulator.Run(f.StopEverything)

	// start informers...
	f.informerFactory.Start(f.StopEverything)

	// Watch and cache all ReplicaSet objects. Scheduler needs to find all pods
	// created by the same services or ReplicationControllers/ReplicaSets, so that it can spread them correctly.
	// Cache this locally.
	cache.NewReflector(f.createReplicaSetLW(), &extensions.ReplicaSet{}, f.ReplicaSetLister.Indexer, 0).RunUntil(f.StopEverything)
}

func (f *ConfigFactory) getNextPod() *api.Pod {
	for {
		pod := cache.Pop(f.PodQueue).(*api.Pod)
		if f.responsibleForPod(pod) {
			glog.V(4).Infof("About to try and schedule pod %v", pod.Name)
			return pod
		}
	}
}

func (f *ConfigFactory) responsibleForPod(pod *api.Pod) bool {
	if f.SchedulerName == api.DefaultSchedulerName {
		return pod.Annotations[SchedulerAnnotationKey] == f.SchedulerName || pod.Annotations[SchedulerAnnotationKey] == ""
	} else {
		return pod.Annotations[SchedulerAnnotationKey] == f.SchedulerName
	}
}

func getNodeConditionPredicate() cache.NodeConditionPredicate {
	return func(node *api.Node) bool {
		for i := range node.Status.Conditions {
			cond := &node.Status.Conditions[i]
			// We consider the node for scheduling only when its:
			// - NodeReady condition status is ConditionTrue,
			// - NodeOutOfDisk condition status is ConditionFalse,
			// - NodeNetworkUnavailable condition status is ConditionFalse.
			if cond.Type == api.NodeReady && cond.Status != api.ConditionTrue {
				glog.V(4).Infof("Ignoring node %v with %v condition status %v", node.Name, cond.Type, cond.Status)
				return false
			} else if cond.Type == api.NodeOutOfDisk && cond.Status != api.ConditionFalse {
				glog.V(4).Infof("Ignoring node %v with %v condition status %v", node.Name, cond.Type, cond.Status)
				return false
			} else if cond.Type == api.NodeNetworkUnavailable && cond.Status != api.ConditionFalse {
				glog.V(4).Infof("Ignoring node %v with %v condition status %v", node.Name, cond.Type, cond.Status)
				return false
			}
		}
		// Ignore nodes that are marked unschedulable
		if node.Spec.Unschedulable {
			glog.V(4).Infof("Ignoring node %v since it is unschedulable", node.Name)
			return false
		}
		return true
	}
}

// Returns a cache.ListWatch that finds all pods that need to be
// scheduled.
func (factory *ConfigFactory) createUnassignedNonTerminatedPodLW() *cache.ListWatch {
	selector := fields.ParseSelectorOrDie("spec.nodeName==" + "" + ",status.phase!=" + string(api.PodSucceeded) + ",status.phase!=" + string(api.PodFailed))
	return cache.NewListWatchFromClient(factory.Client.Core().RESTClient(), "pods", api.NamespaceAll, selector)
}

// Returns a cache.ListWatch that finds all pods that are
// already scheduled.
// TODO: return a ListerWatcher interface instead?
func (factory *ConfigFactory) createAssignedNonTerminatedPodLW() *cache.ListWatch {
	selector := fields.ParseSelectorOrDie("spec.nodeName!=" + "" + ",status.phase!=" + string(api.PodSucceeded) + ",status.phase!=" + string(api.PodFailed))
	return cache.NewListWatchFromClient(factory.Client.Core().RESTClient(), "pods", api.NamespaceAll, selector)
}

// createNodeLW returns a cache.ListWatch that gets all changes to nodes.
func (factory *ConfigFactory) createNodeLW() *cache.ListWatch {
	// all nodes are considered to ensure that the scheduler cache has access to all nodes for lookups
	// the NodeCondition is used to filter out the nodes that are not ready or unschedulable
	// the filtered list is used as the super set of nodes to consider for scheduling
	return cache.NewListWatchFromClient(factory.Client.Core().RESTClient(), "nodes", api.NamespaceAll, fields.ParseSelectorOrDie(""))
}

// createPersistentVolumeLW returns a cache.ListWatch that gets all changes to persistentVolumes.
func (factory *ConfigFactory) createPersistentVolumeLW() *cache.ListWatch {
	return cache.NewListWatchFromClient(factory.Client.Core().RESTClient(), "persistentVolumes", api.NamespaceAll, fields.ParseSelectorOrDie(""))
}

// createPersistentVolumeClaimLW returns a cache.ListWatch that gets all changes to persistentVolumeClaims.
func (factory *ConfigFactory) createPersistentVolumeClaimLW() *cache.ListWatch {
	return cache.NewListWatchFromClient(factory.Client.Core().RESTClient(), "persistentVolumeClaims", api.NamespaceAll, fields.ParseSelectorOrDie(""))
}

// Returns a cache.ListWatch that gets all changes to services.
func (factory *ConfigFactory) createServiceLW() *cache.ListWatch {
	return cache.NewListWatchFromClient(factory.Client.Core().RESTClient(), "services", api.NamespaceAll, fields.ParseSelectorOrDie(""))
}

// Returns a cache.ListWatch that gets all changes to controllers.
func (factory *ConfigFactory) createControllerLW() *cache.ListWatch {
	return cache.NewListWatchFromClient(factory.Client.Core().RESTClient(), "replicationControllers", api.NamespaceAll, fields.ParseSelectorOrDie(""))
}

// Returns a cache.ListWatch that gets all changes to replicasets.
func (factory *ConfigFactory) createReplicaSetLW() *cache.ListWatch {
	return cache.NewListWatchFromClient(factory.Client.Extensions().RESTClient(), "replicasets", api.NamespaceAll, fields.ParseSelectorOrDie(""))
}

func (factory *ConfigFactory) makeDefaultErrorFunc(backoff *podBackoff, podQueue *cache.FIFO) func(pod *api.Pod, err error) {
	return func(pod *api.Pod, err error) {
		if err == scheduler.ErrNoNodesAvailable {
			glog.V(4).Infof("Unable to schedule %v %v: no nodes are registered to the cluster; waiting", pod.Namespace, pod.Name)
		} else {
			glog.Errorf("Error scheduling %v %v: %v; retrying", pod.Namespace, pod.Name, err)
		}
		backoff.gc()
		// Retry asynchronously.
		// Note that this is extremely rudimentary and we need a more real error handling path.
		go func() {
			defer runtime.HandleCrash()
			podID := types.NamespacedName{
				Namespace: pod.Namespace,
				Name:      pod.Name,
			}

			entry := backoff.getEntry(podID)
			if !entry.TryWait(backoff.maxDuration) {
				glog.Warningf("Request for pod %v already in flight, abandoning", podID)
				return
			}
			// Get the pod again; it may have changed/been scheduled already.
			getBackoff := initialGetBackoff
			for {
				pod, err := factory.Client.Core().Pods(podID.Namespace).Get(podID.Name)
				if err == nil {
					if len(pod.Spec.NodeName) == 0 {
						podQueue.AddIfNotPresent(pod)
					}
					break
				}
				if errors.IsNotFound(err) {
					glog.Warningf("A pod %v no longer exists", podID)
					return
				}
				glog.Errorf("Error getting pod %v for retry: %v; retrying...", podID, err)
				if getBackoff = getBackoff * 2; getBackoff > maximalGetBackoff {
					getBackoff = maximalGetBackoff
				}
				time.Sleep(getBackoff)
			}
		}()
	}
}

// nodeEnumerator allows a cache.Poller to enumerate items in an api.NodeList
type nodeEnumerator struct {
	*api.NodeList
}

// Len returns the number of items in the node list.
func (ne *nodeEnumerator) Len() int {
	if ne.NodeList == nil {
		return 0
	}
	return len(ne.Items)
}

// Get returns the item (and ID) with the particular index.
func (ne *nodeEnumerator) Get(index int) interface{} {
	return &ne.Items[index]
}

type binder struct {
	Client clientset.Interface
}

// Bind just does a POST binding RPC.
func (b *binder) Bind(binding *api.Binding) error {
	glog.V(3).Infof("Attempting to bind %v to %v", binding.Name, binding.Target.Name)
	ctx := api.WithNamespace(api.NewContext(), binding.Namespace)
	return b.Client.Core().RESTClient().Post().Namespace(api.NamespaceValue(ctx)).Resource("bindings").Body(binding).Do().Error()
	// TODO: use Pods interface for binding once clusters are upgraded
	// return b.Pods(binding.Namespace).Bind(binding)
}

type podConditionUpdater struct {
	Client clientset.Interface
}

func (p *podConditionUpdater) Update(pod *api.Pod, condition *api.PodCondition) error {
	glog.V(2).Infof("Updating pod condition for %s/%s to (%s==%s)", pod.Namespace, pod.Name, condition.Type, condition.Status)
	if api.UpdatePodCondition(&pod.Status, condition) {
		_, err := p.Client.Core().Pods(pod.Namespace).UpdateStatus(pod)
		return err
	}
	return nil
}

type clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time {
	return time.Now()
}

// backoffEntry is single threaded.  in particular, it only allows a single action to be waiting on backoff at a time.
// It is expected that all users will only use the public TryWait(...) method
// It is also not safe to copy this object.
type backoffEntry struct {
	backoff     time.Duration
	lastUpdate  time.Time
	reqInFlight int32
}

// tryLock attempts to acquire a lock via atomic compare and swap.
// returns true if the lock was acquired, false otherwise
func (b *backoffEntry) tryLock() bool {
	return atomic.CompareAndSwapInt32(&b.reqInFlight, 0, 1)
}

// unlock returns the lock.  panics if the lock isn't held
func (b *backoffEntry) unlock() {
	if !atomic.CompareAndSwapInt32(&b.reqInFlight, 1, 0) {
		panic(fmt.Sprintf("unexpected state on unlocking: %+v", b))
	}
}

// TryWait tries to acquire the backoff lock, maxDuration is the maximum allowed period to wait for.
func (b *backoffEntry) TryWait(maxDuration time.Duration) bool {
	if !b.tryLock() {
		return false
	}
	defer b.unlock()
	b.wait(maxDuration)
	return true
}

func (entry *backoffEntry) getBackoff(maxDuration time.Duration) time.Duration {
	duration := entry.backoff
	newDuration := time.Duration(duration) * 2
	if newDuration > maxDuration {
		newDuration = maxDuration
	}
	entry.backoff = newDuration
	glog.V(4).Infof("Backing off %s for pod %+v", duration.String(), entry)
	return duration
}

func (entry *backoffEntry) wait(maxDuration time.Duration) {
	time.Sleep(entry.getBackoff(maxDuration))
}

type podBackoff struct {
	perPodBackoff   map[types.NamespacedName]*backoffEntry
	lock            sync.Mutex
	clock           clock
	defaultDuration time.Duration
	maxDuration     time.Duration
}

func (p *podBackoff) getEntry(podID types.NamespacedName) *backoffEntry {
	p.lock.Lock()
	defer p.lock.Unlock()
	entry, ok := p.perPodBackoff[podID]
	if !ok {
		entry = &backoffEntry{backoff: p.defaultDuration}
		p.perPodBackoff[podID] = entry
	}
	entry.lastUpdate = p.clock.Now()
	return entry
}

func (p *podBackoff) gc() {
	p.lock.Lock()
	defer p.lock.Unlock()
	now := p.clock.Now()
	for podID, entry := range p.perPodBackoff {
		if now.Sub(entry.lastUpdate) > p.maxDuration {
			delete(p.perPodBackoff, podID)
		}
	}
}
