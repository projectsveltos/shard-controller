/*
Copyright 2024. projectsveltos.io. All rights reserved.

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

package fv_test

import (
	"context"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/projectsveltos/libsveltos/lib/k8s_utils"
	controllerSharding "github.com/projectsveltos/shard-controller/pkg/sharding"
)

const (
	shardComponentsConfigFlag = "--shard-components-config"
	addLabel                  = "add-label"
)

var _ = Describe("Shard components patch", Serial, func() {
	var configMapName string

	AfterEach(func() {
		if configMapName != "" {
			Byf("Deleting ConfigMap %s/%s", sveltosNamespace, configMapName)
			cm := &corev1.ConfigMap{}
			err := k8sClient.Get(context.TODO(),
				types.NamespacedName{Namespace: sveltosNamespace, Name: configMapName}, cm)
			if err == nil {
				Expect(k8sClient.Delete(context.TODO(), cm)).To(Succeed())
			}
			configMapName = ""
		}

		By("Removing --shard-components-config from shard-controller args")
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			shardController := &appsv1.Deployment{}
			if err := k8sClient.Get(context.TODO(),
				types.NamespacedName{Namespace: sveltosNamespace, Name: shardControllerName},
				shardController); err != nil {
				return err
			}
			for i := range shardController.Spec.Template.Spec.Containers {
				container := &shardController.Spec.Template.Spec.Containers[i]
				if container.Name != kubeRbacProxy {
					container.Args = removeShardComponentsConfigArg(container)
				}
			}
			return k8sClient.Update(context.TODO(), shardController)
		})
		Expect(err).To(BeNil())

		By("Resetting cluster shard annotation to empty")
		err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			currentCluster := &clusterv1.Cluster{}
			if err := k8sClient.Get(context.TODO(),
				types.NamespacedName{Namespace: kindWorkloadCluster.Namespace, Name: kindWorkloadCluster.Name},
				currentCluster); err != nil {
				return err
			}
			currentCluster.Annotations[libsveltosv1beta1.ShardAnnotation] = ""
			return k8sClient.Update(context.TODO(), currentCluster)
		})
		Expect(err).To(BeNil())
	})

	It("applies ConfigMap patches to all shard deployments", Label("FV"), func() {
		configMapName = randomString()
		labelKey := "shard-fv-test"
		labelValue := "patched"

		patchJSON := fmt.Sprintf(`[{"op":"add","path":"/metadata/labels/%s","value":%q}]`,
			labelKey, labelValue)
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      configMapName,
				Namespace: sveltosNamespace,
			},
			Data: map[string]string{
				addLabel: fmt.Sprintf("patch: '%s'", patchJSON),
			},
		}
		Byf("Creating ConfigMap %s/%s with label-injection patch", sveltosNamespace, configMapName)
		Expect(k8sClient.Create(context.TODO(), cm)).To(Succeed())

		Byf("Adding %s=%s to shard-controller args", shardComponentsConfigFlag, configMapName)
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			shardController := &appsv1.Deployment{}
			if err := k8sClient.Get(context.TODO(),
				types.NamespacedName{Namespace: sveltosNamespace, Name: shardControllerName},
				shardController); err != nil {
				return err
			}
			for i := range shardController.Spec.Template.Spec.Containers {
				container := &shardController.Spec.Template.Spec.Containers[i]
				if container.Name == managerContainerName {
					container.Args = removeShardComponentsConfigArg(container)
					container.Args = append(container.Args,
						fmt.Sprintf("%s=%s", shardComponentsConfigFlag, configMapName))
					break
				}
			}
			return k8sClient.Update(context.TODO(), shardController)
		})
		Expect(err).To(BeNil())

		shard := randomString()
		Byf("Update Cluster shard annotation to %s", shard)
		currentCluster := &clusterv1.Cluster{}
		Expect(k8sClient.Get(context.TODO(),
			types.NamespacedName{Namespace: kindWorkloadCluster.Namespace, Name: kindWorkloadCluster.Name},
			currentCluster)).To(Succeed())
		currentCluster.Annotations[libsveltosv1beta1.ShardAnnotation] = shard
		Expect(k8sClient.Update(context.TODO(), currentCluster)).To(Succeed())

		verifyAnnotation(shard)

		Byf("Verifying all shard deployments for %s have label %s=%s", shard, labelKey, labelValue)
		verifyDeploymentPresenceWithPatch(shard, labelKey, labelValue)

		Byf("Resetting shard annotation")
		Expect(k8sClient.Get(context.TODO(),
			types.NamespacedName{Namespace: kindWorkloadCluster.Namespace, Name: kindWorkloadCluster.Name},
			currentCluster)).To(Succeed())
		currentCluster.Annotations[libsveltosv1beta1.ShardAnnotation] = ""
		Expect(k8sClient.Update(context.TODO(), currentCluster)).To(Succeed())

		Byf("Verifying projectsveltos deployments are gone for shard %s", shard)
		verifyDeploymentsAreGone(shard)
	})

	It("re-applies updated patches when ConfigMap is modified", Label("FV"), func() {
		configMapName = randomString()
		labelKey := "shard-fv-update-test"
		initialValue := "v1"
		updatedValue := "v2"

		patchJSON := fmt.Sprintf(`[{"op":"add","path":"/metadata/labels/%s","value":%q}]`,
			labelKey, initialValue)
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      configMapName,
				Namespace: sveltosNamespace,
			},
			Data: map[string]string{
				addLabel: fmt.Sprintf("patch: '%s'", patchJSON),
			},
		}
		Byf("Creating ConfigMap %s/%s with initial patch value %s", sveltosNamespace, configMapName, initialValue)
		Expect(k8sClient.Create(context.TODO(), cm)).To(Succeed())

		Byf("Adding %s=%s to shard-controller args", shardComponentsConfigFlag, configMapName)
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			shardController := &appsv1.Deployment{}
			if err := k8sClient.Get(context.TODO(),
				types.NamespacedName{Namespace: sveltosNamespace, Name: shardControllerName},
				shardController); err != nil {
				return err
			}
			for i := range shardController.Spec.Template.Spec.Containers {
				container := &shardController.Spec.Template.Spec.Containers[i]
				if container.Name == managerContainerName {
					container.Args = removeShardComponentsConfigArg(container)
					container.Args = append(container.Args,
						fmt.Sprintf("%s=%s", shardComponentsConfigFlag, configMapName))
					break
				}
			}
			return k8sClient.Update(context.TODO(), shardController)
		})
		Expect(err).To(BeNil())

		shard := randomString()
		Byf("Update Cluster shard annotation to %s", shard)
		currentCluster := &clusterv1.Cluster{}
		Expect(k8sClient.Get(context.TODO(),
			types.NamespacedName{Namespace: kindWorkloadCluster.Namespace, Name: kindWorkloadCluster.Name},
			currentCluster)).To(Succeed())
		currentCluster.Annotations[libsveltosv1beta1.ShardAnnotation] = shard
		Expect(k8sClient.Update(context.TODO(), currentCluster)).To(Succeed())

		Byf("Verifying shard deployments have initial label value %s", initialValue)
		verifyDeploymentPresenceWithPatch(shard, labelKey, initialValue)

		Byf("Updating ConfigMap patch to label value %s", updatedValue)
		updatedPatchJSON := fmt.Sprintf(`[{"op":"add","path":"/metadata/labels/%s","value":%q}]`,
			labelKey, updatedValue)
		err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			currentCM := &corev1.ConfigMap{}
			if err := k8sClient.Get(context.TODO(),
				types.NamespacedName{Namespace: sveltosNamespace, Name: configMapName}, currentCM); err != nil {
				return err
			}
			currentCM.Data[addLabel] = fmt.Sprintf("patch: '%s'", updatedPatchJSON)
			return k8sClient.Update(context.TODO(), currentCM)
		})
		Expect(err).To(BeNil())

		Byf("Verifying shard deployments are re-patched with updated label value %s", updatedValue)
		verifyDeploymentPresenceWithPatch(shard, labelKey, updatedValue)

		Byf("Resetting shard annotation")
		Expect(k8sClient.Get(context.TODO(),
			types.NamespacedName{Namespace: kindWorkloadCluster.Namespace, Name: kindWorkloadCluster.Name},
			currentCluster)).To(Succeed())
		currentCluster.Annotations[libsveltosv1beta1.ShardAnnotation] = ""
		Expect(k8sClient.Update(context.TODO(), currentCluster)).To(Succeed())

		Byf("Verifying projectsveltos deployments are gone for shard %s", shard)
		verifyDeploymentsAreGone(shard)
	})
})

func verifyDeploymentPresenceWithPatch(shardKey, labelKey, labelValue string) {
	templates := [][]byte{
		controllerSharding.GetAddonControllerTemplate(),
		controllerSharding.GetClassifierTemplate(),
		controllerSharding.GetEventManagerTemplate(),
		controllerSharding.GetHealthCheckManagerTemplate(),
		controllerSharding.GetSveltosClusterManagerTemplate(),
	}
	for _, tmpl := range templates {
		verifyDeploymentWithPatch(tmpl, sveltosNamespace, shardKey, labelKey, labelValue)
	}
}

func verifyDeploymentWithPatch(deplTemplate []byte, namespace, shardKey, labelKey, labelValue string) {
	data, err := instantiateTemplate(deplTemplate, shardKey)
	Expect(err).To(BeNil())

	u, err := k8s_utils.GetUnstructured(data)
	Expect(err).To(BeNil())

	Eventually(func() bool {
		currentDeployment := &appsv1.Deployment{}
		err = k8sClient.Get(context.TODO(),
			types.NamespacedName{Namespace: namespace, Name: u.GetName()},
			currentDeployment)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return false
			}
			return false
		}
		labels := currentDeployment.GetLabels()
		return labels[labelKey] == labelValue
	}, timeout, pollingInterval).Should(BeTrue(),
		fmt.Sprintf("deployment %s/%s should have label %s=%s", namespace, u.GetName(), labelKey, labelValue))
}

func removeShardComponentsConfigArg(container *corev1.Container) []string {
	filtered := make([]string, 0, len(container.Args))
	for _, arg := range container.Args {
		if !strings.HasPrefix(arg, shardComponentsConfigFlag) {
			filtered = append(filtered, arg)
		}
	}
	return filtered
}
