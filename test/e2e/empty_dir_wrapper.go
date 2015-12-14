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

package e2e

import (
	"fmt"

	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/util"

	. "github.com/onsi/ginkgo"
)

var _ = Describe("Pod with multiple empty_dir wrapper volumes", func() {
	f := NewFramework("secrets")

	It("should becomes running [Conformance]", func() {
		name := "secret-test-" + string(util.NewUUID())
		volumeName := "secret-volume"
		volumeMountPath := "/etc/secret-volume"

		secret := &api.Secret{
			ObjectMeta: api.ObjectMeta{
				Namespace: f.Namespace.Name,
				Name:      name,
			},
			Data: map[string][]byte{
				"data-1": []byte("value-1\n"),
				"data-2": []byte("value-2\n"),
				"data-3": []byte("value-3\n"),
			},
		}

		By(fmt.Sprintf("Creating secret with name %s", secret.Name))
		defer func() {
			By("Cleaning up the secret")
			if err := f.Client.Secrets(f.Namespace.Name).Delete(secret.Name); err != nil {
				Failf("unable to delete secret %v: %v", secret.Name, err)
			}
		}()
		var err error
		if secret, err = f.Client.Secrets(f.Namespace.Name).Create(secret); err != nil {
			Failf("unable to create test secret %s: %v", secret.Name, err)
		}

		gitVolumeName := "git-volume"
		gitVolumeMountPath := "/etc/git-volume"

		pod := &api.Pod{
			ObjectMeta: api.ObjectMeta{
				Name: "pod-secrets-" + string(util.NewUUID()),
			},
			Spec: api.PodSpec{
				Volumes: []api.Volume{
					{
						Name: volumeName,
						VolumeSource: api.VolumeSource{
							Secret: &api.SecretVolumeSource{
								SecretName: name,
							},
						},
					},
					{
						Name: gitVolumeName,
						VolumeSource: api.VolumeSource{
							GitRepo: &api.GitRepoVolumeSource{
								Repository: "https://github.com/kubernetes/kubedash",
							},
						},
					},
				},
				Containers: []api.Container{
					{
						Name:  "secret-test",
						Image: "gcr.io/google_containers/mounttest:0.2",
						Args: []string{
							"sleep",
							"1000000"},
						VolumeMounts: []api.VolumeMount{
							{
								Name:      volumeName,
								MountPath: volumeMountPath,
								ReadOnly:  true,
							},
							{
								Name:      gitVolumeName,
								MountPath: gitVolumeMountPath,
							},
						},
					},
				},
			},
		}

		defer func() {
			By("Cleaning up the pod")
			if err = f.Client.Pods(f.Namespace.Name).Delete(pod.Name, api.NewDeleteOptions(0)); err != nil {
				Failf("unable to delete pod %v: %v", pod.Name, err)
			}
		}()

		if _, err = f.Client.Pods(f.Namespace.Name).Create(pod); err != nil {
			Failf("unable to create pod %v: %v", pod.Name, err)
		}

		expectNoError(waitForPodRunningInNamespace(f.Client, pod.Name, f.Namespace.Name))
	})
})
