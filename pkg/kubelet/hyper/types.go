/*
Copyright 2015 The Kubernetes Authors All rights reserved.

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

package hyper

import (
	"io"
	grpctypes "k8s.io/kubernetes/pkg/kubelet/hyper/types"
)

const (
	StatusRunning = "running"
	StatusPending = "pending"
	StatusFailed  = "failed"
	StatusSuccess = "succeeded"
)

type HyperContainer struct {
	containerID string
	name        string
	podID       string
	status      string
}

type AttachToContainerOptions struct {
	Container    string
	InputStream  io.Reader
	OutputStream io.Writer
	ErrorStream  io.Writer
	TTY          bool
}

type ContainerLogsOptions struct {
	Container    string
	OutputStream io.Writer
	ErrorStream  io.Writer

	Follow     bool
	Since      int64
	Timestamps bool
	TailLines  int64
}

type ExecInContainerOptions struct {
	Container    string
	InputStream  io.Reader
	OutputStream io.Writer
	ErrorStream  io.Writer
	Commands     []string
	TTY          bool
}

type HyperImage struct {
	repository  string
	tag         string
	imageID     string
	createdAt   int64
	virtualSize int64
}

type HyperPod struct {
	PodID   string
	PodName string
	VmName  string
	Status  string
	PodInfo *grpctypes.PodInfo
}
