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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/golang/glog"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/unversioned"
	clientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
	"k8s.io/kubernetes/pkg/client/record"
	"k8s.io/kubernetes/pkg/credentialprovider"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet/container"
	grpctypes "k8s.io/kubernetes/pkg/kubelet/hyper/types"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
	"k8s.io/kubernetes/pkg/kubelet/network"
	proberesults "k8s.io/kubernetes/pkg/kubelet/prober/results"
	kubetypes "k8s.io/kubernetes/pkg/kubelet/types"
	"k8s.io/kubernetes/pkg/types"
	"k8s.io/kubernetes/pkg/util"
	utilexec "k8s.io/kubernetes/pkg/util/exec"
	"k8s.io/kubernetes/pkg/util/flowcontrol"
	utilruntime "k8s.io/kubernetes/pkg/util/runtime"
	"k8s.io/kubernetes/pkg/util/wait"
)

const (
	hyperMinimumVersion = "0.6.0"

	typeHyper                   = "hyper"
	hyperContainerNamePrefix    = "kube"
	hyperDefaultContainerCPU    = 1
	hyperDefaultContainerMem    = 128
	hyperHostnameMaxLen         = 63
	hyperPodHostNameLabel       = "cloud.sh.hyper.pod.hostname"
	hyperPodSpecDir             = "/var/lib/kubelet/hyper"
	hyperLogsDir                = "/var/run/hyper/Pods"
	minimumGracePeriodInSeconds = 2

	// port-mapping related labels
	whitelistInternalNetsNum   = "cloud.sh.hyper.internalNets"
	whitelistInternalNeti      = "cloud.sh.hyper.internalNets.%d"
	whitelistExternalNetsNum   = "cloud.sh.hyper.externalNets"
	whitelistExternalNeti      = "cloud.sh.hyper.externalNets.%d"
	portmappingsNum            = "cloud.sh.hyper.portmappings"
	portmappingsOwneri         = "cloud.sh.hyper.portmappings.%d.owner"
	portmappingsContainerPorti = "cloud.sh.hyper.portmappings.%d.container"
	portmappingsHostPorti      = "cloud.sh.hyper.portmappings.%d.host"
)

// runtime implements the container runtime for hyper
type runtime struct {
	dockerKeyring       credentialprovider.DockerKeyring
	containerLogsDir    string
	containerRefManager *kubecontainer.RefManager
	runtimeHelper       kubecontainer.RuntimeHelper
	recorder            record.EventRecorder
	livenessManager     proberesults.Manager
	networkPlugin       network.NetworkPlugin
	hyperClient         *HyperClient
	kubeClient          clientset.Interface
	imagePuller         kubecontainer.ImagePuller
	os                  kubecontainer.OSInterface
	version             kubecontainer.Version

	// Disable the internal haproxy service in Hyper pods
	disableHyperInternalService bool

	// Runner of lifecycle events.
	runner kubecontainer.HandlerRunner
}

var _ kubecontainer.Runtime = &runtime{}

// New creates the hyper container runtime which implements the container runtime interface.
func New(runtimeHelper kubecontainer.RuntimeHelper,
	recorder record.EventRecorder,
	networkPlugin network.NetworkPlugin,
	containerRefManager *kubecontainer.RefManager,
	livenessManager proberesults.Manager,
	kubeClient clientset.Interface,
	imageBackOff *flowcontrol.Backoff,
	serializeImagePulls bool,
	httpClient kubetypes.HttpGetter,
	disableHyperInternalService bool,
	containerLogsDir string,
	os kubecontainer.OSInterface,
) (kubecontainer.Runtime, error) {

	hyperClient, err := NewHyperClient()
	if err != nil {
		return nil, err
	}

	hyper := &runtime{
		dockerKeyring:               credentialprovider.NewDockerKeyring(),
		containerLogsDir:            containerLogsDir,
		containerRefManager:         containerRefManager,
		runtimeHelper:               runtimeHelper,
		livenessManager:             livenessManager,
		os:                          os,
		recorder:                    recorder,
		networkPlugin:               networkPlugin,
		hyperClient:                 hyperClient,
		kubeClient:                  kubeClient,
		disableHyperInternalService: disableHyperInternalService,
	}

	if serializeImagePulls {
		hyper.imagePuller = kubecontainer.NewSerializedImagePuller(recorder, hyper, imageBackOff)
	} else {
		hyper.imagePuller = kubecontainer.NewImagePuller(recorder, hyper, imageBackOff)
	}

	version, err := hyper.hyperClient.Version()
	if err != nil {
		return nil, fmt.Errorf("cannot get hyper version: %v", err)
	}

	hyperVersion, err := parseVersion(version)
	if err != nil {
		return nil, fmt.Errorf("cannot get hyper version: %v", err)
	}

	hyper.version = hyperVersion

	hyper.runner = lifecycle.NewHandlerRunner(httpClient, hyper, hyper)

	// CleanupNetwork every second for stopped pods.
	go wait.Until(hyper.CleanupNetwork, time.Second, wait.NeverStop)

	return hyper, nil
}

// Version invokes 'hyper version' to get the version information of the hyper
// runtime on the machine.
// The return values are an int array containers the version number.
func (r *runtime) Version() (kubecontainer.Version, error) {
	return r.version, nil
}

// Type returns the name of the container runtime
func (r *runtime) Type() string {
	return "hyper"
}

func (r *runtime) GetPodContainerID(pod *kubecontainer.Pod) (kubecontainer.ContainerID, error) {
	return kubecontainer.ContainerID{ID: string(pod.ID)}, nil
}

func (r *runtime) Status() error {
	version, err := r.hyperClient.Version()
	if err != nil {
		return fmt.Errorf("cannot get hyper version: %v", err)
	}

	hyperVersion, err := parseVersion(version)
	if err != nil {
		return fmt.Errorf("cannot get hyper version: %v", err)
	}

	// Verify the hyper version.
	result, err := hyperVersion.Compare(hyperMinimumVersion)
	if err != nil {
		return fmt.Errorf("failed to compare current hyper version %v with minimum support version %q - %v", version, hyperMinimumVersion, err)
	}
	if result < 0 {
		return fmt.Errorf("Hyper container runtime version is older than %s", hyperMinimumVersion)
	}
	return nil
}

func (r *runtime) ImageStats() (*kubecontainer.ImageStats, error) {
	// TODO(harryz) this requires to calculate real disk usage based on images history api. Fake it to 1GB for now.
	return &kubecontainer.ImageStats{TotalStorageBytes: 1024 * 1024 * 1024}, nil
}

func parseTimeString(str string) (time.Time, error) {
	t := time.Date(0, 0, 0, 0, 0, 0, 0, time.Local)
	if str == "" {
		return t, nil
	}

	layout := "2006-01-02T15:04:05Z"
	t, err := time.Parse(layout, str)
	if err != nil {
		return t, err
	}

	return t, nil
}

func (r *runtime) getContainerStatus(container *grpctypes.ContainerStatus, image, imageID, startTime string, podLabels map[string]string) *kubecontainer.ContainerStatus {
	status := &kubecontainer.ContainerStatus{}

	_, _, _, containerName, restartCount, _, err := r.parseHyperContainerFullName(container.Name)
	if err != nil {
		return status
	}

	status.Name = containerName
	status.ID = kubecontainer.ContainerID{
		Type: typeHyper,
		ID:   container.ContainerID,
	}
	status.Image = image
	status.ImageID = imageID
	status.RestartCount = restartCount

	switch container.Phase {
	case StatusRunning:
		runningStartedAt, err := parseTimeString(container.Running.StartedAt)
		if err != nil {
			glog.Errorf("Hyper: can't parse runningStartedAt %s", container.Running.StartedAt)
			return status
		}

		// TDOO(harryz) no CreatedAt?
		status.State = kubecontainer.ContainerStateRunning
		status.StartedAt = runningStartedAt
	case StatusFailed, StatusSuccess:
		// TODO: ensure container.Terminated.StartedAt
		if container.Terminated.StartedAt == "" {
			status.StartedAt = time.Now().Add(-2 * time.Second)
		} else {
			terminatedStartedAt, err := parseTimeString(container.Terminated.StartedAt)
			if err != nil {
				glog.Errorf("Hyper: can't parse terminatedStartedAt %s", container.Terminated.StartedAt)
				return status
			}
			status.StartedAt = terminatedStartedAt
		}

		// TODO: ensure container.Terminated.FinishedAt
		if container.Terminated.FinishedAt == "" {
			status.FinishedAt = time.Now()
		} else {
			terminatedFinishedAt, err := parseTimeString(container.Terminated.FinishedAt)
			if err != nil {
				glog.Errorf("Hyper: can't parse terminatedFinishedAt %s", container.Terminated.FinishedAt)
				return status
			}

			status.FinishedAt = terminatedFinishedAt
		}

		var message string
		path := podLabels[containerName]
		if data, err := ioutil.ReadFile(path); err != nil {
			message = fmt.Sprintf("Error on reading termination-log %s: %v", path, err)
		} else {
			message = string(data)
		}

		status.Message = message
		status.State = kubecontainer.ContainerStateExited
		status.Reason = container.Terminated.Reason
		status.ExitCode = int(container.Terminated.ExitCode)
	default:
		if startTime == "" {
			status.StartedAt = time.Now().Add(-2 * time.Second)
		} else {
			startedAt, err := parseTimeString(startTime)
			if err != nil {
				glog.Errorf("Hyper: can't parse startTime %s", container.Terminated.StartedAt)
				return status
			}

			status.StartedAt = startedAt
		}

		status.FinishedAt = time.Now()
		status.State = kubecontainer.ContainerStateExited
		status.Reason = container.Waiting.Reason
		status.ExitCode = 0
	}

	return status
}

func (r *runtime) buildHyperContainerFullName(uid, podName, namespace, containerName string, restartCount int, container api.Container) string {
	return fmt.Sprintf("%s_%s_%s_%s_%s_%d_%s",
		hyperContainerNamePrefix,
		uid,
		podName,
		namespace,
		containerName,
		restartCount,
		strconv.FormatUint(kubecontainer.HashContainer(&container), 16))
}

func (r *runtime) parseHyperContainerFullName(containerName string) (string, string, string, string, int, string, error) {
	parts := strings.Split(containerName, "_")
	if len(parts) != 7 {
		return "", "", "", "", 0, "", fmt.Errorf("failed to parse the container full name %q", containerName)
	}

	restartCount, err := strconv.Atoi(parts[5])
	if err != nil {
		return "", "", "", "", 0, "", fmt.Errorf("failed to parse the container full name %q", containerName)
	}
	return parts[1], parts[2], parts[3], parts[4], restartCount, parts[6], nil
}

// GetPods returns a list containers group by pods. The boolean parameter
// specifies whether the runtime returns all containers including those already
// exited and dead containers (used for garbage collection).
func (r *runtime) GetPods(all bool) ([]*kubecontainer.Pod, error) {
	podInfos, err := r.hyperClient.ListPods()
	if err != nil {
		return nil, err
	}

	var kubepods []*kubecontainer.Pod
	for _, podInfo := range podInfos {
		var pod kubecontainer.Pod
		var containers []*kubecontainer.Container

		glog.V(3).Infof("Hyper: GetPods got pod %s with status: %s", podInfo.PodName, podInfo.Status)

		if !all && podInfo.Status != StatusRunning {
			continue
		}

		podID := podInfo.PodInfo.Spec.Labels[KEY_API_POD_UID]
		podName, podNamespace, err := kubecontainer.ParsePodFullName(podInfo.PodName)
		if err != nil {
			glog.V(5).Infof("Hyper: pod %s is not managed by kubelet", podInfo.PodName)
			continue
		}

		pod.ID = types.UID(podID)
		pod.Name = podName
		pod.Namespace = podNamespace

		for _, cinfo := range podInfo.PodInfo.Spec.Containers {
			var container kubecontainer.Container
			container.ID = kubecontainer.ContainerID{Type: typeHyper, ID: cinfo.ContainerID}
			container.Image = cinfo.Image

			for _, cstatus := range podInfo.PodInfo.Status.ContainerStatus {
				if cstatus.ContainerID == cinfo.ContainerID {
					switch cstatus.Phase {
					case StatusRunning:
						container.State = kubecontainer.ContainerStateRunning
					default:
						container.State = kubecontainer.ContainerStateExited
					}

					// harryz: container.Created is moved to ContainerStatus
					// createAt, err := parseTimeString(cstatus.Running.StartedAt)
					// if err == nil {
					// 	container.Created = createAt.Unix()
					// }
				}
			}

			_, _, _, containerName, _, containerHash, err := r.parseHyperContainerFullName(cinfo.Name)
			if err != nil {
				glog.V(5).Infof("Hyper: container %s is not managed by kubelet", cinfo.Name)
				continue
			}
			container.Name = containerName

			hash, err := strconv.ParseUint(containerHash, 16, 64)
			if err == nil {
				container.Hash = hash
			}

			containers = append(containers, &container)
		}
		pod.Containers = containers

		kubepods = append(kubepods, &pod)
	}

	return kubepods, nil
}

func (r *runtime) buildHyperPodServices(pod *api.Pod) []grpctypes.UserService {
	items, err := r.kubeClient.Core().Services(pod.Namespace).List(api.ListOptions{})
	if err != nil {
		glog.Warningf("Get services failed: %v", err)
		return nil
	}

	var services []grpctypes.UserService
	for _, svc := range items.Items {
		hyperService := grpctypes.UserService{
			ServiceIP: svc.Spec.ClusterIP,
		}
		endpoints, _ := r.kubeClient.Core().Endpoints(pod.Namespace).Get(svc.Name)
		for _, svcPort := range svc.Spec.Ports {
			hyperService.ServicePort = svcPort.Port
			for _, ep := range endpoints.Subsets {
				for _, epPort := range ep.Ports {
					if svcPort.Name == "" || svcPort.Name == epPort.Name {
						for _, eh := range ep.Addresses {
							hyperService.Hosts = append(hyperService.Hosts, &grpctypes.UserServiceBackend{
								HostIP:   eh.IP,
								HostPort: epPort.Port,
							})
						}
					}
				}
			}
			services = append(services, hyperService)
		}
	}

	return services
}

func (r *runtime) getPodHostname(pod *api.Pod) string {
	podHostname := pod.Name
	if hn, ok := pod.Labels[hyperPodHostNameLabel]; ok {
		podHostname = hn
	}

	// Cap hostname at 63 chars (specification is 64bytes which is 63 chars and the null terminating char).
	if len(podHostname) > hyperHostnameMaxLen {
		podHostname = podHostname[:hyperHostnameMaxLen]
	}

	return podHostname
}

type localPortMapping struct {
	// Protocol of the port mapping.
	Protocol string
	// The port number within the container.
	ContainerPort int
	// The port number on the host.
	HostPort int
	Owner    string
}

type portMappingFromLabel struct {
	internalNets []string
	externalNets []string
	portmappings []localPortMapping
}

func (r *runtime) parsePortMappings(labels map[string]string) *portMappingFromLabel {
	glog.V(3).Infof("Parsing pod labels for port mappings: %q", labels)
	internalNetsCount := 0
	externalNetsCount := 0
	portMappingsCount := 0
	result := &portMappingFromLabel{
		internalNets: make([]string, 0),
		externalNets: make([]string, 0),
		portmappings: make([]localPortMapping, 0),
	}

	if v, ok := labels[whitelistInternalNetsNum]; ok {
		if count, err := strconv.Atoi(v); err == nil {
			internalNetsCount = count
		}
	}
	if v, ok := labels[whitelistExternalNetsNum]; ok {
		if count, err := strconv.Atoi(v); err == nil {
			externalNetsCount = count
		}
	}
	if v, ok := labels[portmappingsNum]; ok {
		if count, err := strconv.Atoi(v); err == nil {
			portMappingsCount = count
		}
	}

	glog.Infof("Got portmappings internalNetsCount: %d, externalNetsCount:%d whiteNets count: %d",
		internalNetsCount, externalNetsCount, internalNetsCount)

	for i := 0; i < internalNetsCount; i++ {
		whiteCIDRKey := fmt.Sprintf(whitelistInternalNeti, i)
		whiteCIDR, ok := labels[whiteCIDRKey]
		if !ok {
			glog.Errorf("Can not find label key %s", whiteCIDRKey)
			return nil
		}
		cidr := strings.Replace(whiteCIDR, "_", "/", -1)
		result.internalNets = append(result.internalNets, cidr)
	}

	for i := 0; i < externalNetsCount; i++ {
		whiteCIDRKey := fmt.Sprintf(whitelistExternalNeti, i)
		whiteCIDR, ok := labels[whiteCIDRKey]
		if !ok {
			glog.Errorf("Can not find label key %s", whiteCIDRKey)
			return nil
		}
		cidr := strings.Replace(whiteCIDR, "_", "/", -1)
		result.externalNets = append(result.externalNets, cidr)
	}

	for i := 0; i < portMappingsCount; i++ {
		owner := ""
		protocol := "tcp"
		containerPort := -1
		hostports := make([]int, 0)

		ownerKey := fmt.Sprintf(portmappingsOwneri, i)
		if v, ok := labels[ownerKey]; ok {
			owner = v
		}
		if owner == "" {
			glog.Errorf("Can not find label key: %s", ownerKey)
			return nil
		}

		containerPortKey := fmt.Sprintf(portmappingsContainerPorti, i)
		if v, ok := labels[containerPortKey]; ok {
			parts := strings.Split(v, "_")
			glog.V(3).Infof("Got container port %v", parts)
			if len(parts) != 2 {
				glog.Errorf("Container port %s is not in format 'port_protocol'", v)
				return nil
			}
			protocol = parts[1]
			if port, err := strconv.Atoi(parts[0]); err == nil {
				containerPort = port
			}
		}
		if containerPort == -1 {
			glog.Errorf("Can not find label key: %s", containerPortKey)
			return nil
		}

		hostPortKey := fmt.Sprintf(portmappingsHostPorti, i)
		if value, ok := labels[hostPortKey]; ok {
			parts := strings.Split(value, "_")
			for _, v := range parts {
				if strings.Contains(v, "-") {
					parts := strings.Split(v, "-")
					if len(parts) != 2 {
						return nil
					}
					start, err := strconv.Atoi(parts[0])
					if err != nil {
						return nil
					}
					end, err := strconv.Atoi(parts[1])
					if err != nil {
						return nil
					}
					for i := start; i <= end; i++ {
						hostports = append(hostports, i)
					}
				} else {
					hostPort := -1
					if port, err := strconv.Atoi(v); err == nil {
						hostPort = port
					}
					if hostPort == -1 {
						return nil
					}
					hostports = append(hostports, hostPort)
				}
			}
		}

		if len(hostports) == 0 {
			return nil
		}

		for _, hp := range hostports {
			result.portmappings = append(result.portmappings, localPortMapping{
				Owner:         owner,
				ContainerPort: containerPort,
				HostPort:      hp,
				Protocol:      protocol,
			})
		}
	}

	glog.V(3).Infof("Got port mappings from labels: %v", result)
	return result
}

func (r *runtime) buildHyperPod(pod *api.Pod, restartCount int, pullSecrets []api.Secret) ([]byte, error) {
	// check and pull image
	for _, c := range pod.Spec.Containers {
		if err, _ := r.imagePuller.PullImage(pod, &c, pullSecrets); err != nil {
			return nil, err
		}
	}

	// build hyper volume spec
	specMap := make(map[string]interface{})

	volumes := make([]map[string]interface{}, 0, 1)
	volumeMap, found := r.runtimeHelper.ListVolumesForPod(pod.UID)
	if found {
		// process rbd volume globally
		for name, mounter := range volumeMap {
			glog.V(4).Infof("Hyper: volume %s, path %s, meta %s", name, mounter.GetPath(), mounter.GetMetaData())
			v := make(map[string]interface{})
			v[KEY_NAME] = name

			// Process rbd volume
			metadata := mounter.GetMetaData()
			if metadata != nil {
				glog.V(3).Infof("Hyper: volume %s type %v", name, metadata["volume_type"])
				if metadata["volume_type"].(string) == "rbd" {
					v[KEY_VOLUME_DRIVE] = metadata["volume_type"]
					v[KEY_VOLUME_SOURCE] = "rbd:" + metadata["name"].(string)
					monitors := make([]string, 0, 1)
					for _, host := range metadata["hosts"].([]interface{}) {
						for _, port := range metadata["ports"].([]interface{}) {
							monitors = append(monitors, fmt.Sprintf("%s:%s", host.(string), port.(string)))
						}
					}
					v["option"] = map[string]interface{}{
						"user":     metadata["auth_username"],
						"keyring":  metadata["keyring"],
						"monitors": monitors,
					}
				} else if metadata["volume_type"].(string) == "nfs" {
					v[KEY_VOLUME_DRIVE] = metadata["volume_type"]
					v[KEY_VOLUME_SOURCE] = metadata["source"].(string)
				}
			} else {
				glog.V(4).Infof("Hyper: volume %s %s", name, mounter.GetPath())

				v[KEY_VOLUME_DRIVE] = VOLUME_TYPE_VFS
				v[KEY_VOLUME_SOURCE] = mounter.GetPath()
			}

			volumes = append(volumes, v)
		}

		glog.V(4).Infof("Hyper volumes: %v", volumes)
	}

	if !r.disableHyperInternalService {
		services := r.buildHyperPodServices(pod)
		if services == nil {
			// services can't be null for kubernetes, so fake one if it is null
			services = []grpctypes.UserService{
				{
					ServiceIP:   "127.0.0.2",
					ServicePort: 65534,
				},
			}
		}
		specMap["services"] = services
	}

	// build hyper containers spec
	var containers []map[string]interface{}
	var k8sHostNeeded = true
	dnsServers := make(map[string]string)
	portMappings := r.parsePortMappings(pod.Labels)
	terminationMsgLabels := make(map[string]string)
	for _, container := range pod.Spec.Containers {
		c := make(map[string]interface{})
		c[KEY_NAME] = r.buildHyperContainerFullName(
			string(pod.UID),
			string(pod.Name),
			string(pod.Namespace),
			container.Name,
			restartCount,
			container)
		c[KEY_IMAGE] = container.Image
		c[KEY_TTY] = container.TTY

		if container.WorkingDir != "" {
			c[KEY_WORKDIR] = container.WorkingDir
		}

		opts, err := r.runtimeHelper.GenerateRunContainerOptions(pod, &container, "")
		if err != nil {
			return nil, err
		}

		command, args := kubecontainer.ExpandContainerCommandAndArgs(&container, opts.Envs)
		if len(command) > 0 {
			c[KEY_ENTRYPOINT] = command
		}
		if len(args) > 0 {
			c[KEY_COMMAND] = args
		}

		// dns
		for _, dns := range opts.DNS {
			dnsServers[dns] = dns
		}

		// envs
		envs := make([]map[string]string, 0, 1)
		for _, e := range opts.Envs {
			envs = append(envs, map[string]string{
				"env":   e.Name,
				"value": e.Value,
			})
		}
		c[KEY_ENVS] = envs

		// port-mappings
		var ports []map[string]interface{}
		if portMappings != nil {
			for _, v := range portMappings.portmappings {
				if strings.HasPrefix(container.Name, v.Owner) {
					p := make(map[string]interface{})
					p[KEY_CONTAINER_PORT] = v.ContainerPort
					p[KEY_HOST_PORT] = v.HostPort
					p[KEY_PROTOCOL] = v.Protocol
					ports = append(ports, p)
				}
			}
		} else {
			for _, mapping := range opts.PortMappings {
				p := make(map[string]interface{})
				p[KEY_CONTAINER_PORT] = mapping.ContainerPort
				if mapping.HostPort != 0 {
					p[KEY_HOST_PORT] = mapping.HostPort
				}
				p[KEY_PROTOCOL] = mapping.Protocol
				ports = append(ports, p)
			}
		}

		c[KEY_PORTS] = ports

		// NOTE: PodContainerDir is from TerminationMessagePath, TerminationMessagePath  is default to /dev/termination-log
		if opts.PodContainerDir != "" && container.TerminationMessagePath != "" {
			// In docker runtime, the container log path contains the container ID.
			// However, for hyper runtime, we cannot get the container ID before the
			// the container is launched, so here we generate a random uuid to enable
			// us to map a container's termination message path to an unique log file
			// on the disk.
			randomUID := util.NewUUID()
			containerLogPath := path.Join(opts.PodContainerDir, string(randomUID))
			fs, err := os.Create(containerLogPath)
			if err != nil {
				return nil, err
			}

			if err := fs.Close(); err != nil {
				return nil, err
			}
			mnt := &kubecontainer.Mount{
				// Use a random name for the termination message mount, so that
				// when a container restarts, it will not overwrite the old termination
				// message.
				Name:          fmt.Sprintf("termination-message-%s", randomUID),
				ContainerPath: container.TerminationMessagePath,
				HostPath:      containerLogPath,
				ReadOnly:      false,
			}
			opts.Mounts = append(opts.Mounts, *mnt)

			// set termination msg labels with host path
			terminationMsgLabels[container.Name] = mnt.HostPath
		}

		// volumes
		if len(opts.Mounts) > 0 {
			var containerVolumes []map[string]interface{}
			for _, volume := range opts.Mounts {
				v := make(map[string]interface{})
				v[KEY_MOUNTPATH] = volume.ContainerPath
				v[KEY_VOLUME] = volume.Name
				v[KEY_READONLY] = volume.ReadOnly
				containerVolumes = append(containerVolumes, v)

				if k8sHostNeeded {
					// Setup global hosts volume
					if volume.Name == "k8s-managed-etc-hosts" {
						k8sHostNeeded = false
						volumes = append(volumes, map[string]interface{}{
							KEY_NAME:          volume.Name,
							KEY_VOLUME_DRIVE:  VOLUME_TYPE_VFS,
							KEY_VOLUME_SOURCE: volume.HostPath,
						})
					}

					// Setup global termination msg volume
					if strings.HasPrefix(volume.Name, "termination-message") {
						k8sHostNeeded = false

						volumes = append(volumes, map[string]interface{}{
							KEY_NAME:          volume.Name,
							KEY_VOLUME_DRIVE:  VOLUME_TYPE_VFS,
							KEY_VOLUME_SOURCE: volume.HostPath,
						})
					}
				}
			}
			c[KEY_VOLUMES] = containerVolumes
		}

		containers = append(containers, c)
	}
	specMap[KEY_CONTAINERS] = containers
	specMap[KEY_VOLUMES] = volumes
	if portMappings != nil {
		portMappingWhiteList := make(map[string]interface{})
		if len(portMappings.externalNets) > 0 {
			portMappingWhiteList["externalNetworks"] = portMappings.externalNets
		}
		if len(portMappings.internalNets) > 0 {
			portMappingWhiteList["internalNetworks"] = portMappings.internalNets
		}
		specMap[KEY_WHITE_NETS] = portMappingWhiteList
	}

	// dns
	if len(dnsServers) > 0 {
		dns := []string{}
		for d := range dnsServers {
			dns = append(dns, d)
		}
		specMap[KEY_DNS] = dns
	}

	// build hyper pod resources spec
	var podCPULimit, podMemLimit int64
	var labels map[string]string
	podResource := make(map[string]int64)
	for _, container := range pod.Spec.Containers {
		resource := container.Resources.Limits
		var containerCPULimit, containerMemLimit int64
		for name, limit := range resource {
			switch name {
			case api.ResourceCPU:
				containerCPULimit = limit.MilliValue()
			case api.ResourceMemory:
				containerMemLimit = limit.MilliValue()
			}
		}
		if containerCPULimit == 0 {
			containerCPULimit = hyperDefaultContainerCPU
		}
		if containerMemLimit == 0 {
			containerMemLimit = hyperDefaultContainerMem * 1024 * 1024 * 1000
		}
		podCPULimit += containerCPULimit
		podMemLimit += containerMemLimit

		// generate heapster needed labels
		// TODO: keep these labels up to date if the pod changes
		labels = newLabels(&container, pod, restartCount, false)
	}

	podResource[KEY_VCPU] = (podCPULimit + 999) / 1000
	podResource[KEY_MEMORY] = ((podMemLimit) / 1000 / 1024) / 1024
	specMap[KEY_RESOURCE] = podResource
	glog.V(5).Infof("Hyper: pod limit vcpu=%v mem=%vMiB", podResource[KEY_VCPU], podResource[KEY_MEMORY])

	// Setup labels
	podLabels := map[string]string{KEY_API_POD_UID: string(pod.UID)}
	for k, v := range pod.Labels {
		podLabels[k] = v
	}
	// append heapster needed labels
	// NOTE(harryz): this only works for one pod one container model for now.
	for k, v := range labels {
		podLabels[k] = v
	}

	// append termination message label
	for k, v := range terminationMsgLabels {
		podLabels[k] = v
	}

	specMap[KEY_LABELS] = podLabels

	// other params required
	specMap[KEY_ID] = kubecontainer.BuildPodFullName(pod.Name, pod.Namespace)
	specMap[KEY_HOSTNAME] = r.getPodHostname(pod)

	podData, err := json.Marshal(specMap)
	if err != nil {
		return nil, err
	}

	return podData, nil
}

func (r *runtime) savePodSpec(spec, podFullName string) error {
	// ensure hyperPodSpecDir is created
	_, err := os.Stat(hyperPodSpecDir)
	if err != nil && os.IsNotExist(err) {
		e := os.MkdirAll(hyperPodSpecDir, 0755)
		if e != nil {
			return e
		}
	}

	// save spec to file
	specFileName := path.Join(hyperPodSpecDir, podFullName)
	err = ioutil.WriteFile(specFileName, []byte(spec), 0664)
	if err != nil {
		return err
	}

	return nil
}

func (r *runtime) getPodSpec(podFullName string) (string, error) {
	specFileName := path.Join(hyperPodSpecDir, podFullName)
	_, err := os.Stat(specFileName)
	if err != nil {
		return "", err
	}

	spec, err := ioutil.ReadFile(specFileName)
	if err != nil {
		return "", err
	}

	return string(spec), nil
}

func (r *runtime) GetPodRestartCount(podID string) (int, error) {
	containers, err := r.hyperClient.ListContainers()
	if err != nil {
		return 0, err
	}

	for _, c := range containers {
		if c.podID != podID {
			continue
		}

		_, _, _, _, restartCount, _, err := r.parseHyperContainerFullName(c.name)
		if err != nil {
			continue
		}

		return restartCount, nil
	}

	return 0, nil
}

func (r *runtime) RunPod(pod *api.Pod, restartCount int, pullSecrets []api.Secret) error {
	var (
		err         error
		podData     []byte
		podFullName string
		podID       string
		podStatus   *kubecontainer.PodStatus
	)

	podData, err = r.buildHyperPod(pod, restartCount, pullSecrets)
	if err != nil {
		glog.Errorf("Hyper: buildHyperPod failed, error: %v", err)
		return err
	}

	podFullName = kubecontainer.BuildPodFullName(pod.Name, pod.Namespace)
	err = r.savePodSpec(string(podData), podFullName)
	if err != nil {
		glog.Errorf("Hyper: savePodSpec failed, error: %v", err)
		return err
	}

	defer func() {
		if err != nil {
			specFileName := path.Join(hyperPodSpecDir, podFullName)
			_, err = os.Stat(specFileName)
			if err == nil {
				e := os.Remove(specFileName)
				if e != nil {
					glog.Warningf("Hyper: delete spec file for %s failed, error: %v", podFullName, e)
				}
			}

			if podID != "" {
				destroyErr := r.hyperClient.RemovePod(podID)
				if destroyErr != nil {
					glog.Errorf("Hyper: destory pod %s (ID:%s) failed: %v", pod.Name, podID, destroyErr)
				}
			}

			tearDownError := r.networkPlugin.TearDownPod(pod.Namespace, pod.Name, kubecontainer.ContainerID{}, "hyper")
			if tearDownError != nil {
				glog.Warningf("Hyper: networkPlugin.TearDownPod failed: %v, kubelet will continue to rm pod %s", tearDownError, pod.Name)
			}
		}
	}()

	// Setup pod's network by network plugin
	// TODO: Add hostname in network plugin interface
	hostname := r.getPodHostname(pod)
	err = r.networkPlugin.SetUpPod(pod.Namespace, pod.Name, kubecontainer.DockerID(hostname).ContainerID(), "hyper")
	if err != nil {
		glog.Errorf("Hyper: networkPlugin.SetUpPod %s failed, error: %v", pod.Name, err)
		return err
	}

	// Create and start hyper pod
	specData, err := r.getPodSpec(podFullName)
	if err != nil {
		glog.Errorf("Hyper: create pod %s failed, error: %v", podFullName, err)
		return err
	}

	var podSpec grpctypes.UserPod
	err = json.Unmarshal([]byte(specData), &podSpec)
	if err != nil {
		glog.Errorf("Hyper: marshal pod %s from specData error: %v", podFullName, err)
	}

	podID, err = r.hyperClient.CreatePod(&podSpec)
	if err != nil {
		glog.Errorf("Hyper: create pod %s failed, error: %v", podData, err)
		return err
	}

	err = r.hyperClient.StartPod(podID)
	if err != nil {
		glog.Errorf("Hyper: start pod %s (ID:%s) failed, error: %v", pod.Name, podID, err)
		return err
	}

	podStatus, err = r.GetPodStatus(pod.UID, pod.Name, pod.Namespace)
	if err != nil {
		return err
	}
	runningPod := kubecontainer.ConvertPodStatusToRunningPod(podStatus)

	for _, container := range pod.Spec.Containers {
		var containerID kubecontainer.ContainerID

		for _, runningContainer := range runningPod.Containers {
			if container.Name == runningContainer.Name {
				containerID = runningContainer.ID
			}
		}

		// Update container references
		ref, err := kubecontainer.GenerateContainerRef(pod, &container)
		if err != nil {
			glog.Errorf("Couldn't make a ref to pod %q, container %v: '%v'", pod.Name, container.Name, err)
		} else {
			r.containerRefManager.SetRef(containerID, ref)
		}

		// Create a symbolic link to the Hyper container log file using a name
		// which captures the full pod name, the container name and the
		// container ID. Cluster level logging will capture these symbolic
		// filenames which can be used for search terms in Elasticsearch or for
		// labels for Cloud Logging.
		containerLogFile := path.Join(hyperLogsDir, podID, fmt.Sprintf("%s-json.log", containerID.ID))
		symlinkFile := LogSymlink(r.containerLogsDir, podFullName, container.Name, containerID.ID)
		if err = r.os.Symlink(containerLogFile, symlinkFile); err != nil {
			glog.Errorf("Failed to create symbolic link to the log file of pod %q container %q: %v", podFullName, container.Name, err)
		}

		if container.Lifecycle != nil && container.Lifecycle.PostStart != nil {
			msg, handlerErr := r.runner.Run(containerID, pod, &container, container.Lifecycle.PostStart)
			if handlerErr != nil {
				err = fmt.Errorf("PostStart handler: %v, error msg is: %v", handlerErr, msg)
				if e := r.KillPod(pod, runningPod, nil); e != nil {
					glog.Errorf("KillPod %v failed: %v", podFullName, e)
				}
				return err
			}
		}
	}

	return nil
}

// Syncs the running pod into the desired pod.
func (r *runtime) SyncPod(pod *api.Pod, podStatus api.PodStatus, internalPodStatus *kubecontainer.PodStatus, pullSecrets []api.Secret, backOff *flowcontrol.Backoff) (result kubecontainer.PodSyncResult) {
	var err error
	defer func() {
		if err != nil {
			result.Fail(err)
		}
	}()

	// TODO: (random-liu) Stop using running pod in SyncPod()
	// TODO: (random-liu) Rename podStatus to apiPodStatus, rename internalPodStatus to podStatus, and use new pod status as much as possible,
	// we may stop using apiPodStatus someday.
	runningPod := kubecontainer.ConvertPodStatusToRunningPod(internalPodStatus)
	podFullName := kubecontainer.BuildPodFullName(pod.Name, pod.Namespace)

	// Add references to all containers.
	unidentifiedContainers := make(map[kubecontainer.ContainerID]*kubecontainer.Container)
	for _, c := range runningPod.Containers {
		unidentifiedContainers[c.ID] = c
	}

	restartPod := false
	for _, container := range pod.Spec.Containers {
		expectedHash := kubecontainer.HashContainer(&container)

		c := runningPod.FindContainerByName(container.Name)
		if c == nil {
			if kubecontainer.ShouldContainerBeRestarted(&container, pod, internalPodStatus) {
				glog.V(3).Infof("Container %+v is dead, but RestartPolicy says that we should restart it.", container)
				glog.Warningf("Got restart policy request of container %v, with pod status: %+v", container.Name, internalPodStatus)
				restartPod = true
				break
			}
			continue
		}

		containerChanged := c.Hash != 0 && c.Hash != expectedHash
		if containerChanged {
			glog.V(4).Infof("Pod %q container %q hash changed (%d vs %d), it will be killed and re-created.",
				podFullName, container.Name, c.Hash, expectedHash)
			restartPod = true
			break
		}

		liveness, found := r.livenessManager.Get(c.ID)
		if found && liveness != proberesults.Success && pod.Spec.RestartPolicy != api.RestartPolicyNever {
			glog.Infof("Pod %q container %q is unhealthy, it will be killed and re-created.", podFullName, container.Name)
			restartPod = true
			break
		}

		delete(unidentifiedContainers, c.ID)
	}

	// If there is any unidentified containers, restart the pod.
	if len(unidentifiedContainers) > 0 {
		restartPod = true
	}

	if restartPod {
		restartCount := 0
		// Only kill existing pod
		podID, err := r.hyperClient.GetPodIDByName(podFullName)
		if err == nil && len(podID) > 0 {
			// Update pod restart count
			restartCount, err = r.GetPodRestartCount(podID)
			if err != nil {
				glog.Errorf("Hyper: get pod startcount failed: %v", err)
				return
			}
			restartCount++

			if err = r.KillPod(pod, runningPod, nil); err != nil {
				glog.Errorf("Hyper: kill pod %s failed, error: %s", runningPod.Name, err)
				return
			}
		}

		if err := r.RunPod(pod, restartCount, pullSecrets); err != nil {
			glog.Errorf("Hyper: run pod %s failed, error: %s", pod.Name, err)
			return
		}
	}
	return
}

// KillPod kills all the containers of a pod.
func (r *runtime) KillPod(pod *api.Pod, runningPod kubecontainer.Pod, gracePeriodOverride *int64) error {
	var (
		podID        string
		podFullName  string
		podName      string
		podNamespace string
		err          error
	)

	podName = runningPod.Name
	podNamespace = runningPod.Namespace
	if len(podName) == 0 && pod != nil {
		podName = pod.Name
		podNamespace = pod.Namespace
	}
	if len(podName) == 0 {
		return nil
	}

	podFullName = kubecontainer.BuildPodFullName(podName, podNamespace)
	glog.V(4).Infof("Hyper: killing pod %q.", podFullName)

	defer func() {
		// Teardown pod's network
		err = r.networkPlugin.TearDownPod(podNamespace, podName, kubecontainer.ContainerID{}, "hyper")
		if err != nil {
			glog.Warningf("Hyper: networkPlugin.TearDownPod failed, error: %v", err)
		}

		// Delete pod spec file
		specFileName := path.Join(hyperPodSpecDir, podFullName)
		_, err = os.Stat(specFileName)
		if err == nil {
			e := os.Remove(specFileName)
			if e != nil {
				glog.Warningf("Hyper: delete spec file for %s failed, error: %v", runningPod.Name, e)
			}
		}
	}()

	// preStop hook
	for _, c := range runningPod.Containers {
		r.containerRefManager.ClearRef(c.ID)

		var container *api.Container
		if pod != nil {
			for i, containerSpec := range pod.Spec.Containers {
				if c.Name == containerSpec.Name {
					container = &pod.Spec.Containers[i]
					break
				}
			}
		}

		// TODO(harryz) not sure how to use gracePeriodOverride here
		gracePeriod := int64(minimumGracePeriodInSeconds)
		if pod != nil {
			switch {
			case pod.DeletionGracePeriodSeconds != nil:
				gracePeriod = *pod.DeletionGracePeriodSeconds
			case pod.Spec.TerminationGracePeriodSeconds != nil:
				gracePeriod = *pod.Spec.TerminationGracePeriodSeconds
			}
		}

		start := unversioned.Now()
		if pod != nil && container != nil && container.Lifecycle != nil && container.Lifecycle.PreStop != nil {
			glog.V(4).Infof("Running preStop hook for container %q", container.Name)
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer utilruntime.HandleCrash()
				if msg, err := r.runner.Run(c.ID, pod, container, container.Lifecycle.PreStop); err != nil {
					glog.Errorf("preStop hook for container %q failed: %v, error msg is: %v", container.Name, err, msg)
				}
			}()
			select {
			case <-time.After(time.Duration(gracePeriod) * time.Second):
				glog.V(2).Infof("preStop hook for container %q did not complete in %d seconds", container.Name, gracePeriod)
			case <-done:
				glog.V(4).Infof("preStop hook for container %q completed", container.Name)
			}
			gracePeriod -= int64(unversioned.Now().Sub(start.Time).Seconds())
		}

		// always give containers a minimal shutdown window to avoid unnecessary SIGKILLs
		if gracePeriod < minimumGracePeriodInSeconds {
			gracePeriod = minimumGracePeriodInSeconds
		}
	}

	podInfos, err := r.hyperClient.ListPods()
	if err != nil {
		glog.Errorf("Hyper: ListPods failed, error: %s", err)
		return err
	}

	for _, podInfo := range podInfos {
		if podInfo.PodName == podFullName {
			podID = podInfo.PodID

			// Remove log links
			for _, c := range podInfo.PodInfo.Status.ContainerStatus {
				_, _, _, containerName, _, _, err := r.parseHyperContainerFullName(c.Name)
				if err != nil {
					continue
				}
				symlinkFile := LogSymlink(r.containerLogsDir, podFullName, containerName, c.ContainerID)
				err = os.Remove(symlinkFile)
				if err != nil && !os.IsNotExist(err) {
					glog.Warningf("Failed to remove container log symlink %q: %v", symlinkFile, err)
				}
			}

			break
		}
	}

	err = r.hyperClient.RemovePod(podID)
	if err != nil {
		glog.Errorf("Hyper: remove pod %s failed, error: %s", podID, err)
		return err
	}

	return nil
}

// GetAPIPodStatus returns the status of the given pod.
func (r *runtime) GetAPIPodStatus(pod *api.Pod) (*api.PodStatus, error) {
	// Get the pod status.
	podStatus, err := r.GetPodStatus(pod.UID, pod.Name, pod.Namespace)
	if err != nil {
		return nil, err
	}
	return r.ConvertPodStatusToAPIPodStatus(pod, podStatus)
}

// GetPodStatus retrieves the status of the pod, including the information of
// all containers in the pod. Clients of this interface assume the containers
// statuses in a pod always have a deterministic ordering (eg: sorted by name).
func (r *runtime) GetPodStatus(uid types.UID, name, namespace string) (*kubecontainer.PodStatus, error) {
	status := &kubecontainer.PodStatus{
		ID:        uid,
		Name:      name,
		Namespace: namespace,
	}

	podInfos, err := r.hyperClient.ListPods()
	if err != nil {
		glog.Errorf("Hyper: ListPods failed, error: %s", err)
		return nil, err
	}

	podFullName := kubecontainer.BuildPodFullName(name, namespace)
	for _, podInfo := range podInfos {
		if podInfo.PodName != podFullName {
			continue
		}

		if len(podInfo.PodInfo.Status.PodIP) > 0 {
			status.IP = podInfo.PodInfo.Status.PodIP[0]
		}

		for _, containerInfo := range podInfo.PodInfo.Status.ContainerStatus {
			for _, container := range podInfo.PodInfo.Spec.Containers {
				if container.ContainerID == containerInfo.ContainerID {
					c := r.getContainerStatus(containerInfo, container.Image, container.ImageID,
						podInfo.PodInfo.Status.StartTime, podInfo.PodInfo.Spec.Labels)
					status.ContainerStatuses = append(
						status.ContainerStatuses,
						c)
				}
			}
		}
	}

	glog.V(3).Infof("Hyper: get pod %s status %s", podFullName, status)

	return status, nil
}

// PullImage pulls an image from the network to local storage using the supplied
// secrets if necessary.
func (r *runtime) PullImage(image kubecontainer.ImageSpec, pullSecrets []api.Secret) error {
	img := image.Image

	repoToPull, tag := parseImageName(img)
	if exist, _ := r.hyperClient.IsImagePresent(repoToPull, tag); exist {
		return nil
	}

	keyring, err := credentialprovider.MakeDockerKeyring(pullSecrets, r.dockerKeyring)
	if err != nil {
		return err
	}

	creds, ok := keyring.Lookup(repoToPull)
	if !ok || len(creds) == 0 {
		glog.V(4).Infof("Hyper: pulling image %s without credentials", img)
	}

	var credential string
	if len(creds) > 0 {
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(creds[0]); err != nil {
			return err
		}
		credential = base64.URLEncoding.EncodeToString(buf.Bytes())
	}

	err = r.hyperClient.PullImage(img, credential)
	if err != nil {
		return fmt.Errorf("Hyper: Failed to pull image: %v", err)
	}

	if exist, _ := r.hyperClient.IsImagePresent("haproxy", "1.5"); !exist {
		err = r.hyperClient.PullImage("haproxy:1.5", credential)
		if err != nil {
			return fmt.Errorf("Hyper: Failed to pull haproxy:1.5 image: %v", err)
		}
	}

	return nil
}

// IsImagePresent checks whether the container image is already in the local storage.
func (r *runtime) IsImagePresent(image kubecontainer.ImageSpec) (bool, error) {
	repoToPull, tag := parseImageName(image.Image)
	glog.V(4).Infof("Hyper: checking is image %s present", image.Image)
	exist, err := r.hyperClient.IsImagePresent(repoToPull, tag)
	if err != nil {
		glog.Warningf("Hyper: checking image failed, error: %s", err)
		return false, err
	}

	return exist, nil
}

// Gets all images currently on the machine.
func (r *runtime) ListImages() ([]kubecontainer.Image, error) {
	var images []kubecontainer.Image

	if outputs, err := r.hyperClient.ListImages(); err != nil {
		for _, imgInfo := range outputs {
			image := kubecontainer.Image{
				ID:       imgInfo.imageID,
				RepoTags: []string{fmt.Sprintf("%v:%v", imgInfo.repository, imgInfo.tag)},
				Size:     imgInfo.virtualSize,
			}
			images = append(images, image)
		}
	}

	return images, nil
}

// Removes the specified image.
func (r *runtime) RemoveImage(image kubecontainer.ImageSpec) error {
	err := r.hyperClient.RemoveImage(image.Image)
	if err != nil {
		return err
	}

	return nil
}

// GetContainerLogs returns logs of a specific container. By
// default, it returns a snapshot of the container log. Set 'follow' to true to
// stream the log. Set 'follow' to false and specify the number of lines (e.g.
// "100" or "all") to tail the log.
func (r *runtime) GetContainerLogs(pod *api.Pod, containerID kubecontainer.ContainerID, logOptions *api.PodLogOptions, stdout, stderr io.Writer) error {
	glog.V(4).Infof("Hyper: running logs on container %s", containerID.ID)

	var tailLines, since int64
	if logOptions.SinceSeconds != nil && *logOptions.SinceSeconds != 0 {
		since = *logOptions.SinceSeconds
	}
	if logOptions.TailLines != nil && *logOptions.TailLines != 0 {
		tailLines = *logOptions.TailLines
	}
	opts := ContainerLogsOptions{
		Container:    containerID.ID,
		OutputStream: stdout,
		ErrorStream:  stderr,
		Follow:       logOptions.Follow,
		Timestamps:   logOptions.Timestamps,
		Since:        since,
		TailLines:    tailLines,
	}

	return r.hyperClient.ContainerLogs(opts)
}

// hyperExitError implemets /pkg/util/exec.ExitError interface.
type hyperExitError struct{ *exec.ExitError }

var _ utilexec.ExitError = &hyperExitError{}

func (r *hyperExitError) ExitStatus() int {
	if status, ok := r.Sys().(syscall.WaitStatus); ok {
		return status.ExitStatus()
	}
	return 0
}

// Runs the command in the container of the specified pod
func (r *runtime) RunInContainer(containerID kubecontainer.ContainerID, cmd []string) ([]byte, error) {
	glog.V(4).Infof("Hyper: running %s in container %s.", cmd, containerID.ID)

	buffer := bytes.NewBuffer(nil)
	err := r.ExecInContainer(containerID, cmd, nil, nopCloser{buffer}, nil, false)
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			err = &hyperExitError{exitErr}
		}

		return nil, err
	}

	return buffer.ReadBytes('\n')
}

// Forward the specified port from the specified pod to the stream.
func (r *runtime) PortForward(pod *kubecontainer.Pod, port uint16, stream io.ReadWriteCloser) error {
	// TODO: port forward for hyper
	return fmt.Errorf("Hyper: PortForward unimplemented")
}

// Runs the command in the container of the specified pod.
// Attaches the processes stdin, stdout, and stderr. Optionally uses a
// tty.
func (r *runtime) ExecInContainer(containerID kubecontainer.ContainerID, cmd []string, stdin io.Reader, stdout, stderr io.WriteCloser, tty bool) error {
	glog.V(4).Infof("Hyper: execing %s in container %s.", cmd, containerID.ID)

	opts := ExecInContainerOptions{
		Container:    containerID.ID,
		InputStream:  stdin,
		OutputStream: stdout,
		ErrorStream:  stderr,
		Commands:     cmd,
		TTY:          tty,
	}

	return r.hyperClient.Exec(opts)
}

func (r *runtime) AttachContainer(containerID kubecontainer.ContainerID, stdin io.Reader, stdout, stderr io.WriteCloser, tty bool) error {
	glog.V(4).Infof("Hyper: attaching container %s.", containerID.ID)

	opts := AttachToContainerOptions{
		Container:    containerID.ID,
		InputStream:  stdin,
		OutputStream: stdout,
		ErrorStream:  stderr,
		TTY:          tty,
	}

	return r.hyperClient.Attach(opts)
}

// TODO(yifan): Delete this function when the logic is moved to kubelet.
func (r *runtime) ConvertPodStatusToAPIPodStatus(pod *api.Pod, status *kubecontainer.PodStatus) (*api.PodStatus, error) {
	apiPodStatus := &api.PodStatus{
		PodIP:             status.IP,
		ContainerStatuses: make([]api.ContainerStatus, 0, 1),
	}

	containerStatuses := make(map[string]*api.ContainerStatus)
	for _, c := range status.ContainerStatuses {
		var st api.ContainerState
		switch c.State {
		case kubecontainer.ContainerStateRunning:
			st.Running = &api.ContainerStateRunning{
				StartedAt: unversioned.NewTime(c.StartedAt),
			}
		case kubecontainer.ContainerStateExited:
			st.Terminated = &api.ContainerStateTerminated{
				ExitCode:    int32(c.ExitCode),
				StartedAt:   unversioned.NewTime(c.StartedAt),
				Reason:      c.Reason,
				Message:     c.Message,
				FinishedAt:  unversioned.NewTime(c.FinishedAt),
				ContainerID: c.ID.String(),
			}
		default:
			// Unknown state.
			st.Waiting = &api.ContainerStateWaiting{}
		}

		status, ok := containerStatuses[c.Name]
		if !ok {
			containerStatuses[c.Name] = &api.ContainerStatus{
				Name:         c.Name,
				Image:        c.Image,
				ImageID:      c.ImageID,
				ContainerID:  c.ID.String(),
				RestartCount: int32(c.RestartCount),
				State:        st,
			}
			continue
		}

		// Found multiple container statuses, fill that as last termination state.
		if status.LastTerminationState.Waiting == nil &&
			status.LastTerminationState.Running == nil &&
			status.LastTerminationState.Terminated == nil {
			status.LastTerminationState = st
		}
	}

	for _, c := range pod.Spec.Containers {
		cs, ok := containerStatuses[c.Name]
		if !ok {
			cs = &api.ContainerStatus{
				Name:  c.Name,
				Image: c.Image,
				// TODO(yifan): Add reason and message.
				State: api.ContainerState{Waiting: &api.ContainerStateWaiting{}},
			}
		}
		apiPodStatus.ContainerStatuses = append(apiPodStatus.ContainerStatuses, *cs)
	}

	sort.Sort(kubetypes.SortedContainerStatuses(apiPodStatus.ContainerStatuses))

	return apiPodStatus, nil
}

func (r *runtime) GarbageCollect(gcPolicy kubecontainer.ContainerGCPolicy, allSourcesReady bool) error {
	podInfos, err := r.hyperClient.ListPods()
	if err != nil {
		return err
	}

	for _, pod := range podInfos {
		// omit not managed pods
		podName, podNamespace, err := kubecontainer.ParsePodFullName(pod.PodName)
		if err != nil {
			continue
		}

		// omit running pods
		if pod.Status == StatusRunning {
			continue
		}

		lastTime, err := parseTimeString(pod.PodInfo.Status.FinishTime)
		if err != nil {
			continue
		}

		if lastTime.Before(time.Now().Add(-gcPolicy.MinAge)) {
			// Remove log links
			for _, c := range pod.PodInfo.Status.ContainerStatus {
				_, _, _, containerName, _, _, err := r.parseHyperContainerFullName(c.Name)
				if err != nil {
					continue
				}
				symlinkFile := LogSymlink(r.containerLogsDir, pod.PodName, containerName, c.ContainerID)
				err = os.Remove(symlinkFile)
				if err != nil && !os.IsNotExist(err) {
					glog.Warningf("Failed to remove container log symlink %q: %v", symlinkFile, err)
				}
			}

			// TODO(harryz) use allSourcesReady to prevent aggressive actions
			// Remove the pod
			err = r.hyperClient.RemovePod(pod.PodID)
			if err != nil {
				glog.Warningf("Hyper GarbageCollect: remove pod %s failed, error: %s", pod.PodID, err)
				return err
			}

			// KillPod is only called for running Pods, we should teardown network here for non-running Pods
			err = r.networkPlugin.TearDownPod(podNamespace, podName, kubecontainer.ContainerID{}, "hyper")
			if err != nil {
				glog.Warningf("Hyper: networkPlugin.TearDownPod failed, error: %v", err)
			}

			// Delete pod spec file
			specFileName := path.Join(hyperPodSpecDir, pod.PodName)
			_, err = os.Stat(specFileName)
			if err == nil {
				e := os.Remove(specFileName)
				if e != nil {
					glog.Warningf("Hyper: delete spec file for %s failed, error: %v", pod.PodName, e)
				}
			}
		}
	}

	// Remove dead symlinks - should only happen on upgrade
	// from a k8s version without proper log symlink cleanup
	logSymlinks, _ := filepath.Glob(path.Join(r.containerLogsDir, "*.log"))
	for _, logSymlink := range logSymlinks {
		if _, err = os.Stat(logSymlink); os.IsNotExist(err) {
			err = os.Remove(logSymlink)
			if err != nil {
				glog.Warningf("Failed to remove container log dead symlink %q: %v", logSymlink, err)
			}
		}
	}

	return nil
}

func (r *runtime) APIVersion() (kubecontainer.Version, error) {
	return r.version, nil
}

// LogSymlink generates symlink file path for specified container
func LogSymlink(containerLogsDir, podFullName, containerName, containerID string) string {
	return path.Join(containerLogsDir, fmt.Sprintf("%s_%s-%s.log", podFullName, containerName, containerID))
}

// GetNetNS does not make sense in hyper runtime
func (r *runtime) GetNetNS(containerID kubecontainer.ContainerID) (string, error) {
	return "", nil
}

// CleanupNetwork cleanups networks for exited pods.
func (r *runtime) CleanupNetwork() {
	podInfos, err := r.hyperClient.ListPods()
	if err != nil {
		glog.Warningf("[CleanupNetwork] ListPods failed: %v", err)
		return
	}

	for _, pod := range podInfos {
		// omit not managed pods
		podName, podNamespace, err := kubecontainer.ParsePodFullName(pod.PodName)
		if err != nil {
			continue
		}

		// omit running and pending (just created) pods
		// Note that if hyperd is restarted, original exited pod will also change
		// their state to pending, which results in network not cleaned up instantly.
		if pod.Status == StatusRunning || pod.Status == StatusPending {
			continue
		}

		err = r.networkPlugin.TearDownPod(podNamespace, podName, kubecontainer.ContainerID{}, "hyper")
		if err != nil {
			glog.Warningf("[CleanupNetwork] TearDownPod failed for pod %s_%s, error: %v", podName, podNamespace, err)
		} else {
			glog.V(5).Infof("[CleanupNetwork] teardown network for pod %s_%s success", podName, podNamespace)
		}
	}
}
