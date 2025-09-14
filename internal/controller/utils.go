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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/projectsveltos/libsveltos/lib/k8s_utils"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
	libsveltosset "github.com/projectsveltos/libsveltos/lib/set"
	"github.com/projectsveltos/libsveltos/lib/sharding"
	controllerSharding "github.com/projectsveltos/shard-controller/pkg/sharding"
)

var (
	// key: Sveltos/CAPI Cluster; value: shard cluster is part of last it was processed
	clusterMap map[corev1.ObjectReference]string
	// Key: shard; value: list of clusters currently matching this shard
	shardMap map[string]*libsveltosset.Set

	mux sync.RWMutex
)

func InitMaps() {
	clusterMap = map[corev1.ObjectReference]string{}
	shardMap = map[string]*libsveltosset.Set{}
	mux = sync.RWMutex{}
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
	agentInMgmtCluster bool, cluster client.Object, req ctrl.Request, logger logr.Logger) error {

	clusterRef := getObjectReferenceFromObject(c.Scheme(), cluster)

	if err := c.Get(ctx, req.NamespacedName, cluster); err != nil {
		if apierrors.IsNotFound(err) {
			return stopTrackingCluster(ctx, config, clusterRef, logger)
		}
		logger.Error(err, "Failed to fetch cluster")
		return errors.Wrapf(
			err,
			"Failed to fetch cluster %s:%s",
			cluster.GetObjectKind().GroupVersionKind().Kind, req.NamespacedName,
		)
	}

	// Handle deleted cluster
	if !cluster.GetDeletionTimestamp().IsZero() {
		return stopTrackingCluster(ctx, config, clusterRef, logger)
	}

	currentShard := ""
	annotations := cluster.GetAnnotations()
	if len(annotations) != 0 {
		currentShard = annotations[sharding.ShardAnnotation]
	}

	return trackCluster(ctx, config, c, agentInMgmtCluster, clusterRef, currentShard, logger)
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
	cluster *corev1.ObjectReference, currentShardKey string, logger logr.Logger) error {

	mux.Lock()
	defer mux.Unlock()

	oldShard, ok := clusterMap[*cluster]
	if ok && oldShard == currentShardKey {
		// Cluster is already tracked. And cluster shard has not changed.
		return nil
	}

	if ok {
		if shardMap[oldShard].Has(cluster) &&
			shardMap[oldShard].Len() == 1 {
			// By removing cluster, no more clusters will match oldShard.
			// Remove controllers first
			logger.V(logs.LogInfo).Info(fmt.Sprintf("no more clusters for shard %s", oldShard))
			if err := undeployControllers(ctx, config, oldShard, logger); err != nil {
				return err
			}
		}

		// Updates shard map (key: shard; value: list of cluster matching the shard)
		// for the old shard by adding cluster as a match
		clusterPerShard := shardMap[oldShard]
		clusterPerShard.Erase(cluster)
		shardMap[oldShard] = clusterPerShard
	}

	// Update Cluster shard (key: cluster; value: cluster current shard)
	clusterMap[*cluster] = currentShardKey

	// If cluster is first cluster matching this shard, deploy projectsveltos deployment for
	// currentShardKey
	if shardMap[currentShardKey] == nil || shardMap[currentShardKey].Len() == 0 {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("first cluster matching shard %q", currentShardKey))
		if err := deployControllers(ctx, c, currentShardKey, agentInMgmtCluster, logger); err != nil {
			return err
		}
	}

	// Updates shard map (key: shard; value: list of cluster matching the shard)
	// for the current shard by adding cluster as a match
	clusterPerShard := shardMap[currentShardKey]
	if clusterPerShard == nil {
		clusterPerShard = &libsveltosset.Set{}
	}
	clusterPerShard.Insert(cluster)
	shardMap[currentShardKey] = clusterPerShard

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
	agentInMgmtCluster bool, logger logr.Logger) error {

	if shardKey == "" {
		// Clusters with no shard annotation are managed by the default projectsveltos deployments
		return nil
	}

	logger = logger.WithValues("shard", shardKey)
	logger.V(logs.LogDebug).Info("deploy projectsveltos controllers for shard")

	var err error
	addonControllerTemplate := controllerSharding.GetAddonControllerTemplate()
	if agentInMgmtCluster {
		logger.V(logs.LogDebug).Info("setting agent-in-mgmt-cluster")
		addonControllerTemplate, err = setOptions(addonControllerTemplate)
		if err != nil {
			return err
		}
	}
	err = deployDeployment(ctx, c, addonControllerTemplate, shardKey)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to create addon-controller deployment %v", err))
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
	err = deployDeployment(ctx, c, classifierTemplate, shardKey)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to create classifier deployment %v", err))
		return err
	}

	sveltosClusterTemplate := controllerSharding.GetSveltosClusterManagerTemplate()
	err = deployDeployment(ctx, c, sveltosClusterTemplate, shardKey)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to create sveltoscluster-manager deployment %v", err))
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
	err = deployDeployment(ctx, c, eventManagerTemplate, shardKey)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to create event-manager deployment %v", err))
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
	err = deployDeployment(ctx, c, healthcheckManagerTemplate, shardKey)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to create healthcheck-manager deployment %v", err))
		return err
	}

	return nil
}

func undeployControllers(ctx context.Context, config *rest.Config, shardKey string, logger logr.Logger) error {
	if shardKey == "" {
		// Clusters with no shard annotation are managed by the default projectsveltos deployments
		return nil
	}

	logger = logger.WithValues("shard", shardKey)
	logger.V(logs.LogDebug).Info("undeploy projectsveltos controllers for shard")

	addonControllerTemplate := controllerSharding.GetAddonControllerTemplate()
	err := undeployDeployment(ctx, config, addonControllerTemplate, shardKey)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to create addon-controller deployment %v", err))
		return err
	}

	classifierTemplate := controllerSharding.GetClassifierTemplate()
	err = undeployDeployment(ctx, config, classifierTemplate, shardKey)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to create classifier deployment %v", err))
		return err
	}

	sveltosClusterTemplate := controllerSharding.GetSveltosClusterManagerTemplate()
	err = undeployDeployment(ctx, config, sveltosClusterTemplate, shardKey)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to create sveltoscluster-manager deployment %v", err))
		return err
	}

	eventManagerTemplate := controllerSharding.GetEventManagerTemplate()
	err = undeployDeployment(ctx, config, eventManagerTemplate, shardKey)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to create event-manager deployment %v", err))
		return err
	}

	healthcheckManagerTemplate := controllerSharding.GetHealthCheckManagerTemplate()
	err = undeployDeployment(ctx, config, healthcheckManagerTemplate, shardKey)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to create healthcheck-manager deployment %v", err))
		return err
	}

	return nil
}

func deployDeployment(ctx context.Context, c client.Client,
	deploymentTemplate []byte, shardKey string) error {

	data, err := instantiateTemplate(deploymentTemplate, shardKey)
	if err != nil {
		return err
	}

	deployment, err := k8s_utils.GetUnstructured(data)
	if err != nil {
		return err
	}

	err = c.Create(ctx, deployment)
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil
		}

		return err
	}

	return nil
}

func undeployDeployment(ctx context.Context, config *rest.Config,
	deploymentTemplate []byte, shardKey string) error {

	data, err := instantiateTemplate(deploymentTemplate, shardKey)
	if err != nil {
		return err
	}

	deployment, err := k8s_utils.GetUnstructured(data)
	if err != nil {
		return err
	}

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
