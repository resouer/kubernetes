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

function kube::util::setup_openstack() {
	echo "Start $FUNCNAME"
	
	yum install -y centos-release-openstack-mitaka
	yum update -y
	yum install -y openstack-packstack

	packstack --gen-answer-file=/root/packstack_answer_file.txt

	sed -i "s/CONFIG_NOVA_NETWORK_PUBIF=eth0/CONFIG_NOVA_NETWORK_PUBIF=${IF_NAME}/g" /root/packstack_answer_file.txt
	sed -i 's/CONFIG_PROVISION_DEMO=y/CONFIG_PROVISION_DEMO=n/g' /root/packstack_answer_file.txt
	sed -i 's/CONFIG_SWIFT_INSTALL=y/CONFIG_SWIFT_INSTALL=n/g' /root/packstack_answer_file.txt
	sed -i 's/CONFIG_NEUTRON_METERING_AGENT_INSTALL=n/CONFIG_NEUTRON_METERING_AGENT_INSTALL=y/g' /root/packstack_answer_file.txt
	sed -i 's/CONFIG_NAGIOS_INSTALL=y/CONFIG_NAGIOS_INSTALL=n/g' /root/packstack_answer_file.txt
	sed -i 's/CONFIG_KEYSTONE_ADMIN_PW=.*/CONFIG_KEYSTONE_ADMIN_PW=admin/g' /root/packstack_answer_file.txt
	sed -i 's/CONFIG_KEYSTONE_DEMO_PW=.*/CONFIG_KEYSTONE_DEMO_PW=demo/g' /root/packstack_answer_file.txt
	sed -i 's/CONFIG_LBAAS_INSTALL=n/CONFIG_LBAAS_INSTALL=y/g' /root/packstack_answer_file.txt
	sed -i 's/CONFIG_NEUTRON_FWAAS=n/CONFIG_NEUTRON_FWAAS=y/g' /root/packstack_answer_file.txt
	sed -i 's/CONFIG_NOVA_INSTALL=y/CONFIG_NOVA_INSTALL=n/g' /root/packstack_answer_file.txt
	sed -i 's/CONFIG_CEILOMETER_INSTALL=y/CONFIG_CEILOMETER_INSTALL=n/g' /root/packstack_answer_file.txt
	sed -i 's/CONFIG_AODH_INSTALL=y/CONFIG_AODH_INSTALL=n/g' /root/packstack_answer_file.txt
	sed -i 's/CONFIG_GNOCCHI_INSTALL=y/CONFIG_GNOCCHI_INSTALL=n/g' /root/packstack_answer_file.txt
	sed -i 's/CONFIG_GLANCE_INSTALL=y/CONFIG_GLANCE_INSTALL=n/g' /root/packstack_answer_file.txt

	# Install OpenStack
	packstack --answer-file=/root/packstack_answer_file.txt

	## Create external network
	source /root/keystonerc_admin
	neutron net-create --router:external br-ex
	neutron subnet-create br-ex 58.215.33.0/24
	sed -i 's/#dns_domain = openstacklocal/dns_domain = hypernetes/g' /etc/neutron/neutron.conf
}
