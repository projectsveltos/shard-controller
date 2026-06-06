/*
Copyright 2023. projectsveltos.io. All rights reserved.

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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/projectsveltos/libsveltos/lib/k8s_utils"
	controllerSharding "github.com/projectsveltos/shard-controller/pkg/sharding"
)

const (
	shardControllerName   = "shard-controller"
	agentInMgmtClusterArg = "--agent-in-mgmt-cluster"
	kubeRbacProxy         = "kube-rbac-proxy"
	managerContainerName  = "manager"
)

var _ = Describe("Agent in management cluster mode", Serial, func() {
	AfterEach(func() {
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
					container.Args = removeAgentInMgmtClusterArg(container)
				}
			}
			return k8sClient.Update(context.TODO(), shardController)
		})
		Expect(err).To(BeNil())
	})

	It("Start Sveltos deployment with agent-in-mgmt-cluster option", Label("FV"), func() {
		updated := false
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
					container.Args = removeAgentInMgmtClusterArg(container)
					container.Args = append(container.Args, agentInMgmtClusterArg)
					updated = true
					break
				}
			}
			return k8sClient.Update(context.TODO(), shardController)
		})
		Expect(err).To(BeNil())
		Expect(updated).To(BeTrue())

		shard := randomString()
		Byf("Update Cluster shard annotation to %s", shard)
		currentCluster := &clusterv1.Cluster{}
		Expect(k8sClient.Get(context.TODO(),
			types.NamespacedName{Namespace: kindWorkloadCluster.Namespace, Name: kindWorkloadCluster.Name},
			currentCluster)).To(Succeed())
		currentCluster.Annotations[libsveltosv1beta1.ShardAnnotation] = shard
		Expect(k8sClient.Update(context.TODO(), currentCluster)).To(Succeed())

		verifyAnnotation(shard)

		Byf("Verifying projectsveltos deployments are created for shard %s", shard)
		verifyDeploymentPresence(shard)

		By("Verify addon-controller deployment args")
		addonControllerTemplate := controllerSharding.GetAddonControllerTemplate()
		data, err := instantiateTemplate(addonControllerTemplate, shard)
		Expect(err).To(BeNil())

		deployment, err := k8s_utils.GetUnstructured(data)
		Expect(err).To(BeNil())
		deployment.SetNamespace(sveltosNamespace)

		addonDeployment := &appsv1.Deployment{}
		Expect(k8sClient.Get(context.TODO(),
			types.NamespacedName{Namespace: deployment.GetNamespace(), Name: deployment.GetName()},
			addonDeployment)).To(Succeed())

		By(fmt.Sprintf("Verifying agent-in-mgmt-cluster is set on deployment %s/%s",
			addonDeployment.GetNamespace(), addonDeployment.GetName()))
		foundContainer := false
		for i := range addonDeployment.Spec.Template.Spec.Containers {
			container := &addonDeployment.Spec.Template.Spec.Containers[i]
			if container.Name != kubeRbacProxy {
				foundContainer = true
				verifyAgentInMgmtClusterArg(container)
			}
		}
		Expect(foundContainer).To(BeTrue())
	})
})

func verifyAgentInMgmtClusterArg(container *corev1.Container) {
	found := false
	for i := range container.Args {
		arg := &container.Args[i]
		if strings.Contains(*arg, agentInMgmtClusterArg) {
			found = true
			break
		}
	}

	Expect(found).To(BeTrue())
}

func removeAgentInMgmtClusterArg(container *corev1.Container) []string {
	newArgs := make([]string, 0)
	for i := range container.Args {
		if container.Args[i] != agentInMgmtClusterArg {
			newArgs = append(newArgs, container.Args[i])
		}
	}
	return newArgs
}
