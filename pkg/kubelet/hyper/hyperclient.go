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
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/docker/docker/pkg/parsers"
	"github.com/docker/docker/pkg/term"
	"golang.org/x/net/context"
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

	KEY_API_POD_UID = "k8s.hyper.sh/uid"

	TYPE_CONTAINER = "container"
	TYPE_POD       = "pod"

	VOLUME_TYPE_VFS = "vfs"
)

type HyperClient struct {
	proto  string
	addr   string
	scheme string
	client grpctypes.PublicAPIClient
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

type hijackOptions struct {
	in     io.Reader
	stdout io.Writer
	stderr io.Writer
	data   interface{}
	tty    bool
}

/*
{"Cause":"VM shut down","Code":0,"ID":"pod-MavdmoyvEP"}
*/
type podRemoveResult struct {
	Cause string `json:"Cause"`
	Code  int    `json:"Code"`
	ID    string `json:"ID"`
}

type HyperPod struct {
	PodID   string
	PodName string
	VmName  string
	Status  string
	PodInfo *grpctypes.PodInfo
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

var (
	ErrConnectionRefused = errors.New("Cannot connect to the Hyper daemon. Is 'hyperd' running on this host?")
)

func (cli *HyperClient) encodeData(data string) (*bytes.Buffer, error) {
	params := bytes.NewBuffer(nil)
	if data != "" {
		if _, err := params.Write([]byte(data)); err != nil {
			return nil, err
		}
	}
	return params, nil
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

func (cli *HyperClient) clientRequest(method, path string, in io.Reader, headers map[string][]string) (io.ReadCloser, string, int, *net.Conn, *httputil.ClientConn, error) {
	expectedPayload := (method == "POST" || method == "PUT")
	if expectedPayload && in == nil {
		in = bytes.NewReader([]byte{})
	}
	req, err := http.NewRequest(method, path, in)
	if err != nil {
		return nil, "", -1, nil, nil, err
	}
	req.Header.Set("User-Agent", "kubelet")
	req.URL.Host = cli.addr
	req.URL.Scheme = cli.scheme

	if headers != nil {
		for k, v := range headers {
			req.Header[k] = v
		}
	}

	if expectedPayload && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "text/plain")
	}

	var dial net.Conn
	dial, err = net.DialTimeout(HYPER_PROTO, HYPER_ADDR, 32*time.Second)
	if err != nil {
		return nil, "", -1, nil, nil, err
	}

	clientconn := httputil.NewClientConn(dial, nil)
	resp, err := clientconn.Do(req)
	statusCode := -1
	if resp != nil {
		statusCode = resp.StatusCode
	}
	if err != nil {
		if strings.Contains(err.Error(), "connection refused") {
			return nil, "", statusCode, &dial, clientconn, ErrConnectionRefused
		}

		return nil, "", statusCode, &dial, clientconn, fmt.Errorf("An error occurred trying to connect: %v", err)
	}

	if statusCode < 200 || statusCode >= 400 {
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return nil, "", statusCode, &dial, clientconn, err
		}
		if len(body) == 0 {
			return nil, "", statusCode, nil, nil, fmt.Errorf("Error: request returned %s for API route and version %s, check if the server supports the requested API version", http.StatusText(statusCode), req.URL)
		}

		return nil, "", statusCode, &dial, clientconn, fmt.Errorf("%s", bytes.TrimSpace(body))
	}

	return resp.Body, resp.Header.Get("Content-Type"), statusCode, &dial, clientconn, nil
}

func (cli *HyperClient) call(method, path string, data string, headers map[string][]string) ([]byte, int, error) {
	params, err := cli.encodeData(data)
	if err != nil {
		return nil, -1, err
	}

	if data != "" {
		if headers == nil {
			headers = make(map[string][]string)
		}
		headers["Content-Type"] = []string{"application/json"}
	}

	body, _, statusCode, dial, clientconn, err := cli.clientRequest(method, path, params, headers)
	if dial != nil {
		defer (*dial).Close()
	}
	if clientconn != nil {
		defer clientconn.Close()
	}
	if err != nil {
		return nil, statusCode, err
	}

	if body == nil {
		return nil, statusCode, err
	}

	defer body.Close()

	result, err := ioutil.ReadAll(body)
	if err != nil {
		return nil, -1, err
	}

	return result, statusCode, nil
}

func (cli *HyperClient) stream(method, path string, in io.Reader, out io.Writer, headers map[string][]string) error {
	body, _, _, dial, clientconn, err := cli.clientRequest(method, path, in, headers)
	if dial != nil {
		defer (*dial).Close()
	}
	if clientconn != nil {
		defer clientconn.Close()
	}
	if err != nil {
		return err
	}

	defer body.Close()

	if out != nil {
		_, err := io.Copy(out, body)
		return err
	}

	return nil

}

func (client *HyperClient) Version() (string, error) {
	request := grpctypes.VersionRequest{}

	response, err := client.client.Version(context.Background(), &request)
	if err != nil {
		return "", err
	}

	return response.Version, nil
}

func (client *HyperClient) GetPodIDByName(podName string) (string, error) {
	request := grpctypes.PodListRequest{}

	response, err := client.client.PodList(context.Background(), &request)
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

func (client *HyperClient) ListPods() ([]HyperPod, error) {
	request := grpctypes.PodListRequest{}

	response, err := client.client.PodList(context.Background(), &request)
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

		res, err := client.client.PodInfo(context.Background(), &req)
		if err != nil {
			return nil, err
		}

		hyperPod.PodInfo = res.PodInfo

		result = append(result, hyperPod)
	}

	return result, nil
}

func (client *HyperClient) ListContainers() ([]HyperContainer, error) {
	request := grpctypes.ContainerListRequest{}

	response, err := client.client.ContainerList(context.Background(), &request)
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

func (client *HyperClient) Info() (*grpctypes.InfoResponse, error) {
	request := grpctypes.InfoRequest{}
	response, err := client.client.Info(context.Background(), &request)
	if err != nil {
		return nil, err
	}

	return response, nil
}

func (client *HyperClient) ListImages() ([]HyperImage, error) {
	request := grpctypes.ImageListRequest{
		All: false,
	}

	response, err := client.client.ImageList(context.Background(), &request)
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

func (client *HyperClient) RemoveImage(imageID string) error {
	request := grpctypes.ImageRemoveRequest{
		Image: imageID,
	}

	_, err := client.client.ImageRemove(context.Background(), &request)
	if err != nil {
		return err
	}

	return nil
}

func (client *HyperClient) RemovePod(podID string) error {
	request := grpctypes.PodRemoveRequest{
		PodID: podID,
	}
	_, err := client.client.PodRemove(context.Background(), &request)
	if err != nil {
		return err
	}

	return nil
}

func (client *HyperClient) StartPod(podID string) error {
	stream, err := client.client.PodStart(context.Background())
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

func (client *HyperClient) StopPod(podID string) error {
	request := grpctypes.PodStopRequest{
		PodID: podID,
	}
	_, err := client.client.PodStop(context.Background(), &request)
	if err != nil {
		return err
	}

	return nil
}

func (client *HyperClient) PullImage(image string, credential string) error {
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

	request := grpctypes.ImagePullRequest{
		Image: imageName,
		Tag:   tag,
		Auth:  authConfig,
	}
	stream, err := client.client.ImagePull(context.Background(), &request)
	if err != nil {
		return err
	}

	for {
		_, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}

	return nil
}

func (client *HyperClient) CreatePod(podSpec *grpctypes.UserPod) (string, error) {
	request := grpctypes.PodCreateRequest{
		PodSpec: podSpec,
	}
	response, err := client.client.PodCreate(context.Background(), &request)
	if err != nil {
		return "", err
	}

	return response.PodID, nil
}

func (c *HyperClient) GetExitCode(container, tag string) error {
	v := url.Values{}
	v.Set("container", container)
	v.Set(KEY_TAG, tag)
	code := -1

	body, _, err := c.call("GET", "/exitcode?"+v.Encode(), "", nil)
	if err != nil {
		return err
	}

	err = json.Unmarshal(body, &code)
	if err != nil {
		return err
	}

	if code != 0 {
		return fmt.Errorf("Exit code %d", code)
	}

	return nil
}

func (c *HyperClient) GetTag() string {
	dictionary := "0123456789abcdefghijklmnopqrstuvwxyz"

	var bytes = make([]byte, 8)
	rand.Read(bytes)
	for k, v := range bytes {
		bytes[k] = dictionary[v%byte(len(dictionary))]
	}
	return string(bytes)
}

func (c *HyperClient) hijack(method, path string, hijackOptions hijackOptions) error {
	var params io.Reader
	if hijackOptions.data != nil {
		buf, err := json.Marshal(hijackOptions.data)
		if err != nil {
			return err
		}
		params = bytes.NewBuffer(buf)
	}

	if hijackOptions.tty {
		in, isTerm := term.GetFdInfo(hijackOptions.in)
		if isTerm {
			state, err := term.SetRawTerminal(in)
			if err != nil {
				return err
			}

			defer term.RestoreTerminal(in, state)
		}
	}
	req, err := http.NewRequest(method, fmt.Sprintf("/v%s%s", hyperMinimumVersion, path), params)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", "kubelet")
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "tcp")
	req.Host = HYPER_ADDR

	dial, err := net.Dial(HYPER_PROTO, HYPER_ADDR)
	if err != nil {
		return err
	}

	clientconn := httputil.NewClientConn(dial, nil)
	defer clientconn.Close()

	clientconn.Do(req)

	rwc, br := clientconn.Hijack()
	defer rwc.Close()

	errChanOut := make(chan error, 1)
	errChanIn := make(chan error, 1)
	exit := make(chan bool)

	if hijackOptions.stdout == nil && hijackOptions.stderr == nil {
		close(errChanOut)
	}
	if hijackOptions.stdout == nil {
		hijackOptions.stdout = ioutil.Discard
	}
	if hijackOptions.stderr == nil {
		hijackOptions.stderr = ioutil.Discard
	}

	go func() {
		defer func() {
			close(errChanOut)
			close(exit)
		}()

		_, err := io.Copy(hijackOptions.stdout, br)
		errChanOut <- err
	}()

	go func() {
		defer close(errChanIn)

		if hijackOptions.in != nil {
			_, err := io.Copy(rwc, hijackOptions.in)
			errChanIn <- err

			rwc.(interface {
				CloseWrite() error
			}).CloseWrite()
		}
	}()

	<-exit
	select {
	case err = <-errChanIn:
		return err
	case err = <-errChanOut:
		return err
	}

	return nil
}

func (client *HyperClient) Attach(opts AttachToContainerOptions) error {
	if opts.Container == "" {
		return fmt.Errorf("No Such Container %s", opts.Container)
	}

	tag := client.GetTag()
	v := url.Values{}
	v.Set(KEY_TYPE, TYPE_CONTAINER)
	v.Set(KEY_VALUE, opts.Container)
	v.Set(KEY_TAG, tag)
	path := "/attach?" + v.Encode()
	err := client.hijack("POST", path, hijackOptions{
		in:     opts.InputStream,
		stdout: opts.OutputStream,
		stderr: opts.ErrorStream,
		tty:    opts.TTY,
	})

	if err != nil {
		return err
	}

	return client.GetExitCode(opts.Container, tag)
}

func (client *HyperClient) Exec(opts ExecInContainerOptions) error {
	if opts.Container == "" {
		return fmt.Errorf("No Such Container %s", opts.Container)
	}

	createRequest := grpctypes.ExecCreateRequest{
		ContainerID: opts.Container,
		Command:     opts.Commands,
		Tty:         opts.TTY,
	}

	createResponse, err := client.client.ExecCreate(context.Background(), &createRequest)
	if err != nil {
		return err
	}

	execId := createResponse.ExecID

	stream, err := client.client.ExecStart(context.Background())
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

func (client *HyperClient) ContainerLogs(opts ContainerLogsOptions) error {
	request := grpctypes.ContainerLogsRequest{
		Container:  opts.Container,
		Follow:     opts.Follow,
		Timestamps: opts.Timestamps,
		Tail:       fmt.Sprintf("%d", opts.TailLines),
		Since:      fmt.Sprintf("%d", opts.Since),
		Stdout:     true,
		Stderr:     true,
	}

	stream, err := client.client.ContainerLogs(context.Background(), &request)
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
	if outputs, err := client.ListImages(); err == nil {
		for _, imgInfo := range outputs {
			if imgInfo.repository == repo && imgInfo.tag == tag {
				return true, nil
			}
		}
	}
	return false, nil
}

func (client *HyperClient) ListServices(podId string) ([]*grpctypes.UserService, error) {
	request := grpctypes.ServiceListRequest{
		PodID: podId,
	}

	response, err := client.client.ServiceList(context.Background(), &request)
	if err != nil {
		if strings.Contains(err.Error(), "doesn't have services discovery") {
			return nil, nil
		} else {
			return nil, err
		}
	}

	return response.Services, nil
}

func (client *HyperClient) UpdateServices(podId string, services []*grpctypes.UserService) error {
	request := grpctypes.ServiceUpdateRequest{
		PodID:    podId,
		Services: services,
	}

	_, err := client.client.ServiceUpdate(context.Background(), &request)
	if err != nil {
		return err
	}

	return nil
}

func (client *HyperClient) UpdatePodLabels(podId string, labels map[string]string) error {
	request := grpctypes.PodLabelsRequest{
		PodID:    podId,
		Override: true,
		Labels:   labels,
	}

	_, err := client.client.SetPodLabels(context.Background(), &request)
	if err != nil {
		return err
	}

	return nil
}
