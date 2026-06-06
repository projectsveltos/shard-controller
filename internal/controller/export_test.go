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
	"context"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
)

var (
	TrackCluster        = trackCluster
	StopTrackingCluster = stopTrackingCluster
	ProcessCluster      = processCluster
	DeployControllers   = deployControllers
	UndeployControllers = undeployControllers
	RedeployAllShards   = redeployAllShards

	GetPatchesFromConfigMap = getPatchesFromConfigMap
	ApplyShardPatches       = applyShardPatches

	ClusterMap = &clusterMap
	ShardMap   = &shardMap
)

// DeployDeployment exposes deployDeployment for tests with no patches.
func DeployDeployment(ctx context.Context, c client.Client, deploymentTemplate []byte,
	sveltosNamespace, shardKey string) error {

	return deployDeployment(ctx, c, deploymentTemplate, sveltosNamespace, shardKey, nil, logr.Discard())
}

// DeployDeploymentWithPatches exposes the full deployDeployment signature for patch-aware tests.
func DeployDeploymentWithPatches(ctx context.Context, c client.Client, deploymentTemplate []byte,
	sveltosNamespace, shardKey string, patches []libsveltosv1beta1.Patch, logger logr.Logger) error {

	return deployDeployment(ctx, c, deploymentTemplate, sveltosNamespace, shardKey, patches, logger)
}

// GetShardComponentsPatches exposes getShardComponentsPatches for tests.
func GetShardComponentsPatches(ctx context.Context, c client.Client, logger logr.Logger,
) ([]libsveltosv1beta1.Patch, error) {

	return getShardComponentsPatches(ctx, c, logger)
}

// SetShardComponentsConfigMapForTest lets tests set the ConfigMap name without going through main.
var SetShardComponentsConfigMapForTest = SetShardComponentsConfigMap
