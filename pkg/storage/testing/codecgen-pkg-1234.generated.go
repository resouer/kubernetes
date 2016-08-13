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

package testing

import (
	"bytes"
	codec1978 "github.com/ugorji/go/codec"
	"go/format"
	"os"
	"reflect"
	"strings"
)

func CodecGenTempWrite1234() {
	fout, err := os.Create("types.generated.go")
	if err != nil {
		panic(err)
	}
	defer fout.Close()
	var out bytes.Buffer

	var typs []reflect.Type

	var t0 TestResource
	typs = append(typs, reflect.TypeOf(t0))

	codec1978.Gen(&out, "", "testing", "1234", false, codec1978.NewTypeInfos(strings.Split("codec,json", ",")), typs...)
	bout, err := format.Source(out.Bytes())
	if err != nil {
		fout.Write(out.Bytes())
		panic(err)
	}
	fout.Write(bout)
}
