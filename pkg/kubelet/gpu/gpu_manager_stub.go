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

	"github.com/golang/glog"

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

// Resource translation to max level specified in nodeInfo

// TranslateResource translates resources to next level
func TranslateResource(nodeInfo *schedulercache.NodeInfo, container *v1.Container,
	stagePrefix string, thisStage string, nextStage string) {

	// see if translation needed
	translationNeeded := false
	re := regexp.MustCompile(stagePrefix + "/" + thisStage + `/(.*?)/` + nextStage + `(.*)`)
	for key := range nodeInfo.AllocatableResource().OpaqueIntResources {
		matches := re.FindStringSubmatch(string(key))
		if (len(matches)) >= 2 {
			translationNeeded = true
			break
		}
	}
	if !translationNeeded {
		return
	}

	// find max existing index
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
	re2 := regexp.MustCompile(stagePrefix + "/" + nextStage + "/" + `((.*?)/(.*))`)
	newList := make(v1.ResourceList)
	groupMap := make(map[string]string)
	// ordered addition to make sure groupIndex is deterministic based on order
	reqKeys := v1.SortedStringKeys(container.Resources.Requests)
	for _, resKey := range reqKeys {
		val := container.Resources.Requests[v1.ResourceName(resKey)]
		matches := re.FindStringSubmatch(string(resKey))
		newResKey := v1.ResourceName(resKey)
		if len(matches) == 0 { // does not qualify as next stage resource
			matches = re2.FindStringSubmatch(string(resKey))
			if len(matches) >= 4 {
				mapGrp, available := groupMap[matches[2]]
				if !available {
					groupMap[matches[2]] = strconv.Itoa(groupIndex)
					groupIndex++
					mapGrp = groupMap[matches[2]]
				}
				newResKey = v1.ResourceName(stagePrefix + "/" + thisStage + "/" + mapGrp + "/" + nextStage + "/" + matches[1])
			}
		}
		newList[newResKey] = val
	}
	container.Resources.Requests = newList
	glog.V(5).Infoln("New Resources", container.Resources.Requests)
}

// TranslateGPUResources translates GPU resources to max level
func TranslateGPUResources(nodeInfo *schedulercache.NodeInfo, container *v1.Container) error {
	requests := container.Resources.Requests

	// First stage translation, translate # of cards to simple GPU resources - extra stage
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

	// perform 2nd stage translation if needed
	TranslateResource(nodeInfo, container, v1.ResourceGroupPrefix, "gpugrp", "gpu")

	return nil
}
