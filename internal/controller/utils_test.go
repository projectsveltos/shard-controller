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

package controller_test

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2/textlogger"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	libsveltosset "github.com/projectsveltos/libsveltos/lib/set"
	"github.com/projectsveltos/libsveltos/lib/sharding"
	"github.com/projectsveltos/shard-controller/internal/controller"
)

var (
	nginxDeploymentTemplate = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-deployment
  namespace: %s
  labels:
        app: nginx
spec:
  replicas: 1
  selector:
    matchLabels:
      app: nginx
  template:
    metadata:
      labels:
        app: nginx
    spec:
      containers:
      - name: nginx
        image: nksoni/demo-git-repo
        ports:
        - containerPort: 80`
)

var _ = Describe("Utils", func() {
	var logger logr.Logger

	BeforeEach(func() {
		logger = textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1)))
	})

	It("trackCluster starts tracking a cluster", func() {
		clusterRef := getClusterRef()
		shardKey := randomString()

		// First cluster being registered as part of shardKey
		Expect(controller.TrackCluster(context.TODO(), testEnv.Config, testEnv.Client, false,
			clusterRef, shardKey, logger)).To(BeNil())

		currentShard, ok := (*controller.ClusterMap)[*clusterRef]
		Expect(ok).To(BeTrue())
		Expect(currentShard).To(Equal(shardKey))

		verifyClusterIsRegisteredForShard(clusterRef, shardKey)
	})

	It("trackCluster removes cluster from previous registered shard", func() {
		clusterRef := getClusterRef()

		oldShardKey := randomString()
		newShardKey := randomString()

		// First cluster being registered as part of shardKey
		Expect(controller.TrackCluster(context.TODO(), testEnv.Config, testEnv.Client,
			false, clusterRef, oldShardKey, logger)).To(BeNil())
		verifyClusterIsRegisteredForShard(clusterRef, oldShardKey)

		// First cluster being registered as part of shardKey
		Expect(controller.TrackCluster(context.TODO(), testEnv.Config, testEnv.Client,
			false, clusterRef, newShardKey, logger)).To(BeNil())
		verifyClusterIsRegisteredForShard(clusterRef, newShardKey)

		// Verify cluster is not registered anymore as matching oldShardKey
		v, ok := (*controller.ShardMap)[oldShardKey]
		Expect(ok).To(BeTrue())
		Expect(v).ToNot(BeNil())
		Expect(v.Len()).To(BeZero())

		currentShard, ok := (*controller.ClusterMap)[*clusterRef]
		Expect(ok).To(BeTrue())
		Expect(currentShard).To(Equal(newShardKey))
	})

	It("trackCluster registers all clusters matching a shard", func() {
		clusterRef := getClusterRef()

		shardKey := randomString()

		// First cluster being registered as part of shardKey
		Expect(controller.TrackCluster(context.TODO(), testEnv.Config, testEnv.Client,
			false, clusterRef, shardKey, logger)).To(BeNil())
		verifyClusterIsRegisteredForShard(clusterRef, shardKey)

		// Second cluster being registered as part of shardKey
		newClusterRef := getClusterRef()
		Expect(controller.TrackCluster(context.TODO(), testEnv.Config, testEnv.Client,
			false, newClusterRef, shardKey, logger)).To(BeNil())
		verifyClusterIsRegisteredForShard(newClusterRef, shardKey)
	})

	It("stopTrackingCluster removes cluster from internal maps", func() {
		clusterRef := getClusterRef()

		shardKey := randomString()

		// First cluster being registered as part of shardKey
		Expect(controller.TrackCluster(context.TODO(), testEnv.Config, testEnv.Client,
			false, clusterRef, shardKey, logger)).To(BeNil())
		verifyClusterIsRegisteredForShard(clusterRef, shardKey)

		Expect(controller.StopTrackingCluster(context.TODO(), testEnv.Config, clusterRef, logger)).To(Succeed())

		// Verify cluster is not registered anymore as matching shardKey
		v, ok := (*controller.ShardMap)[shardKey]
		Expect(ok).To(BeTrue())
		Expect(v).ToNot(BeNil())
		Expect(v.Len()).To(BeZero())

		_, ok = (*controller.ClusterMap)[*clusterRef]
		Expect(ok).To(BeFalse())
	})

	It("stopTrackingCluster when cluster is not tracked does nothing", func() {
		clusterRef := getClusterRef()

		shardKey := randomString()

		// First cluster being registered as part of shardKey
		Expect(controller.TrackCluster(context.TODO(), testEnv.Config, testEnv.Client,
			false, clusterRef, shardKey, logger)).To(BeNil())
		verifyClusterIsRegisteredForShard(clusterRef, shardKey)

		Expect(controller.StopTrackingCluster(context.TODO(), testEnv.Config, clusterRef, logger)).To(Succeed())

		// Verify cluster is not registered anymore as matching oldShardKey
		v, ok := (*controller.ShardMap)[shardKey]
		Expect(ok).To(BeTrue())
		Expect(v).ToNot(BeNil())
		Expect(v.Len()).To(BeZero())

		_, ok = (*controller.ClusterMap)[*clusterRef]
		Expect(ok).To(BeFalse())

		Expect(controller.StopTrackingCluster(context.TODO(), testEnv.Config, clusterRef, logger)).To(Succeed())

		// Verify cluster is not registered anymore as matching oldShardKey
		v, ok = (*controller.ShardMap)[shardKey]
		Expect(ok).To(BeTrue())
		Expect(v).ToNot(BeNil())
		Expect(v.Len()).To(BeZero())

		_, ok = (*controller.ClusterMap)[*clusterRef]
		Expect(ok).To(BeFalse())
	})

	It("processCluster, for existing cluster, starts tracking it", func() {
		shardKey := randomString()
		cluster := &libsveltosv1beta1.SveltosCluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: randomString(),
				Name:      randomString(),
				Annotations: map[string]string{
					sharding.ShardAnnotation: shardKey,
				},
			},
		}

		req := ctrl.Request{
			NamespacedName: types.NamespacedName{
				Namespace: cluster.Namespace,
				Name:      cluster.Name,
			},
		}

		namespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: cluster.Namespace,
			},
		}

		Expect(testEnv.Create(context.TODO(), namespace)).To(Succeed())
		Expect(waitForObject(context.TODO(), testEnv.Client, namespace)).To(Succeed())

		Expect(testEnv.Create(context.TODO(), cluster)).To(Succeed())
		Expect(waitForObject(context.TODO(), testEnv.Client, cluster)).To(Succeed())

		err := controller.ProcessCluster(context.TODO(), testEnv.Config, testEnv.Client,
			false, cluster, req, logger)
		Expect(err).To(BeNil())

		clusterRef := &corev1.ObjectReference{
			Namespace:  cluster.Namespace,
			Name:       cluster.Name,
			Kind:       libsveltosv1beta1.SveltosClusterKind,
			APIVersion: libsveltosv1beta1.GroupVersion.String(),
		}
		verifyClusterIsRegisteredForShard(clusterRef, shardKey)

		cluster.Annotations = map[string]string{}
		Expect(testEnv.Update(context.TODO(), cluster)).To(Succeed())

		Eventually(func() bool {
			currentCluster := &libsveltosv1beta1.SveltosCluster{}
			err = testEnv.Get(context.TODO(),
				types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, currentCluster)
			if err != nil {
				return false
			}
			return len(currentCluster.Annotations) == 0
		}, timeout, pollingInterval).Should(BeTrue())

		err = controller.ProcessCluster(context.TODO(), testEnv.Config, testEnv.Client,
			false, cluster, req, logger)
		Expect(err).To(BeNil())

		verifyClusterIsRegisteredForShard(clusterRef, "")
		// Verify cluster is not registered anymore as matching oldShardKey
		v, ok := (*controller.ShardMap)[shardKey]
		Expect(ok).To(BeTrue())
		Expect(v).ToNot(BeNil())
		Expect(v.Len()).To(BeZero())
	})

	It("processCluster, for deleted cluster, stops tracking it", func() {
		shardKey := randomString()
		cluster := &libsveltosv1beta1.SveltosCluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: randomString(),
				Name:      randomString(),
				Annotations: map[string]string{
					sharding.ShardAnnotation: shardKey,
				},
			},
		}

		clusterRef := &corev1.ObjectReference{
			Namespace:  cluster.Namespace,
			Name:       cluster.Name,
			Kind:       libsveltosv1beta1.SveltosClusterKind,
			APIVersion: libsveltosv1beta1.GroupVersion.String(),
		}

		req := ctrl.Request{
			NamespacedName: types.NamespacedName{
				Namespace: cluster.Namespace,
				Name:      cluster.Name,
			},
		}

		// Prepare maps
		clusters := &libsveltosset.Set{}
		clusters.Insert(clusterRef)
		(*controller.ShardMap)[shardKey] = clusters
		(*controller.ClusterMap)[*clusterRef] = shardKey

		// Cluster does not exist
		err := controller.ProcessCluster(context.TODO(), testEnv.Config, testEnv.Client,
			false, cluster, req, logger)
		Expect(err).To(BeNil())

		// Verify cluster is not registered anymore as matching oldShardKey
		v, ok := (*controller.ShardMap)[shardKey]
		Expect(ok).To(BeTrue())
		Expect(v).ToNot(BeNil())
		Expect(v.Len()).To(BeZero())

		_, ok = (*controller.ClusterMap)[*clusterRef]
		Expect(ok).To(BeFalse())
	})

	It("deployDeployment deploys a deployment", func() {
		namespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: randomString(),
			},
		}

		initObjects := []client.Object{
			namespace,
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(initObjects...).Build()

		shard := "shard1"
		nginxDeployment := fmt.Sprintf(nginxDeploymentTemplate, namespace.Name)
		Expect(controller.DeployDeployment(context.TODO(), c, []byte(nginxDeployment), shard)).To(Succeed())

		deploymentList := &appsv1.DeploymentList{}
		listOptions := []client.ListOption{
			client.InNamespace(namespace.Name),
		}

		Expect(c.List(context.TODO(), deploymentList, listOptions...)).To(Succeed())
		Expect(len(deploymentList.Items)).To(Equal(1))
	})

	It("deployControllers deploys projectsveltos controllers for a given shard", func() {
		namespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: projectsveltoNs,
			},
		}

		initObjects := []client.Object{
			namespace,
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(initObjects...).Build()

		Expect(controller.DeployControllers(context.TODO(), c, randomString(),
			false, logger)).To(Succeed())

		deploymentList := &appsv1.DeploymentList{}
		listOptions := []client.ListOption{
			client.InNamespace(namespace.Name),
		}

		const expectedDeployment = 5 // addon-controller, event-manager, healthcheck-manager,
		// classifier, sveltoscluster-manager
		Expect(c.List(context.TODO(), deploymentList, listOptions...)).To(Succeed())
		Expect(len(deploymentList.Items)).To(Equal(expectedDeployment))
	})

	It("deployControllers deploys projectsveltos controllers passing agent-in-mgmt-cluster option", func() {
		deploymentList := &appsv1.DeploymentList{}
		listOptions := []client.ListOption{
			client.InNamespace(projectsveltoNs),
		}

		Expect(testEnv.List(context.TODO(), deploymentList, listOptions...)).To(Succeed())

		currentDeploments := len(deploymentList.Items)

		Expect(controller.DeployControllers(context.TODO(), testEnv.Client, randomString(),
			true, logger)).To(Succeed())

		const expectedNewDeployment = 5 // addon-controller, event-manager, healthcheck-manager,
		// classifier, sveltoscluster-manager
		Eventually(func() bool {
			err := testEnv.List(context.TODO(), deploymentList, listOptions...)
			if err != nil {
				return false
			}
			return len(deploymentList.Items) == currentDeploments+expectedNewDeployment
		}, timeout, pollingInterval).Should(BeTrue())
	})

	It("undeployControllers undeploys projectsveltos controllers for a given shard", func() {
		listOptions := []client.ListOption{
			client.InNamespace(projectsveltoNs),
		}

		deploymentList := &appsv1.DeploymentList{}
		Expect(testEnv.List(context.TODO(), deploymentList, listOptions...)).To(Succeed())
		currentDeployments := len(deploymentList.Items)

		shardKey := randomString()
		Expect(controller.DeployControllers(context.TODO(), testEnv.Client, shardKey,
			false, logger)).To(Succeed())

		const expectedDeployment = 5 // addon-controller, event-manager, healthcheck-manager,
		// classifier, sveltoscluster-manager
		Eventually(func() bool {
			err := testEnv.List(context.TODO(), deploymentList, listOptions...)
			if err != nil {
				return false
			}
			return len(deploymentList.Items) == expectedDeployment+currentDeployments
		}, timeout, pollingInterval).Should(BeTrue())

		Expect(controller.UndeployControllers(context.TODO(), testEnv.Config, shardKey, logger)).To(Succeed())

		Eventually(func() bool {
			err := testEnv.List(context.TODO(), deploymentList, listOptions...)
			if err != nil {
				return false
			}
			return len(deploymentList.Items) == currentDeployments
		}, timeout, pollingInterval).Should(BeTrue())
	})
})

func getClusterRef() *corev1.ObjectReference {
	return &corev1.ObjectReference{
		Name:       randomString(),
		Namespace:  randomString(),
		Kind:       string(libsveltosv1beta1.ClusterTypeSveltos),
		APIVersion: libsveltosv1beta1.GroupVersion.String(),
	}
}

func verifyClusterIsRegisteredForShard(clusterRef *corev1.ObjectReference, shardKey string) {
	v, ok := (*controller.ShardMap)[shardKey]
	Expect(ok).To(BeTrue())
	Expect(v).ToNot(BeNil())
	Expect(v.Has(clusterRef))
}
