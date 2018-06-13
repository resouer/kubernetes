/*
Copyright 2016 The Kubernetes Authors.

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

package priorities

import (
	"fmt"
	"math"
	"strings"

	"k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/scheduler/algorithm"
	schedulerapi "k8s.io/kubernetes/pkg/scheduler/api"
	schedulercache "k8s.io/kubernetes/pkg/scheduler/cache"
	"k8s.io/kubernetes/pkg/util/parsers"
)

// This is a reasonable size range of all container images. 90%ile of images on dockerhub drops into this range.
const (
	mb         int64 = 1024 * 1024
	minImgSize int64 = 23 * mb
	maxImgSize int64 = 1000 * mb
)

// ImageLocality contains information to calculate image locality priority.
type ImageLocality struct {
	nodeLister algorithm.NodeLister
}

func NewImageLocalityPriority(nodeLister algorithm.NodeLister) (algorithm.PriorityMapFunction, algorithm.PriorityReduceFunction) {
	imageLocality := &ImageLocality{
		nodeLister: nodeLister,
	}

	// TODO(harry): reduce?
	return imageLocality.CalculateSpreadPriorityMap, nil
}

// CalculateSpreadPriorityMap is a priority function that favors nodes that already have requested pod container's images.
// It will detect whether the requested images are present on a node, and then calculate a score ranging from 0 to 10
// based on the total size of those images.
// - If none of the images are present, this node will be given the lowest priority.
// - If some of the images are present on a node, the larger their sizes' sum, the higher the node's priority.
func (i *ImageLocality) CalculateSpreadPriorityMap(pod *v1.Pod, meta interface{}, nodeInfo *schedulercache.NodeInfo) (schedulerapi.HostPriority, error) {
	node := nodeInfo.Node()
	if node == nil {
		return schedulerapi.HostPriority{}, fmt.Errorf("node not found")
	}

	nodes, err := i.nodeLister.List()
	if err != nil {
		return schedulerapi.HostPriority{}, fmt.Errorf("failed to list nodes")
	}
	sumSize, discount := totalImageSize(nodeInfo, pod.Spec.Containers, nodes)
	// TODO(harry): Evaluate the performance impact.
	return schedulerapi.HostPriority{
		Host:  node.Name,
		Score: calculateScoreFromSize(sumSize, discount),
	}, nil

}

// calculateScoreFromSize calculates the priority of a node. sumSize is sum size of requested images on this node.
// 1. Split image size range into 10 buckets.
// 2. Decide the priority of a given sumSize based on which bucket it belongs to.
func calculateScoreFromSize(sumSize int64, discount float64) int {
	var score int
	switch {
	case sumSize == 0 || sumSize < minImgSize:
		// 0 means none of the images required by this pod are present on this
		// node or the total size of the images present is too small to be taken into further consideration.
		score = 0
	case sumSize >= maxImgSize:
		// If existing images' total size is larger than max, just make it highest priority.
		score = schedulerapi.MaxPriority
	default:
		score = int((int64(schedulerapi.MaxPriority) * (sumSize - minImgSize) / (maxImgSize - minImgSize)) + 1)
	}
	// TODO(harry): or, when score > 5, give it a discount?
	return int(math.Ceil(float64(score) * discount))
}

// totalImageSize returns the total image size of all the containers that are already on the node.
func totalImageSize(nodeInfo *schedulercache.NodeInfo, containers []v1.Container, nodes []*v1.Node) (int64, float64) {
	var (
		total    int64
		discount float64
	)

	imageSizes := nodeInfo.ImageSizes()
	for _, container := range containers {
		imageNameOnNode := normalizedImageName(container.Image)
		discount = distributionDiscount(imageNameOnNode, nodes)
		if size, ok := imageSizes[imageNameOnNode]; ok {
			total += size
		}
	}

	return total, discount
}

func distributionDiscount(image string, nodes []*v1.Node) float64 {
	var count float64
	for _, node := range nodes {
		if imagePresents(node, image) {
			count++
		}
	}
	return count / float64(len(nodes))
}

// TODO(harry): this is tricky, we may want to prepare this information in other place.
func imagePresents(node *v1.Node, imageName string) bool {
	for _, containerImg := range node.Status.Images {
		for _, name := range containerImg.Names {
			if name == imageName {
				return true
			}
		}
	}
	return false
}

// normalizedImageName returns the CRI compliant name for a given image.
// TODO: cover the corner cases of missed matches, e.g,
// 1. Using Docker as runtime and docker.io/library/test:tag in pod spec, but only test:tag will present in node status
// 2. Using the implicit registry, i.e., test:tag or library/test:tag in pod spec but only docker.io/library/test:tag
// in node status; note that if users consistently use one registry format, this should not happen.
func normalizedImageName(name string) string {
	if strings.LastIndex(name, ":") <= strings.LastIndex(name, "/") {
		name = name + ":" + parsers.DefaultImageTag
	}
	return name
}
