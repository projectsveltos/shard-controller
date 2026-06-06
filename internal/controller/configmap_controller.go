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

package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
)

// ConfigMapReconciler watches the shard-components ConfigMap and re-deploys all active shard
// controller sets whenever it changes.
type ConfigMapReconciler struct {
	client.Client
	Scheme               *runtime.Scheme
	AgentInMgmtCluster   bool
	DriftDetectionConfig string
	SveltosAgentConfig   string
	SveltosApplierConfig string
}

//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

func (r *ConfigMapReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := ctrl.LoggerFrom(ctx)
	logger.V(logs.LogInfo).Info("shard-components ConfigMap changed, redeploying all active shards")
	return ctrl.Result{}, redeployAllShards(ctx, r.Client, r.AgentInMgmtCluster,
		r.DriftDetectionConfig, r.SveltosAgentConfig, r.SveltosApplierConfig, logger)
}

// SetupWithManager registers the ConfigMapReconciler and installs a predicate that limits
// events to the single named ConfigMap in the Sveltos namespace.
func (r *ConfigMapReconciler) SetupWithManager(mgr ctrl.Manager) error {
	configMapName := getShardComponentsConfigMap()
	p := predicate.NewPredicateFuncs(func(object client.Object) bool {
		return object.GetName() == configMapName &&
			object.GetNamespace() == getSveltosNamespace()
	})
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}, builder.WithPredicates(p)).
		Complete(r)
}
