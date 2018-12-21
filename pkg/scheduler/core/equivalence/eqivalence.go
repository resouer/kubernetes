/*
Copyright 2018 The Kubernetes Authors.

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

// Package equivalence defines Pod equivalence classes
package equivalence

import (
	"fmt"
	"sync"

	"k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/scheduler/util"
	"k8s.io/apimachinery/pkg/types"
)

var equivalenceClass *EquivalenceClass

// EquivalenceClass
type EquivalenceClass struct {
	ClassMap map[types.UID]*Class
}

// NewEquivalenceClass create an empty equiv class
func NewEquivalenceClass() *EquivalenceClass {
	if equivalenceClass != nil {
		return equivalenceClass
	}
	classMap := make(map[types.UID]*Class)
	equivalenceClass = &EquivalenceClass{
		ClassMap: classMap,
	}
	return equivalenceClass
}

// CleanEquivalenceClass is used by test
func CleanEquivalenceClass() {
	equivalenceClass = nil
}

// Class is a thread safe map saves and reuses pods for schedule
type Class struct {
	// Equivalence hash
	Hash types.UID
	// Pods wait for schedule in this Class
	PodSet *sync.Map
}

// NewClass return equivalence class if exited or create new equivalence class
func NewClass(pod *v1.Pod) *Class {
	equivHash := GetEquivHash(pod)
	if equivalenceClass == nil {
		equivalenceClass = NewEquivalenceClass()
	}

	if _, ok := equivalenceClass.ClassMap[equivHash]; ok {
		return equivalenceClass.ClassMap[equivHash]
	}

	return &Class{
		Hash:   equivHash,
		PodSet: new(sync.Map),
	}
}

// GetEquivHash generate EquivHash for Pod
func GetEquivHash(pod *v1.Pod) types.UID {
	ownerReferences := pod.GetOwnerReferences()
	if ownerReferences != nil {
		return ownerReferences[0].UID
	}

	return ""
}

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

// EquivalenceClassKeyFunc is a convenient default KeyFunc which knows how to make
// keys for API objects which implement meta.Interface.
// The key uses the equivalenceClass.Hash.
func EquivalenceClassKeyFunc(obj interface{}) (string, error) {
	if obj == nil {
		return "", fmt.Errorf("obj is nil")
	}
	equivalenceClass := obj.(*Class)
	hash := (*equivalenceClass).Hash
	return string(hash), nil
}

// HigherPriorityPod return true when priority of the first pod is higher than
// the second one. It takes arguments of the type "interface{}" to be used with
// SortableList, but expects those arguments to be *Class.
func HigherPriorityEquivalenceClass(class1, class2 interface{}) bool {
	var pod1 *v1.Pod
	var pod2 *v1.Pod

	class1.(*Class).PodSet.Range(func(k, v interface{}) bool {
		if v != nil {
			pod1 = v.(*v1.Pod)
			return false
		}
		return true
	})
	class2.(*Class).PodSet.Range(func(k, v interface{}) bool {
		if v != nil {
			pod2 = v.(*v1.Pod)
			return false
		}
		return true
	})
	return util.GetPodPriority(pod1) > util.GetPodPriority(pod2)
}