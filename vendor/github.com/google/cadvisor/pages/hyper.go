// Copyright 2014 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package pages

import (
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	//"github.com/google/cadvisor/container/hyper"
	info "github.com/google/cadvisor/info/v1"
	"github.com/google/cadvisor/manager"

	"github.com/golang/glog"
)

const HyperPage = "/hyper/"

func getHyperDisplayName(cont info.ContainerReference) string {
	if len(cont.Aliases) < 3 {
		return cont.Name
	}

	containerID := cont.Aliases[2]
	containerName := cont.Aliases[1]
	for _, alias := range cont.Aliases {
		if strings.HasPrefix(alias, "pod-") {
			containerID = alias
			break
		}
	}

	return fmt.Sprintf("%s (%s)", containerID, containerName)
}

func serveHyperPage(m manager.Manager, w http.ResponseWriter, u *url.URL) error {
	start := time.Now()

	// The container name is the path after the handler
	containerName := u.Path[len(HyperPage)-1:]
	rootDir := getRootDir(containerName)

	var data *pageData
	if containerName == "/" {
		// Get the containers.
		reqParams := info.ContainerInfoRequest{
			NumStats: 0,
		}
		conts, err := m.AllHyperContainers(&reqParams)
		if err != nil {
			return fmt.Errorf("failed to get container %q with error: %v", containerName, err)
		}
		subcontainers := make([]link, 0, len(conts))
		for _, cont := range conts {
			subcontainers = append(subcontainers, link{
				Text: getHyperDisplayName(cont.ContainerReference),
				Link: path.Join(rootDir, HyperPage, cont.ContainerReference.Name),
			})
		}

		// Get Hyper status
		status, err := m.HyperInfo()
		if err != nil {
			return err
		}
		hyperStatus, driverStatus := toStatusKV(status)

		// Get Images
		images, err := m.HyperImages()
		if err != nil {
			return err
		}

		hyperContainersText := "Hyper Pods"
		data = &pageData{
			DisplayName: hyperContainersText,
			ParentContainers: []link{
				{
					Text: hyperContainersText,
					Link: path.Join(rootDir, HyperPage),
				}},
			Subcontainers:      subcontainers,
			Root:               rootDir,
			DockerStatus:       hyperStatus,
			DockerDriverStatus: driverStatus,
			DockerImages:       images,
		}
	} else {
		// Get the container.
		reqParams := info.ContainerInfoRequest{
			NumStats: 60,
		}
		cont, err := m.HyperContainer(containerName, &reqParams)
		if err != nil {
			return fmt.Errorf("failed to get container %q with error: %v", containerName, err)
		}
		displayName := getHyperDisplayName(cont.ContainerReference)

		// Make a list of the parent containers and their links
		var parentContainers []link
		parentContainers = append(parentContainers, link{
			Text: "Hyper Pods",
			Link: path.Join(rootDir, HyperPage),
		})
		parentContainers = append(parentContainers, link{
			Text: displayName,
			Link: path.Join(rootDir, HyperPage, cont.Name),
		})

		// Get the MachineInfo
		machineInfo, err := m.GetMachineInfo()
		if err != nil {
			return err
		}
		data = &pageData{
			DisplayName:            displayName,
			ContainerName:          escapeContainerName(cont.Name),
			ParentContainers:       parentContainers,
			Spec:                   cont.Spec,
			Stats:                  cont.Stats,
			MachineInfo:            machineInfo,
			ResourcesAvailable:     cont.Spec.HasCpu || cont.Spec.HasMemory || cont.Spec.HasNetwork,
			CpuAvailable:           cont.Spec.HasCpu,
			MemoryAvailable:        cont.Spec.HasMemory,
			NetworkAvailable:       cont.Spec.HasNetwork,
			FsAvailable:            cont.Spec.HasFilesystem,
			CustomMetricsAvailable: cont.Spec.HasCustomMetrics,
			Root: rootDir,
		}
	}

	err := pageTemplate.Execute(w, data)
	if err != nil {
		glog.Errorf("Failed to apply template: %s", err)
	}

	glog.V(5).Infof("Request took %s", time.Since(start))
	return nil
}
