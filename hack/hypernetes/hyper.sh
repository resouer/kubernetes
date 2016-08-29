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

function kube::util::setup_hyperd() {
	echo "Start $FUNCNAME"

	yum install -y libvirt
	#yum install qemu-system-x86 seabios
	cat >> /etc/libvirt/qemu.conf <<EOF
user = "root"
group = "root"
clear_emulator_capabilities = 0
EOF

	curl -sSL http://hypercontainer.io/install | bash
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

	mkdir -p ${GOPATH}/src/github.com/hyperhq
	git clone https://github.com/hyperhq/hyperd.git ${GOPATH}/src/github.com/hyperhq/hyperd
	git clone https://github.com/hyperhq/runv.git ${GOPATH}/src/github.com/hyperhq/runv
	git clone https://github.com/hyperhq/hyperstart.git ${GOPATH}/src/github.com/hyperhq/hyperstart
	cd ${GOPATH}/src/github.com/hyperhq/hyperd
	./autogen.sh && ./configure --prefix=/usr && make && make install

	cd ${GOPATH}/src/github.com/hyperhq/hyperstart
	./autogen.sh && ./configure && make && /bin/cp build/{hyper-initrd.img,kernel} /var/lib/hyper/

	systemctl restart hyperd
}
