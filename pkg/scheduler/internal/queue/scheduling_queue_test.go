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

package queue

import (
	"fmt"
	"reflect"
	"sync"
	"testing"

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	podutil "k8s.io/kubernetes/pkg/api/v1/pod"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	utilfeaturetesting "k8s.io/apiserver/pkg/util/feature/testing"
	"k8s.io/kubernetes/pkg/scheduler/util"
	"k8s.io/kubernetes/pkg/features"
	"k8s.io/kubernetes/pkg/scheduler/core/equivalence"
	"k8s.io/klog"
)

var negPriority, lowPriority, midPriority, highPriority, veryHighPriority = int32(-100), int32(0), int32(100), int32(1000), int32(10000)
var mediumPriority = (lowPriority + highPriority) / 2
var highPriorityPod, highPriNominatedPod, medPriorityPod, unschedulablePod = v1.Pod{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "hpp",
		Namespace: "ns1",
		UID:       "hppns1",
	},
	Spec: v1.PodSpec{
		Priority: &highPriority,
	},
},
	v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hpp",
			Namespace: "ns1",
			UID:       "hppns1",
		},
		Spec: v1.PodSpec{
			Priority: &highPriority,
		},
		Status: v1.PodStatus{
			NominatedNodeName: "node1",
		},
	},
	v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mpp",
			Namespace: "ns2",
			UID:       "mppns2",
			Annotations: map[string]string{
				"annot2": "val2",
			},
		},
		Spec: v1.PodSpec{
			Priority: &mediumPriority,
		},
		Status: v1.PodStatus{
			NominatedNodeName: "node1",
		},
	},
	v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "up",
			Namespace: "ns1",
			UID:       "upns1",
			Annotations: map[string]string{
				"annot2": "val2",
			},
		},
		Spec: v1.PodSpec{
			Priority: &lowPriority,
		},
		Status: v1.PodStatus{
			Conditions: []v1.PodCondition{
				{
					Type:   v1.PodScheduled,
					Status: v1.ConditionFalse,
					Reason: v1.PodReasonUnschedulable,
				},
			},
			NominatedNodeName: "node1",
		},
	}

func TestPriorityQueue_Add(t *testing.T) {
	q := NewPriorityQueue(nil)
	if err := q.Add(&medPriorityPod); err != nil {
		t.Errorf("add failed: %v", err)
	}
	if err := q.Add(&unschedulablePod); err != nil {
		t.Errorf("add failed: %v", err)
	}
	if err := q.Add(&highPriorityPod); err != nil {
		t.Errorf("add failed: %v", err)
	}
	expectedNominatedPods := map[string][]*v1.Pod{
		"node1": {&medPriorityPod, &unschedulablePod},
	}
	if !reflect.DeepEqual(q.nominatedPods, expectedNominatedPods) {
		t.Errorf("Unexpected nominated map after adding pods. Expected: %v, got: %v", expectedNominatedPods, q.nominatedPods)
	}
	if p, err := q.Pop(); err != nil || p != &highPriorityPod {
		t.Errorf("Expected: %v after Pop, but got: %v", highPriorityPod.Name, p.Name)
	}
	if p, err := q.Pop(); err != nil || p != &medPriorityPod {
		t.Errorf("Expected: %v after Pop, but got: %v", medPriorityPod.Name, p.Name)
	}
	if p, err := q.Pop(); err != nil || p != &unschedulablePod {
		t.Errorf("Expected: %v after Pop, but got: %v", unschedulablePod.Name, p.Name)
	}
	if len(q.nominatedPods["node1"]) != 2 {
		t.Errorf("Expected medPriorityPod and unschedulablePod to be still present in nomindatePods: %v", q.nominatedPods["node1"])
	}
}

func TestPriorityQueue_AddIfNotPresent(t *testing.T) {
	q := NewPriorityQueue(nil)
	q.unschedulableQ.addOrUpdate(&highPriNominatedPod)
	q.AddIfNotPresent(&highPriNominatedPod) // Must not add anything.
	q.AddIfNotPresent(&medPriorityPod)
	q.AddIfNotPresent(&unschedulablePod)
	expectedNominatedPods := map[string][]*v1.Pod{
		"node1": {&medPriorityPod, &unschedulablePod},
	}
	if !reflect.DeepEqual(q.nominatedPods, expectedNominatedPods) {
		t.Errorf("Unexpected nominated map after adding pods. Expected: %v, got: %v", expectedNominatedPods, q.nominatedPods)
	}
	if p, err := q.Pop(); err != nil || p != &medPriorityPod {
		t.Errorf("Expected: %v after Pop, but got: %v", medPriorityPod.Name, p.Name)
	}
	if p, err := q.Pop(); err != nil || p != &unschedulablePod {
		t.Errorf("Expected: %v after Pop, but got: %v", unschedulablePod.Name, p.Name)
	}
	if len(q.nominatedPods["node1"]) != 2 {
		t.Errorf("Expected medPriorityPod and unschedulablePod to be still present in nomindatePods: %v", q.nominatedPods["node1"])
	}
	if q.unschedulableQ.get(&highPriNominatedPod) != &highPriNominatedPod {
		t.Errorf("Pod %v was not found in the unschedulableQ.", highPriNominatedPod.Name)
	}
}

func TestPriorityQueue_AddUnschedulableIfNotPresent(t *testing.T) {
	q := NewPriorityQueue(nil)
	q.Add(&highPriNominatedPod)
	q.AddUnschedulableIfNotPresent(&highPriNominatedPod) // Must not add anything.
	q.AddUnschedulableIfNotPresent(&medPriorityPod)      // This should go to activeQ.
	q.AddUnschedulableIfNotPresent(&unschedulablePod)
	expectedNominatedPods := map[string][]*v1.Pod{
		"node1": {&highPriNominatedPod, &medPriorityPod, &unschedulablePod},
	}
	if !reflect.DeepEqual(q.nominatedPods, expectedNominatedPods) {
		t.Errorf("Unexpected nominated map after adding pods. Expected: %v, got: %v", expectedNominatedPods, q.nominatedPods)
	}
	if p, err := q.Pop(); err != nil || p != &highPriNominatedPod {
		t.Errorf("Expected: %v after Pop, but got: %v", highPriNominatedPod.Name, p.Name)
	}
	if p, err := q.Pop(); err != nil || p != &medPriorityPod {
		t.Errorf("Expected: %v after Pop, but got: %v", medPriorityPod.Name, p.Name)
	}
	if len(q.nominatedPods) != 1 {
		t.Errorf("Expected nomindatePods to have one element: %v", q.nominatedPods)
	}
	if q.unschedulableQ.get(&unschedulablePod) != &unschedulablePod {
		t.Errorf("Pod %v was not found in the unschedulableQ.", unschedulablePod.Name)
	}
}

func TestPriorityQueue_Pop(t *testing.T) {
	q := NewPriorityQueue(nil)
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if p, err := q.Pop(); err != nil || p != &medPriorityPod {
			t.Errorf("Expected: %v after Pop, but got: %v", medPriorityPod.Name, p.Name)
		}
		if len(q.nominatedPods["node1"]) != 1 {
			t.Errorf("Expected medPriorityPod to be present in nomindatePods: %v", q.nominatedPods["node1"])
		}
	}()
	q.Add(&medPriorityPod)
	wg.Wait()
}

func TestPriorityQueue_Update(t *testing.T) {
	q := NewPriorityQueue(nil)
	q.Update(nil, &highPriorityPod)
	if _, exists, _ := q.activeQ.Get(&highPriorityPod); !exists {
		t.Errorf("Expected %v to be added to activeQ.", highPriorityPod.Name)
	}
	if len(q.nominatedPods) != 0 {
		t.Errorf("Expected nomindatePods to be empty: %v", q.nominatedPods)
	}
	// Update highPriorityPod and add a nominatedNodeName to it.
	q.Update(&highPriorityPod, &highPriNominatedPod)
	if q.activeQ.Len() != 1 {
		t.Error("Expected only one item in activeQ.")
	}
	if len(q.nominatedPods) != 1 {
		t.Errorf("Expected one item in nomindatePods map: %v", q.nominatedPods)
	}
	// Updating an unschedulable pod which is not in any of the two queues, should
	// add the pod to activeQ.
	q.Update(&unschedulablePod, &unschedulablePod)
	if _, exists, _ := q.activeQ.Get(&unschedulablePod); !exists {
		t.Errorf("Expected %v to be added to activeQ.", unschedulablePod.Name)
	}
	// Updating a pod that is already in activeQ, should not change it.
	q.Update(&unschedulablePod, &unschedulablePod)
	if len(q.unschedulableQ.pods) != 0 {
		t.Error("Expected unschedulableQ to be empty.")
	}
	if _, exists, _ := q.activeQ.Get(&unschedulablePod); !exists {
		t.Errorf("Expected: %v to be added to activeQ.", unschedulablePod.Name)
	}
	if p, err := q.Pop(); err != nil || p != &highPriNominatedPod {
		t.Errorf("Expected: %v after Pop, but got: %v", highPriorityPod.Name, p.Name)
	}
}

func TestPriorityQueue_Delete(t *testing.T) {
	q := NewPriorityQueue(nil)
	q.Update(&highPriorityPod, &highPriNominatedPod)
	q.Add(&unschedulablePod)
	if err := q.Delete(&highPriNominatedPod); err != nil {
		t.Errorf("delete failed: %v", err)
	}
	if _, exists, _ := q.activeQ.Get(&unschedulablePod); !exists {
		t.Errorf("Expected %v to be in activeQ.", unschedulablePod.Name)
	}
	if _, exists, _ := q.activeQ.Get(&highPriNominatedPod); exists {
		t.Errorf("Didn't expect %v to be in activeQ.", highPriorityPod.Name)
	}
	if len(q.nominatedPods) != 1 {
		t.Errorf("Expected nomindatePods to have only 'unschedulablePod': %v", q.nominatedPods)
	}
	if err := q.Delete(&unschedulablePod); err != nil {
		t.Errorf("delete failed: %v", err)
	}
	if len(q.nominatedPods) != 0 {
		t.Errorf("Expected nomindatePods to be empty: %v", q.nominatedPods)
	}
}

func TestPriorityQueue_MoveAllToActiveQueue(t *testing.T) {
	q := NewPriorityQueue(nil)
	q.Add(&medPriorityPod)
	q.unschedulableQ.addOrUpdate(&unschedulablePod)
	q.unschedulableQ.addOrUpdate(&highPriorityPod)
	q.MoveAllToActiveQueue()
	if q.activeQ.Len() != 3 {
		t.Error("Expected all items to be in activeQ.")
	}
}

// TestPriorityQueue_AssignedPodAdded tests AssignedPodAdded. It checks that
// when a pod with pod affinity is in unschedulableQ and another pod with a
// matching label is added, the unschedulable pod is moved to activeQ.
func TestPriorityQueue_AssignedPodAdded(t *testing.T) {
	affinityPod := unschedulablePod.DeepCopy()
	affinityPod.Name = "afp"
	affinityPod.Spec = v1.PodSpec{
		Affinity: &v1.Affinity{
			PodAffinity: &v1.PodAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: []v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "service",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"securityscan", "value2"},
								},
							},
						},
						TopologyKey: "region",
					},
				},
			},
		},
		Priority: &mediumPriority,
	}
	labelPod := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "lbp",
			Namespace: affinityPod.Namespace,
			Labels:    map[string]string{"service": "securityscan"},
		},
		Spec: v1.PodSpec{NodeName: "machine1"},
	}

	q := NewPriorityQueue(nil)
	q.Add(&medPriorityPod)
	// Add a couple of pods to the unschedulableQ.
	q.unschedulableQ.addOrUpdate(&unschedulablePod)
	q.unschedulableQ.addOrUpdate(affinityPod)
	// Simulate addition of an assigned pod. The pod has matching labels for
	// affinityPod. So, affinityPod should go to activeQ.
	q.AssignedPodAdded(&labelPod)
	if q.unschedulableQ.get(affinityPod) != nil {
		t.Error("affinityPod is still in the unschedulableQ.")
	}
	if _, exists, _ := q.activeQ.Get(affinityPod); !exists {
		t.Error("affinityPod is not moved to activeQ.")
	}
	// Check that the other pod is still in the unschedulableQ.
	if q.unschedulableQ.get(&unschedulablePod) == nil {
		t.Error("unschedulablePod is not in the unschedulableQ.")
	}
}

func TestPriorityQueue_WaitingPodsForNode(t *testing.T) {
	q := NewPriorityQueue(nil)
	q.Add(&medPriorityPod)
	q.Add(&unschedulablePod)
	q.Add(&highPriorityPod)
	if p, err := q.Pop(); err != nil || p != &highPriorityPod {
		t.Errorf("Expected: %v after Pop, but got: %v", highPriorityPod.Name, p.Name)
	}
	expectedList := []*v1.Pod{&medPriorityPod, &unschedulablePod}
	if !reflect.DeepEqual(expectedList, q.WaitingPodsForNode("node1")) {
		t.Error("Unexpected list of nominated Pods for node.")
	}
	if q.WaitingPodsForNode("node2") != nil {
		t.Error("Expected list of nominated Pods for node2 to be empty.")
	}
}

func TestUnschedulablePodsMap(t *testing.T) {
	var pods = []*v1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "p0",
				Namespace: "ns1",
				Annotations: map[string]string{
					"annot1": "val1",
				},
			},
			Status: v1.PodStatus{
				NominatedNodeName: "node1",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "p1",
				Namespace: "ns1",
				Annotations: map[string]string{
					"annot": "val",
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "p2",
				Namespace: "ns2",
				Annotations: map[string]string{
					"annot2": "val2", "annot3": "val3",
				},
			},
			Status: v1.PodStatus{
				NominatedNodeName: "node3",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "p3",
				Namespace: "ns4",
			},
			Status: v1.PodStatus{
				NominatedNodeName: "node1",
			},
		},
	}
	var updatedPods = make([]*v1.Pod, len(pods))
	updatedPods[0] = pods[0].DeepCopy()
	updatedPods[1] = pods[1].DeepCopy()
	updatedPods[3] = pods[3].DeepCopy()

	tests := []struct {
		name                   string
		podsToAdd              []*v1.Pod
		expectedMapAfterAdd    map[string]*v1.Pod
		podsToUpdate           []*v1.Pod
		expectedMapAfterUpdate map[string]*v1.Pod
		podsToDelete           []*v1.Pod
		expectedMapAfterDelete map[string]*v1.Pod
	}{
		{
			name:      "create, update, delete subset of pods",
			podsToAdd: []*v1.Pod{pods[0], pods[1], pods[2], pods[3]},
			expectedMapAfterAdd: map[string]*v1.Pod{
				util.GetPodFullName(pods[0]): pods[0],
				util.GetPodFullName(pods[1]): pods[1],
				util.GetPodFullName(pods[2]): pods[2],
				util.GetPodFullName(pods[3]): pods[3],
			},
			podsToUpdate: []*v1.Pod{updatedPods[0]},
			expectedMapAfterUpdate: map[string]*v1.Pod{
				util.GetPodFullName(pods[0]): updatedPods[0],
				util.GetPodFullName(pods[1]): pods[1],
				util.GetPodFullName(pods[2]): pods[2],
				util.GetPodFullName(pods[3]): pods[3],
			},
			podsToDelete: []*v1.Pod{pods[0], pods[1]},
			expectedMapAfterDelete: map[string]*v1.Pod{
				util.GetPodFullName(pods[2]): pods[2],
				util.GetPodFullName(pods[3]): pods[3],
			},
		},
		{
			name:      "create, update, delete all",
			podsToAdd: []*v1.Pod{pods[0], pods[3]},
			expectedMapAfterAdd: map[string]*v1.Pod{
				util.GetPodFullName(pods[0]): pods[0],
				util.GetPodFullName(pods[3]): pods[3],
			},
			podsToUpdate: []*v1.Pod{updatedPods[3]},
			expectedMapAfterUpdate: map[string]*v1.Pod{
				util.GetPodFullName(pods[0]): pods[0],
				util.GetPodFullName(pods[3]): updatedPods[3],
			},
			podsToDelete:           []*v1.Pod{pods[0], pods[3]},
			expectedMapAfterDelete: map[string]*v1.Pod{},
		},
		{
			name:      "delete non-existing and existing pods",
			podsToAdd: []*v1.Pod{pods[1], pods[2]},
			expectedMapAfterAdd: map[string]*v1.Pod{
				util.GetPodFullName(pods[1]): pods[1],
				util.GetPodFullName(pods[2]): pods[2],
			},
			podsToUpdate: []*v1.Pod{updatedPods[1]},
			expectedMapAfterUpdate: map[string]*v1.Pod{
				util.GetPodFullName(pods[1]): updatedPods[1],
				util.GetPodFullName(pods[2]): pods[2],
			},
			podsToDelete: []*v1.Pod{pods[2], pods[3]},
			expectedMapAfterDelete: map[string]*v1.Pod{
				util.GetPodFullName(pods[1]): updatedPods[1],
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upm := newUnschedulablePodsMap()
			for _, p := range test.podsToAdd {
				upm.addOrUpdate(p)
			}
			if !reflect.DeepEqual(upm.pods, test.expectedMapAfterAdd) {
				t.Errorf("Unexpected map after adding pods. Expected: %v, got: %v",
					test.expectedMapAfterAdd, upm.pods)
			}

			if len(test.podsToUpdate) > 0 {
				for _, p := range test.podsToUpdate {
					upm.addOrUpdate(p)
				}
				if !reflect.DeepEqual(upm.pods, test.expectedMapAfterUpdate) {
					t.Errorf("Unexpected map after updating pods. Expected: %v, got: %v",
						test.expectedMapAfterUpdate, upm.pods)
				}
			}
			for _, p := range test.podsToDelete {
				upm.delete(p)
			}
			if !reflect.DeepEqual(upm.pods, test.expectedMapAfterDelete) {
				t.Errorf("Unexpected map after deleting pods. Expected: %v, got: %v",
					test.expectedMapAfterDelete, upm.pods)
			}
			upm.clear()
			if len(upm.pods) != 0 {
				t.Errorf("Expected the map to be empty, but has %v elements.", len(upm.pods))
			}
		})
	}
}

func TestSchedulingQueue_Close(t *testing.T) {
	tests := []struct {
		name        string
		q           SchedulingQueue
		expectedErr error
	}{
		{
			name:        "FIFO close",
			q:           NewFIFO(),
			expectedErr: fmt.Errorf(queueClosed),
		},
		{
			name:        "PriorityQueue close",
			q:           NewPriorityQueue(nil),
			expectedErr: fmt.Errorf(queueClosed),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wg := sync.WaitGroup{}
			wg.Add(1)
			go func() {
				defer wg.Done()
				pod, err := test.q.Pop()
				if err.Error() != test.expectedErr.Error() {
					t.Errorf("Expected err %q from Pop() if queue is closed, but got %q", test.expectedErr.Error(), err.Error())
				}
				if pod != nil {
					t.Errorf("Expected pod nil from Pop() if queue is closed, but got: %v", pod)
				}
			}()
			test.q.Close()
			wg.Wait()
		})
	}
}

// TestRecentlyTriedPodsGoBack tests that pods which are recently tried and are
// unschedulable go behind other pods with the same priority. This behavior
// ensures that an unschedulable pod does not block head of the queue when there
// are frequent events that move pods to the active queue.
func TestRecentlyTriedPodsGoBack(t *testing.T) {
	q := NewPriorityQueue(nil)
	// Add a few pods to priority queue.
	for i := 0; i < 5; i++ {
		p := v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("test-pod-%v", i),
				Namespace: "ns1",
				UID:       types.UID(fmt.Sprintf("tp00%v", i)),
			},
			Spec: v1.PodSpec{
				Priority: &highPriority,
			},
			Status: v1.PodStatus{
				NominatedNodeName: "node1",
			},
		}
		q.Add(&p)
	}
	// Simulate a pod being popped by the scheduler, determined unschedulable, and
	// then moved back to the active queue.
	p1, err := q.Pop()
	if err != nil {
		t.Errorf("Error while popping the head of the queue: %v", err)
	}
	// Update pod condition to unschedulable.
	podutil.UpdatePodCondition(&p1.Status, &v1.PodCondition{
		Type:    v1.PodScheduled,
		Status:  v1.ConditionFalse,
		Reason:  v1.PodReasonUnschedulable,
		Message: "fake scheduling failure",
	})
	// Put in the unschedulable queue.
	q.AddUnschedulableIfNotPresent(p1)
	// Move all unschedulable pods to the active queue.
	q.MoveAllToActiveQueue()
	// Simulation is over. Now let's pop all pods. The pod popped first should be
	// the last one we pop here.
	for i := 0; i < 5; i++ {
		p, err := q.Pop()
		if err != nil {
			t.Errorf("Error while popping pods from the queue: %v", err)
		}
		if (i == 4) != (p1 == p) {
			t.Errorf("A pod tried before is not the last pod popped: i: %v, pod name: %v", i, p.Name)
		}
	}
}

// TestHighPriorityBackoff tests that a high priority pod does not block
// other pods if it is unschedulable
func TestHighProirotyBackoff(t *testing.T) {
	q := NewPriorityQueue(nil)

	midPod := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-midpod",
			Namespace: "ns1",
			UID:       types.UID("tp-mid"),
		},
		Spec: v1.PodSpec{
			Priority: &midPriority,
		},
		Status: v1.PodStatus{
			NominatedNodeName: "node1",
		},
	}
	highPod := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-highpod",
			Namespace: "ns1",
			UID:       types.UID("tp-high"),
		},
		Spec: v1.PodSpec{
			Priority: &highPriority,
		},
		Status: v1.PodStatus{
			NominatedNodeName: "node1",
		},
	}
	q.Add(&midPod)
	q.Add(&highPod)
	// Simulate a pod being popped by the scheduler, determined unschedulable, and
	// then moved back to the active queue.
	p, err := q.Pop()
	if err != nil {
		t.Errorf("Error while popping the head of the queue: %v", err)
	}
	if p != &highPod {
		t.Errorf("Expected to get high prority pod, got: %v", p)
	}
	// Update pod condition to unschedulable.
	podutil.UpdatePodCondition(&p.Status, &v1.PodCondition{
		Type:    v1.PodScheduled,
		Status:  v1.ConditionFalse,
		Reason:  v1.PodReasonUnschedulable,
		Message: "fake scheduling failure",
	})
	// Put in the unschedulable queue.
	q.AddUnschedulableIfNotPresent(p)
	// Move all unschedulable pods to the active queue.
	q.MoveAllToActiveQueue()

	p, err = q.Pop()
	if err != nil {
		t.Errorf("Error while popping the head of the queue: %v", err)
	}
	if p != &midPod {
		t.Errorf("Expected to get mid prority pod, got: %v", p)
	}
}

var highPriorityPod1, highPriorityPod2, highPriNominatedPod1, highPriNominatedPod2, medPriorityPod1,
medPriorityPod2, unschedulablePod1, unschedulablePod2 = v1.Pod{
	ObjectMeta: metav1.ObjectMeta{
		Name:            "hpp1",
		Namespace:       "ns1",
		UID:             "hpp1ns1",
		OwnerReferences: getOwnerReferences("high"),
	},
	Spec: v1.PodSpec{
		Priority: &highPriority,
	},
},
	v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "hpp2",
			Namespace:       "ns1",
			UID:             "hpp2ns1",
			OwnerReferences: getOwnerReferences("high"),
		},
		Spec: v1.PodSpec{
			Priority: &highPriority,
		},
	},
	v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "hpp1",
			Namespace:       "ns1",
			UID:             "hpp1ns1",
			OwnerReferences: getOwnerReferences("high-Nominated"),
		},
		Spec: v1.PodSpec{
			Priority: &highPriority,
		},
		Status: v1.PodStatus{
			NominatedNodeName: "node1",
		},
	},
	v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "hpp2",
			Namespace:       "ns1",
			UID:             "hpp2ns1",
			OwnerReferences: getOwnerReferences("high-Nominated"),
		},
		Spec: v1.PodSpec{
			Priority: &highPriority,
		},
		Status: v1.PodStatus{
			NominatedNodeName: "node1",
		},
	},
	v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mpp1",
			Namespace: "ns2",
			UID:       "mpp1ns2",
			Annotations: map[string]string{
				"annot2": "val2",
			},
			OwnerReferences: getOwnerReferences("medium"),
		},
		Spec: v1.PodSpec{
			Priority: &mediumPriority,
		},
		Status: v1.PodStatus{
			NominatedNodeName: "node1",
		},
	},
	v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mpp2",
			Namespace: "ns2",
			UID:       "mpp2ns2",
			Annotations: map[string]string{
				"annot2": "val2",
			},
			OwnerReferences: getOwnerReferences("medium"),
		},
		Spec: v1.PodSpec{
			Priority: &mediumPriority,
		},
		Status: v1.PodStatus{
			NominatedNodeName: "node1",
		},
	},
	v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "up1",
			Namespace: "ns1",
			UID:       "up1ns1",
			Annotations: map[string]string{
				"annot2": "val2",
			},
			OwnerReferences: getOwnerReferences("low"),
		},
		Spec: v1.PodSpec{
			Priority: &lowPriority,
		},
		Status: v1.PodStatus{
			Conditions: []v1.PodCondition{
				{
					Type:   v1.PodScheduled,
					Status: v1.ConditionFalse,
					Reason: v1.PodReasonUnschedulable,
				},
			},
			NominatedNodeName: "node1",
		},
	},
	v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "up2",
			Namespace: "ns1",
			UID:       "up2ns1",
			Annotations: map[string]string{
				"annot2": "val2",
			},
			OwnerReferences: getOwnerReferences("low"),
		},
		Spec: v1.PodSpec{
			Priority: &lowPriority,
		},
		Status: v1.PodStatus{
			Conditions: []v1.PodCondition{
				{
					Type:   v1.PodScheduled,
					Status: v1.ConditionFalse,
					Reason: v1.PodReasonUnschedulable,
				},
			},
			NominatedNodeName: "node1",
		},
	}

func getOwnerReferences(name string) []metav1.OwnerReference {
	metaOwnerReferences := make([]metav1.OwnerReference, 0)
	metaOwnerReferences = append(metaOwnerReferences, metav1.OwnerReference{
		Kind:       "Job",
		Name:       name,
		UID:        "0eadea6c-e897-11e8-8123-6c92bf3affc9",
		APIVersion: "batch/v1",
	})
	return metaOwnerReferences
}

func clean(p *PriorityQueue) {
	equivalence.CleanEquivalenceCache()
}

func TestECachePriorityQueue_Add(t *testing.T) {
	// Enable PodPriority feature gate.
	defer utilfeaturetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate,
		features.EnableEquivalenceClass, true)()
	q := NewPriorityQueue(nil)
	defer clean(q)

	q.Add(&medPriorityPod1)
	q.Add(&unschedulablePod1)
	q.Add(&highPriorityPod1)
	q.Add(&medPriorityPod2)
	q.Add(&unschedulablePod2)
	q.Add(&highPriorityPod2)
	expectedNominatedPods := map[string][]*v1.Pod{
		"node1": {&medPriorityPod1, &unschedulablePod1, &medPriorityPod2, &unschedulablePod2},
	}

	if !reflect.DeepEqual(q.nominatedPods, expectedNominatedPods) {
		t.Errorf("Unexpected nominated map after adding pods. Expected: %v, got: %v", expectedNominatedPods, q.nominatedPods)
	}

	p, _ := q.Pop()
	podSet := q.equivalenceCache.Cache[equivalence.GetEquivHash(p)].PodSet
	if p, ok := podSet.Load(highPriorityPod1.UID); !ok || p.(*v1.Pod) != &highPriorityPod1 {
		t.Errorf("Expected: %v after Pop, but got: %v", highPriorityPod.Name, p.(*v1.Pod).Name)
	}
	if p, ok := podSet.Load(highPriorityPod2.UID); !ok || p.(*v1.Pod) != &highPriorityPod2 {
		t.Errorf("Expected: %v after Pop, but got: %v", highPriorityPod.Name, p.(*v1.Pod).Name)
	}

	p, _ = q.Pop()
	podSet = q.equivalenceCache.Cache[equivalence.GetEquivHash(p)].PodSet
	if p, ok := podSet.Load(medPriorityPod1.UID); !ok || p.(*v1.Pod) != &medPriorityPod1 {
		t.Errorf("Expected: %v after Pop, but got: %v", medPriorityPod1.Name, p.(*v1.Pod).Name)
	}
	if p, ok := podSet.Load(medPriorityPod2.UID); !ok || p.(*v1.Pod) != &medPriorityPod2 {
		t.Errorf("Expected: %v after Pop, but got: %v", medPriorityPod2.Name, p.(*v1.Pod).Name)
	}

	p, _ = q.Pop()
	podSet = q.equivalenceCache.Cache[equivalence.GetEquivHash(p)].PodSet
	if p, ok := podSet.Load(unschedulablePod1.UID); !ok || p.(*v1.Pod) != &unschedulablePod1 {
		t.Errorf("Expected: %v after Pop, but got: %v", unschedulablePod1.Name, p.(*v1.Pod).Name)
	}
	if p, ok := podSet.Load(unschedulablePod2.UID); !ok || p.(*v1.Pod) != &unschedulablePod2 {
		t.Errorf("Expected: %v after Pop, but got: %v", unschedulablePod2.Name, p.(*v1.Pod).Name)
	}

	if len(q.nominatedPods) != 0 {
		t.Errorf("Expected nomindatePods to be empty: %v", q.nominatedPods)
	}
}

func TestECachePriorityQueue_AddIfNotPresent(t *testing.T) {
	// Enable PodPriority feature gate.
	defer utilfeaturetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.EnableEquivalenceClass, true)()
	q := NewPriorityQueue(nil)
	defer clean(q)

	q.unschedulableQ.addOrUpdate(&highPriNominatedPod1)
	q.unschedulableQ.addOrUpdate(&highPriNominatedPod2)

	q.AddIfNotPresent(&highPriNominatedPod1) // Must not add anything.
	q.AddIfNotPresent(&medPriorityPod1)
	q.AddIfNotPresent(&unschedulablePod1)
	q.AddIfNotPresent(&highPriNominatedPod2) // Must not add anything.
	q.AddIfNotPresent(&medPriorityPod2)
	q.AddIfNotPresent(&unschedulablePod2)
	expectedNominatedPods := map[string][]*v1.Pod{
		"node1": {&medPriorityPod1, &unschedulablePod1, &medPriorityPod2, &unschedulablePod2},
	}

	if !reflect.DeepEqual(q.nominatedPods, expectedNominatedPods) {
		t.Errorf("Unexpected nominated map after adding pods. Expected: %v, got: %v", expectedNominatedPods, q.nominatedPods)
	}

	p, _ := q.Pop()
	podSet := q.equivalenceCache.Cache[equivalence.GetEquivHash(p)].PodSet
	if p, ok := podSet.Load(medPriorityPod1.UID); !ok || p.(*v1.Pod) != &medPriorityPod1 {
		t.Errorf("Expected: %v after Pop, but got: %v", medPriorityPod1.Name, p.(*v1.Pod).Name)
	}
	if p, ok := podSet.Load(medPriorityPod2.UID); !ok || p.(*v1.Pod) != &medPriorityPod2 {
		t.Errorf("Expected: %v after Pop, but got: %v", medPriorityPod2.Name, p.(*v1.Pod).Name)
	}

	p, _ = q.Pop()
	podSet = q.equivalenceCache.Cache[equivalence.GetEquivHash(p)].PodSet
	if p, ok := podSet.Load(unschedulablePod1.UID); !ok || p.(*v1.Pod) != &unschedulablePod1 {
		t.Errorf("Expected: %v after Pop, but got: %v", unschedulablePod1.Name, p.(*v1.Pod).Name)
	}
	if p, ok := podSet.Load(unschedulablePod2.UID); !ok || p.(*v1.Pod) != &unschedulablePod2 {
		t.Errorf("Expected: %v after Pop, but got: %v", unschedulablePod2.Name, p.(*v1.Pod).Name)
	}

	if len(q.nominatedPods) != 0 {
		t.Errorf("Expected nomindatePods to be empty: %v", q.nominatedPods)
	}

	p = q.unschedulableQ.get(&highPriNominatedPod1)
	if p != &highPriNominatedPod1 {
		t.Errorf("Expected: %v after Pop, but got: %v", highPriNominatedPod1.Name, p.Name)
	}
	podSet = q.equivalenceCache.Cache[equivalence.GetEquivHash(p)].PodSet
	if p, ok := podSet.Load(highPriNominatedPod1.UID); !ok || p.(*v1.Pod) != &highPriNominatedPod1 {
		t.Errorf("Expected: %v after Pop, but got: %v", highPriNominatedPod1.Name, p.(*v1.Pod).Name)
	}
	if p, ok := podSet.Load(highPriNominatedPod2.UID); !ok || p.(*v1.Pod) != &highPriNominatedPod2 {
		t.Errorf("Expected: %v after Pop, but got: %v", highPriNominatedPod2.Name, p.(*v1.Pod).Name)
	}
}

func TestECachePriorityQueue_AddUnschedulableIfNotPresent(t *testing.T) {
	// Enable PodPriority feature gate.
	defer utilfeaturetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.EnableEquivalenceClass, true)()
	//utilfeature.DefaultFeatureGate.Set(fmt.Sprintf("%s=true", features.EnableEquivalenceClass))
	q := NewPriorityQueue(nil)
	defer clean(q)

	q.Add(&highPriNominatedPod1)
	q.Add(&highPriNominatedPod2)
	q.AddUnschedulableIfNotPresent(&highPriNominatedPod1) // Must not add anything.
	q.AddUnschedulableIfNotPresent(&medPriorityPod1)      // This should go to activeQ.
	q.AddUnschedulableIfNotPresent(&unschedulablePod1)
	q.AddUnschedulableIfNotPresent(&highPriNominatedPod2) // Must not add anything.
	q.AddUnschedulableIfNotPresent(&medPriorityPod2)      // This should go to activeQ.
	q.AddUnschedulableIfNotPresent(&unschedulablePod2)

	expectedNominatedPods := map[string][]*v1.Pod{
		"node1": {&highPriNominatedPod1, &highPriNominatedPod2, &medPriorityPod1, &unschedulablePod1, &medPriorityPod2, &unschedulablePod2},
	}
	if !reflect.DeepEqual(q.nominatedPods, expectedNominatedPods) {
		t.Errorf("Unexpected nominated map after adding pods. Expected: %v, got: %v", expectedNominatedPods, q.nominatedPods)
	}
	p, _ := q.Pop()
	podSet := q.equivalenceCache.Cache[equivalence.GetEquivHash(p)].PodSet
	if p, ok := podSet.Load(highPriNominatedPod1.UID); !ok || p.(*v1.Pod) != &highPriNominatedPod1 {
		t.Errorf("Expected: %v after Pop, but got: %v", highPriNominatedPod1.Name, p.(*v1.Pod).Name)
	}
	if p, ok := podSet.Load(highPriNominatedPod2.UID); !ok || p.(*v1.Pod) != &highPriNominatedPod2 {
		t.Errorf("Expected: %v after Pop, but got: %v", highPriNominatedPod2.Name, p.(*v1.Pod).Name)
	}

	p, _ = q.Pop()
	podSet = q.equivalenceCache.Cache[equivalence.GetEquivHash(p)].PodSet
	if p, ok := podSet.Load(medPriorityPod1.UID); !ok || p.(*v1.Pod) != &medPriorityPod1 {
		t.Errorf("Expected: %v after Pop, but got: %v", medPriorityPod1.Name, p.(*v1.Pod).Name)
	}
	if p, ok := podSet.Load(medPriorityPod2.UID); !ok || p.(*v1.Pod) != &medPriorityPod2 {
		t.Errorf("Expected: %v after Pop, but got: %v", medPriorityPod2.Name, p.(*v1.Pod).Name)
	}

	p = q.unschedulableQ.get(&unschedulablePod1)
	podSet = q.equivalenceCache.Cache[equivalence.GetEquivHash(p)].PodSet
	if p, ok := podSet.Load(unschedulablePod1.UID); !ok || p.(*v1.Pod) != &unschedulablePod1 {
		t.Errorf("Expected: %v after Pop, but got: %v", unschedulablePod1.Name, p.(*v1.Pod).Name)
	}
	if p, ok := podSet.Load(unschedulablePod2.UID); !ok || p.(*v1.Pod) != &unschedulablePod2 {
		t.Errorf("Expected: %v after Pop, but got: %v", unschedulablePod2.Name, p.(*v1.Pod).Name)
	}
}

func TestECachePriorityQueue_Pop(t *testing.T) {
	// Enable PodPriority feature gate.
	//utilfeature.DefaultFeatureGate.Set(fmt.Sprintf("%s=true", features.EnableEquivalenceClass))
	defer utilfeaturetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate,
		features.EnableEquivalenceClass, true)()
	q := NewPriorityQueue(nil)
	defer clean(q)

	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		p, _ := q.Pop()
		podSet := q.equivalenceCache.Cache[equivalence.GetEquivHash(p)].PodSet
		if p, ok := podSet.Load(medPriorityPod1.UID); !ok || p.(*v1.Pod) != &medPriorityPod1 {
			t.Errorf("Expected: %v after Pop, but got: %v", medPriorityPod1.Name, p.(*v1.Pod).Name)
		}
		if p, ok := podSet.Load(medPriorityPod2.UID); !ok || p.(*v1.Pod) != &medPriorityPod2 {
			t.Errorf("Expected: %v after Pop, but got: %v", medPriorityPod2.Name, p.(*v1.Pod).Name)
		}
	}()
	q.Add(&medPriorityPod1)
	q.Add(&medPriorityPod2)
	wg.Wait()
}

func TestECachePriorityQueue_Update(t *testing.T) {
	// Enable PodPriority feature gate.
	//utilfeature.DefaultFeatureGate.Set(fmt.Sprintf("%s=true", features.EnableEquivalenceClass))
	defer utilfeaturetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate,
		features.EnableEquivalenceClass, true)()
	q := NewPriorityQueue(nil)
	defer clean(q)

	q.Update(nil, &highPriorityPod1)
	if _, exists, _ := q.activeQ.Get(equivalence.NewClass(&highPriorityPod1)); !exists {
		t.Errorf("Expected %v to be added to activeQ.", highPriorityPod1.Name)
	}
	podSet := q.equivalenceCache.Cache[equivalence.GetEquivHash(&highPriorityPod1)].PodSet
	if p, ok := podSet.Load(highPriorityPod1.UID); !ok || p.(*v1.Pod) != &highPriorityPod1 {
		t.Errorf("Expected: %v after Pop, but got: %v", highPriorityPod1.Name, p.(*v1.Pod).Name)
	}

	if len(q.nominatedPods) != 0 {
		t.Errorf("Expected nomindatePods to be empty: %v", q.nominatedPods)
	}
	// Update highPriorityPod and add a nominatedNodeName to it.
	q.Update(&highPriorityPod1, &highPriNominatedPod1)
	if  q.activeQ.Len() != 1 {
		t.Error("Expected only one item in activeQ.")
	}
	if len(q.nominatedPods) != 1 {
		t.Errorf("Expected one item in nomindatePods map: %v", q.nominatedPods)
	}

	// Updating an unschedulable pod which is not in any of the two queues, should
	// add the pod to activeQ.
	q.Update(&unschedulablePod1, &unschedulablePod1)
	if _, exists, _ := q.activeQ.Get(equivalence.NewClass(&unschedulablePod1)); !exists {
		t.Errorf("Expected %v to be added to activeQ.", unschedulablePod1.Name)
	}

	// Updating a pod that is already in activeQ, should not change it.
	q.Update(&unschedulablePod1, &unschedulablePod1)
	if len(q.unschedulableQ.equivalenceClasses) != 0 {
		t.Error("Expected unschedulableQ to be empty.")
	}
	if _, exists, _ := q.activeQ.Get(equivalence.NewClass(&unschedulablePod1)); !exists {
		t.Errorf("Expected: %v to be added to activeQ.", unschedulablePod1.Name)
	}
	if p, err := q.Pop(); err != nil || p != &highPriNominatedPod1 {
		t.Errorf("Expected: %v after Pop, but got: %v", highPriorityPod1.Name, p.Name)
	}
}

func TestECachePriorityQueue_Delete(t *testing.T) {
	// Enable PodPriority feature gate.
	//utilfeature.DefaultFeatureGate.Set(fmt.Sprintf("%s=true", features.EnableEquivalenceClass))
	defer utilfeaturetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate,
		features.EnableEquivalenceClass, true)()
	q := NewPriorityQueue(nil)
	defer clean(q)

	q.Update(&highPriorityPod1, &highPriNominatedPod1)
	q.Add(&unschedulablePod1)
	q.Delete(&highPriNominatedPod1)
	if _, exists, _ := q.activeQ.Get(equivalence.NewClass(&unschedulablePod1)); !exists {
		t.Errorf("Expected %v to be in activeQ.", unschedulablePod1.Name)
	}
	if _, exists, _ := q.activeQ.Get(equivalence.NewClass(&highPriNominatedPod1)); exists {
		t.Errorf("Didn't expect %v to be in activeQ.", highPriNominatedPod1.Name)
	}
	if len(q.nominatedPods) != 1 {
		t.Errorf("Expected nomindatePods to have only 'unschedulablePod': %v", q.nominatedPods)
	}
	q.Delete(&unschedulablePod1)
	if len(q.nominatedPods) != 0 {
		t.Errorf("Expected nomindatePods to be empty: %v", q.nominatedPods)
	}
}

func TestECachePriorityQueue_MoveAllToActiveQueue(t *testing.T) {
	// Enable PodPriority feature gate.
	//utilfeature.DefaultFeatureGate.Set(fmt.Sprintf("%s=true", features.EnableEquivalenceClass))
	defer utilfeaturetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate,
		features.EnableEquivalenceClass, true)()
	q := NewPriorityQueue(nil)
	defer clean(q)

	q.Add(&medPriorityPod1)
	q.unschedulableQ.addOrUpdate(&unschedulablePod1)
	q.unschedulableQ.addOrUpdate(&highPriorityPod1)

	q.MoveAllToActiveQueue()
	if q.activeQ.Len() != 3 {
		t.Error("Expected all items to be in activeQ.")
	}
}

// TestPriorityQueue_AssignedPodAdded tests AssignedPodAdded. It checks that
// when a pod with pod affinity is in unschedulableQ and another pod with a
// matching label is added, the unschedulable pod is moved to activeQ.
func TestECachePriorityQueue_AssignedPodAdded(t *testing.T) {
	affinityPod := unschedulablePod1.DeepCopy()
	affinityPod.Name = "afp"
	affinityPod.OwnerReferences = getOwnerReferences("affinity")
	affinityPod.Spec = v1.PodSpec{
		Affinity: &v1.Affinity{
			PodAffinity: &v1.PodAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: []v1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "service",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"securityscan", "value2"},
								},
							},
						},
						TopologyKey: "region",
					},
				},
			},
		},
		Priority: &mediumPriority,
	}
	labelPod := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "lbp",
			Namespace: affinityPod.Namespace,
			Labels:    map[string]string{"service": "securityscan"},
		},
		Spec: v1.PodSpec{NodeName: "machine1"},
	}

	// Enable PodPriority feature gate.
	//utilfeature.DefaultFeatureGate.Set(fmt.Sprintf("%s=true", features.EnableEquivalenceClass))
	defer utilfeaturetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate,
		features.EnableEquivalenceClass, true)()
	q := NewPriorityQueue(nil)
	defer clean(q)

	q.Add(&medPriorityPod1)

	// Add a couple of pods to the unschedulableQ.
	q.unschedulableQ.addOrUpdate(&unschedulablePod1)
	q.unschedulableQ.addOrUpdate(affinityPod)

	// Simulate addition of an assigned pod. The pod has matching labels for
	// affinityPod. So, affinityPod should go to activeQ.
	q.AssignedPodAdded(&labelPod)

	if q.unschedulableQ.get(affinityPod) != nil {
		t.Error("affinityPod is still in the unschedulableQ.")
	}
	if _, exists, _ := q.activeQ.Get(equivalence.NewClass(affinityPod)); !exists {
		t.Error("affinityPod is not moved to activeQ.")
	}
	// Check that the other pod is still in the unschedulableQ.
	if q.unschedulableQ.get(&unschedulablePod1) == nil {
		t.Error("unschedulablePod is not in the unschedulableQ.")
	}
}

func TestECachePriorityQueue_WaitingPodsForNode(t *testing.T) {
	// Enable PodPriority feature gate.
	//utilfeature.DefaultFeatureGate.Set(fmt.Sprintf("%s=true", features.EnableEquivalenceClass))
	defer utilfeaturetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate,
		features.EnableEquivalenceClass, true)()
	q := NewPriorityQueue(nil)
	defer clean(q)

	q.Add(&medPriorityPod1)
	q.Add(&unschedulablePod1)
	q.Add(&highPriorityPod1)
	q.Add(&medPriorityPod2)
	q.Add(&unschedulablePod2)
	q.Add(&highPriorityPod2)

	p, _ := q.Pop()
	podSet := q.equivalenceCache.Cache[equivalence.GetEquivHash(p)].PodSet
	if p, ok := podSet.Load(highPriorityPod1.UID); !ok || p.(*v1.Pod) != &highPriorityPod1 {
		t.Errorf("Expected: %v after Pop, but got: %v", highPriorityPod1.Name, p.(*v1.Pod).Name)
	}
	if p, ok := podSet.Load(highPriorityPod2.UID); !ok || p.(*v1.Pod) != &highPriorityPod2 {
		t.Errorf("Expected: %v after Pop, but got: %v", highPriorityPod2.Name, p.(*v1.Pod).Name)
	}

	expectedList := []*v1.Pod{&medPriorityPod1, &unschedulablePod1, &medPriorityPod2, &unschedulablePod2}
	if !reflect.DeepEqual(expectedList, q.WaitingPodsForNode("node1")) {
		t.Error("Unexpected list of nominated Pods for node.")
		t.Errorf("Expected: %v , but got: %v", expectedList, q.WaitingPodsForNode("node1"))
	}
	if q.WaitingPodsForNode("node2") != nil {
		t.Error("Expected list of nominated Pods for node2 to be empty.")
	}
}

func TestECacheUnschedulablePodsMap(t *testing.T) {
	// Enable PodPriority feature gate.
	//utilfeature.DefaultFeatureGate.Set(fmt.Sprintf("%s=true", features.EnableEquivalenceClass))
	defer utilfeaturetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate,
		features.EnableEquivalenceClass, true)()

	var pods = []*v1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "p0",
				Namespace: "ns1",
				UID:       "p0ns1",
				Annotations: map[string]string{
					"annot1": "val1",
				},
				OwnerReferences: getOwnerReferences("p0"),
			},
			Status: v1.PodStatus{
				NominatedNodeName: "node1",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "p1",
				Namespace: "ns1",
				UID:       "p1ns1",
				Annotations: map[string]string{
					"annot": "val",
				},
				OwnerReferences: getOwnerReferences("p1"),
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "p2",
				Namespace: "ns2",
				UID:       "p2ns1",
				Annotations: map[string]string{
					"annot2": "val2", "annot3": "val3",
				},
				OwnerReferences: getOwnerReferences("p2"),
			},
			Status: v1.PodStatus{
				NominatedNodeName: "node3",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "p3",
				Namespace:       "ns4",
				UID:       "p3ns4",
				OwnerReferences: getOwnerReferences("p3"),
			},
			Status: v1.PodStatus{
				NominatedNodeName: "node1",
			},
		},
	}
	var updatedPods = make([]*v1.Pod, len(pods))
	updatedPods[0] = pods[0].DeepCopy()
	updatedPods[1] = pods[1].DeepCopy()
	updatedPods[3] = pods[3].DeepCopy()

	tests := []struct {
		name                   string
		podsToAdd              []*v1.Pod
		expectedMapAfterAdd    map[string]*v1.Pod
		podsToUpdate           []*v1.Pod
		expectedMapAfterUpdate map[string]*v1.Pod
		podsToDelete           []*v1.Pod
		expectedMapAfterDelete map[string]*v1.Pod
	}{
		{
			name:      "create, update, delete subset of pods",
			podsToAdd: []*v1.Pod{pods[0], pods[1], pods[2], pods[3]},
			expectedMapAfterAdd: map[string]*v1.Pod{
				util.GetPodFullName(pods[0]): pods[0],
				util.GetPodFullName(pods[1]): pods[1],
				util.GetPodFullName(pods[2]): pods[2],
				util.GetPodFullName(pods[3]): pods[3],
			},
			podsToUpdate: []*v1.Pod{updatedPods[0]},
			expectedMapAfterUpdate: map[string]*v1.Pod{
				util.GetPodFullName(pods[0]): updatedPods[0],
				util.GetPodFullName(pods[1]): pods[1],
				util.GetPodFullName(pods[2]): pods[2],
				util.GetPodFullName(pods[3]): pods[3],
			},
			podsToDelete: []*v1.Pod{pods[0], pods[1]},
			expectedMapAfterDelete: map[string]*v1.Pod{
				util.GetPodFullName(pods[2]): pods[2],
				util.GetPodFullName(pods[3]): pods[3],
			},
		},
		{
			name:      "create, update, delete all",
			podsToAdd: []*v1.Pod{pods[0], pods[3]},
			expectedMapAfterAdd: map[string]*v1.Pod{
				util.GetPodFullName(pods[0]): pods[0],
				util.GetPodFullName(pods[3]): pods[3],
			},
			podsToUpdate: []*v1.Pod{updatedPods[3]},
			expectedMapAfterUpdate: map[string]*v1.Pod{
				util.GetPodFullName(pods[0]): pods[0],
				util.GetPodFullName(pods[3]): updatedPods[3],
			},
			podsToDelete:           []*v1.Pod{pods[0], pods[3]},
			expectedMapAfterDelete: map[string]*v1.Pod{},
		},
		{
			name:      "delete non-existing and existing pods",
			podsToAdd: []*v1.Pod{pods[1], pods[2]},
			expectedMapAfterAdd: map[string]*v1.Pod{
				util.GetPodFullName(pods[1]): pods[1],
				util.GetPodFullName(pods[2]): pods[2],
			},
			podsToUpdate: []*v1.Pod{updatedPods[1]},
			expectedMapAfterUpdate: map[string]*v1.Pod{
				util.GetPodFullName(pods[1]): updatedPods[1],
				util.GetPodFullName(pods[2]): pods[2],
			},
			podsToDelete: []*v1.Pod{pods[2], pods[3]},
			expectedMapAfterDelete: map[string]*v1.Pod{
				util.GetPodFullName(pods[1]): updatedPods[1],
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pods := make(map[string]*v1.Pod)
			upm := newUnschedulablePodsMap()
			for _, p := range test.podsToAdd {
				upm.addOrUpdate(p)
			}
			for _, equivalenceClass := range upm.equivalenceClasses {
				f := func(k, v interface{}) bool {
					pods[util.GetPodFullName(v.(*v1.Pod))] = v.(*v1.Pod)
					return true
				}
				klog.Error(equivalenceClass.PodSet)
				equivalenceClass.PodSet.Range(f)
			}
			if !reflect.DeepEqual(pods, test.expectedMapAfterAdd) {
				t.Errorf("Unexpected map after adding pods. Expected: %v, got: %v",
					test.expectedMapAfterAdd, pods)
			}

			if len(test.podsToUpdate) > 0 {
				for _, p := range test.podsToUpdate {
					upm.addOrUpdate(p)
				}
				pods = make(map[string]*v1.Pod)
				for _, equivalenceClass := range upm.equivalenceClasses {
					f := func(k, v interface{}) bool {
						pods[util.GetPodFullName(v.(*v1.Pod))] = v.(*v1.Pod)
						return true
					}
					equivalenceClass.PodSet.Range(f)
				}
				if !reflect.DeepEqual(pods, test.expectedMapAfterUpdate) {
					t.Errorf("Unexpected map after updating pods. Expected: %v, got: %v",
						test.expectedMapAfterUpdate, pods)
				}
			}

			for _, p := range test.podsToDelete {
				upm.delete(p)
			}
			pods = make(map[string]*v1.Pod)
			for _, equivalenceClass := range upm.equivalenceClasses {
				f := func(k, v interface{}) bool {
					pods[util.GetPodFullName(v.(*v1.Pod))] = v.(*v1.Pod)
					return true
				}
				equivalenceClass.PodSet.Range(f)
			}
			if !reflect.DeepEqual(pods, test.expectedMapAfterDelete) {
				t.Errorf("Unexpected map after deleting pods. Expected: %v, got: %v",
					test.expectedMapAfterDelete, pods)
			}

			upm.clear()
			pods = make(map[string]*v1.Pod)
			for _, equivalenceClass := range upm.equivalenceClasses {
				f := func(k, v interface{}) bool {
					pods[util.GetPodFullName(v.(*v1.Pod))] = v.(*v1.Pod)
					return true
				}
				equivalenceClass.PodSet.Range(f)
			}
			if len(upm.pods) != 0 {
				t.Errorf("Expected the map to be empty, but has %v elements.", len(pods))
			}
		})
	}
}
