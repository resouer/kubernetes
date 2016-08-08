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
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func isHyperVirtualMachine(name string) (string, error) {
	re := regexp.MustCompile("vm-[0-9a-zA-Z]+")
	vmName := re.FindString(strings.Replace(name, "\\x2d", "-", -1))
	if len(vmName) > 0 {
		return vmName, nil
	}

	return "", fmt.Errorf("%s isn't a hyper vm", name)
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

func floatToInt64(src string) string {
	dst, err := strconv.ParseFloat(src, 10)
	if err != nil {
		return ""
	}

	return fmt.Sprintf("%v", int64(dst))
}

func transformJson(src string) string {
	re := regexp.MustCompile(`\d\.\d+[eE]\+\d\d`)
	return re.ReplaceAllStringFunc(src, floatToInt64)
}
