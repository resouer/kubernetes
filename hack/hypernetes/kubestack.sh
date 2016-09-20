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

function kube::util::setup_kubestack() {
	echo "Start $FUNCNAME"
	
	mkdir -p $GO_HYPERHQ_ROOT
	kube::util::clone_git_repo https://github.com/hyperhq/kubestack.git $GO_HYPERHQ_ROOT/kubestack/
	cd $GO_HYPERHQ_ROOT/kubestack && make && make install

	## Configure KubeStack
	source /root/keystonerc_admin
	EXT_NET_ID=$(neutron net-show br-ex | awk '/ id /{print $4}')
	rm -rf /etc/kubestack
	mkdir /etc/kubestack/
	cat > /etc/kubestack/kubestack.conf <<EOF
[Global]
auth-url = http://${IF_IP}:5000/v2.0
username = admin
password = admin
tenant-name = admin
region = RegionOne
ext-net-id = ${EXT_NET_ID}

[LoadBalancer]
create-monitor = yes
monitor-delay = 1m
monitor-timeout = 30s
monitor-max-retries = 3

[Plugin]
plugin-name = ovs
EOF

	cat > /usr/lib/systemd/system/kubestack.service <<EOF
[Unit]
Description=OpenStack Network Provider for Hypernetes
After=syslog.target network.target openvswitch.service
Requires=openvswitch.service

[Service]
ExecStart=/usr/local/bin/kubestack \
  -logtostderr=false -v=4 \
  -port=127.0.0.1:4237 \
  -log_dir=/var/log/kubestack \
  -conf=/etc/kubestack/kubestack.conf
Restart=on-failure

[Install]
WantedBy=multi-user.target
EOF

	rm -rf /var/log/kubestack
	mkdir -p /var/log/kubestack
	systemctl enable kubestack.service
	systemctl daemon-reload
	systemctl start kubestack.service
}

