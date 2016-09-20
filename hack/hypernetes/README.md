# Deployment scripts for hypernetes

## System requirements

The script is only tested on CentOS 7.2 with at least 4GB memory. It will install latest hypernetes with hyperd and OpenStack Mitaka.

Note: The script should be run as `root`.

## Usage

1. Setup environment variables:

```sh
export HOSTNAME="k8s"
export IF_NAME="eth0"
export IF_IP=""
export GOPATH="/gopath"
```

if you want use your own kebestack\hyperd\runv\hyperstart, you cloud set DEV_MODE to "y"(`export DEV_MODE="y"`) to use your own repo in $GOPATH.

2. Clone hypernetes to go path:

```sh
mkdir -p ${GOPATH}/src/k8s.io
# Run 'yum -y install git' if git is not installed.
git clone https://github.com/hyperhq/hypernetes.git ${GOPATH}/src/k8s.io/kubernetes
```

3. Run the script to install hypernetes

```sh
cd ${GOPATH}/src/k8s.io/kubernetes
hack/local-up-hypernetes.sh
```

## QA

### Upgrade CentOS 7.0/7.1 to 7.2

Run following scripts and reboot the system:

```sh
yum clean all
yum -y update
```

### Install failed due to slow network

For slow networks, the installation may fail with timeout. When this happens, just comment out successed steps from `hack/local-up-hypernetes.sh` and re-run the script to continue the installation.

[![Analytics](https://kubernetes-site.appspot.com/UA-36037335-10/GitHub/hack/hypernetes/README.md?pixel)]()
