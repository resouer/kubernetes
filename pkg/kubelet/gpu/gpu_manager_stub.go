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

package gpu

import (
	"fmt"
	"regexp"
	"strconv"

	v1 "k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/resource"
	"k8s.io/kubernetes/plugin/pkg/scheduler/schedulercache"
)

type gpuManagerStub struct{}

func (gms *gpuManagerStub) Start() error {
	return nil
}

func (gms *gpuManagerStub) Capacity() v1.ResourceList {
	return nil
}

// AllocateGPU Returns volumename, volumedriver, devices
func (gms *gpuManagerStub) AllocateGPU(_ *v1.Pod, _ *v1.Container) (volumeName string, volumeDriver string, devices []string, err error) {
	devices = nil
	err = fmt.Errorf("GPUs are not supported")
	return volumeName, volumeDriver, devices, err
}

func NewGPUManagerStub() GPUManager {
	return &gpuManagerStub{}
}

func AddResource(list v1.ResourceList, key string, val int64) {
	list[v1.ResourceName(key)] = *resource.NewQuantity(val, resource.DecimalSI)
}

func TranslateGPUResources(nodeInfo *schedulercache.NodeInfo, container *v1.Container) error {
	requests := container.Resources.Requests

	// First stage translation, translate # of cards to simple GPU resources
	re := regexp.MustCompile(v1.ResourceGroupPrefix + "/gpu/" + `(.*?)/cards`)

	neededGPUQ := requests[v1.ResourceNvidiaGPU]
	neededGPUs := neededGPUQ.Value()
	haveGPUs := 0
	maxGPUIndex := -1
	for res := range container.Resources.Requests {
		matches := re.FindStringSubmatch(string(res))
		if len(matches) >= 2 {
			haveGPUs++
			gpuIndex, err := strconv.Atoi(matches[1])
			if err == nil {
				if gpuIndex > maxGPUIndex {
					maxGPUIndex = gpuIndex
				}
			}
		}
	}
	diffGPU := int(neededGPUs - int64(haveGPUs))
	for i := 0; i < diffGPU; i++ {
		gpuIndex := maxGPUIndex + i + 1
		AddResource(requests, v1.ResourceGroupPrefix+"/gpu/"+strconv.Itoa(gpuIndex)+"/cards", 1)
	}

	stages := 1
	re = regexp.MustCompile(v1.ResourceGroupPrefix + `/gpu/(.*?)/device/(.*)`)
	for key := range nodeInfo.AllocatableResource().OpaqueIntResources {
		matches := re.FindStringSubmatch(string(key))
		if (len(matches)) >= 2 {
			stages = 2
			break
		}
	}

	if stages == 2 {
		// second stage translation
		maxGroupIndex := -1
		for res := range container.Resources.Requests {
			matches := re.FindStringSubmatch(string(res))
			if len(matches) >= 2 {
				groupIndex, err := strconv.Atoi(matches[1])
				if err == nil {
					if groupIndex > maxGroupIndex {
						maxGroupIndex = groupIndex
					}
				}
			}
		}
		groupIndex := maxGroupIndex + 1
		re2 := regexp.MustCompile(v1.ResourceGroupPrefix + `/gpu/((.*?)/(.*))`)
		newList := make(v1.ResourceList)
		groupMap := make(map[string]string)
		for res, val := range container.Resources.Requests {
			matches := re.FindStringSubmatch(string(res))
			newRes := res
			if len(matches) == 0 {
				matches = re2.FindStringSubmatch(string(res))
				if len(matches) >= 4 {
					mapDevice, available := groupMap[matches[2]]
					if !available {
						groupMap[matches[2]] = strconv.Itoa(groupIndex)
						groupIndex++
						mapDevice = groupMap[matches[2]]
					}
					newRes = v1.ResourceName(v1.ResourceGroupPrefix + "/gpu/" + mapDevice + "/device/" + matches[1])
				}
			}
			newList[newRes] = val
		}
		container.Resources.Requests = newList
		fmt.Println("New Resources", container.Resources.Requests)
	}

	return nil
}
