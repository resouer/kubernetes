package predicates

import (
	"flag"
	"fmt"
	"math"
	"testing"

	"github.com/golang/glog"

	"regexp"

	v1 "k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/plugin/pkg/scheduler/schedulercache"
)

type cont struct {
	name           string
	res            map[string]int64
	grpres         map[string]int64
	expectedGrpLoc map[string]string
}

type PodEx struct {
	pod           *v1.Pod
	expectedScore float64
	icont         []cont
	rcont         []cont
}

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
	//fmt.Println("AA", node.Status.Allocatable)
	nodeInfo := schedulercache.NewNodeInfo()
	nodeInfo.SetNode(&node)

	glog.V(7).Infoln("AllocatableResource", len(nodeInfo.AllocatableResource().OpaqueIntResources), nodeInfo.AllocatableResource())

	return nodeInfo
}

func setExpectedResources(c *cont) {
	expectedGrpLoc := make(map[string]string)
	for key, val := range c.expectedGrpLoc {
		re := regexp.MustCompile(key + `/(.*)`)
		for keyRes := range c.grpres {
			matches := re.FindStringSubmatch(keyRes)
			if len(matches) >= 2 {
				newKey := v1.ResourceGroupPrefix + "/" + key + "/" + matches[1]
				newVal := v1.ResourceGroupPrefix + "/" + val + "/" + matches[1]
				expectedGrpLoc[newKey] = newVal
			}
		}
	}
	c.expectedGrpLoc = expectedGrpLoc
}

func createPod(name string, expScore float64, iconts []cont, rconts []cont) (*v1.Pod, *PodEx) {
	pod := v1.Pod{ObjectMeta: v1.ObjectMeta{Name: name}, Spec: v1.PodSpec{}}
	spec := &pod.Spec

	var contR v1.ResourceList

	for index, icont := range iconts {
		setExpectedResources(&iconts[index])
		contR = addContainer(&spec.InitContainers, icont.name)
		setResource(contR, icont.res, icont.grpres)
		glog.V(7).Infoln(icont.name, spec.InitContainers[index].Resources.Requests)
	}
	for index, rcont := range rconts {
		setExpectedResources(&rconts[index])
		contR = addContainer(&spec.Containers, rcont.name)
		setResource(contR, rcont.res, rcont.grpres)
		glog.V(7).Infoln(rcont.name, spec.Containers[index].Resources.Requests)
	}

	podEx := PodEx{pod: &pod, icont: iconts, rcont: rconts, expectedScore: expScore}

	return &pod, &podEx
}

func sampleTest(pod *v1.Pod, podEx *PodEx, nodeInfo *schedulercache.NodeInfo) {
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

func testContainerAllocs(t *testing.T, conts []cont, podConts []v1.Container) {
	if len(conts) != len(podConts) {
		t.Errorf("Number of containers don't match - expected %v - have %v", len(conts), len(podConts))
		return
	}
	for ci, c := range conts {
		if len(c.expectedGrpLoc) != len(podConts[ci].Resources.AllocateFrom) {
			t.Errorf("Number of resources don't match - expected %v %v - have %v %v",
				len(c.expectedGrpLoc), c.expectedGrpLoc,
				len(podConts[ci].Resources.AllocateFrom), podConts[ci].Resources.AllocateFrom)
			return
		}
		for key, val := range c.expectedGrpLoc {
			valP, available := podConts[ci].Resources.AllocateFrom[v1.ResourceName(key)]
			if !available {
				t.Errorf("Expected key %v not available", key)
			} else if string(valP) != val {
				t.Errorf("Expected value for key %v not same - expected %v - have %v",
					key, val, valP)
			}
		}
	}
}

func testPodAllocs(t *testing.T, pod *v1.Pod, podEx *PodEx, nodeInfo *schedulercache.NodeInfo) {
	spec := &pod.Spec
	found, _, score := PodFitsGroupConstraints(nodeInfo, spec)
	if found {
		if podEx.rcont[0].expectedGrpLoc == nil {
			t.Errorf("Group allocation found when it should not be found")
		} else {
			if math.Abs(score-podEx.expectedScore)/podEx.expectedScore > 0.01 {
				t.Errorf("Score not correct - expected %v - have %v", podEx.expectedScore, score)
			}
			testContainerAllocs(t, podEx.icont, pod.Spec.InitContainers)
			testContainerAllocs(t, podEx.rcont, pod.Spec.Containers)
		}
	} else {
		if podEx.rcont[0].expectedGrpLoc != nil {
			t.Errorf("Group allocation not found when it should be found")
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
			"gpu/dev4/memory": 178000, "gpu/dev4/cards": 1},
	)

	// required resources
	pod, podEx := createPod("pod1", 2.994582,
		[]cont{
			{name: "Init0",
				res:            map[string]int64{"A1": 2200, "B1": 2000},
				grpres:         map[string]int64{"gpu/0/memory": 100000, "gpu/0/cards": 1},
				expectedGrpLoc: map[string]string{"gpu/0": "gpu/dev4"}}},
		[]cont{
			{"Run0",
				map[string]int64{"A1": 3000, "B1": 1000},
				map[string]int64{
					"gpu/a/memory": 256000, "gpu/a/cards": 1,
					"gpu/b/memory": 178000, "gpu/b/cards": 1},
				map[string]string{
					"gpu/a": "gpu/dev2",
					"gpu/b": "gpu/dev4"},
			},
			{name: "Run1",
				res: map[string]int64{"A1": 1000, "B1": 2000},
				grpres: map[string]int64{
					"gpu/0/memory": 190000, "gpu/0/cards": 1, "gpu/0/enumType": int64(0x3)},
				expectedGrpLoc: map[string]string{"gpu/0": "gpu/dev3"},
			},
		},
	)

	//sampleTest(pod, podEx, nodeInfo)
	testPodAllocs(t, pod, podEx, nodeInfo)

	glog.Flush()
}
