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

package equivalence

import (
	"testing"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// makeBasicPod returns a Pod object with many of the fields populated.
func makeBasicPod(name string) *v1.Pod {
	isController := true
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "test-ns",
			Labels:    map[string]string{"app": "web", "env": "prod"},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "v1",
					Kind:       "ReplicationController",
					Name:       "rc",
					UID:        "123",
					Controller: &isController,
				},
			},
		},
		Spec: v1.PodSpec{
			Affinity: &v1.Affinity{
				NodeAffinity: &v1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
						NodeSelectorTerms: []v1.NodeSelectorTerm{
							{
								MatchExpressions: []v1.NodeSelectorRequirement{
									{
										Key:      "failure-domain.beta.kubernetes.io/zone",
										Operator: "Exists",
									},
								},
							},
						},
					},
				},
				PodAffinity: &v1.PodAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: []v1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"app": "db"}},
							TopologyKey: "kubernetes.io/hostname",
						},
					},
				},
				PodAntiAffinity: &v1.PodAntiAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: []v1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"app": "web"}},
							TopologyKey: "kubernetes.io/hostname",
						},
					},
				},
			},
			InitContainers: []v1.Container{
				{
					Name:  "init-pause",
					Image: "gcr.io/google_containers/pause",
					Resources: v1.ResourceRequirements{
						Limits: v1.ResourceList{
							"cpu": resource.MustParse("1"),
							"mem": resource.MustParse("100Mi"),
						},
					},
				},
			},
			Containers: []v1.Container{
				{
					Name:  "pause",
					Image: "gcr.io/google_containers/pause",
					Resources: v1.ResourceRequirements{
						Limits: v1.ResourceList{
							"cpu": resource.MustParse("1"),
							"mem": resource.MustParse("100Mi"),
						},
					},
					VolumeMounts: []v1.VolumeMount{
						{
							Name:      "nfs",
							MountPath: "/srv/data",
						},
					},
				},
			},
			NodeSelector: map[string]string{"node-type": "awesome"},
			Tolerations: []v1.Toleration{
				{
					Effect:   "NoSchedule",
					Key:      "experimental",
					Operator: "Exists",
				},
			},
			Volumes: []v1.Volume{
				{
					VolumeSource: v1.VolumeSource{
						PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
							ClaimName: "someEBSVol1",
						},
					},
				},
				{
					VolumeSource: v1.VolumeSource{
						PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
							ClaimName: "someEBSVol2",
						},
					},
				},
				{
					Name: "nfs",
					VolumeSource: v1.VolumeSource{
						NFS: &v1.NFSVolumeSource{
							Server: "nfs.corp.example.com",
						},
					},
				},
			},
		},
	}
}

func TestGetEquivalenceHash(t *testing.T) {
	pod1 := makeBasicPod("pod1")
	pod2 := makeBasicPod("pod2")

	pod3 := makeBasicPod("pod3")
	pod3.Spec.Volumes = []v1.Volume{
		{
			VolumeSource: v1.VolumeSource{
				PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
					ClaimName: "someEBSVol111",
				},
			},
		},
	}

	pod4 := makeBasicPod("pod4")
	pod4.Spec.Volumes = []v1.Volume{
		{
			VolumeSource: v1.VolumeSource{
				PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
					ClaimName: "someEBSVol222",
				},
			},
		},
	}

	pod5 := makeBasicPod("pod5")
	pod5.Spec.Volumes = []v1.Volume{}

	pod6 := makeBasicPod("pod6")
	pod6.Spec.Volumes = nil

	pod7 := makeBasicPod("pod7")
	pod7.Spec.NodeSelector = nil

	pod8 := makeBasicPod("pod8")
	pod8.Spec.NodeSelector = make(map[string]string)

	type podInfo struct {
		pod         *v1.Pod
		hashIsValid bool
	}

	tests := []struct {
		name         string
		podInfoList  []podInfo
		isEquivalent bool
	}{
		{
			name: "pods with everything the same except name",
			podInfoList: []podInfo{
				{pod: pod1, hashIsValid: true},
				{pod: pod2, hashIsValid: true},
			},
			isEquivalent: true,
		},
		{
			name: "pods that only differ in their PVC volume sources",
			podInfoList: []podInfo{
				{pod: pod3, hashIsValid: true},
				{pod: pod4, hashIsValid: true},
			},
			isEquivalent: false,
		},
		{
			name: "pods that have no volumes, but one uses nil and one uses an empty slice",
			podInfoList: []podInfo{
				{pod: pod5, hashIsValid: true},
				{pod: pod6, hashIsValid: true},
			},
			isEquivalent: true,
		},
		{
			name: "pods that have no NodeSelector, but one uses nil and one uses an empty map",
			podInfoList: []podInfo{
				{pod: pod7, hashIsValid: true},
				{pod: pod8, hashIsValid: true},
			},
			isEquivalent: true,
		},
	}

	var (
		targetPodInfo podInfo
		targetHash    types.UID
	)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for i, podInfo := range test.podInfoList {
				testPod := podInfo.pod
				eclassInfo := NewClass(testPod)
				if eclassInfo == nil && podInfo.hashIsValid {
					t.Errorf("Failed: pod %v is expected to have valid hash", testPod)
				}

				if eclassInfo != nil {
					// NOTE(harry): the first element will be used as target so
					// this logic can't verify more than two inequivalent pods
					if i == 0 {
						targetHash = eclassInfo.Hash
						targetPodInfo = podInfo
					} else {
						if targetHash != eclassInfo.Hash {
							if test.isEquivalent {
								t.Errorf("Failed: pod: %v is expected to be equivalent to: %v", testPod, targetPodInfo.pod)
							}
						}
					}
				}
			}
		})
	}
}

func BenchmarkEquivalenceHash(b *testing.B) {
	pod := makeBasicPod("test")
	for i := 0; i < b.N; i++ {
		getEquivalencePod(pod)
	}
}
