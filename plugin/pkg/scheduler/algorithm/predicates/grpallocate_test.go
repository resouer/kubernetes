package predicates

import (
	"flag"
	"fmt"
	"testing"

	"github.com/golang/glog"

	v1 "k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/plugin/pkg/scheduler/schedulercache"
)

func printContainerAllocation(cont *v1.Container) {
	//glog.V(5).Infoln("Allocated", cont.Resources.Allocated)
	for resKey, resVal := range cont.Resources.Requests {
		fmt.Println("Resource", cont.Name+"/"+string(resKey), "TakenFrom", cont.Resources.AllocateFrom[resKey], "Amt", resVal)
	}
}

func printPodAllocation(spec *v1.PodSpec) {
	fmt.Printf("\nRunningContainers\n\n")
	for _, cont := range spec.Containers {
		printContainerAllocation(&cont)
		fmt.Printf("\n")
	}
	fmt.Printf("\nInitContainers\n\n")
	for _, cont := range spec.InitContainers {
		printContainerAllocation(&cont)
		fmt.Printf("\n")
	}
}

func setRes(res v1.ResourceList, name string, amt int64) {
	quant := res[v1.ResourceName(name)]
	quantP := &quant
	quantP.Set(amt)
	res[v1.ResourceName(name)] = quant
}

func setGrpRes(res v1.ResourceList, name string, amt int64) {
	fullName := v1.ResourceName(v1.ResourceGroupPrefix + "/" + name)
	quant := res[fullName]
	quantP := &quant
	quantP.Set(amt)
	res[fullName] = quant
}

func addContainer(cont *[]v1.Container, name string) v1.ResourceList {
	c := v1.Container{}
	c.Name = name
	c.Resources.Requests = make(v1.ResourceList)
	*cont = append(*cont, c)
	return c.Resources.Requests
}

func TestGrpAllocate1(t *testing.T) {
	flag.Parse()

	pod := v1.Pod{ObjectMeta: v1.ObjectMeta{Name: "pod1"}, Spec: v1.PodSpec{}}
	spec := &pod.Spec

	// allocatable resources
	alloc := v1.ResourceList{}
	setRes(alloc, "A1", 4000)
	setRes(alloc, "B1", 3000)
	setGrpRes(alloc, "gpu/dev0/memory", 100000)
	setGrpRes(alloc, "gpu/dev0/cards", 1)
	setGrpRes(alloc, "gpu/dev1/memory", 256000)
	setGrpRes(alloc, "gpu/dev1/cards", 1)
	setGrpRes(alloc, "gpu/dev1/enumType", int64(0x1))
	setGrpRes(alloc, "gpu/dev2/memory", 257000)
	setGrpRes(alloc, "gpu/dev2/cards", 1)
	setGrpRes(alloc, "gpu/dev3/memory", 192000)
	setGrpRes(alloc, "gpu/dev3/cards", 1)
	setGrpRes(alloc, "gpu/dev4/memory", 178000)
	setGrpRes(alloc, "gpu/dev4/cards", 1)
	setGrpRes(alloc, "gpu/dev3/enumType", int64(0x1))
	node := v1.Node{ObjectMeta: v1.ObjectMeta{Name: "node1"}, Status: v1.NodeStatus{Capacity: alloc, Allocatable: alloc}}
	fmt.Println("AA", node.Status.Allocatable)
	nodeInfo := schedulercache.NewNodeInfo()
	nodeInfo.SetNode(&node)
	glog.V(7).Infoln("AllocatableResource", len(nodeInfo.AllocatableResource().OpaqueIntResources), nodeInfo.AllocatableResource())

	// required resources
	contR := addContainer(&spec.InitContainers, "Init0")
	setRes(contR, "A1", 2200)
	setRes(contR, "B1", 2000)
	setGrpRes(contR, "gpu/0/memory", 100000)
	setGrpRes(contR, "gpu/0/cards", 1)
	glog.V(7).Infoln("Init0Required", spec.InitContainers[0].Resources.Requests)

	contR = addContainer(&spec.Containers, "Run0")
	setRes(contR, "A1", 3000)
	setRes(contR, "B1", 1000)
	setGrpRes(contR, "gpu/a/memory", 256000)
	setGrpRes(contR, "gpu/a/cards", 1)
	setGrpRes(contR, "gpu/b/memory", 178000)
	setGrpRes(contR, "gpu/b/cards", 1)
	glog.V(7).Infoln("Run0Required", spec.Containers[0].Resources.Requests)
	contR = addContainer(&spec.Containers, "Run1")
	setRes(contR, "A1", 1000)
	setRes(contR, "B1", 2000)
	setGrpRes(contR, "gpu/0/memory", 190000)
	setGrpRes(contR, "gpu/0/cards", 1)
	setGrpRes(contR, "gpu/0/enumType", int64(0x3))
	glog.V(7).Infoln("Run1Required", spec.Containers[1].Resources.Requests)

	// now perform allocation
	found, reasons, score := PodFitsGroupConstraints(nodeInfo, spec)
	//fmt.Println("AllocatedFromF", spec.InitContainers[0].Resources)
	fmt.Printf("Found: %t Score: %f\n", found, score)
	fmt.Printf("Reasons\n")
	for _, reason := range reasons {
		fmt.Println(reason.GetReason())
	}
	if found {
		printPodAllocation(spec)
		usedResources := nodeInfo.ComputePodGroupResources(spec)
		spec.NodeName = node.ObjectMeta.Name

		updatedNode := schedulercache.NewNodeInfo(&pod)

		for usedRes, usedAmt := range usedResources {
			fmt.Println("Resource", usedRes, "AmtUsed", usedAmt)
		}
		for usedRes, usedAmt := range updatedNode.RequestedResource().OpaqueIntResources {
			fmt.Println("RequestedResource", usedRes, "Amt", usedAmt)
		}
	}

	glog.Flush()
}

