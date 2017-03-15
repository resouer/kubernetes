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

// ResourceList is a map, no need for pointer
func setResource(alloc v1.ResourceList, res map[string]int64, grpres map[string]int64) {
	// set resource
	for key, val := range res {
		setRes(alloc, key, val)
	}
	// set group resource
	for key, val := range grpres {
		setGrpRes(alloc, key, val)
	}
}

func createNode(name string, res map[string]int64, grpres map[string]int64) *schedulercache.NodeInfo {
	alloc := v1.ResourceList{}
	setResource(alloc, res, grpres)
	node := v1.Node{ObjectMeta: v1.ObjectMeta{Name: name}, Status: v1.NodeStatus{Capacity: alloc, Allocatable: alloc}}
	fmt.Println("AA", node.Status.Allocatable)
	nodeInfo := schedulercache.NewNodeInfo()
	nodeInfo.SetNode(&node)

	glog.V(7).Infoln("AllocatableResource", len(nodeInfo.AllocatableResource().OpaqueIntResources), nodeInfo.AllocatableResource())

	return nodeInfo
}

type cont struct {
	name   string
	res    map[string]int64
	grpres map[string]int64
}

func createPod(name string, iconts []cont, rconts []cont) *v1.Pod {
	pod := v1.Pod{ObjectMeta: v1.ObjectMeta{Name: name}, Spec: v1.PodSpec{}}
	spec := &pod.Spec

	var contR v1.ResourceList

	for index, icont := range iconts {
		contR = addContainer(&spec.InitContainers, icont.name)
		setResource(contR, icont.res, icont.grpres)
		glog.V(7).Infoln(icont.name, spec.InitContainers[index].Resources.Requests)
	}
	for index, rcont := range rconts {
		contR = addContainer(&spec.Containers, rcont.name)
		setResource(contR, rcont.res, rcont.grpres)
		glog.V(7).Infoln(rcont.name, spec.Containers[index].Resources.Requests)
	}

	return &pod
}

func sampleTest(pod *v1.Pod, nodeInfo *schedulercache.NodeInfo) {
	// now perform allocation
	spec := &pod.Spec
	found, reasons, score := PodFitsGroupConstraints(nodeInfo, spec)
	//fmt.Println("AllocatedFromF", spec.InitContainers[0].Resources)
	fmt.Printf("Found: %t Score: %f\n", found, score)
	fmt.Printf("Reasons\n")
	for _, reason := range reasons {
		fmt.Println(reason.GetReason())
	}
	if found {
		printPodAllocation(spec)
		usedResources, _ := nodeInfo.ComputePodGroupResources(spec, false)
		node := nodeInfo.Node()
		spec.NodeName = node.ObjectMeta.Name

		updatedNode := schedulercache.NewNodeInfo(pod)

		for usedRes, usedAmt := range usedResources {
			fmt.Println("Resource", usedRes, "AmtUsed", usedAmt)
		}
		for usedRes, usedAmt := range updatedNode.RequestedResource().OpaqueIntResources {
			fmt.Println("RequestedResource", usedRes, "Amt", usedAmt)
		}
	}
}

func TestGrpAllocate1(t *testing.T) {
	flag.Parse()

	// allocatable resources
	nodeInfo := createNode("node1",
		map[string]int64{"A1": 4000, "B1": 3000},
		map[string]int64{
			"gpu/dev0/memory": 100000, "gpu/dev0/cards": 1,
			"gpu/dev1/memory": 256000, "gpu/dev1/cards": 1, "gpu/dev1/enumType": int64(0x1),
			"gpu/dev2/memory": 257000, "gpu/dev2/cards": 1,
			"gpu/dev3/memory": 192000, "gpu/dev3/cards": 1, "gpu/dev3/enumType": int64(0x1),
			"gpu/dev4/memory": 178000, "gpu/dev4/cards": 1})

	// required resources
	pod := createPod("pod1",
		[]cont{
			{name: "Init0",
				res:    map[string]int64{"A1": 2200, "B1": 2000},
				grpres: map[string]int64{"gpu/0/memory": 100000, "gpu/0/cards": 1}}},
		[]cont{
			{"Run0",
				map[string]int64{"A1": 3000, "B1": 1000},
				map[string]int64{
					"gpu/a/memory": 256000, "gpu/a/cards": 1,
					"gpu/b/memory": 178000, "gpu/b/cards": 1}},
			{name: "Run1",
				res: map[string]int64{"A1": 1000, "B1": 2000},
				grpres: map[string]int64{
					"gpu/0/memory": 190000, "gpu/0/cards": 1, "gpu/0/enumType": int64(0x3)}}})

	sampleTest(pod, nodeInfo)

	glog.Flush()
}
