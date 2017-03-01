package predicates

import (
	"flag"
	"fmt"
	"testing"

	"github.com/golang/glog"
	"k8s.io/client-go/pkg/api/v1"
)

func printContainerAllocation(cont *Container) {
	//glog.V(5).Infoln("Allocated", cont.Resources.Allocated)
	for resKey, resVal := range cont.Resources.Requests {
		fmt.Println("Resource", cont.Name+"/"+resKey, "TakenFrom", cont.Resources.Allocated[resKey], "Amt", resVal)
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

func TestGrpAllocate1(t *testing.T) {
	flag.Parse()

	spec := PodSpec{}
	info := *NewNodeInfo()

	// allocatable resources
	alloc := info.allocatable.OpaqueIntResources
	alloc["A1"] = 4000
	alloc["B1"] = 3000
	alloc["alpha.kubernetes.io/grp/gpu/dev0/memory"] = 100000
	alloc["alpha.kubernetes.io/grp/gpu/dev0/cards"] = 1
	alloc["alpha.kubernetes.io/grp/gpu/dev1/memory"] = 256000
	alloc["alpha.kubernetes.io/grp/gpu/dev1/cards"] = 1
	alloc["alpha.kubernetes.io/grp/gpu/dev1/enumType"] = int64(0x1)
	alloc["alpha.kubernetes.io/grp/gpu/dev2/memory"] = 257000
	alloc["alpha.kubernetes.io/grp/gpu/dev2/cards"] = 1
	alloc["alpha.kubernetes.io/grp/gpu/dev3/memory"] = 192000
	alloc["alpha.kubernetes.io/grp/gpu/dev3/cards"] = 1
	alloc["alpha.kubernetes.io/grp/gpu/dev3/enumType"] = int64(0x1)
	alloc["alpha.kubernetes.io/grp/gpu/dev4/memory"] = 178000
	alloc["alpha.kubernetes.io/grp/gpu/dev4/cards"] = 1
	for key := range alloc {
		if !isEnumResource(key) {
			info.scorer[key] = leftoverScoreFunc
		} else {
			info.scorer[key] = enumScoreFunc
		}
	}

	// required resources
	spec.InitContainers = make([]Container, 1)
	spec.InitContainers[0] = *NewContainer()
	spec.InitContainers[0].Name = "Init0"
	contR := spec.InitContainers[0].Resources.Requests
	contR["A1"] = 2200
	contR["B1"] = 2000
	contR["alpha.kubernetes.io/grp/gpu/0/memory"] = 100000
	contR["alpha.kubernetes.io/grp/gpu/0/cards"] = 1

	spec.Containers = make([]Container, 2)
	spec.Containers[0] = *NewContainer()
	spec.Containers[0].Name = "Run0"
	contR = spec.Containers[0].Resources.Requests
	contR["A1"] = 3000
	contR["B1"] = 1000
	contR["alpha.kubernetes.io/grp/gpu/a/memory"] = 256000
	contR["alpha.kubernetes.io/grp/gpu/a/cards"] = 1 // take whole card
	contR["alpha.kubernetes.io/grp/gpu/b/memory"] = 178000
	contR["alpha.kubernetes.io/grp/gpu/b/cards"] = 1
	spec.Containers[1] = *NewContainer()
	spec.Containers[1].Name = "Run1"
	contR = spec.Containers[1].Resources.Requests
	contR["A1"] = 1000
	contR["B1"] = 2000
	contR["alpha.kubernetes.io/grp/gpu/0/memory"] = 190000
	contR["alpha.kubernetes.io/grp/gpu/0/cards"] = 1
	contR["alpha.kubernetes.io/grp/gpu/0/enumType"] = int64(0x3)

	// now perform allocation
	found, reasons, score := info.PodFitsGroupConstraints(&spec)
	//fmt.Println("AllocatedFromF", spec.InitContainers[0].Resources)
	fmt.Printf("Found: %t Score: %f\n", found, score)
	fmt.Printf("Reasons\n")
	for _, reason := range reasons {
		fmt.Println(reason.GetReason())
	}
	if found {
		printPodAllocation(&spec)
		usedResources := info.TakePodResource(&spec)
		for usedRes, usedAmt := range usedResources {
			fmt.Println("Resource", usedRes, "AmtUsed", usedAmt)
		}
		for usedRes, usedAmt := range info.requested.OpaqueIntResources {
			fmt.Println("RequestedResource", usedRes, "Amt", usedAmt)
		}
	}

	glog.Flush()
}
