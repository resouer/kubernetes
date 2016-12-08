#!/bin/bash

# Copyright 2016 The Kubernetes Authors All rights reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -o errexit
set -o nounset
set -o pipefail

KUBE_ROOT=$(dirname "${BASH_SOURCE}")/..
PROTO_ROOT=${KUBE_ROOT}/pkg/kubelet/hyper/types
_tmp="${KUBE_ROOT}/_tmp"

cleanup() {
  rm -rf "${_tmp}"
}

trap "cleanup" EXIT SIGINT

mkdir -p ${_tmp}
cp ${PROTO_ROOT}/types.pb.go ${_tmp}

ret=0
hack/update-generated-hyperd-grpc-types.sh
diff -I "gzipped FileDescriptorProto" -I "0x" -Naupr ${_tmp}/types.pb.go ${PROTO_ROOT}/types.pb.go || ret=$?
if [[ $ret -eq 0 ]]; then
    echo "Generated hyperd grpc types from proto up to date."
else
    echo "Generated hyperd grpc types from proto is out of date. Please run hack/update-generated-hyperd-grpc-types.sh"
    exit 1
fi
