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

func TranslateGPUResources(container *v1.Container) error {
	re := regexp.MustCompile(v1.ResourceGroupPrefix + "/gpu/" + `(.*?)/cards`)

	requests := container.Resources.Requests

	neededGPUQ := requests[v1.ResourceNvidiaGPU]
	neededGPUs := neededGPUQ.Value()
	haveGPUs := 0
	maxGPUIndex := -1
	for _, res := range container.Resources.AllocateFrom {
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
	return nil
}
