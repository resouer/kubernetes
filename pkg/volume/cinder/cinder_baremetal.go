/*
Copyright 2015 The Kubernetes Authors All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cinder

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/golang/glog"
	"github.com/rackspace/gophercloud"
	"github.com/rackspace/gophercloud/openstack/blockstorage/v2/extensions/volumeactions"
)

type CinderBaremetalUtil struct {
	client             *cinderClient
	hostname           string
	isNoMountSupported bool
}

func (cb *CinderBaremetalUtil) AttachDiskBaremetal(b *cinderVolumeMounter, globalPDPath string) error {
	glog.V(4).Infof("Begin to attach volume %v", b.pdName)
	volume, err := cb.client.getVolume(b.pdName)
	if err != nil {
		glog.Errorf("Get volume %s error: %v", b.pdName, err)
		return err
	}

	var attached bool
	if len(volume.Attachments) > 0 || volume.Status != "available" {
		for _, att := range volume.Attachments {
			if att["host_name"].(string) == cb.hostname && att["device"].(string) == b.GetPath() {
				glog.V(5).Infof("Volume %s is already attached", b.pdName)
				attached = true
				break
			}
		}

		if !attached {
			return fmt.Errorf("Volume %s is not available", b.pdName)
		}
	}

	connectionInfo, err := cb.client.getConnectionInfo(volume.ID, cb.getConnectionOptions())
	if err != nil {
		return err
	}

	volumeType := connectionInfo["driver_volume_type"].(string)
	data := connectionInfo["data"].(map[string]interface{})
	data["volume_type"] = volumeType
	if volumeType == "rbd" {
		data["keyring"] = cb.client.keyring
	}
	b.cinderVolume.metadata = data

	// already attached, just return
	if attached {
		return nil
	}

	mountMode := volumeactions.ReadWrite
	if b.readOnly {
		mountMode = volumeactions.ReadOnly
	}

	// attach volume
	attachOpts := volumeactions.AttachOpts{
		MountPoint: b.GetPath(),
		Mode:       mountMode,
		HostName:   cb.hostname,
	}

	err = cb.client.attach(volume.ID, attachOpts)
	if err != nil && err.Error() != "EOF" {
		return err
	}

	cinderDriver, err := GetCinderDriver(volumeType)
	if err != nil {
		glog.Warningf("Get cinder driver %s failed: %v", volumeType, err)
		cb.client.detach(volume.ID)
		return err
	}

	err = cinderDriver.Format(data, b.fsType)
	if err != nil {
		glog.Warningf("Format cinder volume %s failed: %v", b.pdName, err)
		cb.client.detach(volume.ID)
		return err
	}

	if cb.isNoMountSupported && volumeType == "rbd" {
		glog.V(4).Infof("Volume %s willn't be mounted on host since rbd is natively supported",
			volume.Name)
	} else {
		err = cinderDriver.Attach(data, globalPDPath)
		if err != nil {
			cb.client.detach(volume.ID)
			return err
		}
	}

	return nil
}

// Unmounts the device and detaches the disk from the kubelet's host machine.
func (cb *CinderBaremetalUtil) DetachDiskBaremetal(cd *cinderVolumeUnmounter, globalPDPath string) error {
	volume, err := cb.client.getVolume(cd.pdName)
	if err != nil {
		if eo, ok := err.(*gophercloud.UnexpectedResponseCodeError); ok && eo.Actual == http.StatusNotFound{
			glog.V(1).Infof("detach: volume %s (%s) not found", cd.pdName, cd.volName)
			return nil
		}
		return err
	}

	connectionInfo, err := cb.client.getConnectionInfo(volume.ID, cb.getConnectionOptions())
	if err != nil {
		return err
	}

	volumeType := connectionInfo["driver_volume_type"].(string)
	cinderDriver, err := GetCinderDriver(volumeType)
	if err != nil {
		glog.Warningf("Get cinder driver %s failed: %v", volumeType, err)
		return err
	}
	data := connectionInfo["data"].(map[string]interface{})
	if volumeType == "rbd" {
		data["keyring"] = cb.client.keyring
	}

	if cb.isNoMountSupported && volumeType == "rbd" {
		glog.V(4).Infof("Volume %s is not mounted since rbd is natively supported", volume.Name)
	} else {
		err = cinderDriver.Detach(data, globalPDPath)
		if err != nil {
			return err
		}
	}

	err = cb.client.terminateConnection(volume.ID, cb.getConnectionOptions())
	if err != nil {
		return err
	}

	if volume.Status == "available" {
		return nil
	}

	err = cb.client.detach(volume.ID)
	if err != nil {
		return err
	}

	return nil
}

// Get iscsi initiator
func (cb *CinderBaremetalUtil) getIscsiInitiator() string {
	contents, err := ioutil.ReadFile("/etc/iscsi/initiatorname.iscsi")
	if err != nil {
		return ""
	}

	lines := strings.Split(string(contents), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "InitiatorName=") {
			return strings.Split(line, "=")[1]
		}
	}

	return ""
}

// Get cinder connections options
func (cb *CinderBaremetalUtil) getConnectionOptions() *volumeactions.ConnectorOpts {
	connector := volumeactions.ConnectorOpts{
		Host:      cb.hostname,
		Initiator: cb.getIscsiInitiator(),
	}

	return &connector
}
