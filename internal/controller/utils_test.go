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
	"github.com/projectsveltos/libsveltos/lib/k8s_utils"
	libsveltosset "github.com/projectsveltos/libsveltos/lib/set"
	"github.com/projectsveltos/shard-controller/internal/controller"
)

const (
	// simplePatchYAML is a minimal patch entry used across multiple test cases.
	simplePatchYAML = `patch: '[{"op":"add","path":"/metadata/labels/k","value":"v"}]'`
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
			"", "", "", clusterRef, shardKey, logger)).To(BeNil())

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
			false, "", "", "", clusterRef, oldShardKey, logger)).To(BeNil())
		verifyClusterIsRegisteredForShard(clusterRef, oldShardKey)

		// First cluster being registered as part of shardKey
		Expect(controller.TrackCluster(context.TODO(), testEnv.Config, testEnv.Client,
			false, "", "", "", clusterRef, newShardKey, logger)).To(BeNil())
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
			false, "", "", "", clusterRef, shardKey, logger)).To(BeNil())
		verifyClusterIsRegisteredForShard(clusterRef, shardKey)

		// Second cluster being registered as part of shardKey
		newClusterRef := getClusterRef()
		Expect(controller.TrackCluster(context.TODO(), testEnv.Config, testEnv.Client,
			false, "", "", "", newClusterRef, shardKey, logger)).To(BeNil())
		verifyClusterIsRegisteredForShard(newClusterRef, shardKey)
	})

	It("stopTrackingCluster removes cluster from internal maps", func() {
		clusterRef := getClusterRef()

		shardKey := randomString()

		// First cluster being registered as part of shardKey
		Expect(controller.TrackCluster(context.TODO(), testEnv.Config, testEnv.Client,
			false, "", "", "", clusterRef, shardKey, logger)).To(BeNil())
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
			false, "", "", "", clusterRef, shardKey, logger)).To(BeNil())
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
					libsveltosv1beta1.ShardAnnotation: shardKey,
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
			false, "", "", "", cluster, req, logger)
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
			false, "", "", "", cluster, req, logger)
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
					libsveltosv1beta1.ShardAnnotation: shardKey,
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
			false, "", "", "", cluster, req, logger)
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
				Name: sveltosNamespace,
			},
		}

		initObjects := []client.Object{
			namespace,
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(initObjects...).Build()

		shard := "shard1"
		nginxDeployment := fmt.Sprintf(nginxDeploymentTemplate, namespace.Name)
		Expect(controller.DeployDeployment(context.TODO(), c, []byte(nginxDeployment),
			sveltosNamespace, shard)).To(Succeed())

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
				Name: sveltosNamespace,
			},
		}

		initObjects := []client.Object{
			namespace,
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(initObjects...).Build()

		Expect(controller.DeployControllers(context.TODO(), c, randomString(),
			false, "", "", "", logger)).To(Succeed())

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
			client.InNamespace(sveltosNamespace),
		}

		Expect(testEnv.List(context.TODO(), deploymentList, listOptions...)).To(Succeed())

		currentDeploments := len(deploymentList.Items)

		Expect(controller.DeployControllers(context.TODO(), testEnv.Client, randomString(),
			true, "", "", "", logger)).To(Succeed())

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
			client.InNamespace(sveltosNamespace),
		}

		deploymentList := &appsv1.DeploymentList{}
		Expect(testEnv.List(context.TODO(), deploymentList, listOptions...)).To(Succeed())
		currentDeployments := len(deploymentList.Items)

		shardKey := randomString()
		Expect(controller.DeployControllers(context.TODO(), testEnv.Client, shardKey,
			false, "", "", "", logger)).To(Succeed())

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

	It("deployControllers accepts driftDetectionConfig and sveltosAgentConfig parameters", func() {
		driftCfg := "drift-config-value"
		agentCfg := "agent-config-value"
		applierCfg := "applier-config-value"
		shardKey := randomString()

		// Verify deployControllers accepts and processes the config parameters without error
		Expect(controller.DeployControllers(context.TODO(), testEnv.Client, shardKey,
			false, driftCfg, agentCfg, applierCfg, logger)).To(Succeed())

		// Verify addon-controller deployment was created with correct shard name
		addonDeployment := &appsv1.Deployment{}
		err := testEnv.Get(context.TODO(),
			types.NamespacedName{
				Namespace: sveltosNamespace,
				Name:      "addon-controller-" + shardKey,
			},
			addonDeployment)
		Expect(err).To(BeNil())
		Expect(addonDeployment).ToNot(BeNil())
		Expect(addonDeployment.Spec.Template.Spec.Containers).ToNot(BeEmpty())

		// Verify classifier deployment was created with correct shard name
		classifierDeployment := &appsv1.Deployment{}
		err = testEnv.Get(context.TODO(),
			types.NamespacedName{
				Namespace: sveltosNamespace,
				Name:      "classifier-manager-" + shardKey,
			},
			classifierDeployment)
		Expect(err).To(BeNil())
		Expect(classifierDeployment).ToNot(BeNil())
		Expect(classifierDeployment.Spec.Template.Spec.Containers).ToNot(BeEmpty())

		// Verify addon deployment has correct config args
		addonContainers := addonDeployment.Spec.Template.Spec.Containers
		addonArgs := make(map[string]bool)
		for _, container := range addonContainers {
			for _, arg := range container.Args {
				addonArgs[arg] = true
			}
		}
		Expect(addonArgs).To(HaveKey("--drift-detection-config="+driftCfg),
			"addon-controller must have --drift-detection-config arg with correct value")

		// Verify classifier deployment has correct config args
		classifierContainers := classifierDeployment.Spec.Template.Spec.Containers
		classifierArgs := make(map[string]bool)
		for _, container := range classifierContainers {
			for _, arg := range container.Args {
				classifierArgs[arg] = true
			}
		}
		Expect(classifierArgs).To(HaveKey("--sveltos-agent-config="+agentCfg),
			"classifier must have --sveltos-agent-config arg with correct value")
		Expect(classifierArgs).To(HaveKey("--sveltos-applier-config="+applierCfg),
			"classifier must have --sveltos-applier-config arg with correct value")
	})

	It("deployControllers passes correct shard-key to controllers", func() {
		listOptions := []client.ListOption{
			client.InNamespace(sveltosNamespace),
		}

		deploymentList := &appsv1.DeploymentList{}
		Expect(testEnv.List(context.TODO(), deploymentList, listOptions...)).To(Succeed())
		currentDeployments := len(deploymentList.Items)

		shardKey := randomString()

		// Deploy controllers with specific shard key
		Expect(controller.DeployControllers(context.TODO(), testEnv.Client, shardKey,
			false, "", "", "", logger)).To(Succeed())

		const expectedDeployment = 5 // addon-controller, event-manager, healthcheck-manager,
		// classifier, sveltoscluster-manager
		Eventually(func() bool {
			err := testEnv.List(context.TODO(), deploymentList, listOptions...)
			if err != nil {
				return false
			}
			return len(deploymentList.Items) == expectedDeployment+currentDeployments
		}, timeout, pollingInterval).Should(BeTrue())

		// Verify addon-controller deployment has correct shard key
		addonDeployment := &appsv1.Deployment{}
		err := testEnv.Get(context.TODO(),
			types.NamespacedName{
				Namespace: sveltosNamespace,
				Name:      "addon-controller-" + shardKey,
			},
			addonDeployment)
		Expect(err).To(BeNil())
		Expect(addonDeployment).ToNot(BeNil())

		addonContainers := addonDeployment.Spec.Template.Spec.Containers
		addonArgs := make(map[string]bool)
		for _, container := range addonContainers {
			for _, arg := range container.Args {
				addonArgs[arg] = true
			}
		}
		Expect(addonArgs).To(HaveKey("--shard-key="+shardKey),
			"addon-controller should have --shard-key arg with correct shard")

		// Verify classifier deployment has correct shard key
		classifierDeployment := &appsv1.Deployment{}
		err = testEnv.Get(context.TODO(),
			types.NamespacedName{
				Namespace: sveltosNamespace,
				Name:      "classifier-manager-" + shardKey,
			},
			classifierDeployment)
		Expect(err).To(BeNil())
		Expect(classifierDeployment).ToNot(BeNil())

		classifierContainers := classifierDeployment.Spec.Template.Spec.Containers
		classifierArgs := make(map[string]bool)
		for _, container := range classifierContainers {
			for _, arg := range container.Args {
				classifierArgs[arg] = true
			}
		}
		Expect(classifierArgs).To(HaveKey("--shard-key="+shardKey),
			"classifier should have --shard-key arg with correct shard")
	})

	It("deployControllers omits optional args when config values are not provided", func() {
		listOptions := []client.ListOption{
			client.InNamespace(sveltosNamespace),
		}

		deploymentList := &appsv1.DeploymentList{}
		Expect(testEnv.List(context.TODO(), deploymentList, listOptions...)).To(Succeed())
		currentDeployments := len(deploymentList.Items)

		shardKey := randomString()

		// Deploy with empty config values (args should be omitted)
		Expect(controller.DeployControllers(context.TODO(), testEnv.Client, shardKey,
			false, "", "", "", logger)).To(Succeed())

		const expectedDeployment = 5
		Eventually(func() bool {
			err := testEnv.List(context.TODO(), deploymentList, listOptions...)
			if err != nil {
				return false
			}
			return len(deploymentList.Items) == expectedDeployment+currentDeployments
		}, timeout, pollingInterval).Should(BeTrue())

		// Verify addon-controller deployment does not have --drift-detection-config arg
		addonDeployment := &appsv1.Deployment{}
		err := testEnv.Get(context.TODO(),
			types.NamespacedName{
				Namespace: sveltosNamespace,
				Name:      "addon-controller-" + shardKey,
			},
			addonDeployment)
		Expect(err).To(BeNil())

		addonArgs := make(map[string]bool)
		for _, container := range addonDeployment.Spec.Template.Spec.Containers {
			for _, arg := range container.Args {
				addonArgs[arg] = true
			}
		}
		// Check that drift-detection-config arg is NOT present
		for arg := range addonArgs {
			Expect(arg).ToNot(ContainSubstring("--drift-detection-config"),
				"addon-controller should not have --drift-detection-config arg when not provided")
		}

		// Verify classifier deployment does not have sveltos config args
		classifierDeployment := &appsv1.Deployment{}
		err = testEnv.Get(context.TODO(),
			types.NamespacedName{
				Namespace: sveltosNamespace,
				Name:      "classifier-manager-" + shardKey,
			},
			classifierDeployment)
		Expect(err).To(BeNil())

		classifierArgs := make(map[string]bool)
		for _, container := range classifierDeployment.Spec.Template.Spec.Containers {
			for _, arg := range container.Args {
				classifierArgs[arg] = true
			}
		}
		// Check that sveltos config args are NOT present
		for arg := range classifierArgs {
			Expect(arg).ToNot(ContainSubstring("--sveltos-agent-config"),
				"classifier should not have --sveltos-agent-config arg when not provided")
			Expect(arg).ToNot(ContainSubstring("--sveltos-applier-config"),
				"classifier should not have --sveltos-applier-config arg when not provided")
		}
	})

	Context("getPatchesFromConfigMap", func() {
		It("returns empty slice for empty ConfigMap data", func() {
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: randomString(), Namespace: sveltosNamespace},
			}
			patches, err := controller.GetPatchesFromConfigMap(cm, logger)
			Expect(err).To(BeNil())
			Expect(patches).To(BeEmpty())
		})

		It("defaults target to apps/v1 Deployment when Target is nil", func() {
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: randomString(), Namespace: sveltosNamespace},
				Data:       map[string]string{"p": simplePatchYAML},
			}
			patches, err := controller.GetPatchesFromConfigMap(cm, logger)
			Expect(err).To(BeNil())
			Expect(patches).To(HaveLen(1))
			Expect(patches[0].Target).ToNot(BeNil())
			Expect(patches[0].Target.Group).To(Equal("apps"))
			Expect(patches[0].Target.Kind).To(Equal("Deployment"))
		})

		It("preserves an explicit target when provided", func() {
			patchYAML := "patch: '[{\"op\":\"add\",\"path\":\"/metadata/labels/k\",\"value\":\"v\"}]'\ntarget:\n  kind: ConfigMap\n  group: \"\"\n"
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: randomString(), Namespace: sveltosNamespace},
				Data:       map[string]string{"p": patchYAML},
			}
			patches, err := controller.GetPatchesFromConfigMap(cm, logger)
			Expect(err).To(BeNil())
			Expect(patches).To(HaveLen(1))
			Expect(patches[0].Target.Kind).To(Equal("ConfigMap"))
			Expect(patches[0].Target.Group).To(Equal(""))
		})

		It("parses multiple patches from multiple data keys", func() {
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: randomString(), Namespace: sveltosNamespace},
				Data: map[string]string{
					"patch1": simplePatchYAML,
					"patch2": simplePatchYAML,
				},
			}
			patches, err := controller.GetPatchesFromConfigMap(cm, logger)
			Expect(err).To(BeNil())
			Expect(patches).To(HaveLen(2))
		})

		It("returns error when patch field is empty", func() {
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: randomString(), Namespace: sveltosNamespace},
				Data:       map[string]string{"p": "target:\n  kind: Deployment\n"},
			}
			_, err := controller.GetPatchesFromConfigMap(cm, logger)
			Expect(err).ToNot(BeNil())
		})

		It("returns error for invalid YAML", func() {
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: randomString(), Namespace: sveltosNamespace},
				Data:       map[string]string{"p": "patch: [unclosed"},
			}
			_, err := controller.GetPatchesFromConfigMap(cm, logger)
			Expect(err).ToNot(BeNil())
		})
	})

	Context("applyShardPatches", func() {
		It("returns original bytes unchanged when patches is empty", func() {
			original := []byte(fmt.Sprintf(nginxDeploymentTemplate, sveltosNamespace))
			result, err := controller.ApplyShardPatches(original, nil, logger)
			Expect(err).To(BeNil())
			Expect(result).To(Equal(original))
		})

		It("applies a JSON6902 patch that matches the deployment", func() {
			deployYAML := []byte(fmt.Sprintf(nginxDeploymentTemplate, sveltosNamespace))
			patches := []libsveltosv1beta1.Patch{
				{
					Patch:  `[{"op":"add","path":"/metadata/labels/injected","value":"yes"}]`,
					Target: &libsveltosv1beta1.PatchSelector{Group: "apps", Kind: "Deployment"},
				},
			}
			result, err := controller.ApplyShardPatches(deployYAML, patches, logger)
			Expect(err).To(BeNil())
			u, err := k8s_utils.GetUnstructured(result)
			Expect(err).To(BeNil())
			Expect(u.GetLabels()).To(HaveKeyWithValue("injected", "yes"))
		})

		It("leaves the deployment unchanged when the patch targets a different kind", func() {
			deployYAML := []byte(fmt.Sprintf(nginxDeploymentTemplate, sveltosNamespace))
			patches := []libsveltosv1beta1.Patch{
				{
					Patch:  `[{"op":"add","path":"/metadata/labels/injected","value":"yes"}]`,
					Target: &libsveltosv1beta1.PatchSelector{Group: "", Kind: "ConfigMap"},
				},
			}
			result, err := controller.ApplyShardPatches(deployYAML, patches, logger)
			Expect(err).To(BeNil())
			u, err := k8s_utils.GetUnstructured(result)
			Expect(err).To(BeNil())
			Expect(u.GetLabels()).ToNot(HaveKey("injected"))
		})
	})

	Context("getShardComponentsPatches", func() {
		AfterEach(func() {
			controller.SetShardComponentsConfigMapForTest("")
		})

		It("returns nil when no ConfigMap name is configured", func() {
			controller.SetShardComponentsConfigMapForTest("")
			c := fake.NewClientBuilder().WithScheme(scheme).Build()
			patches, err := controller.GetShardComponentsPatches(context.TODO(), c, logger)
			Expect(err).To(BeNil())
			Expect(patches).To(BeNil())
		})

		It("returns nil when the named ConfigMap does not exist", func() {
			controller.SetShardComponentsConfigMapForTest(randomString())
			c := fake.NewClientBuilder().WithScheme(scheme).Build()
			patches, err := controller.GetShardComponentsPatches(context.TODO(), c, logger)
			Expect(err).To(BeNil())
			Expect(patches).To(BeNil())
		})

		It("returns parsed patches when the ConfigMap exists", func() {
			cmName := randomString()
			controller.SetShardComponentsConfigMapForTest(cmName)
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: cmName, Namespace: sveltosNamespace},
				Data:       map[string]string{"p": simplePatchYAML},
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()
			patches, err := controller.GetShardComponentsPatches(context.TODO(), c, logger)
			Expect(err).To(BeNil())
			Expect(patches).To(HaveLen(1))
			Expect(patches[0].Target.Group).To(Equal("apps"))
			Expect(patches[0].Target.Kind).To(Equal("Deployment"))
		})
	})

	Context("redeployAllShards", func() {
		BeforeEach(func() {
			controller.InitMaps()
		})

		It("does nothing when no active shards exist", func() {
			c := fake.NewClientBuilder().WithScheme(scheme).Build()
			Expect(controller.RedeployAllShards(context.TODO(), c, false, "", "", "", logger)).To(Succeed())

			deploymentList := &appsv1.DeploymentList{}
			Expect(c.List(context.TODO(), deploymentList)).To(Succeed())
			Expect(deploymentList.Items).To(BeEmpty())
		})

		It("deploys controllers for each active shard", func() {
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: sveltosNamespace},
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(namespace).Build()

			shardKey := randomString()
			clusterRef := getClusterRef()
			clusters := &libsveltosset.Set{}
			clusters.Insert(clusterRef)
			(*controller.ShardMap)[shardKey] = clusters
			(*controller.ClusterMap)[*clusterRef] = shardKey

			Expect(controller.RedeployAllShards(context.TODO(), c, false, "", "", "", logger)).To(Succeed())

			deploymentList := &appsv1.DeploymentList{}
			Expect(c.List(context.TODO(), deploymentList, client.InNamespace(sveltosNamespace))).To(Succeed())
			const expectedDeployments = 5
			Expect(deploymentList.Items).To(HaveLen(expectedDeployments))
		})
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
