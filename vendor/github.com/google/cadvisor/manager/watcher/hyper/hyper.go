// Copyright 2016 Google Inc. All Rights Reserved.
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

// Package rkt implements the watcher interface for rkt
package hyper

import (
	"time"

	"github.com/google/cadvisor/container/hyper"
	"github.com/google/cadvisor/manager/watcher"

	"github.com/golang/glog"
)

const (
	runningStatus = "running"
	WatchInterval = 3 * time.Second
)

type hyperContainerWatcher struct {
	// Signal for watcher thread to stop.
	stopWatcher chan error
	isPod       bool
	client      *hyper.HyperClient
	// container watcher
	containers map[string]string
}

func NewHyperContainerWatcher() (watcher.ContainerWatcher, error) {
	watcher := &hyperContainerWatcher{
		stopWatcher: make(chan error),
		containers:  make(map[string]string),
	}

	return watcher, nil
}

func (self *hyperContainerWatcher) Start(events chan watcher.ContainerEvent) error {
	go self.detectHyperContainers(events)
	return nil
}

func (self *hyperContainerWatcher) detectHyperContainers(events chan watcher.ContainerEvent) {
	glog.Infof("starting detectHyperContainers thread")
	ticker := time.Tick(10 * time.Second)
	// curpods is used to store known pods
	curpods := make(map[string]hyper.HyperPod)

	for {
		select {
		case <-ticker:
			pods, err := self.listRunningPods()
			if err != nil {
				glog.Errorf("detectHyperContainers: listRunningPods failed: %v", err)
				continue
			}
			curpods = self.syncRunningPods(pods, events, curpods)

		case <-self.stopWatcher:
			glog.Infof("Exiting hyperContainer Thread")
			return
		}
	}
}

func (self *hyperContainerWatcher) syncRunningPods(pods []hyper.HyperPod, events chan watcher.ContainerEvent, curpods map[string]hyper.HyperPod) map[string]hyper.HyperPod {
	newpods := make(map[string]hyper.HyperPod)

	for _, pod := range pods {
		newpods[pod.PodID] = pod
		if _, ok := curpods[pod.PodID]; !ok {
			glog.V(5).Infof("hyperPod to add = %v, %v", pod.PodName, pod.VmName)
			self.sendUpdateEvent(pod.VmName, events)
		}
	}

	for id, pod := range curpods {
		if _, ok := newpods[id]; !ok {
			glog.V(5).Infof("hyperPod to delete = %v, %v", pod.PodName, pod.VmName)
			self.sendDestroyEvent(pod.VmName, events)
		}
	}

	return newpods
}

func (self *hyperContainerWatcher) sendUpdateEvent(name string, events chan watcher.ContainerEvent) {
	events <- watcher.ContainerEvent{
		EventType:   watcher.ContainerAdd,
		Name:        name,
		WatchSource: watcher.Hyper,
	}
}

func (self *hyperContainerWatcher) sendDestroyEvent(name string, events chan watcher.ContainerEvent) {
	events <- watcher.ContainerEvent{
		EventType:   watcher.ContainerDelete,
		Name:        name,
		WatchSource: watcher.Hyper,
	}
}

func (self *hyperContainerWatcher) listRunningPods() ([]hyper.HyperPod, error) {
	var runningPods []hyper.HyperPod
	client := hyper.NewHyperClient()

	pods, err := client.ListPods()

	if err != nil {
		return nil, err
	}

	for _, pod := range pods {
		if pod.Status == runningStatus {
			runningPods = append(runningPods, pod)
		}
	}

	return runningPods, nil
}

func (self *hyperContainerWatcher) Stop() error {
	// Rendezvous with the watcher thread.
	self.stopWatcher <- nil
	return nil
}
