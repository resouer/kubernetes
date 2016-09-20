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

function kube::util::verify_system() {
	echo "Start $FUNCNAME"

	version=$(cat /etc/redhat-release)
	if [[ "$version" == "" ]]; then
		echo "Only CentOS 7.2 is supported."
		exit 1
	fi

	if ! [[ "$version" =~ "CentOS Linux release 7.2" ]]; then
		echo "Only CentOS 7.2 is supported."
		echo "Run 'yum -y update' upgrade the system."
		exit 1
	fi
}

function kube::util::setup_ssh() {
	echo "Start $FUNCNAME"
	local key_path="/root/.ssh/id_rsa"
        if ! [[ -e ${key_path} ]] ; then
		ssh-keygen -f ${key_path} -t rsa -N ''
	fi
	cat "${key_path}.pub" >> /root/.ssh/authorized_keys
	ssh-keyscan $HOSTNAME >> ~/.ssh/known_hosts
}

function kube::util::setup_hostname() {
	echo "Start $FUNCNAME"
	sed -i "/${HOSTNAME}/d" /etc/hosts
	echo "${IF_IP}    ${HOSTNAME}" >> /etc/hosts
	hostnamectl set-hostname ${HOSTNAME}
}

function kube::util::setup_network() {
	echo "Start $FUNCNAME"
	iptables -F
	iptables -X
	sed -i 's/^SELINUX=.*$/SELINUX=disabled/g' /etc/selinux/config
	if type sestatus &>/dev/null && sestatus | grep -i "Current mode" | grep enforcing ; then
		setenforce 0
	fi
	cat >> /etc/sysctl.conf <<EOF
net.ipv4.ip_forward=1
fs.file-max=1000000
net.ipv4.tcp_keepalive_intvl=1
net.ipv4.tcp_keepalive_time=5
net.ipv4.tcp_keepalive_probes=5
EOF

	sysctl -p

	cat > /etc/security/limits.conf <<EOF
* soft nofile 65535
* hard nofile 65535
EOF
}

function kube::util::install_golang() {
	echo "Start $FUNCNAME"
	yum -y install git
	curl -L https://storage.googleapis.com/golang/go1.6.3.linux-amd64.tar.gz | tar -C /usr/local -zxf -
	echo 'export GOPATH=/gopath/' >> /root/.bashrc
	echo 'export PATH=$PATH:$GOPATH/bin:/usr/local/bin:/usr/local/go/bin/' >> /root/.bashrc
	go get github.com/tools/godep
}

function kube::util::setup_golang() {
	echo "Start $FUNCNAME"
	if [[ ! -z "$(which go)" ]]; then
		go_version=($(go version))
		if [[ "${go_version[2]}" < "go1.6" && "${go_version[2]}" != "devel" ]]; then
			echo "GO version is outdated."
			kube::util::install_golang
		fi
	else
		kube::util::install_golang
	fi
}

function kube::util::upgrade_centos() {
	# Upgrade CentOS to latest version if not CentOS 7.2.
	echo "Start $FUNCNAME"
	yum clean all
	yum -y update
	echo "Do not forget to reboot the system."
}

function kube::util::get_ip() {
	ifconfig ${IF_NAME} | awk '/inet /{print $2}'
}

function kube::util::ensure_yum_ready() {
	if pgrep yum 2>&1 1>/dev/null; then
		rm -r /var/run/yum.pid
	fi
	sleep 3
}

function kube::util::clone_git_repo() {
	local repo_url=$1
	local repo_path=$2
	if [[ -d  ${repo_path} ]] && (cd $repo_path && git status 2>&1 1>/dev/null) && [ ${DEV_MODE} = "y" ] ; then
		echo "Git repo ${repo_path} already exist, leave it unchange in dev mode"
	else
		rm -rf ${repo_path}
		git clone ${repo_url} ${repo_path}
	fi
}

