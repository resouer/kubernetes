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

package priorities

import (
	"time"

	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/plugin/pkg/scheduler/algorithm"
	schedulerapi "k8s.io/kubernetes/plugin/pkg/scheduler/api"
	"k8s.io/kubernetes/plugin/pkg/scheduler/schedulercache"
)

type LeastNewlyCreatedPods struct {
	newPodAge int
}

func NewLeastNewlyCreatedPodsPriority(newPodAge int) algorithm.PriorityFunction {
	leastNewPods := &LeastNewlyCreatedPods{
		newPodAge: newPodAge,
	}
	return leastNewPods.LeastNewlyCreatedPodsPriority
}

// LeastNewlyCreatedPodsPriority prefers node with less newly created pod so we can spread the burden of pod creating
func (lnp *LeastNewlyCreatedPods) LeastNewlyCreatedPodsPriority(pod *api.Pod, nodeNameToInfo map[string]*schedulercache.NodeInfo, nodeLister algorithm.NodeLister) (schedulerapi.HostPriorityList, error) {
	nodes, err := nodeLister.List()
	if err != nil {
		return schedulerapi.HostPriorityList{}, err
	}

	list := schedulerapi.HostPriorityList{}
	for _, node := range nodes.Items {
		list = append(list, lnp.calculateLeastNewlyCreatedPodsPriority(node, nodeNameToInfo[node.Name]))
	}
	return list, nil
}

func (lnp *LeastNewlyCreatedPods) calculateLeastNewlyCreatedPodsPriority(node api.Node, nodeInfo *schedulercache.NodeInfo) schedulerapi.HostPriority {
	var count int
	for _, pod := range nodeInfo.Pods() {
		// if this pod is younger than NewPodAge, consider it as newly created
		if pod.GetCreationTimestamp().After(time.Now().Add(-time.Duration(lnp.newPodAge) * time.Second)) {
			count++
		}
	}
	// calculate score by count
	var score int
	if podLength := len(nodeInfo.Pods()); podLength != 0 {
		score = 10 - int(float64(count)/float64(podLength)*10.0)
	} else {
		score = 10
	}

	return schedulerapi.HostPriority{
		Host:  node.Name,
		Score: score,
	}
}
