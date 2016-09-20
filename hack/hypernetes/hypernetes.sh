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

function kube::util::download_hypernetes() {
	echo "Start $FUNCNAME"
	yum -y install kubernetes etcd
	curl -p -SL https://github.com/hyperhq/hypernetes/releases/download/v1.2.0/kubernetes-server-linux-amd64.tar.gz -o /tmp/kubernetes-server-linux-amd64.tar.gz
	tar zxvf /tmp/kubernetes-server-linux-amd64.tar.gz -C /usr/bin/
	rm -f /tmp/kubernetes-server-linux-amd64.tar.gz
}

function kube::util::build_hypernetes() {
	echo "Start $FUNCNAME"
	cd ${GO_K8S_ROOT}/kubernetes
	hack/build-go.sh cmd/kube-proxy cmd/kube-apiserver cmd/kube-controller-manager cmd/kubelet cmd/kubectl plugin/cmd/kube-scheduler
	/bin/cp -f _output/local/bin/linux/amd64/* /usr/bin/
}

function kube::util::setup_hypernetes() {
	echo "Start $FUNCNAME"

	rm -rf /var/lib/kubernetes /srv/kubernetes /var/log/kubernetes /var/run/kubernetes
	mkdir -p /var/lib/kubernetes /srv/kubernetes  /var/log/kubernetes /var/run/kubernetes
	chown kube:kube /var/log/kubernetes/
	chown kube:kube /var/run/kubernetes/
	chown kube:kube /srv/kubernetes
	# openssl genrsa -out /var/lib/kubernetes/serviceaccount.key 2048
	# chown kube:kube /var/lib/kubernetes/serviceaccount.key
	CERT_GROUP=kube CERT_DIR=/srv/kubernetes ${GO_K8S_ROOT}/kubernetes/cluster/saltbase/salt/generate-cert/make-ca-cert.sh $(hostname -i) IP:10.254.0.1,IP:${IF_IP},DNS:kubernetes,DNS:kubernetes.default,DNS:kubernetes.default.svc,DNS:kubernetes.default.svc.cluster.local,DNS:k8s.local

	cat >> /etc/etcd/etcd.conf <<EOF
ETCD_LISTEN_CLIENT_URLS="http://${IF_IP}:2379"
EOF

	cat > /etc/kubernetes/config <<EOF
KUBE_LOGTOSTDERR="--logtostderr=false --log-dir=/var/log/kubernetes"
KUBE_LOG_LEVEL="--v=3”
KUBE_ALLOW_PRIV="--allow_privileged=false"
KUBE_MASTER="--master=http://${IF_IP}:8080"
EOF

	cat > /etc/kubernetes/apiserver <<EOF
KUBE_API_ADDRESS="--insecure-bind-address=${IF_IP}"
KUBE_API_PORT="--port=8080"
KUBELET_PORT="--kubelet_port=10250"
KUBE_ETCD_SERVERS="--etcd_servers=http://${IF_IP}:2379"
KUBE_SERVICE_ADDRESSES="--service-cluster-ip-range=10.254.0.0/16"
KUBE_ADMISSION_CONTROL="--admission_control=NamespaceLifecycle,NamespaceExists,LimitRanger,SecurityContextDeny,ServiceAccount,ResourceQuota"
KUBE_API_ARGS="--client-ca-file=/srv/kubernetes/ca.crt --tls-cert-file=/srv/kubernetes/server.cert --tls-private-key-file=/srv/kubernetes/server.key"
EOF

	cat > /etc/kubernetes/controller-manager <<EOF
KUBE_CONTROLLER_MANAGER_ARGS="--service-account-private-key-file=/srv/kubernetes/server.key  --network-provider=127.0.0.1:4237"
EOF

	cat > /etc/kubernetes/proxy <<EOF
KUBE_PROXY_ARGS="--proxy-mode=haproxy"
EOF

	cat > /etc/kubernetes/kubelet <<EOF
KUBELET_ADDRESS="--address=${IF_IP}"
KUBELET_HOSTNAME="--hostname_override=${HOSTNAME}"
KUBELET_API_SERVER="--api_servers=http://${IF_IP}:8080"
KUBELET_ARGS="--container-runtime=hyper --network-provider=127.0.0.1:4237 --cinder-config=/etc/kubernetes/cinder.conf"
EOF

	RBD_KEY=`cat /etc/ceph/ceph.client.cinder.keyring  | grep 'key = ' | awk -F\ \=\  '{print $2}'`
	cat > /etc/kubernetes/cinder.conf <<EOF
[Global]
auth-url = http://${IF_IP}:5000/v2.0
username = admin
password = admin
tenant-name = admin
region = RegionOne

[RBD]
keyring = "${RBD_KEY}"
EOF

	systemctl restart etcd
	systemctl restart kubestack
	systemctl restart kube-apiserver
	systemctl restart kube-scheduler
	systemctl restart kube-controller-manager
	systemctl restart kubelet
	systemctl restart kube-proxy

	systemctl enable etcd
	systemctl enable kubestack
	systemctl enable kube-apiserver
	systemctl enable kube-scheduler
	systemctl enable kube-controller-manager
	systemctl enable kubelet
	systemctl enable kube-proxy
}

function kube::util::setup_kubectl() {
	echo "Start $FUNCNAME"
	
	kubectl config set-cluster default --server=http://${IF_IP}:8080 --insecure-skip-tls-verify=true
	kubectl config set-context default --cluster=default
	kubectl config use-context default
}
