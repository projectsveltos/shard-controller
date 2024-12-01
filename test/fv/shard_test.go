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
	"bytes"
	"context"
	"html/template"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	"github.com/projectsveltos/libsveltos/lib/k8s_utils"
	"github.com/projectsveltos/libsveltos/lib/sharding"
	controllerSharding "github.com/projectsveltos/shard-controller/pkg/sharding"
)

var _ = Describe("Shard", func() {
	It("React to cluster shard annotation changed", Label("FV"), func() {
		shard := randomString()
		Byf("Update Cluster shard annotation to %s", shard)
		currentCluster := &clusterv1.Cluster{}
		Expect(k8sClient.Get(context.TODO(),
			types.NamespacedName{Namespace: kindWorkloadCluster.Namespace, Name: kindWorkloadCluster.Name},
			currentCluster)).To(Succeed())
		currentCluster.Annotations[sharding.ShardAnnotation] = shard
		Expect(k8sClient.Update(context.TODO(), currentCluster)).To(Succeed())

		verifyAnnotation(shard)

		Byf("Verifying projectsveltos deployments are created for shard %s", shard)
		verifyDeploymentPresence(shard)

		newShard := randomString()
		Byf("change cluster shard to %s", newShard)
		Expect(k8sClient.Get(context.TODO(),
			types.NamespacedName{Namespace: kindWorkloadCluster.Namespace, Name: kindWorkloadCluster.Name},
			currentCluster)).To(Succeed())
		currentCluster.Annotations[sharding.ShardAnnotation] = newShard
		Expect(k8sClient.Update(context.TODO(), currentCluster)).To(Succeed())

		Byf("Verifying projectsveltos deployments are created for shard %s", newShard)
		verifyDeploymentPresence(newShard)

		Byf("Verifying projectsveltos deployments are gone for shard %s", shard)
		verifyDeploymentsAreGone(shard)

		Byf("change cluster shard to empty")
		Expect(k8sClient.Get(context.TODO(),
			types.NamespacedName{Namespace: kindWorkloadCluster.Namespace, Name: kindWorkloadCluster.Name},
			currentCluster)).To(Succeed())
		currentCluster.Annotations[sharding.ShardAnnotation] = ""
		Expect(k8sClient.Update(context.TODO(), currentCluster)).To(Succeed())

		Byf("Verifying projectsveltos deployments are gone for shard %s", newShard)
		verifyDeploymentsAreGone(newShard)
	})
})

func verifyDeploymentPresence(shardKey string) {
	By("Verify addon-controller deployment")
	addonControllerTemplate := controllerSharding.GetAddonControllerTemplate()
	verifyDeployment(addonControllerTemplate, shardKey)

	By("Verify classifier deployment")
	classifierTemplate := controllerSharding.GetClassifierTemplate()
	verifyDeployment(classifierTemplate, shardKey)

	By("Verify event-manager deployment")
	eventManagerTemplate := controllerSharding.GetEventManagerTemplate()
	verifyDeployment(eventManagerTemplate, shardKey)

	By("Verify healtchcheck-manager deployment")
	healthCheckTemplate := controllerSharding.GetHealthCheckManagerTemplate()
	verifyDeployment(healthCheckTemplate, shardKey)

	By("Verify sveltos-cluster deployment")
	sveltosClusterTemplate := controllerSharding.GetSveltosClusterManagerTemplate()
	verifyDeployment(sveltosClusterTemplate, shardKey)
}

func verifyDeployment(deplTemplate []byte, shardKey string) {
	data, err := instantiateTemplate(deplTemplate, shardKey)
	Expect(err).To(BeNil())

	deployment, err := k8s_utils.GetUnstructured(data)
	Expect(err).To(BeNil())

	Eventually(func() bool {
		currentDeployment := &appsv1.Deployment{}
		err = k8sClient.Get(context.TODO(),
			types.NamespacedName{Namespace: deployment.GetNamespace(), Name: deployment.GetName()},
			currentDeployment)
		return err == nil
	}, timeout, pollingInterval).Should(BeTrue())
}

func verifyDeploymentsAreGone(shardKey string) {
	By("Verify addon-controller deployment is gone")
	addonControllerTemplate := controllerSharding.GetAddonControllerTemplate()
	verifyDeploymentIsGone(addonControllerTemplate, shardKey)

	By("Verify classifier deployment is gone")
	classifierTemplate := controllerSharding.GetClassifierTemplate()
	verifyDeploymentIsGone(classifierTemplate, shardKey)

	By("Verify event-manager deployment is gone")
	eventManagerTemplate := controllerSharding.GetEventManagerTemplate()
	verifyDeploymentIsGone(eventManagerTemplate, shardKey)

	By("Verify healtchcheck-manager deployment is gone")
	healthCheckTemplate := controllerSharding.GetHealthCheckManagerTemplate()
	verifyDeploymentIsGone(healthCheckTemplate, shardKey)

	By("Verify sveltos-cluster deployment is gone")
	sveltosClusterTemplate := controllerSharding.GetSveltosClusterManagerTemplate()
	verifyDeploymentIsGone(sveltosClusterTemplate, shardKey)
}

func verifyDeploymentIsGone(deplTemplate []byte, shardKey string) {
	data, err := instantiateTemplate(deplTemplate, shardKey)
	Expect(err).To(BeNil())

	deployment, err := k8s_utils.GetUnstructured(data)
	Expect(err).To(BeNil())

	Eventually(func() bool {
		currentDeployment := &appsv1.Deployment{}
		err = k8sClient.Get(context.TODO(),
			types.NamespacedName{Namespace: deployment.GetNamespace(), Name: deployment.GetName()},
			currentDeployment)
		if err != nil {
			return apierrors.IsNotFound(err)
		}
		return !currentDeployment.DeletionTimestamp.IsZero()
	}, timeout, pollingInterval).Should(BeTrue())
}

func instantiateTemplate(deploymentTemplate []byte, shardKey string) ([]byte, error) {
	// Store file contents.
	type Info struct {
		SHARD string
	}
	mi := Info{
		SHARD: shardKey,
	}

	// Generate template.
	var buffer bytes.Buffer
	manifesTemplate := template.Must(template.New("deployment-shard").Parse(string(deploymentTemplate)))
	if err := manifesTemplate.Execute(&buffer, mi); err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}

func verifyAnnotation(shard string) {
	currentCluster := &clusterv1.Cluster{}
	Expect(k8sClient.Get(context.TODO(),
		types.NamespacedName{Namespace: kindWorkloadCluster.Namespace, Name: kindWorkloadCluster.Name},
		currentCluster)).To(Succeed())
	v, ok := currentCluster.Annotations[sharding.ShardAnnotation]
	Expect(ok).To(BeTrue())
	Expect(v).To(Equal(shard))
}
