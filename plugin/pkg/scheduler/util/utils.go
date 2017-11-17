/*
Copyright 2017 The Kubernetes Authors.

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

package util

import (
	"fmt"
	"sort"

	"k8s.io/api/core/v1"
	policy "k8s.io/api/policy/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apiserver/pkg/util/feature"
	"k8s.io/kubernetes/pkg/apis/scheduling"
	"k8s.io/kubernetes/pkg/features"
)

const DefaultBindAllHostIP = "0.0.0.0"

// GetUsedPorts returns the used host ports of Pods: if 'port' was used, a 'port:true' pair
// will be in the result; but it does not resolve port conflict.
func GetUsedPorts(pods ...*v1.Pod) map[string]bool {
	ports := make(map[string]bool)
	for _, pod := range pods {
		for j := range pod.Spec.Containers {
			container := &pod.Spec.Containers[j]
			for k := range container.Ports {
				podPort := &container.Ports[k]
				// "0" is explicitly ignored in PodFitsHostPorts,
				// which is the only function that uses this value.
				if podPort.HostPort != 0 {
					// user does not explicitly set protocol, default is tcp
					portProtocol := podPort.Protocol
					if podPort.Protocol == "" {
						portProtocol = v1.ProtocolTCP
					}

					// user does not explicitly set hostIP, default is 0.0.0.0
					portHostIP := podPort.HostIP
					if podPort.HostIP == "" {
						portHostIP = "0.0.0.0"
					}

					str := fmt.Sprintf("%s/%s/%d", portProtocol, portHostIP, podPort.HostPort)
					ports[str] = true
				}
			}
		}
	}
	return ports
}

// PodPriorityEnabled indicates whether pod priority feature is enabled.
func PodPriorityEnabled() bool {
	return feature.DefaultFeatureGate.Enabled(features.PodPriority)
}

// GetPodFullName returns a name that uniquely identifies a pod.
func GetPodFullName(pod *v1.Pod) string {
	// Use underscore as the delimiter because it is not allowed in pod name
	// (DNS subdomain format).
	return pod.Name + "_" + pod.Namespace
}

// GetPodPriority return priority of the given pod.
func GetPodPriority(pod *v1.Pod) int32 {
	if pod.Spec.Priority != nil {
		return *pod.Spec.Priority
	}
	// When priority of a running pod is nil, it means it was created at a time
	// that there was no global default priority class and the priority class
	// name of the pod was empty. So, we resolve to the static default priority.
	return scheduling.DefaultPriorityWhenNoDefaultClassExists
}

// SortableList is a list that implements sort.Interface.
type SortableList struct {
	Items    []interface{}
	CompFunc LessFunc
}

// LessFunc is a function that receives two items and returns true if the first
// item should be placed before the second one when the list is sorted.
type LessFunc func(item1, item2 interface{}) bool

var _ = sort.Interface(&SortableList{})

func (l *SortableList) Len() int { return len(l.Items) }

func (l *SortableList) Less(i, j int) bool {
	return l.CompFunc(l.Items[i], l.Items[j])
}

func (l *SortableList) Swap(i, j int) {
	l.Items[i], l.Items[j] = l.Items[j], l.Items[i]
}

// Sort sorts the items in the list using the given CompFunc. Item1 is placed
// before Item2 when CompFunc(Item1, Item2) returns true.
func (l *SortableList) Sort() {
	sort.Sort(l)
}

// HigherPriorityPod return true when priority of the first pod is higher than
// the second one. It takes arguments of the type "interface{}" to be used with
// SortableList, but expects those arguments to be *v1.Pod.
func HigherPriorityPod(pod1, pod2 interface{}) bool {
	return GetPodPriority(pod1.(*v1.Pod)) > GetPodPriority(pod2.(*v1.Pod))
}

// FilterPodsWithPDBViolation groups the given "pods" into two groups of "violatingPods"
// and "nonViolatingPods" based on whether their PDBs will be violated if they are
// preempted.
// This function is stable and does not change the order of received pods. So, if it
// receives a sorted list, grouping will preserve the order of the input list.
func FilterPodsWithPDBViolation(pods []interface{}, pdbs []*policy.PodDisruptionBudget) (violatingPods, nonViolatingPods []*v1.Pod) {
	for _, obj := range pods {
		pod := obj.(*v1.Pod)
		pdbForPodIsViolated := false
		// A pod with no labels will not match any PDB. So, no need to check.
		if len(pod.Labels) != 0 {
			for _, pdb := range pdbs {
				if pdb.Namespace != pod.Namespace {
					continue
				}
				selector, err := metav1.LabelSelectorAsSelector(pdb.Spec.Selector)
				if err != nil {
					continue
				}
				// A PDB with a nil or empty selector matches nothing.
				if selector.Empty() || !selector.Matches(labels.Set(pod.Labels)) {
					continue
				}
				// We have found a matching PDB.
				if pdb.Status.PodDisruptionsAllowed <= 0 {
					pdbForPodIsViolated = true
					break
				}
			}
		}
		if pdbForPodIsViolated {
			violatingPods = append(violatingPods, pod)
		} else {
			nonViolatingPods = append(nonViolatingPods, pod)
		}
	}
	return violatingPods, nonViolatingPods
}
