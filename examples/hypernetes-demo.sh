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

function create-demo-user() {
  source /root/keystonerc_admin
  openstack project create demo
  openstack user create --password demo demo
  openstack role add --project demo --user demo _member_
  cp /root/keystonerc_admin /root/keystonerc_demo
  sed -i 's/admin/demo/g' /root/keystonerc_demo
}

function create-network() {
  source /root/keystonerc_admin
  tenantID=$(openstack project show demo | awk '/id/{print $4}')
  cat | kubectl create -f - <<EOF
apiVersion: v1
kind: Network
metadata:
  name: net2
spec:
  tenantID: ${tenantID}
  subnets:
    subnet2:
      cidr: 192.168.0.0/24
      gateway: 192.168.0.1
EOF
}

function create-namespace() {
  cat | kubectl create -f - <<EOF
apiVersion: v1
kind: Namespace
metadata:
  name: ns2
spec:
  network: net2
EOF
}

function create-pod() {
  cat | kubectl create -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: busybox
  namespace: ns2
  labels:
    app: busybox
spec:
  containers:
  - name: busybox
    image: busybox
    command:
    - "top"
EOF
}

function create-volume-pod() {
  cinder create --name volume 1
  volId=$(cinder show volume | awk '/ id /{print $4}')
  cat | kubectl create -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: web
  namespace: ns2
  labels:
    app: nginx
spec:
  containers:
  - name: nginx
    image: nginx
    ports:
    - containerPort: 80
    volumeMounts:
    - name: nginx-persistent-storage
      mountPath: /var/lib/nginx
  volumes:
  - name: nginx-persistent-storage
    cinder:
      volumeID: ${volId}
      fsType: ext4
EOF
}

function create-service() {
  cat | kubectl create -f - <<EOF
apiVersion: v1
kind: ReplicationController
metadata:
  name: nginx
  namespace: ns2
  labels:
    app: nginx
spec:
  replicas: 2
  selector:
    app: nginx
  template:
    metadata:
      name: nginx
      labels:
        app: nginx
    spec:
      containers:
      - name: nginx
        image: nginx
        ports:
        - containerPort: 80
EOF

  cat | kubectl create -f - <<EOF
apiVersion: v1
kind: Service
metadata:
  name: nginx
  namespace: ns2
spec:
  type: NetworkProvider
  externalIPs:
  - 58.215.33.98
  ports:
  - port: 8078
    name: http
    targetPort: 80
    protocol: TCP
  selector:
    app: nginx
EOF
}
