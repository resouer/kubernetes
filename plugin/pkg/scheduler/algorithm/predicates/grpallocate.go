package predicates

import (
	"reflect"
	"regexp"
	"strings"

	"github.com/golang/glog"

	v1 "k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/plugin/pkg/scheduler/algorithm"
	"k8s.io/kubernetes/plugin/pkg/scheduler/schedulercache"
)

type scoreFunc func(alloctable int64, used int64, requested int64) float64

func toInterfaceArray(val interface{}) []interface{} {
	t := reflect.TypeOf(val)
	tv := reflect.ValueOf(val)
	if t.Kind() == reflect.Array || t.Kind() == reflect.Slice {
		a := make([]interface{}, tv.Len())
		for i := 0; i < tv.Len(); i++ {
			a[i] = tv.Index(i).Interface()
		}
		return a
	}
	panic("Not an array or slice")
}

func assignMapK(x interface{}, keys []interface{}, val interface{}) {
	t := reflect.TypeOf(x)
	if t.Kind() == reflect.Map {
		mv := reflect.ValueOf(x)
		key0, keyR := keys[0], keys[1:]
		if len(keyR) == 0 {
			// at end
			mv.SetMapIndex(reflect.ValueOf(key0), reflect.ValueOf(val))
		} else {
			k := reflect.ValueOf(key0)
			v := mv.MapIndex(k)
			if v == reflect.ValueOf(nil) {
				v = reflect.MakeMap(t.Elem())
				mv.SetMapIndex(reflect.ValueOf(key0), v)
			}
			assignMapK(v.Interface(), keyR, val)
		}
	} else {
		panic("Not a map")
	}
}

func assignMap(x interface{}, keys interface{}, val interface{}) {
	keysA := toInterfaceArray(keys)
	assignMapK(x, keysA, val)
}

func getMapK(x interface{}, keys []interface{}) interface{} {
	t := reflect.TypeOf(x)
	if t.Kind() == reflect.Map {
		mv := reflect.ValueOf(x)
		key0, keyR := keys[0], keys[1:]
		v := mv.MapIndex(reflect.ValueOf(key0))
		if len(keyR) == 0 {
			// at end
			if v == reflect.ValueOf(nil) {
				return reflect.Zero(t.Elem()).Interface()
			}
			return v.Interface()
		}
		return getMapK(v.Interface(), keyR)
	}
	panic("Not a map")
}

func getMap(x interface{}, keys interface{}) interface{} {
	return getMapK(x, toInterfaceArray(keys))
}

func prechecked(constraint string) bool {
	return !v1.IsGroupResourceName(v1.ResourceName(constraint))
}

func findSubGroups(baseGroup string, grp map[string]string) (map[string](map[string](map[string]string)), map[string]bool) {
	subGrp := make(map[string](map[string](map[string]string)))
	isSubGrp := make(map[string]bool)
	// regex tester for groups
	glog.V(5).Infoln("Subgroup def", baseGroup+`/(\S*?)/(\S*?)/(\S*)`)
	re := regexp.MustCompile(baseGroup + `/(\S*?)/(\S*?)/(\S*)`)
	for grpKey, grpElem := range grp {
		matches := re.FindStringSubmatch(grpKey)
		if len(matches) >= 4 {
			assignMap(subGrp, matches[1:], grpElem)
			isSubGrp[grpKey] = true
		} else {
			isSubGrp[grpKey] = false
		}
	}
	return subGrp, isSubGrp
}

func printResMap(res map[string]int64, grp map[string]string) {
	for grpKey, grpElem := range grp {
		glog.V(5).Infoln("Key", grpKey, "GlobalKey", grpElem, "Val", res[grpElem])
	}
}

func leftoverScoreFunc(allocatable int64, used int64, requested int64) float64 {
	leftoverI := allocatable - used - requested // >= 0
	leftoverF := float64(leftoverI)
	allocatableF := float64(allocatable)
	if allocatable != 0 {
		return 1.0 - (leftoverF / allocatableF) // between 0.0 and 1.0
	}
	return 0.0
}

// Straight and simple C to Go translation from https://en.wikipedia.org/wiki/Hamming_weight
func popcount(x uint64) int {
	const (
		m1  = 0x5555555555555555 //binary: 0101...
		m2  = 0x3333333333333333 //binary: 00110011..
		m4  = 0x0f0f0f0f0f0f0f0f //binary:  4 zeros,  4 ones ...
		h01 = 0x0101010101010101 //the sum of 256 to the power of 0,1,2,3...
	)
	x -= (x >> 1) & m1             //put count of each 2 bits into those 2 bits
	x = (x & m2) + ((x >> 2) & m2) //put count of each 4 bits into those 4 bits
	x = (x + (x >> 4)) & m4        //put count of each 8 bits into those 8 bits
	return int((x * h01) >> 56)    //returns left 8 bits of x + (x<<8) + (x<<16) + (x<<24) + ...
}

func enumScoreFunc(allocatable int64, used int64, requested int64) float64 {
	usedMask := uint64(allocatable & (used | requested))
	bitCntAlloc := popcount(uint64(allocatable))
	bitCntUsed := popcount(uint64(usedMask))
	leftoverI := bitCntAlloc - bitCntUsed
	leftoverF := float64(leftoverI)
	allocatableF := float64(bitCntAlloc)
	if bitCntAlloc != 0 {
		return 1.0 - (leftoverF / allocatableF)
	}
	return 0.0
}

// returns whether resource available and score
// can use hash of scorers for different resources
// simple leftover for now for score, 0 is low, 1.0 is high score
func resourceAvailable(
	contName string,
	req map[string]int64, grpReq map[string]string,
	allocRes map[string]int64, grpAllocRes map[string]string, scoreFunc map[string]scoreFunc,
	usedResource map[string]int64, isSubGrp map[string]bool) (bool, []algorithm.PredicateFailureReason, float64) {

	glog.V(5).Infoln("Resource requirments")
	printResMap(req, grpReq)
	glog.V(5).Infoln("Available in group")
	printResMap(allocRes, grpAllocRes)

	score := 0.0
	numCnt := 0
	found := true
	var predicateFails []algorithm.PredicateFailureReason
	for grpReqKey, grpReqElem := range grpReq {
		if !isSubGrp[grpReqKey] {
			// see if resource exists
			glog.V(5).Infoln("Testing for resource", grpReqElem)
			required := req[grpReqElem]
			globalName, available := grpAllocRes[grpReqKey]
			if !available {
				found = false
				predicateFails = append(predicateFails, NewInsufficientResourceError(v1.ResourceName(contName+"/"+grpReqElem), required, int64(0), int64(0)))
				continue
			}
			allocatable := allocRes[globalName]
			used := usedResource[globalName]
			if strings.HasPrefix(strings.ToLower(grpReqKey), "enum") {
				if (uint64(allocatable) & uint64(required)) == uint64(0) {
					found = false
					predicateFails = append(predicateFails, NewInsufficientResourceError(v1.ResourceName(contName+"/"+grpReqElem), required, used, allocatable))
					continue
				}
			} else {
				if allocatable-used < required {
					found = false
					predicateFails = append(predicateFails, NewInsufficientResourceError(v1.ResourceName(contName+"/"+grpReqElem), required, used, allocatable))
					continue
				}
			}
			scoreFn := scoreFunc[globalName]
			if scoreFn != nil {
				score += scoreFn(allocatable, used, required)
			}
			glog.V(5).Infoln("Resource", grpReqElem, "Available")
			numCnt++
		} else {
			glog.V(5).Infoln("No test for subgroup", grpReqElem)
		}
	}
	// penalize for unused resources available
	for grpAllocResKey, grpAllocResElem := range grpAllocRes {
		_, available := grpReq[grpAllocResKey]
		if !available {
			allocatable := allocRes[grpAllocResElem]
			required := int64(0)
			used := usedResource[grpAllocResElem]
			scoreFn := scoreFunc[grpAllocResElem]
			if scoreFn != nil {
				score += scoreFn(allocatable, used, required)
			}
			numCnt++
		}
	}
	lenGrpF := float64(numCnt)
	// score is average score for group
	return found, predicateFails, score / lenGrpF
}

// allocate and return
// attempt to allocate for group, and then allocate subgroups
func allocateSubGroups(
	contName string,
	req map[string]int64, subgrpsReq map[string](map[string](map[string]string)),
	allocRes map[string]int64, subgrpsAllocRes map[string](map[string](map[string]string)), scorer map[string]scoreFunc,
	usedResource map[string]int64, allocated map[string]string,
	usedGroups map[string]bool, bPreferUsed bool, baseGroup string) (bool, []algorithm.PredicateFailureReason, float64) {

	score := 0.0
	found := true
	var predicateFails []algorithm.PredicateFailureReason
	for subgrpsKey, subgrpsElemGrp := range subgrpsReq {
		for subgrpsElemIndex, subgrpsElem := range subgrpsElemGrp {
			foundSubGrp, reasons, scoreGroup := allocateGroup(contName, req, subgrpsElem, allocRes, subgrpsAllocRes[subgrpsKey], scorer, usedResource, allocated,
				usedGroups, bPreferUsed, baseGroup+"/"+subgrpsKey)
			if !foundSubGrp {
				found = false
				searchGroup := baseGroup + "/" + subgrpsKey + "/" + subgrpsElemIndex
				predicateFails = append(predicateFails, NewInsufficientResourceError(v1.ResourceName(contName+"/"+searchGroup), 0, 0, 0))
				predicateFails = append(predicateFails, reasons...)
				continue
			}
			score += scoreGroup
		}
	}
	return found, predicateFails, score
}

// "n" is used to index over list of group resources
//
// "i" is used to index over list of allocatable groups
//
// req is map of requirements
// grpReq is map where key is "group" name of requirement and value is "global" name
// i.e. req[grpReq[n]] is the requirement of "n"th resource in group
//
// allocRes is map of allocatable resources on nodeinfo
// grpsAllocRes is map of groups of allocatable resources
// i.e. allocRes[grpAllocRes[i][n]] is the available resource in the "i"th alloctable group of the "n"th resource
//
// allocated map refers to which resource is being used in allocations
// i.e. allocRes[allocated[grpReq[n]] is the resource used for the "n"th resource in group
//
// usedResource is the amount of utilized resource in the global resource list
// i.e. usedResource[grpAllocRes[i][n]] is used when considering the "i"th alloctable group of the "n"th resource
// i.e. usedResource[allocated[grpReq[n]]] is subtracted from after allocation
func allocateGroup(
	contName string,
	req map[string]int64, grpReq map[string]string,
	allocRes map[string]int64, grpsAllocRes map[string](map[string]string), scorer map[string]scoreFunc,
	usedResource map[string]int64, allocated map[string]string,
	usedGroups map[string]bool, bPreferUsed bool, baseGroup string) (bool, []algorithm.PredicateFailureReason, float64) {

	if len(grpReq) == 0 {
		return true, nil, 0.0
	}

	maxScore := -1.0
	maxScoreKey := ""
	anyFind := false
	maxIsUsedGroup := false
	maxGroupName := ""
	var allocatedSubGrp map[string]string
	var predicateFails []algorithm.PredicateFailureReason

	subgrpsReq, isSubGrp := findSubGroups(baseGroup, grpReq)

	// go over all possible places to allocate
	for grpsAllocResKey, grpsAllocResElem := range grpsAllocRes {
		foundRes, reasons, score := resourceAvailable(contName, req, grpReq, allocRes, grpsAllocResElem, scorer, usedResource, isSubGrp)

		if foundRes == true {
			glog.V(5).Infoln(baseGroup, "group", grpsAllocResKey, "base resource available with score", score)
		}
		// next subgroup
		subgrpsAllocRes, _ := findSubGroups(baseGroup, grpsAllocResElem)
		allocatedNext := make(map[string]string)
		foundNext, reasonsNext, scoreNext := allocateSubGroups(contName, req, subgrpsReq, allocRes, subgrpsAllocRes, scorer, usedResource, allocatedNext,
			usedGroups, bPreferUsed, baseGroup+"/"+grpsAllocResKey)
		if foundRes && foundNext {
			score += scoreNext
			groupName := baseGroup + "/" + grpsAllocResKey // a unique name for the group
			glog.V(5).Infoln(groupName, "total resource available with score", score)
			takeNew := false
			if !bPreferUsed {
				if score >= maxScore {
					takeNew = true
				}
			} else {
				// prefer previously used
				if maxIsUsedGroup {
					// already have used group, only take if current is used and score is higher
					if usedGroups[groupName] && score >= maxScore {
						takeNew = true
					}
				} else {
					// don't have used, take if score higher or used
					if usedGroups[groupName] || score >= maxScore {
						takeNew = true
					}
				}
			}
			if takeNew {
				maxScore = score
				maxScoreKey = grpsAllocResKey
				anyFind = true
				allocatedSubGrp = allocatedNext
				maxIsUsedGroup = usedGroups[groupName]
				maxGroupName = groupName
			}
		}
		if len(grpsAllocRes) == 1 {
			predicateFails = append(predicateFails, reasons...)
			predicateFails = append(predicateFails, reasonsNext...)
		}
	}

	if anyFind {
		for grpReqKey, grpReqValue := range grpReq {
			if isSubGrp[grpReqKey] {
				// get from next
				allocated[grpReqValue] = allocatedSubGrp[grpReqValue]
			} else {
				allocated[grpReqValue] = grpsAllocRes[maxScoreKey][grpReqKey]
			}
			if v1.IsEnumResource(grpReqKey) {
				usedResource[allocated[grpReqValue]] |= (req[grpReqValue] & allocRes[allocated[grpReqValue]])
			} else {
				usedResource[allocated[grpReqValue]] += req[grpReqValue]
			}
		}
		usedGroups[maxGroupName] = true
		return true, nil, maxScore
	}

	return false, predicateFails, 0.0
}

// allocate the main group
func containerFitsGroupConstraints(contReq *v1.Container, allocatable *schedulercache.Resource, scorer map[string]scoreFunc, usedResource map[string]int64,
	usedGroups map[string]bool, bPreferUsed bool) (bool, []algorithm.PredicateFailureReason, float64) {

	// Required resources
	reqName := make(map[string]string)
	req := make(map[string]int64)
	// Quantitites available on NodeInfo
	allocName := make(map[string](map[string]string))
	alloc := make(map[string]int64)
	// where are resources coming from for given constraint
	allocatedLoc := make(map[string]string)
	glog.V(7).Infoln("Requests", contReq.Resources.Requests)
	glog.V(7).Infoln("AllocatableRes", allocatable.OpaqueIntResources)
	for reqRes, reqVal := range contReq.Resources.Requests {
		if !prechecked(string(reqRes)) {
			reqName[string(reqRes)] = string(reqRes)
			req[string(reqRes)] = reqVal.Value()
		}
	}
	for allocRes, allocVal := range allocatable.OpaqueIntResources {
		if !prechecked(string(allocRes)) {
			assignMap(allocName, []string{"0", string(allocRes)}, string(allocRes))
			alloc[string(allocRes)] = allocVal
		}
	}
	glog.V(7).Infoln("Required", reqName, req)
	glog.V(7).Infoln("Allocatable", allocName, alloc)

	found, reasons, score := allocateGroup(contReq.Name, req, reqName, alloc, allocName,
		scorer, usedResource, allocatedLoc,
		usedGroups, bPreferUsed, v1.ResourceGroupPrefix)

	if contReq.Resources.AllocateFrom == nil {
		contReq.Resources.AllocateFrom = make(v1.ResourceLocation)
	}
	for allocatedKey, allocatedLocVal := range allocatedLoc {
		contReq.Resources.AllocateFrom[v1.ResourceName(allocatedKey)] = v1.ResourceName(allocatedLocVal)
	}

	glog.V(5).Infoln("Allocated", allocatedLoc)
	glog.V(5).Infoln("Container allocation found", found, "with score", score)

	return found, reasons, score
}

func initUsedResource(n *schedulercache.NodeInfo) map[string]int64 {
	usedResource := make(map[string]int64)
	requested := n.RequestedResource()
	for resKey, resVal := range requested.OpaqueIntResources {
		usedResource[string(resKey)] = resVal
	}
	return usedResource
}

func defaultScoreFunc(r *schedulercache.Resource) map[string]scoreFunc {
	scorer := make(map[string]scoreFunc)
	for key := range r.OpaqueIntResources {
		keyS := string(key)
		if !prechecked(keyS) {
			if !v1.IsEnumResource(keyS) {
				scorer[keyS] = leftoverScoreFunc
			} else {
				scorer[keyS] = enumScoreFunc
			}
		}
	}
	return scorer
}

// PodFitsGroupConstraints tells if pod fits constraints, score returned is score of running containers
func PodFitsGroupConstraints(n *schedulercache.NodeInfo, spec *v1.PodSpec) (bool, []algorithm.PredicateFailureReason, float64) {
	usedResource := initUsedResource(n)
	usedGroups := make(map[string]bool)
	totalScore := 0.0
	var predicateFails []algorithm.PredicateFailureReason
	found := true

	allocatableV := n.AllocatableResource()
	allocatable := &allocatableV
	scorer := defaultScoreFunc(allocatable)

	// first go over running containers
	// now go over all running containers
	for i := range spec.Containers {
		if fits, reasons, score := containerFitsGroupConstraints(&spec.Containers[i], allocatable, scorer, usedResource, usedGroups, true); fits == false {
			found = false
			predicateFails = append(predicateFails, reasons...)
		} else {
			totalScore += score
		}
	}

	// now go over initialization containers, try to reutilize used groups
	for i := range spec.InitContainers {
		// clear the used resources
		usedResource = initUsedResource(n)
		// container.Resources.Requests contains a map, alloctable contains type Resource
		// prefer groups which are already used by running containers
		if fits, reasons, _ := containerFitsGroupConstraints(&spec.InitContainers[i], allocatable, scorer, usedResource, usedGroups, true); fits == false {
			found = false
			predicateFails = append(predicateFails, reasons...)
		}
	}

	glog.V(4).Infoln("Used", usedGroups)

	return found, predicateFails, totalScore
}
