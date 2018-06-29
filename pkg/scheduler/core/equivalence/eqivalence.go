/*
Copyright 2016 The Kubernetes Authors.

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

// Package equivalence defines Pod equivalence classes and the equivalence class
// cache.
package equivalence

import (
	"fmt"
	"hash/fnv"
	"sync"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/kubernetes/pkg/scheduler/algorithm"
	"k8s.io/kubernetes/pkg/scheduler/algorithm/predicates"
	schedulercache "k8s.io/kubernetes/pkg/scheduler/cache"
	hashutil "k8s.io/kubernetes/pkg/util/hash"
)

// nodeMap is type for a map of nodeName:Cache
type nodeMap map[string]*Cache

// TopLevelEquivCache is a thread safe nodeMap
type TopLevelEquivCache struct {
	nodeToCache nodeMap
}

// NewTopLevelEquivCache populate equiv class cache with all existing nodes
// It should called before scheduling process begin.
// TODO(harry): evaluate sync.Map here. If we require lock invalidates as well.
func NewTopLevelEquivCache() *TopLevelEquivCache {
	return &TopLevelEquivCache{
		nodeToCache: make(nodeMap),
	}
}

// PopulateNodes initializes TopLevelEquivCache with given nodes before scheduling
// loop begin.
func (n *TopLevelEquivCache) PopulateNodes(nodes []*v1.Node) {
	for _, node := range nodes {
		n.AddNode(node.GetName())
	}
}

// AddNode adds node to TopLevelEquivCache
func (n *TopLevelEquivCache) AddNode(name string) {
	n.nodeToCache[name] = newCache()
}

// AddNode removes node from TopLevelEquivCache
func (n *TopLevelEquivCache) RemoveNode(name string) {
	c := n.GetCache(name)
	// In case scheduling is using this second level Cache.
	c.cu.Lock()
	defer c.cu.Unlock()
	delete(n.nodeToCache, name)
}

// GetCache returns the second level Cache for given node name.
func (n *TopLevelEquivCache) GetCache(name string) *Cache {
	return n.nodeToCache[name]
}

// removeCachedPreds deletes cached predicates by given keys.
// This function is thread safe for second level Cache, no need to sync with top level cache.
func (c *Cache) removeCachedPreds(predicateKeys sets.String) {
	c.cu.Lock()
	defer c.cu.Unlock()
	for predicateKey := range predicateKeys {
		delete(c.cache, predicateKey)
	}
}

// InvalidatePredicates clears all cached results for the given predicates.
// TODO(harry): This function is expensive, think twice before use it.
func (n *TopLevelEquivCache) InvalidatePredicates(predicateKeys sets.String) {
	if len(predicateKeys) == 0 {
		return
	}
	for _, c := range n.nodeToCache {
		c.removeCachedPreds(predicateKeys)
	}
}

// InvalidatePredicatesOnNode clears cached results for the given predicates on one node.
func (n *TopLevelEquivCache) InvalidatePredicatesOnNode(nodeName string, predicateKeys sets.String) {
	if len(predicateKeys) == 0 {
		return
	}
	c := n.GetCache(nodeName)
	c.removeCachedPreds(predicateKeys)
}

// InvalidateAllPredicatesOnNode clears all cached results for one node.
func (n *TopLevelEquivCache) InvalidateAllPredicatesOnNode(nodeName string) {
	c := n.GetCache(nodeName)
	// In case scheduling is using this second level Cache.
	c.cu.Lock()
	defer c.cu.Unlock()
	n.nodeToCache[nodeName] = newCache()
}

// InvalidateCachedPredicateItemForPodAdd is a wrapper of
// InvalidateCachedPredicateItem for pod add case
// TODO: This does not belong with the equivalence cache implementation.
func (n *TopLevelEquivCache) InvalidateCachedPredicateItemForPodAdd(pod *v1.Pod, nodeName string) {
	// MatchInterPodAffinity: we assume scheduler can make sure newly bound pod
	// will not break the existing inter pod affinity. So we does not need to
	// invalidate MatchInterPodAffinity when pod added.
	//
	// But when a pod is deleted, existing inter pod affinity may become invalid.
	// (e.g. this pod was preferred by some else, or vice versa)
	//
	// NOTE: assumptions above will not stand when we implemented features like
	// RequiredDuringSchedulingRequiredDuringExecution.

	// NoDiskConflict: the newly scheduled pod fits to existing pods on this node,
	// it will also fits to equivalence class of existing pods

	// GeneralPredicates: will always be affected by adding a new pod
	invalidPredicates := sets.NewString(predicates.GeneralPred)

	// MaxPDVolumeCountPredicate: we check the volumes of pod to make decision.
	for _, vol := range pod.Spec.Volumes {
		if vol.PersistentVolumeClaim != nil {
			invalidPredicates.Insert(predicates.MaxEBSVolumeCountPred, predicates.MaxGCEPDVolumeCountPred, predicates.MaxAzureDiskVolumeCountPred)
		} else {
			if vol.AWSElasticBlockStore != nil {
				invalidPredicates.Insert(predicates.MaxEBSVolumeCountPred)
			}
			if vol.GCEPersistentDisk != nil {
				invalidPredicates.Insert(predicates.MaxGCEPDVolumeCountPred)
			}
			if vol.AzureDisk != nil {
				invalidPredicates.Insert(predicates.MaxAzureDiskVolumeCountPred)
			}
		}
	}
	n.InvalidatePredicatesOnNode(nodeName, invalidPredicates)
}

// Cache saves and reuses the output of predicate functions. Use RunPredicate to
// get or update the cached results. An appropriate Invalidate* function should
// be called when some predicate results are no longer valid.
//
// Internally, results are keyed by node name, predicate name, and "equivalence
// class". (Equivalence class is defined in the `Class` type.) Saved results
// will be reused until an appropriate invalidation function is called.
type Cache struct {
	cu    sync.RWMutex
	cache predicateMap
}

// newCache returns an empty Cache.
func newCache() *Cache {
	return &Cache{
		cache: make(predicateMap),
	}
}

// Class represents a set of pods which are equivalent from the perspective of
// the scheduler. i.e. the scheduler would make the same decision for any pod
// from the same class.
type Class struct {
	// Equivalence hash
	hash uint64
}

// NewClass returns the equivalence class for a given Pod. The returned Class
// objects will be equal for two Pods in the same class. nil values should not
// be considered equal to each other.
//
// NOTE: Make sure to compare types of Class and not *Class.
// TODO(misterikkit): Return error instead of nil *Class.
func NewClass(pod *v1.Pod) *Class {
	equivalencePod := getEquivalencePod(pod)
	if equivalencePod != nil {
		hash := fnv.New32a()
		hashutil.DeepHashObject(hash, equivalencePod)
		return &Class{
			hash: uint64(hash.Sum32()),
		}
	}
	return nil
}

// predicateMap stores resultMaps with predicate name as the key.
type predicateMap map[string]resultMap

// resultMap stores PredicateResult with pod equivalence hash as the key.
type resultMap map[uint64]predicateResult

// predicateResult stores the output of a FitPredicate.
type predicateResult struct {
	Fit         bool
	FailReasons []algorithm.PredicateFailureReason
}

// RunPredicate returns a cached predicate result. In case of a cache miss, the predicate will be
// run and its results cached for the next call.
//
// NOTE: RunPredicate will not update the equivalence cache if the given NodeInfo is stale.
func (c *Cache) RunPredicate(
	pred algorithm.FitPredicate,
	predicateKey string,
	pod *v1.Pod,
	meta algorithm.PredicateMetadata,
	nodeInfo *schedulercache.NodeInfo,
	equivClass *Class,
	cache schedulercache.Cache,
) (bool, []algorithm.PredicateFailureReason, error) {
	if nodeInfo == nil || nodeInfo.Node() == nil {
		// This may happen during tests.
		return false, []algorithm.PredicateFailureReason{}, fmt.Errorf("nodeInfo is nil or node is invalid")
	}

	result, ok := c.lookupResult(pod.GetName(), nodeInfo.Node().GetName(), predicateKey, equivClass.hash)
	if ok {
		return result.Fit, result.FailReasons, nil
	}
	fit, reasons, err := pred(pod, meta, nodeInfo)
	if err != nil {
		return fit, reasons, err
	}
	if cache != nil {
		c.updateResult(pod.GetName(), predicateKey, fit, reasons, equivClass.hash, cache, nodeInfo)
	}
	return fit, reasons, nil
}

// updateResult updates the cached result of a predicate.
// This function is thread safe for second level Cache, no need to sync with top level cache.
func (c *Cache) updateResult(
	podName, predicateKey string,
	fit bool,
	reasons []algorithm.PredicateFailureReason,
	equivalenceHash uint64,
	cache schedulercache.Cache,
	nodeInfo *schedulercache.NodeInfo,
) {
	if nodeInfo == nil || nodeInfo.Node() == nil {
		// This may happen during tests.
		return
	}
	// Skip update if NodeInfo is stale.
	if !cache.IsUpToDate(nodeInfo) {
		return
	}

	predicateItem := predicateResult{
		Fit:         fit,
		FailReasons: reasons,
	}

	c.cu.Lock()
	defer c.cu.Unlock()
	// if cached predicate map already exists, just update the predicate by key
	if predicates, ok := c.cache[predicateKey]; ok {
		// maps in golang are references, no need to add them back
		predicates[equivalenceHash] = predicateItem
	} else {
		c.cache[predicateKey] =
			resultMap{
				equivalenceHash: predicateItem,
			}
	}
}

// lookupResult returns cached predicate results and a bool saying whether a
// cache entry was found.
// This function is thread safe for second level Cache, no need to sync with top level cache.
func (c *Cache) lookupResult(
	podName, nodeName, predicateKey string,
	equivalenceHash uint64,
) (value predicateResult, ok bool) {
	c.cu.RLock()
	defer c.cu.RUnlock()
	value, ok = c.cache[predicateKey][equivalenceHash]
	return value, ok
}

// equivalencePod is the set of pod attributes which must match for two pods to
// be considered equivalent for scheduling purposes. For correctness, this must
// include any Pod field which is used by a FitPredicate.
//
// NOTE: For equivalence hash to be formally correct, lists and maps in the
// equivalencePod should be normalized. (e.g. by sorting them) However, the vast
// majority of equivalent pod classes are expected to be created from a single
// pod template, so they will all have the same ordering.
type equivalencePod struct {
	Namespace      *string
	Labels         map[string]string
	Affinity       *v1.Affinity
	Containers     []v1.Container // See note about ordering
	InitContainers []v1.Container // See note about ordering
	NodeName       *string
	NodeSelector   map[string]string
	Tolerations    []v1.Toleration
	Volumes        []v1.Volume // See note about ordering
}

// getEquivalencePod returns a normalized representation of a pod so that two
// "equivalent" pods will hash to the same value.
func getEquivalencePod(pod *v1.Pod) *equivalencePod {
	ep := &equivalencePod{
		Namespace:      &pod.Namespace,
		Labels:         pod.Labels,
		Affinity:       pod.Spec.Affinity,
		Containers:     pod.Spec.Containers,
		InitContainers: pod.Spec.InitContainers,
		NodeName:       &pod.Spec.NodeName,
		NodeSelector:   pod.Spec.NodeSelector,
		Tolerations:    pod.Spec.Tolerations,
		Volumes:        pod.Spec.Volumes,
	}
	// DeepHashObject considers nil and empty slices to be different. Normalize them.
	if len(ep.Containers) == 0 {
		ep.Containers = nil
	}
	if len(ep.InitContainers) == 0 {
		ep.InitContainers = nil
	}
	if len(ep.Tolerations) == 0 {
		ep.Tolerations = nil
	}
	if len(ep.Volumes) == 0 {
		ep.Volumes = nil
	}
	// Normalize empty maps also.
	if len(ep.Labels) == 0 {
		ep.Labels = nil
	}
	if len(ep.NodeSelector) == 0 {
		ep.NodeSelector = nil
	}
	// TODO(misterikkit): Also normalize nested maps and slices.
	return ep
}
