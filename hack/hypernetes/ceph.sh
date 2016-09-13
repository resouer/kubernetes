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

function kube::util::setup_ceph() {
	echo "Start $FUNCNAME"
	rm -rf /etc/ceph
	mkdir -p /etc/ceph
	if ! kube::util::do_setup_ceph ; then
		echo "Preinstall some package before exectue ceph-deploy..."
		kube::util::ceph_preinstall
		kube::util::do_setup_ceph
	fi
}

function kube::util::do_setup_ceph() {
	echo "Start $FUNCNAME"

	kube::util::ensure_yum_ready

	yum -y install python-pip
	pip install --upgrade ceph-deploy

	rm -rf /root/ceph-cluster
	mkdir /root/ceph-cluster
	cd /root/ceph-cluster
	ceph-deploy new $HOSTNAME
	echo "osd pool default size = 1" >> ceph.conf

	ceph-deploy install $HOSTNAME

	ceph-deploy --overwrite-conf mon create-initial
	if [ "$?" != "0" ]; then
	    sleep 2
	    ceph-deploy --overwrite-conf mon create-initial
	fi

	rm -rf /var/local/osd
	mkdir -p /var/local/osd
	chown ceph:ceph /var/local/osd -R
	ceph-deploy --overwrite-conf osd prepare $HOSTNAME:/var/local/osd
	ceph-deploy osd activate  $HOSTNAME:/var/local/osd

	ceph-deploy mds create $HOSTNAME
	ceph-deploy admin $HOSTNAME
}

function kube::util::setup_cinder() {
	echo "Start $FUNCNAME"
	
	## create pool for cinder
	ceph osd pool create cinder 256 256
	ceph auth get-or-create client.cinder mon 'allow r' osd 'allow class-read object_prefix rbd_children, allow rwx pool=cinder'

	## auth for cinder
	ceph auth get-or-create client.cinder | tee /etc/ceph/ceph.client.cinder.keyring
	chown root:cinder /etc/ceph/ceph.client.cinder.keyring


	## cinder conf
	RBD_SECRET_UUID=$(uuidgen)
	cat >> /etc/cinder/cinder.conf <<EOF
[ceph]
volume_driver = cinder.volume.drivers.rbd.RBDDriver
rbd_pool = cinder
rbd_ceph_conf = /etc/ceph/ceph.conf
rbd_flatten_volume_from_snapshot = false
rbd_max_clone_depth = 5
rbd_store_chunk_size = 4
rados_connect_timeout = -1
glance_api_version = 2
rbd_user = cinder
rbd_secret_uuid = $RBD_SECRET_UUID
EOF

	sed -i 's/^enabled_backends.*$/enabled_backends = ceph/g' /etc/cinder/cinder.conf

	systemctl | awk '/cinder/{print $1}' | while read line; do
	    systemctl restart $line
	done
}

# actually we don't need this function in a good network environment,
# just try again with other ceph repo location before abort the mission
function kube::util::ceph_preinstall() {
	kube::util::ensure_yum_ready
	yum -y install epel-release ceph-radosgw ceph
}
