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

package scheduler

import (
	"hash/adler32"
	"sync"

	"k8s.io/kubernetes/pkg/api/v1"
	hashutil "k8s.io/kubernetes/pkg/util/hash"
	"k8s.io/kubernetes/pkg/util/sets"
	"k8s.io/kubernetes/plugin/pkg/scheduler/algorithm"

	"github.com/golang/glog"
	"github.com/hashicorp/golang-lru"
)

// we use predicate names as cache's key, its count is limited
// this number should be larger than max cluster size
const maxCacheEntries = 5 * 1024

type HostPredicate struct {
	Fit         bool
	FailReasons []algorithm.PredicateFailureReason
}

type AlgorithmCache struct {
	// Only consider predicates for now, priorities rely on: #31606
	predicatesCache *lru.Cache
}

// PredicateMap use predicate string as key
type PredicateMap map[string]HostPredicate

func newAlgorithmCache() AlgorithmCache {
	cache, _ := lru.New(maxCacheEntries)
	return AlgorithmCache{
		predicatesCache: cache,
	}
}

// Store a map of predicate cache with maxsize
type EquivalenceCache struct {
	sync.RWMutex
	getEquivalencePod algorithm.GetEquivalencePodFunc
	algorithmCache    map[uint64]AlgorithmCache
}

func NewEquivalenceCache(getEquivalencePodFunc algorithm.GetEquivalencePodFunc) *EquivalenceCache {
	return &EquivalenceCache{
		getEquivalencePod: getEquivalencePodFunc,
		algorithmCache:    make(map[uint64]AlgorithmCache),
	}
}

func (ec *EquivalenceCache) UpdateCachedPredicateItem(pod *v1.Pod, nodeName, predicateKey string, fit bool, reasons []algorithm.PredicateFailureReason, equivalenceHash uint64) {
	ec.Lock()
	defer ec.Unlock()
	if _, exist := ec.algorithmCache[equivalenceHash]; !exist {
		ec.algorithmCache[equivalenceHash] = newAlgorithmCache()
	}
	predicateItem := HostPredicate{
		Fit:         fit,
		FailReasons: reasons,
	}
	// if cached predicate map already exists, just update the predicate by key
	if v, ok := ec.algorithmCache[equivalenceHash].predicatesCache.Get(nodeName); ok {
		predicateMap := v.(PredicateMap)
		predicateMap[predicateKey] = predicateItem
		ec.algorithmCache[equivalenceHash].predicatesCache.Add(nodeName, predicateMap)

	} else {
		ec.algorithmCache[equivalenceHash].predicatesCache.Add(nodeName,
			PredicateMap{
				predicateKey: predicateItem,
			})
	}
	glog.V(5).Infof("Updated cached predicate: %v on node: %s, with item %v", predicateKey, nodeName, predicateItem)
}

// TryPredicateWithECache returns:
// 1. if fit
// 2. reasons if not fit
// 3. if this cache is invalid
// based on cached predicate results
func (ec *EquivalenceCache) PredicateWithECache(pod *v1.Pod, nodeName, predicateKey string, equivalenceHash uint64) (bool, []algorithm.PredicateFailureReason, bool) {
	ec.RLock()
	defer ec.RUnlock()
	if algorithmCache, exist := ec.algorithmCache[equivalenceHash]; exist {
		if v, exist := algorithmCache.predicatesCache.Get(nodeName); exist {
			predicateMap := v.(PredicateMap)
			if hostPredicate, ok := predicateMap[predicateKey]; ok {
				if hostPredicate.Fit {
					return true, []algorithm.PredicateFailureReason{}, false
				} else {
					return false, hostPredicate.FailReasons, false
				}
			} else {
				return false, []algorithm.PredicateFailureReason{}, true
			}
		}
	}
	// if equivalence cache does not exist, also consider as invalid
	return false, []algorithm.PredicateFailureReason{}, true
}

// InvalidCachedPredicateItem marks all items of given predicateKeys, of all pods, on the given node as invalid
func (ec *EquivalenceCache) InvalidCachedPredicateItem(nodeName string, predicateKeys sets.String) {
	if len(predicateKeys) == 0 {
		return
	}
	ec.Lock()
	defer ec.Unlock()
	for _, algorithmCache := range ec.algorithmCache {
		if v, exist := algorithmCache.predicatesCache.Get(nodeName); exist {
			predicateMap := v.(PredicateMap)
			for predicateKey := range predicateKeys {
				delete(predicateMap, predicateKey)
			}
		}
	}
	glog.V(5).Infof("Invalidate cached predicates: %v on node: %s", predicateKeys, nodeName)
}

// InvalidCachedPredicateItemOfAllNodes marks all items of given predicateKeys, of all pods, on all node as invalid
func (ec *EquivalenceCache) InvalidCachedPredicateItemOfAllNodes(predicateKeys sets.String) {
	if len(predicateKeys) == 0 {
		return
	}
	ec.Lock()
	defer ec.Unlock()
	for equivalenceHash, algorithmCache := range ec.algorithmCache {
		for nodeName := range algorithmCache.predicatesCache.Keys() {
			if v, exist := algorithmCache.predicatesCache.Get(nodeName); exist {
				predicateMap := v.(PredicateMap)
				for predicateKey := range predicateKeys {
					// set the pod's item in predicateMap as invalid
					delete(predicateMap, predicateKey)
					// and add the predicateMap back to the cache
					ec.algorithmCache[equivalenceHash].predicatesCache.Add(nodeName, predicateMap)
				}
			}
		}
	}
	glog.V(5).Infof("Invalidate cached predicates: %v on all node", predicateKeys)
}

// InvalidAllCachedPredicateItemOfNode marks all cached items on given node as invalid
func (ec *EquivalenceCache) InvalidAllCachedPredicateItemOfNode(nodeName string) {
	ec.Lock()
	defer ec.Unlock()
	for _, algorithmCache := range ec.algorithmCache {
		algorithmCache.predicatesCache.Remove(nodeName)
	}

	glog.V(5).Infof("Invalidate all cached predicates on node: %s", nodeName)
}

// InvalidCachedPredicateItemForPod marks item of given predicateKeys, of given pod, on the given node as invalid
func (ec *EquivalenceCache) InvalidCachedPredicateItemForPod(nodeName string, predicateKeys sets.String, pod *v1.Pod) {
	if len(predicateKeys) == 0 {
		return
	}
	ec.Lock()
	defer ec.Unlock()
	equivalenceHash := ec.getHashEquivalencePod(pod)
	if equivalenceHash == 0 {
		// no equivalence pod found, just return
		return
	}
	if algorithmCache, exist := ec.algorithmCache[equivalenceHash]; exist {
		if v, exist := algorithmCache.predicatesCache.Get(nodeName); exist {
			predicateMap := v.(PredicateMap)
			for predicateKey := range predicateKeys {
				// set the pod's item in predicateMap as invalid
				delete(predicateMap, predicateKey)
				// and add the predicateMap back to the cache
				ec.algorithmCache[equivalenceHash].predicatesCache.Add(nodeName, predicateMap)
			}
		}
	}

	glog.V(5).Infof("Invalidate cached predicates %v on node %s, for pod %v", predicateKeys, nodeName, pod.GetName())
}

// InvalidCachedPredicateItemForPodAdd is a wrapper of InvalidCachedPredicateItem for pod add case
func (ec *EquivalenceCache) InvalidCachedPredicateItemForPodAdd(pod *v1.Pod, nodeName string) {
	invalidPredicates := sets.NewString("GeneralPredicates")

	// if len(predicates.GetUsedPorts(pod)) != 0 we should also invalidate "PodFitsHostPorts", but it has been
	// included in "GeneralPredicates"

	// If a pod scheduled to a node with these PV, it may cause disk conflict.
	for _, volume := range pod.Spec.Volumes {
		if volume.GCEPersistentDisk != nil || volume.AWSElasticBlockStore != nil || volume.RBD != nil {
			invalidPredicates.Insert("NoDiskConflict")
		}
	}
	ec.InvalidCachedPredicateItem(nodeName, invalidPredicates)

	// MatchInterPodAffinity, we assume scheduler can make sure newly binded pod will not break the existing
	// inter pod affinity. So we does not need to invalidate anything when pod added.
	// But when a pod is deleted, existing inter pod affinity may be become invalid. (e.g. this pod is preferred or vice versa)
	// NOTE: assumptions above will not stand when we implemented features like RequiredDuringSchedulingRequiredDuringExecution.
}

// getHashEquivalencePod returns the hash of equivalence pod.
// if no equivalence pod found, return 0
func (ec *EquivalenceCache) getHashEquivalencePod(pod *v1.Pod) uint64 {
	equivalencePod := ec.getEquivalencePod(pod)
	if equivalencePod != nil {
		hash := adler32.New()
		hashutil.DeepHashObject(hash, equivalencePod)
		return uint64(hash.Sum32())
	}
	return 0
}
