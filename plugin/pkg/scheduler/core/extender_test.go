/*
Copyright 2015 The Kubernetes Authors.

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

package core

import (
	"fmt"
	"testing"
	"time"

	"k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/kubernetes/plugin/pkg/scheduler/algorithm"
	"k8s.io/kubernetes/plugin/pkg/scheduler/algorithm/predicates"
	schedulerapi "k8s.io/kubernetes/plugin/pkg/scheduler/api"
	"k8s.io/kubernetes/plugin/pkg/scheduler/schedulercache"
	schedulertesting "k8s.io/kubernetes/plugin/pkg/scheduler/testing"
	"k8s.io/kubernetes/plugin/pkg/scheduler/util"
)

type fitPredicate func(pod *v1.Pod, nodeInfo *schedulercache.NodeInfo) (bool, error)
type priorityFunc func(pod *v1.Pod, nodes []*v1.Node) (*schedulerapi.HostPriorityList, error)

type priorityConfig struct {
	function priorityFunc
	weight   int
}

func errorPredicateExtender(pod *v1.Pod, nodeInfo *schedulercache.NodeInfo) (bool, error) {
	return false, fmt.Errorf("Some error")
}

func falsePredicateExtender(pod *v1.Pod, nodeInfo *schedulercache.NodeInfo) (bool, error) {
	return false, nil
}

func truePredicateExtender(pod *v1.Pod, nodeInfo *schedulercache.NodeInfo) (bool, error) {
	return true, nil
}

func machine1PredicateExtender(pod *v1.Pod, nodeInfo *schedulercache.NodeInfo) (bool, error) {
	if nodeInfo.Node().GetName() == "machine1" {
		return true, nil
	}
	return false, nil
}

func machine2PredicateExtender(pod *v1.Pod, nodeInfo *schedulercache.NodeInfo) (bool, error) {
	if nodeInfo.Node().GetName() == "machine2" {
		return true, nil
	}
	return false, nil
}

func requestDoubleMemoryPredicateExtender(pod *v1.Pod, nodeInfo *schedulercache.NodeInfo) (bool, error) {
	podRequest := predicates.GetResourceRequest(pod)
	allocatable := nodeInfo.AllocatableResource()

	// This pod can only run on node with no less than 2*(pod memory request) available.
	if allocatable.Memory-nodeInfo.RequestedResource().Memory < podRequest.Memory*2 {
		return false, nil
	}
	return true, nil
}

func errorPrioritizerExtender(pod *v1.Pod, nodes []*v1.Node) (*schedulerapi.HostPriorityList, error) {
	return &schedulerapi.HostPriorityList{}, fmt.Errorf("Some error")
}

func machine1PrioritizerExtender(pod *v1.Pod, nodes []*v1.Node) (*schedulerapi.HostPriorityList, error) {
	result := schedulerapi.HostPriorityList{}
	for _, node := range nodes {
		score := 1
		if node.Name == "machine1" {
			score = 10
		}
		result = append(result, schedulerapi.HostPriority{Host: node.Name, Score: score})
	}
	return &result, nil
}

func machine2PrioritizerExtender(pod *v1.Pod, nodes []*v1.Node) (*schedulerapi.HostPriorityList, error) {
	result := schedulerapi.HostPriorityList{}
	for _, node := range nodes {
		score := 1
		if node.Name == "machine2" {
			score = 10
		}
		result = append(result, schedulerapi.HostPriority{Host: node.Name, Score: score})
	}
	return &result, nil
}

func machine2Prioritizer(_ *v1.Pod, nodeNameToInfo map[string]*schedulercache.NodeInfo, nodes []*v1.Node) (schedulerapi.HostPriorityList, error) {
	result := []schedulerapi.HostPriority{}
	for _, node := range nodes {
		score := 1
		if node.Name == "machine2" {
			score = 10
		}
		result = append(result, schedulerapi.HostPriority{Host: node.Name, Score: score})
	}
	return result, nil
}

type FakeExtender struct {
	predicates       []fitPredicate
	prioritizers     []priorityConfig
	weight           int
	nodeCacheCapable bool
	filteredNodes    []*v1.Node
}

// selectVictimsOnNodeByExtender checks the given nodes->pods map with predicates on extender's side.
// TODO(harry): This is now the same logic as default scheduler, consider simplify it.
// Returns:
// 1. More victim pods (if any) amended by preemption phase of extender.
// 2. Fits or not after preemption phase on extender's side.
func (f *FakeExtender) selectVictimsOnNodeByExtender(
	pod *v1.Pod,
	nodeInfo *schedulercache.NodeInfo,
	originVictims *schedulerapi.Victims,
	pdbs []*policy.PodDisruptionBudget,
) ([]*v1.Pod, int, bool) {
	potentialVictims := util.SortableList{CompFunc: util.HigherPriorityPod}
	nodeInfoCopy := nodeInfo.Clone()

	removePod := func(rp *v1.Pod) {
		nodeInfoCopy.RemovePod(rp)
	}
	addPod := func(ap *v1.Pod) {
		nodeInfoCopy.AddPod(ap)
	}

	// Remove existing victims as they should be preempted anyway
	for _, p := range originVictims.Pods {
		removePod(p)
	}

	// Then check the rest pods with extender. This should be similar to preemption process of default scheduler.
	// As the first step, remove all the lower priority pods from the node and
	// check if the given pod can be scheduled.
	podPriority := util.GetPodPriority(pod)
	for _, p := range nodeInfoCopy.Pods() {
		if util.GetPodPriority(p) < podPriority {
			potentialVictims.Items = append(potentialVictims.Items, p)
			removePod(p)
		}
	}
	potentialVictims.Sort()

	// If the new pod does not fit after removing all the lower priority pods,
	// we are almost done and this node is not suitable for preemption.
	if fits, _ := f.doPredicate(pod, nodeInfoCopy); !fits {
		return nil, 0, false
	}

	victims := []*v1.Pod{}
	numViolatingVictim := 0
	// Try to reprieve as many pods as possible. We first try to reprieve the PDB
	// violating victims and then other non-violating ones. In both cases, we start
	// from the highest priority victims.
	violatingVictims, nonViolatingVictims := util.FilterPodsWithPDBViolation(potentialVictims.Items, pdbs)
	reprievePod := func(p *v1.Pod) bool {
		addPod(p)
		fits, _ := f.doPredicate(pod, nodeInfoCopy)
		if !fits {
			removePod(p)
			victims = append(victims, p)
		}
		return fits
	}
	for _, p := range violatingVictims {
		if !reprievePod(p) {
			numViolatingVictim++
		}
	}

	// Now we try to reprieve non-violating victims.
	for _, p := range nonViolatingVictims {
		reprievePod(p)
	}
	return victims, numViolatingVictim, true
}

func (f *FakeExtender) ProcessPreemption(
	pod *v1.Pod,
	nodeToVictims map[*v1.Node]*schedulerapi.Victims,
	nodeNameToInfo map[string]*schedulercache.NodeInfo,
	pdbs []*policy.PodDisruptionBudget,
) (schedulerapi.ExtenderPreemptionResult, error) {
	nodeToVictimsCopy := map[*v1.Node]*schedulerapi.Victims{}
	// We don't want to change the original nodeToVictims
	for k, v := range nodeToVictims {
		nodeToVictimsCopy[k] = v
	}

	for node, victims := range nodeToVictimsCopy {
		// Try to do preemption on extender side.
		extenderVictimPods, extendernPDBViolations, fits := f.selectVictimsOnNodeByExtender(pod, nodeNameToInfo[node.GetName()], victims, pdbs)
		// If it's unfit after extender's preemption, this node is unresolvable by preemption overall,
		// let's remove it from potential preemption nodes.
		if !fits {
			delete(nodeToVictimsCopy, node)
		} else {
			// Append new victims to original victims
			nodeToVictimsCopy[node].Pods = append(victims.Pods, extenderVictimPods...)
			nodeToVictimsCopy[node].NumPDBViolations = victims.NumPDBViolations + extendernPDBViolations
		}
	}
	return nodeToVictimsCopy, nil
}

// doPredicate run predicates of extender one by one for given pod and node.
// Returns: fits or not.
func (f *FakeExtender) doPredicate(pod *v1.Pod, nodeInfo *schedulercache.NodeInfo) (bool, error) {
	fits := true
	for _, predicate := range f.predicates {
		fit, err := predicate(pod, nodeInfo)
		if err != nil {
			return false, err
		}
		if !fit {
			fits = false
			break
		}
	}
	return fits, nil
}

func (f *FakeExtender) Filter(pod *v1.Pod, nodes []*v1.Node, nodeNameToInfo map[string]*schedulercache.NodeInfo) ([]*v1.Node, schedulerapi.FailedNodesMap, error) {
	filtered := []*v1.Node{}
	failedNodesMap := schedulerapi.FailedNodesMap{}
	for _, node := range nodes {
		fits, err := f.doPredicate(pod, nodeNameToInfo[node.GetName()])
		if err != nil {
			return []*v1.Node{}, schedulerapi.FailedNodesMap{}, err
		}
		if fits {
			filtered = append(filtered, node)
		} else {
			failedNodesMap[node.Name] = "FakeExtender failed"
		}
	}

	f.filteredNodes = filtered
	if f.nodeCacheCapable {
		return filtered, failedNodesMap, nil
	}
	return filtered, failedNodesMap, nil
}

func (f *FakeExtender) Prioritize(pod *v1.Pod, nodes []*v1.Node) (*schedulerapi.HostPriorityList, int, error) {
	result := schedulerapi.HostPriorityList{}
	combinedScores := map[string]int{}
	for _, prioritizer := range f.prioritizers {
		weight := prioritizer.weight
		if weight == 0 {
			continue
		}
		priorityFunc := prioritizer.function
		prioritizedList, err := priorityFunc(pod, nodes)
		if err != nil {
			return &schedulerapi.HostPriorityList{}, 0, err
		}
		for _, hostEntry := range *prioritizedList {
			combinedScores[hostEntry.Host] += hostEntry.Score * weight
		}
	}
	for host, score := range combinedScores {
		result = append(result, schedulerapi.HostPriority{Host: host, Score: score})
	}
	return &result, f.weight, nil
}

func (f *FakeExtender) Bind(binding *v1.Binding) error {
	if len(f.filteredNodes) != 0 {
		for _, node := range f.filteredNodes {
			if node.Name == binding.Target.Name {
				f.filteredNodes = nil
				return nil
			}
		}
		err := fmt.Errorf("Node %v not in filtered nodes %v", binding.Target.Name, f.filteredNodes)
		f.filteredNodes = nil
		return err
	}
	return nil
}

func (f *FakeExtender) IsBinder() bool {
	return true
}

var _ algorithm.SchedulerExtender = &FakeExtender{}

func TestGenericSchedulerWithExtenders(t *testing.T) {
	tests := []struct {
		name                 string
		predicates           map[string]algorithm.FitPredicate
		prioritizers         []algorithm.PriorityConfig
		extenders            []FakeExtender
		extenderPredicates   []fitPredicate
		extenderPrioritizers []priorityConfig
		nodes                []string
		expectedHost         string
		expectsErr           bool
	}{
		{
			predicates:   map[string]algorithm.FitPredicate{"true": truePredicate},
			prioritizers: []algorithm.PriorityConfig{{Map: EqualPriorityMap, Weight: 1}},
			extenders: []FakeExtender{
				{
					predicates: []fitPredicate{truePredicateExtender},
				},
				{
					predicates: []fitPredicate{errorPredicateExtender},
				},
			},
			nodes:      []string{"machine1", "machine2"},
			expectsErr: true,
			name:       "test 1",
		},
		{
			predicates:   map[string]algorithm.FitPredicate{"true": truePredicate},
			prioritizers: []algorithm.PriorityConfig{{Map: EqualPriorityMap, Weight: 1}},
			extenders: []FakeExtender{
				{
					predicates: []fitPredicate{truePredicateExtender},
				},
				{
					predicates: []fitPredicate{falsePredicateExtender},
				},
			},
			nodes:      []string{"machine1", "machine2"},
			expectsErr: true,
			name:       "test 2",
		},
		{
			predicates:   map[string]algorithm.FitPredicate{"true": truePredicate},
			prioritizers: []algorithm.PriorityConfig{{Map: EqualPriorityMap, Weight: 1}},
			extenders: []FakeExtender{
				{
					predicates: []fitPredicate{truePredicateExtender},
				},
				{
					predicates: []fitPredicate{machine1PredicateExtender},
				},
			},
			nodes:        []string{"machine1", "machine2"},
			expectedHost: "machine1",
			name:         "test 3",
		},
		{
			predicates:   map[string]algorithm.FitPredicate{"true": truePredicate},
			prioritizers: []algorithm.PriorityConfig{{Map: EqualPriorityMap, Weight: 1}},
			extenders: []FakeExtender{
				{
					predicates: []fitPredicate{machine2PredicateExtender},
				},
				{
					predicates: []fitPredicate{machine1PredicateExtender},
				},
			},
			nodes:      []string{"machine1", "machine2"},
			expectsErr: true,
			name:       "test 4",
		},
		{
			predicates:   map[string]algorithm.FitPredicate{"true": truePredicate},
			prioritizers: []algorithm.PriorityConfig{{Map: EqualPriorityMap, Weight: 1}},
			extenders: []FakeExtender{
				{
					predicates:   []fitPredicate{truePredicateExtender},
					prioritizers: []priorityConfig{{errorPrioritizerExtender, 10}},
					weight:       1,
				},
			},
			nodes:        []string{"machine1"},
			expectedHost: "machine1",
			name:         "test 5",
		},
		{
			predicates:   map[string]algorithm.FitPredicate{"true": truePredicate},
			prioritizers: []algorithm.PriorityConfig{{Map: EqualPriorityMap, Weight: 1}},
			extenders: []FakeExtender{
				{
					predicates:   []fitPredicate{truePredicateExtender},
					prioritizers: []priorityConfig{{machine1PrioritizerExtender, 10}},
					weight:       1,
				},
				{
					predicates:   []fitPredicate{truePredicateExtender},
					prioritizers: []priorityConfig{{machine2PrioritizerExtender, 10}},
					weight:       5,
				},
			},
			nodes:        []string{"machine1", "machine2"},
			expectedHost: "machine2",
			name:         "test 6",
		},
		{
			predicates:   map[string]algorithm.FitPredicate{"true": truePredicate},
			prioritizers: []algorithm.PriorityConfig{{Function: machine2Prioritizer, Weight: 20}},
			extenders: []FakeExtender{
				{
					predicates:   []fitPredicate{truePredicateExtender},
					prioritizers: []priorityConfig{{machine1PrioritizerExtender, 10}},
					weight:       1,
				},
			},
			nodes:        []string{"machine1", "machine2"},
			expectedHost: "machine2", // machine2 has higher score
			name:         "test 7",
		},
	}

	for _, test := range tests {
		extenders := []algorithm.SchedulerExtender{}
		for ii := range test.extenders {
			extenders = append(extenders, &test.extenders[ii])
		}
		cache := schedulercache.New(time.Duration(0), wait.NeverStop)
		for _, name := range test.nodes {
			cache.AddNode(&v1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}})
		}
		queue := NewSchedulingQueue()
		scheduler := NewGenericScheduler(
			cache, nil, queue, test.predicates, algorithm.EmptyPredicateMetadataProducer, test.prioritizers, algorithm.EmptyMetadataProducer, extenders, nil)
		podIgnored := &v1.Pod{}
		machine, err := scheduler.Schedule(podIgnored, schedulertesting.FakeNodeLister(makeNodeList(test.nodes)))
		if test.expectsErr {
			if err == nil {
				t.Errorf("Unexpected non-error for %s, machine %s", test.name, machine)
			}
		} else {
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				continue
			}
			if test.expectedHost != machine {
				t.Errorf("Failed : %s, Expected: %s, Saw: %s", test.name, test.expectedHost, machine)
			}
		}
	}
}
