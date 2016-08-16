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

package hyper

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"mime"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"

	"github.com/golang/glog"
	"time"
)

const (
	HYPER_PROTO       = "unix"
	HYPER_ADDR        = "/var/run/hyper.sock"
	HYPER_SCHEME      = "http"
	HYPER_MINVERSION  = "0.4.0"
	DEFAULT_IMAGE_TAG = "latest"

	KEY_ID             = "id"
	KEY_IMAGEID        = "imageId"
	KEY_IMAGENAME      = "imageName"
	KEY_ITEM           = "item"
	KEY_DNS            = "dns"
	KEY_MEMORY         = "memory"
	KEY_POD_ID         = "podId"
	KEY_POD_NAME       = "podName"
	KEY_RESOURCE       = "resource"
	KEY_VCPU           = "vcpu"
	KEY_TTY            = "tty"
	KEY_TYPE           = "type"
	KEY_VALUE          = "value"
	KEY_NAME           = "name"
	KEY_IMAGE          = "image"
	KEY_VOLUMES        = "volumes"
	KEY_CONTAINERS     = "containers"
	KEY_VOLUME_SOURCE  = "source"
	KEY_VOLUME_DRIVE   = "driver"
	KEY_ENVS           = "envs"
	KEY_CONTAINER_PORT = "containerPort"
	KEY_HOST_PORT      = "hostPort"
	KEY_PROTOCOL       = "protocol"
	KEY_PORTS          = "ports"
	KEY_MOUNTPATH      = "path"
	KEY_READONLY       = "readOnly"
	KEY_VOLUME         = "volume"
	KEY_COMMAND        = "command"
	KEY_WORKDIR        = "workdir"
	KEY_VM             = "vm"
	VOLUME_TYPE_VFS    = "vfs"
	TYPE_CONTAINER     = "container"
	TYPE_POD           = "pod"
)

type HyperClient struct {
	proto  string
	addr   string
	scheme string
}

type AttachToContainerOptions struct {
	Container    string
	InputStream  io.Reader
	OutputStream io.Writer
	ErrorStream  io.Writer
}

type ExecInContainerOptions struct {
	Container    string
	InputStream  io.Reader
	OutputStream io.Writer
	ErrorStream  io.Writer
	Commands     []string
}

type hijackOptions struct {
	in     io.Reader
	stdout io.Writer
	stderr io.Writer
	data   interface{}
}

func NewHyperClient() *HyperClient {
	var (
		scheme = HYPER_SCHEME
		proto  = HYPER_PROTO
		addr   = HYPER_ADDR
	)

	return &HyperClient{
		proto:  proto,
		addr:   addr,
		scheme: scheme,
	}
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

func MatchesContentType(contentType, expectedType string) bool {
	mimetype, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		glog.V(4).Infof("Error parsing media type: %s error: %v", contentType, err)
	}
	return err == nil && mimetype == expectedType
}

func (client *HyperClient) Version() (string, error) {
	body, _, err := client.call("GET", "/version", "", nil)
	if err != nil {
		return "", err
	}

	var info map[string]interface{}
	err = json.Unmarshal(body, &info)
	if err != nil {
		return "", err
	}

	version, ok := info["Version"]
	if !ok {
		return "", fmt.Errorf("Can not get hyper version")
	}

	return version.(string), nil
}

func (client *HyperClient) ListPods() ([]HyperPod, error) {
	return client.ListPodsByVM("")
}

func (client *HyperClient) ListPodsByVM(vm string) ([]HyperPod, error) {
	v := url.Values{}
	v.Set(KEY_ITEM, TYPE_POD)
	if vm != "" {
		v.Set(KEY_VM, vm)
	}
	body, _, err := client.call("GET", "/list?"+v.Encode(), "", nil)
	if err != nil {
		return nil, err
	}

	var podList map[string]interface{}
	err = json.Unmarshal(body, &podList)
	if err != nil {
		return nil, err
	}

	var result []HyperPod
	for _, pod := range podList["podData"].([]interface{}) {
		fields := strings.Split(pod.(string), ":")
		var hyperPod HyperPod
		hyperPod.PodID = fields[0]
		hyperPod.PodName = fields[1]
		hyperPod.VmName = fields[2]
		hyperPod.Status = fields[3]

		values := url.Values{}
		values.Set(KEY_POD_NAME, hyperPod.PodID)
		body, _, err = client.call("GET", "/pod/info?"+values.Encode(), "", nil)
		if err != nil {
			return nil, err
		}

		err = json.Unmarshal(body, &hyperPod.PodInfo)
		if err != nil {
			return nil, err
		}

		result = append(result, hyperPod)
	}

	return result, nil
}

func (client *HyperClient) GetContainer(name string) (*Container, error) {
	values := url.Values{}
	values.Set("container", name)

	body, _, err := client.call("GET", "/container/info?"+values.Encode(), "", nil)
	if err != nil {
		return nil, err
	}

	var result Container
	err = json.Unmarshal(body, &result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

func (client *HyperClient) GetPod(name string) (*PodInfo, error) {
	values := url.Values{}
	values.Set(KEY_POD_NAME, name)

	body, _, err := client.call("GET", "/pod/info?"+values.Encode(), "", nil)
	if err != nil {
		return nil, err
	}

	var result PodInfo
	err = json.Unmarshal(body, &result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

func (client *HyperClient) ListContainers() ([]HyperContainer, error) {
	v := url.Values{}
	v.Set(KEY_ITEM, TYPE_CONTAINER)
	body, _, err := client.call("GET", "/list?"+v.Encode(), "", nil)
	if err != nil {
		return nil, err
	}

	var containerList map[string]interface{}
	err = json.Unmarshal(body, &containerList)
	if err != nil {
		return nil, err
	}

	var result []HyperContainer
	for _, container := range containerList["cData"].([]interface{}) {
		fields := strings.Split(container.(string), ":")
		var h HyperContainer
		h.ContainerID = fields[0]
		if len(fields[1]) < 1 {
			return nil, errors.New("Hyper container name not resolved")
		}
		h.Name = fields[1][1:]
		h.PodID = fields[2]
		h.Status = fields[3]

		result = append(result, h)
	}

	return result, nil
}

func (client *HyperClient) Info() (map[string]interface{}, error) {
	body, _, err := client.call("GET", "/info", "", nil)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		return nil, err
	}

	return result, nil
}

func (client *HyperClient) ListImages() ([]HyperImage, error) {
	v := url.Values{}
	v.Set("all", "no")
	body, _, err := client.call("GET", "/images/get?"+v.Encode(), "", nil)
	if err != nil {
		return nil, err
	}

	var images map[string][]string
	err = json.Unmarshal(body, &images)
	if err != nil {
		return nil, err
	}

	var hyperImages []HyperImage
	for _, image := range images["imagesList"] {
		imageDesc := strings.Split(image, ":")
		if len(imageDesc) != 5 {
			glog.Warning("Hyper: can not parse image info")
			return nil, fmt.Errorf("Hyper: can not parse image info")
		}

		var imageHyper HyperImage
		imageHyper.Repository = imageDesc[0]
		imageHyper.Tag = imageDesc[1]
		imageHyper.ImageID = imageDesc[2]

		createdAt, err := strconv.ParseInt(imageDesc[3], 10, 0)
		if err != nil {
			return nil, err
		}
		imageHyper.CreatedAt = createdAt

		virtualSize, err := strconv.ParseInt(imageDesc[4], 10, 0)
		if err != nil {
			return nil, err
		}
		imageHyper.VirtualSize = virtualSize

		hyperImages = append(hyperImages, imageHyper)
	}

	return hyperImages, nil
}
