# Hypernetes [![Build Status](https://travis-ci.org/hyperhq/hypernetes.svg?branch=master)](https://travis-ci.org/hyperhq/hypernetes)

## What is Hypernetes?

Hypernetes is a secure, multi-tenant [Kubernetes](http://kubernetes.io) distro. Simply put,

Hypernetes = Bare-metal + [Hyper](https://hyper.sh) + Kubernetes + [KeyStone](https://github.com/openstack/keystone) + [Cinder](https://github.com/openstack/cinder) + [Neutron](https://github.com/openstack/neutron).

## What Hypernetes does?

Hypernetes envisions a future of ***"Container-as-a-Service without IaaS"***. The idea is to combine the orchestration power in Kubernetes and [the runtime isolation in Hyper](https://hyper.sh/why-hyper.html) to build the truly secure multi-tenant CaaS platform. 

![](https://trello-attachments.s3.amazonaws.com/55545e127c7cbe0ec5b82f2b/1660x705/895000bf0d7e25aee600d3cfaf0fd3f2/upload_10_19_2015_at_3_02_11_PM.png)

Hypernetes also integrates with a number of OpenStack projects:

- Keystone (Authentication)
- Neutron (SDN)
- Cinder/Ceph (Persistent Storage)

## How Hypernetes works?

![Architecture Diagram](architecture.png?raw=true "Architecture overview")

## Getting Started

Please check [h8s getting started guide](hack/hypernetes/README.md) to play with Hypernetes.

## Find out more

* [Documentation](https://github.com/hyperhq/hypernetes-book)
* [Get Started](https://github.com/hyperhq/hypernetes-book/blob/master/user-guide/user-guide.md)

## Contact Us

* Twitter: [@hyper_sh](https://twitter.com/hyper_sh)
* Blog: [https://blog.hyper.sh](https://blog.hyper.sh)
* Slack: [#hyper](https://slack.hyper.sh/) (The IRC has been migrated to slack.)

[![Analytics](https://kubernetes-site.appspot.com/UA-36037335-10/GitHub/README.md?pixel)]()

