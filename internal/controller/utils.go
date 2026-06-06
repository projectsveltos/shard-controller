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

package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"strings"
	"sync"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/projectsveltos/libsveltos/lib/k8s_utils"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
	"github.com/projectsveltos/libsveltos/lib/patcher"
	libsveltosset "github.com/projectsveltos/libsveltos/lib/set"
	controllerSharding "github.com/projectsveltos/shard-controller/pkg/sharding"
)

var (
	// key: Sveltos/CAPI Cluster; value: shard cluster is part of last it was processed
	clusterMap map[corev1.ObjectReference]string
	// Key: shard; value: list of clusters currently matching this shard
	shardMap map[string]*libsveltosset.Set

	mux sync.RWMutex

	shardComponentsConfigMap string
)

func InitMaps() {
	clusterMap = map[corev1.ObjectReference]string{}
	shardMap = map[string]*libsveltosset.Set{}
	mux = sync.RWMutex{}
}

func SetShardComponentsConfigMap(name string) {
	shardComponentsConfigMap = name
}

func getShardComponentsConfigMap() string {
	return shardComponentsConfigMap
}

type ReportMode int

const (
	// Default mode. In this mode, Classifier running
	// in the management cluster periodically collect
	// ClassifierReport from Sveltos/CAPI clusters
	CollectFromManagementCluster ReportMode = iota

	// In this mode, classifier agent sends ClassifierReport
	// to management cluster.
	// SveltosAgent is provided with Kubeconfig to access
	// management cluster and can only update ClassifierReport/
	// HealthCheckReport/EventReport
	AgentSendReportsNoGateway
)

//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=debuggingconfigurations,verbs=get;list;watch
//+kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get;list;watch

func InitScheme() (*runtime.Scheme, error) {
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		return nil, err
	}
	if err := clusterv1.AddToScheme(s); err != nil {
		return nil, err
	}
	if err := libsveltosv1beta1.AddToScheme(s); err != nil {
		return nil, err
	}
	if err := apiextensionsv1.AddToScheme(s); err != nil {
		return nil, err
	}
	return s, nil
}

func processCluster(ctx context.Context, config *rest.Config, c client.Client,
	agentInMgmtCluster bool, driftDetectionConfig, sveltosAgentConfig, sveltosApplierConfig string,
	cluster client.Object, req ctrl.Request, logger logr.Logger) error {

	if err := c.Get(ctx, req.NamespacedName, cluster); err != nil {
		if apierrors.IsNotFound(err) {
			// cluster is empty here; derive the GVK from the scheme so the ref is complete.
			addTypeInformationToObject(c.Scheme(), cluster)
			apiVersion, kind := cluster.GetObjectKind().GroupVersionKind().ToAPIVersionAndKind()
			notFoundRef := &corev1.ObjectReference{
				Namespace:  req.Namespace,
				Name:       req.Name,
				Kind:       kind,
				APIVersion: apiVersion,
			}
			return stopTrackingCluster(ctx, config, notFoundRef, logger)
		}
		logger.Error(err, "Failed to fetch cluster")
		return errors.Wrapf(
			err,
			"Failed to fetch cluster %s:%s",
			cluster.GetObjectKind().GroupVersionKind().Kind, req.NamespacedName,
		)
	}

	// clusterRef is computed after Get so namespace/name are populated.
	clusterRef := getObjectReferenceFromObject(c.Scheme(), cluster)

	// Handle deleted cluster
	if !cluster.GetDeletionTimestamp().IsZero() {
		return stopTrackingCluster(ctx, config, clusterRef, logger)
	}

	currentShard := ""
	annotations := cluster.GetAnnotations()
	if len(annotations) != 0 {
		currentShard = annotations[libsveltosv1beta1.ShardAnnotation]
	}

	return trackCluster(ctx, config, c, agentInMgmtCluster, driftDetectionConfig, sveltosAgentConfig, sveltosApplierConfig, clusterRef, currentShard, logger)
}

// trackCluster starts tracking a cluster:
// - if cluster was previously tracked matching another shard, removes
// it from list of cluster matching that shard
// - updates cluster map with current cluster shard
// - adds cluster to list of clusters currently matching current shard
// If cluster is the first one seen for currentShardKey, starts projectsveltos
// deployment for the shard
// If cluster was previously associated to a different shard and after
// cluster moves to currentShardKey, no more clusters are part of old shard,
// removes projectsveltos deployments for old shard.
func trackCluster(ctx context.Context, config *rest.Config, c client.Client, agentInMgmtCluster bool,
	driftDetectionConfig, sveltosAgentConfig, sveltosApplierConfig string, cluster *corev1.ObjectReference, currentShardKey string, logger logr.Logger) error {

	mux.Lock()
	defer mux.Unlock()

	oldShard, alreadyTracked := clusterMap[*cluster]
	if alreadyTracked && oldShard == currentShardKey {
		// Cluster is already tracked. And cluster shard has not changed.
		return nil
	}

	if alreadyTracked {
		// shardMap[oldShard] may be nil when a previous reconcile failed after
		// updating clusterMap but before updating shardMap. Guard every access.
		oldClusterPerShard := shardMap[oldShard]
		if oldClusterPerShard != nil && oldClusterPerShard.Has(cluster) &&
			oldClusterPerShard.Len() == 1 {
			// By removing cluster, no more clusters will match oldShard.
			// Remove controllers first
			logger.V(logs.LogInfo).Info(fmt.Sprintf("no more clusters for shard %s", oldShard))
			if err := undeployControllers(ctx, config, oldShard, logger); err != nil {
				return err
			}
		}

		if oldClusterPerShard != nil {
			oldClusterPerShard.Erase(cluster)
			shardMap[oldShard] = oldClusterPerShard
			logger.V(logs.LogInfo).Info(fmt.Sprintf("removed cluster from shard %q: %d cluster(s) remaining",
				oldShard, oldClusterPerShard.Len()))
		}
	}

	// If cluster is first cluster matching this shard, deploy projectsveltos deployment for
	// currentShardKey
	if shardMap[currentShardKey] == nil || shardMap[currentShardKey].Len() == 0 {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("first cluster matching shard %q", currentShardKey))
		if err := deployControllers(ctx, c, currentShardKey, agentInMgmtCluster, driftDetectionConfig, sveltosAgentConfig, sveltosApplierConfig, logger); err != nil {
			return err
		}
	}

	// Update both maps only after deployControllers succeeds, keeping clusterMap
	// and shardMap consistent even when deployControllers returns an error.
	clusterPerShard := shardMap[currentShardKey]
	if clusterPerShard == nil {
		clusterPerShard = &libsveltosset.Set{}
	}
	clusterPerShard.Insert(cluster)
	shardMap[currentShardKey] = clusterPerShard
	clusterMap[*cluster] = currentShardKey
	logger.V(logs.LogInfo).Info(fmt.Sprintf("added cluster to shard %q: %d cluster(s) total",
		currentShardKey, clusterPerShard.Len()))

	return nil
}

// stopTrackingCluster stops tracking a cluster, so it gets last registered
// cluster shard, and removes the cluster as one of the clusters matching it.
// If no more clusters are matching the shard, delete projectsveltos clusters for that shard.
func stopTrackingCluster(ctx context.Context, config *rest.Config, cluster *corev1.ObjectReference,
	logger logr.Logger) error {

	logger = logger.WithValues("cluster", fmt.Sprintf("%s:%s/%s", cluster.Kind, cluster.Namespace, cluster.Name))

	mux.Lock()
	defer mux.Unlock()

	oldShardKey := clusterMap[*cluster]
	delete(clusterMap, *cluster)

	clusterPerShard := shardMap[oldShardKey]
	if clusterPerShard != nil {
		if clusterPerShard.Has(cluster) {
			clusterPerShard.Erase(cluster)
			shardMap[oldShardKey] = clusterPerShard
			if shardMap[oldShardKey].Len() == 0 {
				logger.V(logs.LogInfo).Info(
					fmt.Sprintf("no more clusters matching shard %s. Removing controllers", oldShardKey))
				return undeployControllers(ctx, config, oldShardKey, logger)
			}
			logger.V(logs.LogInfo).Info(fmt.Sprintf("removed cluster from shard %q: %d cluster(s) remaining",
				oldShardKey, shardMap[oldShardKey].Len()))
		}
	}

	return nil
}

// getObjectReferenceFromObject returns the Key that can be used in the internal reconciler maps.
func getObjectReferenceFromObject(scheme *runtime.Scheme, obj client.Object) *corev1.ObjectReference {
	addTypeInformationToObject(scheme, obj)

	apiVersion, kind := obj.GetObjectKind().GroupVersionKind().ToAPIVersionAndKind()

	return &corev1.ObjectReference{
		Namespace:  obj.GetNamespace(),
		Name:       obj.GetName(),
		Kind:       kind,
		APIVersion: apiVersion,
	}
}

func addTypeInformationToObject(scheme *runtime.Scheme, obj client.Object) {
	gvks, _, err := scheme.ObjectKinds(obj)
	if err != nil {
		panic(1)
	}

	for _, gvk := range gvks {
		if gvk.Kind == "" {
			continue
		}
		if gvk.Version == "" || gvk.Version == runtime.APIVersionInternal {
			continue
		}
		obj.GetObjectKind().SetGroupVersionKind(gvk)
		break
	}
}

func deployControllers(ctx context.Context, c client.Client, shardKey string, //nolint: funlen // deploying sveltos controllers
	agentInMgmtCluster bool, driftDetectionConfig, sveltosAgentConfig, sveltosApplierConfig string, logger logr.Logger) error {

	if shardKey == "" {
		// Clusters with no shard annotation are managed by the default projectsveltos deployments
		return nil
	}

	logger = logger.WithValues("shard", shardKey)
	logger.V(logs.LogDebug).Info("deploy projectsveltos controllers for shard")

	patches, err := getShardComponentsPatches(ctx, c, logger)
	if err != nil {
		return err
	}

	addonControllerTemplate := controllerSharding.GetAddonControllerTemplate()
	if agentInMgmtCluster {
		logger.V(logs.LogDebug).Info("setting agent-in-mgmt-cluster")
		addonControllerTemplate, err = setOptions(addonControllerTemplate)
		if err != nil {
			return err
		}
	}
	addonControllerTemplate, err = addDriftDetectionConfig(addonControllerTemplate, driftDetectionConfig)
	if err != nil {
		return err
	}
	err = deployDeployment(ctx, c, addonControllerTemplate, getSveltosNamespace(), shardKey, patches, logger)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to deploy addon-controller %v", err))
		return err
	}

	classifierTemplate := controllerSharding.GetClassifierTemplate()
	if agentInMgmtCluster {
		logger.V(logs.LogDebug).Info("setting agent-in-mgmt-cluster")
		classifierTemplate, err = setOptions(classifierTemplate)
		if err != nil {
			return err
		}
	}
	classifierTemplate, err = addClassifierConfigs(classifierTemplate, sveltosAgentConfig, sveltosApplierConfig)
	if err != nil {
		return err
	}
	err = deployDeployment(ctx, c, classifierTemplate, getSveltosNamespace(), shardKey, patches, logger)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to deploy classifier %v", err))
		return err
	}

	sveltosClusterTemplate := controllerSharding.GetSveltosClusterManagerTemplate()
	err = deployDeployment(ctx, c, sveltosClusterTemplate, getSveltosNamespace(), shardKey, patches, logger)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to deploy sveltoscluster-manager %v", err))
		return err
	}

	eventManagerTemplate := controllerSharding.GetEventManagerTemplate()
	if agentInMgmtCluster {
		logger.V(logs.LogDebug).Info("setting agent-in-mgmt-cluster")
		eventManagerTemplate, err = setOptions(eventManagerTemplate)
		if err != nil {
			return err
		}
	}
	err = deployDeployment(ctx, c, eventManagerTemplate, getSveltosNamespace(), shardKey, patches, logger)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to deploy event-manager %v", err))
		return err
	}

	healthcheckManagerTemplate := controllerSharding.GetHealthCheckManagerTemplate()
	if agentInMgmtCluster {
		logger.V(logs.LogDebug).Info("setting agent-in-mgmt-cluster")
		healthcheckManagerTemplate, err = setOptions(healthcheckManagerTemplate)
		if err != nil {
			return err
		}
	}
	err = deployDeployment(ctx, c, healthcheckManagerTemplate, getSveltosNamespace(), shardKey, patches, logger)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to deploy healthcheck-manager %v", err))
		return err
	}

	return nil
}

func addDriftDetectionConfig(tmpl []byte, driftDetectionConfig string) ([]byte, error) {
	if driftDetectionConfig == "" {
		return tmpl, nil
	}
	return appendArgsToContainer(tmpl, "controller",
		map[string]string{"--drift-detection-config": driftDetectionConfig})
}

func addClassifierConfigs(tmpl []byte, sveltosAgentConfig, sveltosApplierConfig string) ([]byte, error) {
	if sveltosAgentConfig == "" && sveltosApplierConfig == "" {
		return tmpl, nil
	}
	classifierArgsToAdd := make(map[string]string)
	if sveltosAgentConfig != "" {
		classifierArgsToAdd["--sveltos-agent-config"] = sveltosAgentConfig
	}
	if sveltosApplierConfig != "" {
		classifierArgsToAdd["--sveltos-applier-config"] = sveltosApplierConfig
	}
	return appendArgsToContainer(tmpl, "manager", classifierArgsToAdd)
}

func undeployControllers(ctx context.Context, config *rest.Config, shardKey string, logger logr.Logger) error {
	if shardKey == "" {
		// Clusters with no shard annotation are managed by the default projectsveltos deployments
		return nil
	}

	logger = logger.WithValues("shard", shardKey)
	logger.V(logs.LogDebug).Info("undeploy projectsveltos controllers for shard")

	addonControllerTemplate := controllerSharding.GetAddonControllerTemplate()
	err := undeployDeployment(ctx, config, addonControllerTemplate, getSveltosNamespace(), shardKey)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to create addon-controller deployment %v", err))
		return err
	}

	classifierTemplate := controllerSharding.GetClassifierTemplate()
	err = undeployDeployment(ctx, config, classifierTemplate, getSveltosNamespace(), shardKey)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to create classifier deployment %v", err))
		return err
	}

	sveltosClusterTemplate := controllerSharding.GetSveltosClusterManagerTemplate()
	err = undeployDeployment(ctx, config, sveltosClusterTemplate, getSveltosNamespace(), shardKey)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to create sveltoscluster-manager deployment %v", err))
		return err
	}

	eventManagerTemplate := controllerSharding.GetEventManagerTemplate()
	err = undeployDeployment(ctx, config, eventManagerTemplate, getSveltosNamespace(), shardKey)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to create event-manager deployment %v", err))
		return err
	}

	healthcheckManagerTemplate := controllerSharding.GetHealthCheckManagerTemplate()
	err = undeployDeployment(ctx, config, healthcheckManagerTemplate, getSveltosNamespace(), shardKey)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to create healthcheck-manager deployment %v", err))
		return err
	}

	return nil
}

func deployDeployment(ctx context.Context, c client.Client,
	deploymentTemplate []byte, sveltosNamespace string, shardKey string,
	patches []libsveltosv1beta1.Patch, logger logr.Logger) error {

	data, err := instantiateTemplate(deploymentTemplate, shardKey)
	if err != nil {
		return err
	}

	data, err = applyShardPatches(data, patches, logger)
	if err != nil {
		return err
	}

	deployment, err := k8s_utils.GetUnstructured(data)
	if err != nil {
		return err
	}
	deployment.SetNamespace(sveltosNamespace)

	ac := client.ApplyConfigurationFromUnstructured(deployment)
	return c.Apply(ctx, ac, client.ForceOwnership, client.FieldOwner("shard-controller"))
}

func undeployDeployment(ctx context.Context, config *rest.Config,
	deploymentTemplate []byte, sveltosNamespace string, shardKey string) error {

	data, err := instantiateTemplate(deploymentTemplate, shardKey)
	if err != nil {
		return err
	}

	deployment, err := k8s_utils.GetUnstructured(data)
	if err != nil {
		return err
	}
	deployment.SetNamespace(sveltosNamespace)

	// Use clientset instead of client. client, even when passing namespace, requires
	// list permissions at cluster level
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}
	deploymentsClient := clientset.AppsV1().Deployments(deployment.GetNamespace())
	err = deploymentsClient.Delete(ctx, deployment.GetName(), metav1.DeleteOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	return nil
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

// getShardComponentsPatches fetches the shard-components ConfigMap and returns its patches.
// Returns nil if no ConfigMap name is configured or the ConfigMap does not exist yet.
func getShardComponentsPatches(ctx context.Context, c client.Client,
	logger logr.Logger) ([]libsveltosv1beta1.Patch, error) {

	configMapName := getShardComponentsConfigMap()
	if configMapName == "" {
		return nil, nil
	}

	configMap := &corev1.ConfigMap{}
	err := c.Get(ctx, types.NamespacedName{Namespace: getSveltosNamespace(), Name: configMapName}, configMap)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.V(logs.LogInfo).Info(fmt.Sprintf("shard-components ConfigMap %s not found", configMapName))
			return nil, nil
		}
		return nil, err
	}

	return getPatchesFromConfigMap(configMap, logger)
}

// getPatchesFromConfigMap parses each value in the ConfigMap's data as a libsveltosv1beta1.Patch.
// If a patch has no target, it defaults to targeting apps/v1 Deployments.
func getPatchesFromConfigMap(configMap *corev1.ConfigMap, logger logr.Logger) ([]libsveltosv1beta1.Patch, error) {
	patches := make([]libsveltosv1beta1.Patch, 0, len(configMap.Data))
	for k := range configMap.Data {
		p := &libsveltosv1beta1.Patch{}
		if err := yaml.Unmarshal([]byte(configMap.Data[k]), p); err != nil {
			logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to unmarshal patch for key %s: %v", k, err))
			return nil, err
		}
		if p.Patch == "" {
			return nil, fmt.Errorf("ConfigMap %s: value of key %s is not a valid Patch", configMap.Name, k)
		}
		if p.Target == nil {
			p.Target = &libsveltosv1beta1.PatchSelector{Kind: "Deployment", Group: "apps"}
		}
		patches = append(patches, *p)
	}
	return patches, nil
}

// applyShardPatches applies patches to a deployment template byte slice and returns the result.
// Patches whose target selector does not match the deployment are silently skipped.
// Returns the original bytes unchanged when patches is empty.
func applyShardPatches(deploymentData []byte, patches []libsveltosv1beta1.Patch,
	logger logr.Logger) ([]byte, error) {

	if len(patches) == 0 {
		return deploymentData, nil
	}

	u, err := k8s_utils.GetUnstructured(deploymentData)
	if err != nil {
		return nil, err
	}

	p := &patcher.CustomPatchPostRenderer{Patches: patches}
	patched, err := p.RunUnstructured([]*unstructured.Unstructured{u})
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to apply shard-components patches: %v", err))
		return nil, err
	}

	if len(patched) == 0 {
		return deploymentData, nil
	}

	buf := bytes.NewBuffer([]byte{})
	if err := json.NewEncoder(buf).Encode(patched[0].Object); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// redeployAllShards re-applies deployments for every active shard, picking up any ConfigMap changes.
func redeployAllShards(ctx context.Context, c client.Client, agentInMgmtCluster bool,
	driftDetectionConfig, sveltosAgentConfig, sveltosApplierConfig string, logger logr.Logger) error {

	mux.RLock()
	activeShards := make([]string, 0)
	for shardKey, clusters := range shardMap {
		if shardKey != "" && clusters != nil && clusters.Len() > 0 {
			activeShards = append(activeShards, shardKey)
		}
	}
	mux.RUnlock()

	for _, shardKey := range activeShards {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("redeploying controllers for shard %s", shardKey))
		if err := deployControllers(ctx, c, shardKey, agentInMgmtCluster,
			driftDetectionConfig, sveltosAgentConfig, sveltosApplierConfig, logger); err != nil {
			return err
		}
	}
	return nil
}

func setOptions(deplTemplate []byte) ([]byte, error) {
	u, err := k8s_utils.GetUnstructured(deplTemplate)
	if err != nil {
		return nil, err
	}

	depl := appsv1.Deployment{}
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(u.UnstructuredContent(), &depl)
	if err != nil {
		return nil, err
	}

	for i := range depl.Spec.Template.Spec.Containers {
		for j := range depl.Spec.Template.Spec.Containers[i].Args {
			args := &depl.Spec.Template.Spec.Containers[i].Args[j]
			if strings.Contains(*args, "agent-in-mgmt-cluster") {
				lastIdx := len(depl.Spec.Template.Spec.Containers[i].Args) - 1
				depl.Spec.Template.Spec.Containers[i].Args[j] = depl.Spec.Template.Spec.Containers[i].Args[lastIdx]
				depl.Spec.Template.Spec.Containers[i].Args = depl.Spec.Template.Spec.Containers[i].Args[:lastIdx]
				break
			}
		}

		depl.Spec.Template.Spec.Containers[i].Args = append(
			depl.Spec.Template.Spec.Containers[i].Args,
			"--agent-in-mgmt-cluster=true")
	}

	for i := range depl.Spec.Template.Spec.InitContainers {
		for j := range depl.Spec.Template.Spec.InitContainers[i].Args {
			args := &depl.Spec.Template.Spec.InitContainers[i].Args[j]
			if strings.Contains(*args, "agent-in-mgmt-cluster") {
				lastIdx := len(depl.Spec.Template.Spec.InitContainers[i].Args) - 1
				depl.Spec.Template.Spec.InitContainers[i].Args[j] = depl.Spec.Template.Spec.InitContainers[i].Args[lastIdx]
				depl.Spec.Template.Spec.InitContainers[i].Args = depl.Spec.Template.Spec.InitContainers[i].Args[:lastIdx]
				break
			}
		}

		depl.Spec.Template.Spec.InitContainers[i].Args = append(
			depl.Spec.Template.Spec.InitContainers[i].Args,
			"--agent-in-mgmt-cluster=true")
	}

	// Create a buffer to store the encoded JSON data.
	buffer := bytes.NewBuffer([]byte{})

	// Create a new encoder and encode the deployment object.
	encoder := json.NewEncoder(buffer)
	err = encoder.Encode(depl)
	if err != nil {
		return nil, err
	}

	// Get the encoded JSON data from the buffer.
	return buffer.Bytes(), nil
}

func appendArgsToContainer(deplTemplate []byte, containerName string, argsToAdd map[string]string) ([]byte, error) {
	u, err := k8s_utils.GetUnstructured(deplTemplate)
	if err != nil {
		return nil, err
	}

	depl := appsv1.Deployment{}
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(u.UnstructuredContent(), &depl)
	if err != nil {
		return nil, err
	}

	for flag, value := range argsToAdd {
		if value == "" {
			continue
		}
		arg := fmt.Sprintf("%s=%s", flag, value)

		for i := range depl.Spec.Template.Spec.Containers {
			if depl.Spec.Template.Spec.Containers[i].Name == containerName {
				depl.Spec.Template.Spec.Containers[i].Args = append(
					depl.Spec.Template.Spec.Containers[i].Args, arg)
			}
		}
	}

	buffer := bytes.NewBuffer([]byte{})
	encoder := json.NewEncoder(buffer)
	err = encoder.Encode(depl)
	if err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}
