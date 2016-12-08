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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/docker/docker/pkg/parsers"
	"github.com/golang/glog"
	"google.golang.org/grpc"
	grpctypes "k8s.io/kubernetes/pkg/kubelet/hyper/types"
)

const (
	HYPER_PROTO       = "unix"
	HYPER_ADDR        = "/var/run/hyper.sock"
	HYPER_SCHEME      = "http"
	HYPER_SERVER      = "127.0.0.1:22318"
	DEFAULT_IMAGE_TAG = "latest"

	KEY_COMMAND        = "command"
	KEY_CONTAINER_PORT = "containerPort"
	KEY_CONTAINERS     = "containers"
	KEY_DNS            = "dns"
	KEY_ENTRYPOINT     = "entrypoint"
	KEY_ENVS           = "envs"
	KEY_HOST_PORT      = "hostPort"
	KEY_HOSTNAME       = "hostname"
	KEY_ID             = "id"
	KEY_IMAGE          = "image"
	KEY_IMAGEID        = "imageId"
	KEY_IMAGENAME      = "imageName"
	KEY_ITEM           = "item"
	KEY_LABELS         = "labels"
	KEY_MEMORY         = "memory"
	KEY_MOUNTPATH      = "path"
	KEY_NAME           = "name"
	KEY_POD_ID         = "podId"
	KEY_POD_NAME       = "podName"
	KEY_PORTS          = "ports"
	KEY_PROTOCOL       = "protocol"
	KEY_READONLY       = "readOnly"
	KEY_RESOURCE       = "resource"
	KEY_TAG            = "tag"
	KEY_TTY            = "tty"
	KEY_TYPE           = "type"
	KEY_VALUE          = "value"
	KEY_VCPU           = "vcpu"
	KEY_VOLUME         = "volume"
	KEY_VOLUME_DRIVE   = "driver"
	KEY_VOLUME_SOURCE  = "source"
	KEY_VOLUMES        = "volumes"
	KEY_WORKDIR        = "workdir"
	KEY_WHITE_NETS     = "portmappingWhiteLists"

	KEY_API_POD_UID = "k8s.hyper.sh/uid"

	TYPE_CONTAINER = "container"
	TYPE_POD       = "pod"

	VOLUME_TYPE_VFS = "vfs"

	//timeout in second for creating context with timeout.
	hyperContextTimeout = 15 * time.Second

	//timeout for image pulling progress report
	defaultImagePullingStuckTimeout = 1 * time.Minute
)

type HyperClient struct {
	proto  string
	addr   string
	scheme string
	client grpctypes.PublicAPIClient
}

func NewHyperClient() (*HyperClient, error) {
	var (
		scheme = HYPER_SCHEME
		proto  = HYPER_PROTO
		addr   = HYPER_ADDR
	)
	conn, err := grpc.Dial(HYPER_SERVER, grpc.WithInsecure())
	if err != nil {
		return nil, err
	}

	return &HyperClient{
		proto:  proto,
		addr:   addr,
		scheme: scheme,
		client: grpctypes.NewPublicAPIClient(conn),
	}, nil
}

// parseImageName parses a docker image string into two parts: repo and tag.
// If tag is empty, return the defaultImageTag.
func parseImageName(image string) (string, string) {
	repoToPull, tag := parsers.ParseRepositoryTag(image)
	// If no tag was specified, use the default "latest".
	if len(tag) == 0 {
		tag = DEFAULT_IMAGE_TAG
	}
	return repoToPull, tag
}

func (c *HyperClient) Version() (string, error) {
	request := grpctypes.VersionRequest{}

	ctx, cancel := getContextWithTimeout(hyperContextTimeout)
	defer cancel()

	response, err := c.client.Version(ctx, &request)
	if err != nil {
		return "", err
	}

	return response.Version, nil
}

func (c *HyperClient) GetPodIDByName(podName string) (string, error) {
	request := grpctypes.PodListRequest{}

	ctx, cancel := getContextWithTimeout(hyperContextTimeout)
	defer cancel()

	response, err := c.client.PodList(ctx, &request)
	if err != nil {
		return "", err
	}

	for _, pod := range response.PodList {
		if pod.PodName == podName {
			return pod.PodID, nil
		}
	}

	return "", fmt.Errorf("Can not get PodID by name %s", podName)
}

func (c *HyperClient) ListPods() ([]HyperPod, error) {
	request := grpctypes.PodListRequest{}

	ctx, cancel := getContextWithTimeout(hyperContextTimeout)
	defer cancel()

	response, err := c.client.PodList(ctx, &request)
	if err != nil {
		return nil, err
	}

	var result []HyperPod
	for _, pod := range response.PodList {

		var hyperPod HyperPod
		hyperPod.PodID = pod.PodID
		hyperPod.PodName = pod.PodName
		hyperPod.VmName = pod.VmID
		hyperPod.Status = pod.Status

		req := grpctypes.PodInfoRequest{PodID: pod.PodID}

		res, err := c.client.PodInfo(ctx, &req)
		if err != nil {
			return nil, err
		}

		hyperPod.PodInfo = res.PodInfo

		result = append(result, hyperPod)
	}

	return result, nil
}

func (c *HyperClient) ListContainers() ([]HyperContainer, error) {
	request := grpctypes.ContainerListRequest{}

	ctx, cancel := getContextWithTimeout(hyperContextTimeout)
	defer cancel()

	response, err := c.client.ContainerList(ctx, &request)
	if err != nil {
		return nil, err
	}

	var result []HyperContainer
	for _, container := range response.ContainerList {
		var h HyperContainer
		h.containerID = container.ContainerID
		h.name = container.ContainerName[1:]
		h.podID = container.PodID
		h.status = container.Status

		result = append(result, h)
	}

	return result, nil
}

func (c *HyperClient) Info() (*grpctypes.InfoResponse, error) {
	request := grpctypes.InfoRequest{}

	ctx, cancel := getContextWithTimeout(hyperContextTimeout)
	defer cancel()

	response, err := c.client.Info(ctx, &request)
	if err != nil {
		return nil, err
	}

	return response, nil
}

func (c *HyperClient) ListImages() ([]HyperImage, error) {
	request := grpctypes.ImageListRequest{
		All: false,
	}

	ctx, cancel := getContextWithTimeout(hyperContextTimeout)
	defer cancel()

	response, err := c.client.ImageList(ctx, &request)
	if err != nil {
		return nil, err
	}

	var hyperImages []HyperImage
	for _, image := range response.ImageList {
		var imageHyper HyperImage
		imageHyper.repository, imageHyper.tag = parseImageName(image.RepoTags[0])
		imageHyper.imageID = image.Id
		imageHyper.createdAt = image.Created
		imageHyper.virtualSize = image.VirtualSize

		hyperImages = append(hyperImages, imageHyper)
	}

	return hyperImages, nil
}

func (c *HyperClient) RemoveImage(imageID string) error {
	request := grpctypes.ImageRemoveRequest{
		Image: imageID,
	}

	ctx, cancel := getContextWithTimeout(hyperContextTimeout)
	defer cancel()

	_, err := c.client.ImageRemove(ctx, &request)
	if err != nil {
		return err
	}

	return nil
}

func (c *HyperClient) RemovePod(podID string) error {
	request := grpctypes.PodRemoveRequest{
		PodID: podID,
	}

	ctx, cancel := getContextWithTimeout(hyperContextTimeout)
	defer cancel()

	_, err := c.client.PodRemove(ctx, &request)
	if err != nil {
		return err
	}

	return nil
}

func (c *HyperClient) StartPod(podID string) error {
	ctx, cancel := getContextWithTimeout(hyperContextTimeout)
	defer cancel()

	stream, err := c.client.PodStart(ctx)
	if err != nil {
		return err
	}

	request := grpctypes.PodStartMessage{
		PodID: podID,
	}
	err = stream.Send(&request)
	if err != nil {
		return err
	}

	_, err = stream.Recv()
	if err != nil {
		return err
	}

	return nil
}

func (c *HyperClient) StopPod(podID string) error {
	request := grpctypes.PodStopRequest{
		PodID: podID,
	}

	ctx, cancel := getContextWithTimeout(hyperContextTimeout)
	defer cancel()

	_, err := c.client.PodStop(ctx, &request)
	if err != nil {
		return err
	}

	return nil
}

func (c *HyperClient) PullImage(image string, credential string) error {
	imageName, tag := parseImageName(image)
	authConfig := &grpctypes.AuthConfig{}
	if credential != "" {
		authJSON := base64.NewDecoder(base64.URLEncoding, strings.NewReader(credential))
		if err := json.NewDecoder(authJSON).Decode(authConfig); err != nil {
			// for a pull it is not an error if no auth was given
			// to increase compatibility with the existing api it is defaulting to be empty
			authConfig = &grpctypes.AuthConfig{}
		}
	}

	// We don't know the how long it will take to finish pulling, use pull progress to control it later
	ctx, cancel := getContextWithCancel()
	defer cancel()

	request := grpctypes.ImagePullRequest{
		Image: imageName,
		Tag:   tag,
		Auth:  authConfig,
	}
	stream, err := c.client.ImagePull(ctx, &request)
	if err != nil {
		return err
	}

	errC := make(chan error)
	progressC := make(chan struct{})
	ticker := time.NewTicker(defaultImagePullingStuckTimeout)
	defer ticker.Stop()

	go func() {
		for {
			_, err := stream.Recv()
			if err == io.EOF {
				errC <- nil
				return
			}
			if err != nil {
				errC <- err
				return
			}
			progressC <- struct{}{}
		}
	}()

	for {
		select {
		case <-ticker.C:
			return fmt.Errorf("Cancel pulling image %q because of no progress for %v", image, defaultImagePullingStuckTimeout)
		case err = <-errC:
			return err
		case <-progressC:
			ticker.Stop()
			ticker = time.NewTicker(defaultImagePullingStuckTimeout)
			glog.V(2).Infof("Pulling image: %s", image)
		}
	}

	return nil
}

func (c *HyperClient) CreatePod(podSpec *grpctypes.UserPod) (string, error) {
	request := grpctypes.PodCreateRequest{
		PodSpec: podSpec,
	}

	ctx, cancel := getContextWithTimeout(hyperContextTimeout)
	defer cancel()

	response, err := c.client.PodCreate(ctx, &request)
	if err != nil {
		return "", err
	}

	return response.PodID, nil
}

func (c *HyperClient) Attach(opts AttachToContainerOptions) error {
	if opts.Container == "" {
		return fmt.Errorf("No Such Container %s", opts.Container)
	}

	ctx, cancel := getContextWithCancel()
	defer cancel()

	stream, err := c.client.Attach(ctx)
	if err != nil {
		return err
	}

	request := grpctypes.AttachMessage{
		ContainerID: opts.Container,
	}
	if err := stream.Send(&request); err != nil {
		return nil
	}

	var recvStdoutError chan error
	if opts.OutputStream != nil {
		recvStdoutError = getReturnValue(func() error {
			for {
				res, err := stream.Recv()
				if err != nil {
					if err == io.EOF {
						return nil
					}
					return err
				}
				n, err := opts.OutputStream.Write(res.Data)
				if err != nil {
					return err
				}
				if n != len(res.Data) {
					return io.ErrShortWrite
				}
			}
		})
	}

	var reqStdinError chan error
	if opts.InputStream != nil {
		reqStdinError = getReturnValue(func() error {
			for {
				req := make([]byte, 512)
				n, err := opts.InputStream.Read(req)
				if err := stream.Send(&grpctypes.AttachMessage{Data: req[:n]}); err != nil {
					return err
				}
				if err == io.EOF {
					return nil
				}
				if err != nil {
					return err
				}
			}
		})
	}

	if opts.OutputStream != nil && opts.InputStream != nil {
		select {
		case err = <-recvStdoutError:
		case err = <-reqStdinError:
		}
	} else if opts.OutputStream != nil {
		err = <-recvStdoutError
	} else if opts.InputStream != nil {
		err = <-reqStdinError
	}

	if err != nil {
		return err
	}

	//TODO: GetExitCode
	return nil
}

func (c *HyperClient) Exec(opts ExecInContainerOptions) error {
	if opts.Container == "" {
		return fmt.Errorf("No Such Container %s", opts.Container)
	}

	createRequest := grpctypes.ExecCreateRequest{
		ContainerID: opts.Container,
		Command:     opts.Commands,
		Tty:         opts.TTY,
	}

	ctx, cancel := getContextWithCancel()
	defer cancel()

	createResponse, err := c.client.ExecCreate(ctx, &createRequest)
	if err != nil {
		return err
	}

	execId := createResponse.ExecID

	stream, err := c.client.ExecStart(ctx)
	if err != nil {
		return err
	}

	startRequest := grpctypes.ExecStartRequest{
		ContainerID: opts.Container,
		ExecID:      execId,
	}
	err = stream.Send(&startRequest)
	if err != nil {
		return err
	}

	var recvStdoutError chan error
	if opts.OutputStream != nil {
		recvStdoutError = getReturnValue(func() error {
			for {
				res, err := stream.Recv()
				if err != nil {
					if err == io.EOF {
						return nil
					}
					return err
				}
				n, err := opts.OutputStream.Write(res.Stdout)
				if err != nil {
					return err
				}
				if n != len(res.Stdout) {
					return io.ErrShortWrite
				}
			}
		})
	}

	var reqStdinError chan error
	if opts.InputStream != nil {
		reqStdinError = getReturnValue(func() error {
			for {
				req := make([]byte, 512)
				n, err := opts.InputStream.Read(req)
				if err := stream.Send(&grpctypes.ExecStartRequest{Stdin: req[:n]}); err != nil {
					return err
				}
				if err == io.EOF {
					return nil
				}
				if err != nil {
					return err
				}
			}
		})
	}

	if opts.OutputStream != nil && opts.InputStream != nil {
		select {
		case err = <-recvStdoutError:
		case err = <-reqStdinError:
		}
	} else if opts.OutputStream != nil {
		err = <-recvStdoutError
	} else if opts.InputStream != nil {
		err = <-reqStdinError
	}

	if err != nil {
		return err
	}

	//TODO: GetExitCode
	return nil
}

func (c *HyperClient) ContainerLogs(opts ContainerLogsOptions) error {
	request := grpctypes.ContainerLogsRequest{
		Container:  opts.Container,
		Follow:     opts.Follow,
		Timestamps: opts.Timestamps,
		Tail:       fmt.Sprintf("%d", opts.TailLines),
		Since:      fmt.Sprintf("%d", opts.Since),
		Stdout:     true,
		Stderr:     true,
	}

	ctx, cancel := getContextWithTimeout(hyperContextTimeout)
	defer cancel()

	stream, err := c.client.ContainerLogs(ctx, &request)
	if err != nil {
		return err
	}

	for {
		res, err := stream.Recv()
		if err == io.EOF {
			if opts.Follow == true {
				continue
			}
			break
		}
		if err != nil {
			return err
		}

		if len(res.Log) > 0 {
			// there are 8 bytes of prefix in every line of hyperd's log
			n, err := opts.OutputStream.Write(res.Log[8:])
			if err != nil {
				return err
			}
			if n != len(res.Log)-8 {
				return io.ErrShortWrite
			}
		}
	}

	return nil
}

func (client *HyperClient) IsImagePresent(repo, tag string) (bool, error) {
	return true, nil
}

func (c *HyperClient) ListServices(podId string) ([]*grpctypes.UserService, error) {
	request := grpctypes.ServiceListRequest{
		PodID: podId,
	}

	ctx, cancel := getContextWithTimeout(hyperContextTimeout)
	defer cancel()

	response, err := c.client.ServiceList(ctx, &request)
	if err != nil {
		if strings.Contains(err.Error(), "doesn't have services discovery") {
			return nil, nil
		} else {
			return nil, err
		}
	}

	return response.Services, nil
}

func (c *HyperClient) UpdateServices(podId string, services []*grpctypes.UserService) error {
	request := grpctypes.ServiceUpdateRequest{
		PodID:    podId,
		Services: services,
	}

	ctx, cancel := getContextWithTimeout(hyperContextTimeout)
	defer cancel()

	_, err := c.client.ServiceUpdate(ctx, &request)
	if err != nil {
		return err
	}

	return nil
}

func (c *HyperClient) UpdatePodLabels(podId string, labels map[string]string) error {
	request := grpctypes.PodLabelsRequest{
		PodID:    podId,
		Override: true,
		Labels:   labels,
	}

	ctx, cancel := getContextWithTimeout(hyperContextTimeout)
	defer cancel()

	_, err := c.client.SetPodLabels(ctx, &request)
	if err != nil {
		return err
	}

	return nil
}
