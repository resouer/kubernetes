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

# Should setup those environment variables before running this scripts.
export HOSTNAME=${HOSTNAME:-"k8s"}
export IF_NAME=${IF_NAME:-"eth0"}
export IF_IP=${IF_IP:-""}
export GOPATH=${GOPATH:-"/gopath"}
export PATH="$PATH:$GOPATH/bin:/usr/local/bin:/usr/local/go/bin/"
export DEV_MODE=${DEV_MODE:-"n"}

# Set env used in script
GO_HYPERHQ_ROOT=${GOPATH}/src/github.com/hyperhq
GO_K8S_ROOT=${GOPATH}/src/k8s.io

# Import essential files
SCRIT_ROOT=$(dirname "${BASH_SOURCE}")
source ${SCRIT_ROOT}/hypernetes/util.sh
source ${SCRIT_ROOT}/hypernetes/openstack.sh
source ${SCRIT_ROOT}/hypernetes/ceph.sh
source ${SCRIT_ROOT}/hypernetes/hyper.sh
source ${SCRIT_ROOT}/hypernetes/hypernetes.sh
source ${SCRIT_ROOT}/hypernetes/kubestack.sh

if [[ "${IF_IP}" == "" ]]; then
	local_ip=$(kube::util::get_ip)
	export IF_IP=${local_ip}
fi

# Verify system
kube::util::verify_system

# Common setup
kube::util::setup_hostname
kube::util::setup_ssh
kube::util::setup_network
kube::util::setup_golang

# Install packages
kube::util::setup_openstack
kube::util::setup_ceph
kube::util::setup_cinder
kube::util::setup_kubestack
kube::util::setup_hyperd
#kube::util::upgrade_hyperd
kube::util::download_hypernetes
kube::util::setup_hypernetes
kube::util::setup_kubectl

echo "Local up hypernetes done."
echo "Check examples/hypernetes-demo.sh for demo usage of hypernetes."
echo "Check https://github.com/hyperhq/hypernetes-book for more documentation."
