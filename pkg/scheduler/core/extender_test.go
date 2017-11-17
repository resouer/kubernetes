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
	"k8s.io/kubernetes/pkg/scheduler/algorithm"
	schedulerapi "k8s.io/kubernetes/pkg/scheduler/api"
	"k8s.io/kubernetes/pkg/scheduler/schedulercache"
	schedulertesting "k8s.io/kubernetes/pkg/scheduler/testing"
)

type fitPredicate func(pod *v1.Pod, node *v1.Node) (bool, error)
type priorityFunc func(pod *v1.Pod, nodes []*v1.Node) (*schedulerapi.HostPriorityList, error)

type priorityConfig struct {
	function priorityFunc
	weight   int
}

func errorPredicateExtender(pod *v1.Pod, node *v1.Node) (bool, error) {
	return false, fmt.Errorf("Some error")
}

func falsePredicateExtender(pod *v1.Pod, node *v1.Node) (bool, error) {
	return false, nil
}

func truePredicateExtender(pod *v1.Pod, node *v1.Node) (bool, error) {
	return true, nil
}

func machine1PredicateExtender(pod *v1.Pod, node *v1.Node) (bool, error) {
	if node.Name == "machine1" {
		return true, nil
	}
	return false, nil
}

func machine2PredicateExtender(pod *v1.Pod, node *v1.Node) (bool, error) {
	if node.Name == "machine2" {
		return true, nil
	}
	return false, nil
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
// Returns:
// 1. More victim pods (if any) amended by preemption phase of extender.
// 2. Number of violating victim (used to calculate PDB).
// 3. Fits or not after preemption phase on extender's side.
func (f *FakeExtender) selectVictimsOnNodeByExtender(
	pod *v1.Pod,
	node *v1.Node,
	originVictims *schedulerapi.Victims,
	pdbs []*policy.PodDisruptionBudget,
) ([]*v1.Pod, int, bool) {
	if fits, _ := f.runPredicate(pod, node); !fits {
		return nil, 0, false
	}

	return []*v1.Pod{}, 0, true
}

func (f *FakeExtender) SupportsPreemption() bool {
	return true
}

func (f *FakeExtender) ProcessPreemption(
	pod *v1.Pod,
	nodeToVictims map[string]*schedulerapi.Victims,
	nodeNameToInfo map[string]*schedulercache.NodeInfo,
	pdbs []*policy.PodDisruptionBudget,
) (schedulerapi.ExtenderPreemptionResult, error) {
	nodeToVictimsCopy := map[string]*schedulerapi.Victims{}
	// We don't want to change the original nodeToVictims
	for k, v := range nodeToVictims {
		nodeToVictimsCopy[k] = v
	}

	for nodeName, victims := range nodeToVictimsCopy {
		// Try to do preemption on extender side.
		extenderVictimPods, extendernPDBViolations, fits := f.selectVictimsOnNodeByExtender(pod, nodeNameToInfo[nodeName].Node(), victims, pdbs)
		// If it's unfit after extender's preemption, this node is unresolvable by preemption overall,
		// let's remove it from potential preemption nodes.
		if !fits {
			delete(nodeToVictimsCopy, nodeName)
		} else {
			// Append new victims to original victims
			nodeToVictimsCopy[nodeName].Pods = append(victims.Pods, extenderVictimPods...)
			nodeToVictimsCopy[nodeName].NumPDBViolations = victims.NumPDBViolations + extendernPDBViolations
		}
	}
	return nodeToVictimsCopy, nil
}

// runPredicate run predicates of extender one by one for given pod and node.
// Returns: fits or not.
func (f *FakeExtender) runPredicate(pod *v1.Pod, node *v1.Node) (bool, error) {
	fits := true
	var err error
	for _, predicate := range f.predicates {
		fits, err = predicate(pod, node)
		if err != nil {
			return false, err
		}
		if !fits {
			break
		}
	}
	return fits, nil
}

func (f *FakeExtender) Filter(pod *v1.Pod, nodes []*v1.Node, nodeNameToInfo map[string]*schedulercache.NodeInfo) ([]*v1.Node, schedulerapi.FailedNodesMap, error) {
	filtered := []*v1.Node{}
	failedNodesMap := schedulerapi.FailedNodesMap{}
	for _, node := range nodes {
		fits, err := f.runPredicate(pod, node)
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
			cache, nil, queue, test.predicates, algorithm.EmptyPredicateMetadataProducer, test.prioritizers, algorithm.EmptyPriorityMetadataProducer, extenders, nil, schedulertesting.FakePersistentVolumeClaimLister{}, false)
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
