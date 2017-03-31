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

// This package re-written by Sanjeev Mehrotra to use nvidia-docker-plugin
package nvidia

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"strconv"

	v1 "k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/resource"
	"k8s.io/kubernetes/pkg/kubelet/dockertools"
	"k8s.io/kubernetes/pkg/kubelet/gpu"
)

type memoryInfo struct {
	Global int64 `json:"Global"`
}

type gpuInfo struct {
	ID     string     `json:"UUID"`
	Model  string     `json:"Model"`
	Path   string     `json:"Path"`
	Memory memoryInfo `json:"Memory"`
	Found  bool       `json:"-"`
	Index  int        `json:"-"`
	InUse  bool       `json:"-"`
}

type versionInfo struct {
	Driver string `json:"Driver"`
	CUDA   string `json:"CUDA"`
}
type gpusInfo struct {
	Version versionInfo `json:"Version"`
	Gpus    []gpuInfo   `json:"Devices"`
}

// nvidiaGPUManager manages nvidia gpu devices.
type nvidiaGPUManager struct {
	sync.Mutex
	gpus    map[string]gpuInfo
	numGpus int
}

// NewNvidiaGPUManager returns a GPUManager that manages local Nvidia GPUs.
// TODO: Migrate to use pod level cgroups and make it generic to all runtimes.
func NewNvidiaGPUManager(dockerClient dockertools.DockerInterface) (gpu.GPUManager, error) {
	if dockerClient == nil {
		return nil, fmt.Errorf("invalid docker client specified")
	}
	return &nvidiaGPUManager{gpus: make(map[string]gpuInfo)}, nil
}

func getResponse(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	return body, err
}

// Initialize the GPU devices
func (ngm *nvidiaGPUManager) UpdateGPUInfo() error {
	ngm.Lock()
	defer ngm.Unlock()

	body, err := getResponse("http://localhost:3476/v1.0/gpu/info/json")
	if err != nil {
		return err
	}
	var gpus gpusInfo
	if err := json.Unmarshal(body, &gpus); err != nil {
		return err
	}

	for key := range ngm.gpus {
		copy := ngm.gpus[key]
		copy.Found = false
		ngm.gpus[key] = copy
	}
	// go over found GPUs and reassign
	for index, gpuFound := range gpus.Gpus {
		gpu, available := ngm.gpus[gpuFound.ID]
		if available {
			gpuFound.InUse = gpu.InUse
		}
		gpuFound.Found = true
		gpuFound.Index = index
		ngm.gpus[gpuFound.ID] = gpuFound
	}
	ngm.numGpus = len(gpus.Gpus) // if ngm.numGpus <> len(ngm.gpus), then some gpus have gone missing

	return nil
}

func (ngm *nvidiaGPUManager) Start() error {
	return ngm.UpdateGPUInfo()
}

// Get how many GPU cards we have.
func (ngm *nvidiaGPUManager) Capacity() v1.ResourceList {
	ngm.UpdateGPUInfo() // don't care about error, ignore it
	gpus := resource.NewQuantity(int64(ngm.numGpus), resource.DecimalSI)
	resourceList := make(v1.ResourceList)
	resourceList[v1.ResourceNvidiaGPU] = *gpus
	for _, val := range ngm.gpus {
		//gpuID := strconv.Itoa(i)
		gpuID := val.ID
		gpu.AddResource(resourceList, v1.ResourceGroupPrefix+"/gpu/"+gpuID+"/memory", val.Memory.Global*int64(1024)*int64(1024))
		gpu.AddResource(resourceList, v1.ResourceGroupPrefix+"/gpu/"+gpuID+"/cards", int64(1))
	}
	return resourceList
}

// AllocateGPU returns VolumeName, VolumeDriver, and list of Devices to use
func (ngm *nvidiaGPUManager) AllocateGPU(pod *v1.Pod, container *v1.Container) (string, string, []string, error) {
	gpuList := []string{}
	volumeDriver := ""
	volumeName := ""
	ngm.Lock()
	defer ngm.Unlock()

	re := regexp.MustCompile(v1.ResourceGroupPrefix + "/gpu/" + `(.*?)/cards`)

	devString := ""
	for _, res := range container.Resources.AllocateFrom {
		matches := re.FindStringSubmatch(string(res))
		if len(matches) >= 2 {
			id := matches[1]
			if devString == "" {
				devString = strconv.Itoa(ngm.gpus[id].Index)
			} else {
				devString += "+" + strconv.Itoa(ngm.gpus[id].Index)
			}
			if ngm.gpus[id].Found {
				gpuList = append(gpuList, ngm.gpus[id].Path)
			}
		}
	}
	devString = "http://localhost:3476/v1.0/docker/cli?dev=" + devString
	body, err := getResponse(devString)
	if err != nil {
		return "", "", nil, err
	}

	re = regexp.MustCompile(`(.*?)=(.*?)`)
	tokens := strings.Split(string(body), " ")
	for _, token := range tokens {
		matches := re.FindStringSubmatch(string(token))
		if len(matches) == 3 {
			key := matches[1]
			val := matches[2]
			if key == `--device` {
				//gpuList = append(gpuList, val)
			} else if key == `volume--driver` {
				volumeDriver = val
			} else if key == `volume` {
				volumeName = val
			}
		}
	}

	return volumeName, volumeDriver, gpuList, nil
}
