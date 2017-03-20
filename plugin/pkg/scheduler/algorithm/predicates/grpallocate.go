package predicates

import (
	"math"
	"reflect"
	"regexp"

	"github.com/golang/glog"

	v1 "k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/plugin/pkg/scheduler/algorithm"
	"k8s.io/kubernetes/plugin/pkg/scheduler/schedulercache"
)

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

// ===================================================

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

// LeftoverScoreFunc provides default scoring function
func LeftoverScoreFunc(allocatable int64, usedByPod int64, usedByNode int64, requested int64, initContainer bool) (
	found bool, score float64, usedByContainer int64, newUsedByPod, newUsedByNode int64) {

	usedByContainer = requested

	if !initContainer {
		newUsedByPod = usedByPod + requested
	} else {
		if requested > usedByPod {
			newUsedByPod = requested
		} else {
			newUsedByPod = usedByPod
		}
	}
	newUsedByNode = usedByNode + (newUsedByPod - usedByPod)

	leftoverI := allocatable - newUsedByNode // >= 0 (between -Inf and allocatable if not found)
	leftoverF := float64(leftoverI)
	allocatableF := float64(allocatable)
	if allocatable != 0 {
		score = 1.0 - (leftoverF / allocatableF) // between 0.0 and 1.0 if leftover between 0 and allocatable
	} else {
		score = 0.0
	}
	found = (leftoverI >= 0)

	return found, score, usedByContainer, newUsedByPod, newUsedByNode // score will be between 0.0 and 1.0 if found = true
}

// AlwaysFoundScoreFunc provides something that always returns true
// want to make allocatable-used as close to requested
func AlwaysFoundScoreFunc(allocatable int64, usedByPod int64, usedByNode int64, requested int64, initContainer bool) (
	found bool, score float64, usedByContainer int64, newUsedByPod, newUsedByNode int64) {

	found, score, usedByContainer, newUsedByPod, newUsedByNode = LeftoverScoreFunc(allocatable, usedByPod, usedByNode, requested, initContainer)
	diff := 1.0 - score          // between -Inf and 1.0
	diff = math.Max(-1.0, diff)  // between -1.0 and 1.0
	score = 1.0 - math.Abs(diff) // between 0.0 and 1.0
	return found, score, usedByContainer, newUsedByPod, newUsedByNode
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

// EnumScoreFunc returns bitwise score
func EnumScoreFunc(allocatable int64, usedByPod int64, usedByNode int64, requested int64, initContainer bool) (
	found bool, score float64, usedByContainer int64, newUsedByPod, newUsedByNode int64) {

	usedMask := uint64(allocatable & (usedByPod | requested))
	bitCntAlloc := popcount(uint64(allocatable))
	bitCntUsed := popcount(usedMask)
	leftoverI := bitCntAlloc - bitCntUsed
	leftoverF := float64(leftoverI)
	allocatableF := float64(bitCntAlloc)
	if bitCntAlloc != 0 {
		score = 1.0 - (leftoverF / allocatableF)
	} else {
		score = 0.0
	}
	found = ((uint64(allocatable) & uint64(requested)) != 0) // at least one bit true
	usedByContainer = allocatable & requested
	newUsedByPod = int64(usedMask)
	newUsedByNode = 0

	return found, score, usedByContainer, newUsedByPod, newUsedByNode
}

// =====================================================

// GrpAllocator is the type to perform group allocations
type GrpAllocator struct {
	// Global read info for all
	ContName      string
	InitContainer bool
	PreferUsed    bool
	// required resource and scorer
	RequiredResource map[string]int64
	ReqScorer        map[string]v1.ResourceScoreFunc
	// allocatable resource and scorer
	AllocResource map[string]int64
	AllocScorer   map[string]v1.ResourceScoreFunc

	// Global read/write info
	UsedGroups map[string]bool

	// Per group info, read only
	// Resource Info - required
	GrpRequiredResource map[string]string
	IsReqSubGrp         map[string]bool
	// Resource Info - allocatable
	GrpAllocResource map[string](map[string]string)
	IsAllocSubGrp    map[string]bool
	// other nodeinfo
	ReqBaseGroupName     string
	AllocBaseGroupPrefix string

	// Used Info, write as group is explored
	Score        float64
	PodResource  map[string]int64
	NodeResource map[string]int64
	AllocateFrom map[string]string
}

// sub group writes into parent group's AllocateFrom, PodResource, and NodeResource
func (grp *GrpAllocator) createSubGroup(
	resourceLocation string,
	requiredSubGrps map[string](map[string](map[string]string)),
	allocSubGrps map[string](map[string](map[string]string)),
	grpName string,
	grpIndex string) *GrpAllocator {

	subGrp := *grp // shallow copy of struct
	// overwrite
	subGrp.GrpRequiredResource = requiredSubGrps[grpName][grpIndex]
	subGrp.GrpAllocResource = allocSubGrps[grpName]
	subGrp.ReqBaseGroupName = grp.ReqBaseGroupName + "/" + grpName + "/" + grpIndex
	subGrp.AllocBaseGroupPrefix = grp.AllocBaseGroupPrefix + "/" + resourceLocation + "/" + grpName
	subGrp.Score = grp.Score

	return &subGrp
}

// cloned group takes on new AllocateFrom, PodResource, and NodeResource
func (grp *GrpAllocator) cloneGroup() *GrpAllocator {
	newGrp := *grp
	// overwrite
	newGrp.AllocateFrom = make(map[string]string)
	newGrp.PodResource = make(map[string]int64)
	newGrp.NodeResource = make(map[string]int64)
	if grp.AllocateFrom != nil {
		for key, val := range grp.AllocateFrom {
			newGrp.AllocateFrom[key] = val
		}
	}
	if grp.PodResource != nil {
		for key, val := range grp.PodResource {
			newGrp.PodResource[key] = val
		}
	}
	if grp.NodeResource != nil {
		for key, val := range grp.NodeResource {
			newGrp.NodeResource[key] = val
		}
	}
	newGrp.Score = grp.Score

	return &newGrp
}

func (grp *GrpAllocator) takeGroup(grpTake *GrpAllocator) {
	grp.AllocateFrom = grpTake.AllocateFrom
	grp.PodResource = grpTake.PodResource
	grp.NodeResource = grpTake.NodeResource
	grp.Score = grpTake.Score
}

// returns whether resource available and score
// can use hash of scorers for different resources
// simple leftover for now for score, 0 is low, 1.0 is high score
func (grp *GrpAllocator) resourceAvailable(resourceLocation string) (bool, []algorithm.PredicateFailureReason) {
	grpAllocRes := grp.GrpAllocResource[resourceLocation]

	glog.V(5).Infoln("Resource requirments")
	printResMap(grp.RequiredResource, grp.GrpRequiredResource)
	glog.V(5).Infoln("Available in group")
	printResMap(grp.AllocResource, grpAllocRes)

	score := 0.0
	numCnt := 0
	found := true
	var predicateFails []algorithm.PredicateFailureReason
	for grpReqKey, grpReqElem := range grp.GrpRequiredResource {
		if !grp.IsReqSubGrp[grpReqKey] {
			// see if resource exists
			glog.V(5).Infoln("Testing for resource", grpReqElem)
			required := grp.RequiredResource[grpReqElem]
			globalName, available := grpAllocRes[grpReqKey]
			if !available {
				found = false
				predicateFails = append(predicateFails, NewInsufficientResourceError(
					v1.ResourceName(grp.ContName+"/"+grpReqElem), required, int64(0), int64(0)))
				continue
			}
			scoreFn := grp.ReqScorer[grpReqElem]
			allocatable := grp.AllocResource[globalName]
			usedPod := grp.PodResource[globalName]
			usedNode := grp.NodeResource[globalName]
			// alternatively, current score can be passed in, and new score returned if score not additive
			foundR, scoreR, _, podR, nodeR := scoreFn(allocatable, usedPod, usedNode, required, grp.InitContainer)
			if !foundR {
				found = false
				predicateFails = append(predicateFails, NewInsufficientResourceError(
					v1.ResourceName(grp.ContName+"/"+grpReqElem), required, usedNode, allocatable))
				continue
			}
			score += scoreR
			grp.PodResource[globalName] = podR
			grp.NodeResource[globalName] = nodeR
			grp.AllocateFrom[grpReqElem] = globalName
			glog.V(5).Infoln("Resource", grpReqElem, "Available")
			numCnt++
		} else {
			glog.V(5).Infoln("No test for subgroup", grpReqElem)
		}
	}
	// penalize for unused resources available
	for grpAllocResKey, grpAllocResElem := range grpAllocRes {
		if !grp.IsAllocSubGrp[grpAllocResKey] {
			_, available := grp.GrpRequiredResource[grpAllocResKey]
			if !available {
				allocatable := grp.AllocResource[grpAllocResElem]
				required := int64(0)
				usedPod := grp.PodResource[grpAllocResElem]
				usedNode := grp.NodeResource[grpAllocResElem]
				scoreFn := grp.AllocScorer[grpAllocResElem]
				if scoreFn != nil {
					_, scoreR, _, podR, nodeR := scoreFn(allocatable, usedPod, usedNode, required, grp.InitContainer)
					score += scoreR
					grp.PodResource[grpAllocResElem] = podR
					grp.NodeResource[grpAllocResElem] = nodeR
				}
				numCnt++
			}
		}
	}
	lenGrpF := float64(numCnt)
	// score is average score for group
	if numCnt > 0 {
		grp.Score += score / lenGrpF
	}

	return found, predicateFails
}

// allocate and return
// attempt to allocate for group, and then allocate subgroups
func (grp *GrpAllocator) allocateSubGroups(
	allocLocationName string,
	subgrpsReq map[string](map[string](map[string]string)),
	subgrpsAllocRes map[string](map[string](map[string]string))) (
	bool, []algorithm.PredicateFailureReason) {

	found := true
	var predicateFails []algorithm.PredicateFailureReason
	for subgrpsKey, subgrpsElemGrp := range subgrpsReq {
		for subgrpsElemIndex := range subgrpsElemGrp {
			subGrp := grp.createSubGroup(allocLocationName, subgrpsReq, subgrpsAllocRes, subgrpsKey, subgrpsElemIndex)
			foundSubGrp, reasons := subGrp.allocateGroup()
			if !foundSubGrp {
				found = false
				predicateFails = append(predicateFails, NewInsufficientResourceError(
					v1.ResourceName(grp.ContName+"/"+subGrp.ReqBaseGroupName), 0, 0, 0))
				predicateFails = append(predicateFails, reasons...)
				continue
			}
			grp.takeGroup(subGrp) // update to current
		}
	}
	return found, predicateFails
}

func (grp *GrpAllocator) allocateGroupAt(location string,
	subgrpsReq map[string](map[string](map[string]string))) (bool, []algorithm.PredicateFailureReason) {

	allocLocationName := grp.AllocBaseGroupPrefix + "/" + location
	grpsAllocResElem := grp.GrpAllocResource[location]
	subgrpsAllocRes, isSubGrp := findSubGroups(allocLocationName, grpsAllocResElem)
	grp.IsAllocSubGrp = isSubGrp

	foundRes, reasons := grp.resourceAvailable(location)

	if foundRes == true {
		glog.V(5).Infoln("group", location, "base resource available with score", grp.Score)
	}

	// next allocatable subgroups for this location
	foundNext, reasonsNext := grp.allocateSubGroups(location, subgrpsReq, subgrpsAllocRes)

	return (foundRes && foundNext), append(reasons, reasonsNext...)
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
func (grp *GrpAllocator) allocateGroup() (bool, []algorithm.PredicateFailureReason) {
	if len(grp.GrpRequiredResource) == 0 {
		return false, nil
	}

	anyFind := false
	maxScoreKey := ""
	maxScoreGrp := grp
	maxIsUsedGroup := false
	maxGroupName := ""
	var predicateFails []algorithm.PredicateFailureReason

	// find subgroups for required resources
	subgrpsReq, isSubGrp := findSubGroups(grp.ReqBaseGroupName, grp.GrpRequiredResource)
	grp.IsReqSubGrp = isSubGrp

	// go over all possible places to allocate
	for grpsAllocResKey := range grp.GrpAllocResource {
		grpCheck := grp.cloneGroup()
		found, reasons := grpCheck.allocateGroupAt(grpsAllocResKey, subgrpsReq)
		allocLocationName := grp.AllocBaseGroupPrefix + "/" + grpsAllocResKey

		if found {
			glog.V(5).Infoln(allocLocationName, "total resource available with score", grpCheck.Score)
			takeNew := false
			if !grp.PreferUsed {
				if grpCheck.Score >= maxScoreGrp.Score {
					takeNew = true
				}
			} else {
				// prefer previously used
				if maxIsUsedGroup {
					// already have used group, only take if current is used and score is higher
					if grp.UsedGroups[allocLocationName] && grpCheck.Score >= maxScoreGrp.Score {
						takeNew = true
					}
				} else {
					// don't have used, take if score higher or used
					if grp.UsedGroups[allocLocationName] || grpCheck.Score >= maxScoreGrp.Score {
						takeNew = true
					}
				}
			}
			if takeNew {
				anyFind = true
				maxScoreKey = grpsAllocResKey
				maxScoreGrp = grpCheck
				maxIsUsedGroup = grp.UsedGroups[allocLocationName]
				maxGroupName = allocLocationName
			}
		}
		if len(grp.GrpAllocResource) == 1 {
			predicateFails = append(predicateFails, reasons...)
		}
	}

	grp.takeGroup(maxScoreGrp) // take the group with max score
	if anyFind {
		glog.V(5).Infoln("Maxscore from key", maxScoreKey)
		grp.UsedGroups[maxGroupName] = true
		return true, nil
	}

	return false, predicateFails
}

func SetScorer(resource string, scorerType int) v1.ResourceScoreFunc {
	if scorerType == v1.DefaultScorer {
		return DefaultScorer(resource)
	}
	if scorerType == v1.LeftOverScorer {
		return LeftoverScoreFunc
	}
	if scorerType == v1.EnumLeftOverScorer {
		return EnumScoreFunc
	}
	return nil
}

// DefaultScorer returns default scorer given a name
func DefaultScorer(resource string) v1.ResourceScoreFunc {
	if !prechecked(resource) {
		if !v1.IsEnumResource(resource) {
			return LeftoverScoreFunc
		}
		return EnumScoreFunc
	}
	return nil
}

func setScoreFunc(r *schedulercache.Resource) map[string]v1.ResourceScoreFunc {
	scorer := make(map[string]v1.ResourceScoreFunc)
	for key := range r.OpaqueIntResources {
		keyS := string(key)
		//scorer[keyS] = DefaultScorer(keyS)
		scorer[keyS] = SetScorer(keyS, r.Scorer[key])
	}
	return scorer
}

// allocate the main group
func containerFitsGroupConstraints(contReq *v1.Container, initContainer bool,
	allocatable *schedulercache.Resource, allocScorer map[string]v1.ResourceScoreFunc,
	podResource map[string]int64, nodeResource map[string]int64,
	usedGroups map[string]bool, bPreferUsed bool) (*GrpAllocator, bool, []algorithm.PredicateFailureReason, float64) {

	grp := &GrpAllocator{}

	// Required resources
	reqName := make(map[string]string)
	req := make(map[string]int64)
	reqScorer := make(map[string]v1.ResourceScoreFunc)
	// Quantitites available on NodeInfo
	allocName := make(map[string](map[string]string))
	alloc := make(map[string]int64)
	glog.V(7).Infoln("Requests", contReq.Resources.Requests)
	glog.V(7).Infoln("AllocatableRes", allocatable.OpaqueIntResources)
	if contReq.Resources.ScorerFn == nil {
		contReq.Resources.ScorerFn = make(map[v1.ResourceName]v1.ResourceScoreFunc)
	}
	for reqRes, reqVal := range contReq.Resources.Requests {
		if !prechecked(string(reqRes)) {
			reqName[string(reqRes)] = string(reqRes)
			req[string(reqRes)] = reqVal.Value()
			scoreFn := SetScorer(string(reqRes), contReq.Resources.Scorer[reqRes])
			contReq.Resources.ScorerFn[reqRes] = scoreFn
			reqScorer[string(reqRes)] = scoreFn
		}
	}
	glog.V(7).Infoln("Required", reqName, req)

	re := regexp.MustCompile(`(\S*)/(\S*)`)
	matches := re.FindStringSubmatch(v1.ResourceGroupPrefix)
	var grpPrefix string
	var grpName string
	if len(matches) != 3 {
		panic("Invalid prefix")
	} else {
		grpPrefix = matches[1]
		grpName = matches[2]
	}
	for allocRes, allocVal := range allocatable.OpaqueIntResources {
		if !prechecked(string(allocRes)) {
			assignMap(allocName, []string{grpName, string(allocRes)}, string(allocRes))
			alloc[string(allocRes)] = allocVal
		}
	}
	glog.V(7).Infoln("Allocatable", allocName, alloc)

	grp.ContName = contReq.Name
	grp.InitContainer = initContainer
	grp.PreferUsed = bPreferUsed
	grp.RequiredResource = req
	grp.ReqScorer = reqScorer
	grp.AllocResource = alloc
	grp.AllocScorer = allocScorer
	grp.UsedGroups = usedGroups
	grp.GrpRequiredResource = reqName
	grp.GrpAllocResource = allocName
	grp.ReqBaseGroupName = v1.ResourceGroupPrefix
	grp.AllocBaseGroupPrefix = grpPrefix
	grp.Score = 0.0
	// pick up current resource usage
	grp.PodResource = podResource
	grp.NodeResource = nodeResource

	found, reasons := grp.allocateGroup()
	score := grp.Score

	if contReq.Resources.AllocateFrom == nil {
		contReq.Resources.AllocateFrom = make(v1.ResourceLocation)
	}
	for allocatedKey, allocatedLocVal := range grp.AllocateFrom {
		contReq.Resources.AllocateFrom[v1.ResourceName(allocatedKey)] = v1.ResourceName(allocatedLocVal)
	}

	glog.V(5).Infoln("Allocated", grp.AllocateFrom)
	glog.V(5).Infoln("PodResources", grp.PodResource)
	glog.V(5).Infoln("NodeResources", grp.NodeResource)
	glog.V(5).Infoln("Container allocation found", found, "with score", score)

	return grp, found, reasons, score
}

func initNodeResource(n *schedulercache.NodeInfo) map[string]int64 {
	nodeResource := make(map[string]int64)
	requested := n.RequestedResource()
	for resKey, resVal := range requested.OpaqueIntResources {
		nodeResource[string(resKey)] = resVal
	}
	return nodeResource
}

// PodFitsGroupConstraints tells if pod fits constraints, score returned is score of running containers
func PodFitsGroupConstraints(n *schedulercache.NodeInfo, spec *v1.PodSpec) (bool, []algorithm.PredicateFailureReason, float64) {
	podResource := make(map[string]int64)
	nodeResource := initNodeResource(n)
	usedGroups := make(map[string]bool)
	totalScore := 0.0
	var predicateFails []algorithm.PredicateFailureReason
	found := true

	allocatableV := n.AllocatableResource()
	allocatable := &allocatableV
	scorer := setScoreFunc(allocatable)

	// first go over running containers
	for i := range spec.Containers {
		grp, fits, reasons, score := containerFitsGroupConstraints(&spec.Containers[i], false, allocatable,
			scorer, podResource, nodeResource, usedGroups, true)
		if fits == false {
			found = false
			predicateFails = append(predicateFails, reasons...)
		} else {
			totalScore += score
		}
		podResource = grp.PodResource
		nodeResource = grp.NodeResource
	}

	// now go over initialization containers, try to reutilize used groups
	for i := range spec.InitContainers {
		// container.Resources.Requests contains a map, alloctable contains type Resource
		// prefer groups which are already used by running containers
		grp, fits, reasons, _ := containerFitsGroupConstraints(&spec.InitContainers[i], true, allocatable,
			scorer, podResource, nodeResource, usedGroups, true)
		if fits == false {
			found = false
			predicateFails = append(predicateFails, reasons...)
		}
		podResource = grp.PodResource
		nodeResource = grp.NodeResource
	}

	glog.V(4).Infoln("Used", usedGroups)

	return found, predicateFails, totalScore
}
