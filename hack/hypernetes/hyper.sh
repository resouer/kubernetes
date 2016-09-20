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

CENTOS_HYPER="hyper-container-0.6-1.el7.centos.x86_64"

function kube::util::setup_hyperd() {
	echo "Start $FUNCNAME"

	yum install -y libvirt
	#yum install qemu-system-x86 seabios
	cat >> /etc/libvirt/qemu.conf <<EOF
user = "root"
group = "root"
clear_emulator_capabilities = 0
EOF

	if ! rpm -qa | grep ${CENTOS_HYPER} &>/dev/null ; then
		curl -sSl http://hypercontainer.io/install | bash
	fi

	cat >/etc/hyper/config <<EOF
Kernel=/var/lib/hyper/kernel
Initrd=/var/lib/hyper/hyper-initrd.img
DisableIptables=true
StorageDriver=devicemapper
Hypervisor=libvirt
gRPCHost=127.0.0.1:22318
EOF

	# Start libvirtd and hyperd services
	systemctl restart libvirtd
	systemctl enable libvirtd
	systemctl restart hyperd
	systemctl enable hyperd
}

function kube::util::upgrade_hyperd() {
	echo "Start $FUNCNAME"
	
	yum -y install git cmake gcc g++ autoconf automake device-mapper-devel sqlite-devel pcre-devel libsepol-devel libselinux-devel systemd-container-devel automake autoconf gcc make glibc-devel glibc-devel.i686 libvirt-devel

	mkdir -p ${GO_HYPERHQ_ROOT}

	kube::util::git_clone_repo https://github.com/hyperhq/hyperd.git ${GO_HYPERHQ_ROOT}/hyperd
	kube::util::git_clone_repo https://github.com/hyperhq/runv.git ${GO_HYPERHQ_ROOT}/runv
	kube::util::git_clone_repo https://github.com/hyperhq/hyperstart.git ${GO_HYPERHQ_ROOT}/hyperstart
	cd ${GO_HYPERHQ_ROOT}/hyperd
	./autogen.sh && ./configure --prefix=/usr && make && make install

	cd ${GO_HYPERHQ_ROOT}/hyperstart
	./autogen.sh && ./configure && make && /bin/cp build/{hyper-initrd.img,kernel} /var/lib/hyper/

	systemctl restart hyperd
}
