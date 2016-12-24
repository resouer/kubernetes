/*
Copyright 2016 The Kubernetes Authors All rights reserved.

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

package nfs

import "k8s.io/kubernetes/pkg/util/mount"

// DummyMounter provides a dummy implementation of mount.Interface
// for the NFS plugin. This implementation assumes that a NFS volume
// is handled by runtime directly and thus kubernetes should do nothing
// but to simply pass the volume to runtime.
type DummyMounter struct{}

var _ = mount.Interface(&DummyMounter{})

// NewDummyMounter returns a mount.Interface for the current system.
func NewDummyMounter() mount.Interface {
	return &DummyMounter{}
}

// Mount mounts source to target as fstype with given options.
func (dm *DummyMounter) Mount(source string, target string, fstype string, options []string) error {
	return nil
}

// Unmount unmounts given target.
func (dm *DummyMounter) Unmount(target string) error {
	return nil
}

// List returns a list of all mounted filesystems.  This can be large.
// On some platforms, reading mounts is not guaranteed consistent (i.e.
// it could change between chunked reads). This is guaranteed to be
// consistent.
func (dm *DummyMounter) List() ([]mount.MountPoint, error) {
	return nil, nil
}

// IsLikelyNotMountPoint determines if a directory is a mountpoint.
// It should return ErrNotExist when the directory does not exist.
func (dm *DummyMounter) IsLikelyNotMountPoint(file string) (bool, error) {
	return true, nil
}
