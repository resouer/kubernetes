
package testing

import (
	codec1978 "github.com/ugorji/go/codec"
	"os"
	"reflect"
	"bytes"
	"strings"
	"go/format"
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

