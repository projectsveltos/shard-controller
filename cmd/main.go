/*
Copyright 2023.

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

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"syscall"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	"github.com/go-logr/logr"
	"github.com/spf13/pflag"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	cliflag "k8s.io/component-base/cli/flag"
	"k8s.io/klog/v2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
	"github.com/projectsveltos/libsveltos/lib/crd"
	"github.com/projectsveltos/libsveltos/lib/logsettings"
	"github.com/projectsveltos/shard-controller/internal/controller"
	//+kubebuilder:scaffold:imports
)

var (
	setupLog           = ctrl.Log.WithName("setup")
	metricsAddr        string
	probeAddr          string
	agentInMgmtCluster bool
	reportMode         controller.ReportMode
	tmpReportMode      int
	restConfigQPS      float32
	restConfigBurst    int
	webhookPort        int
	syncPeriod         time.Duration
)

const (
	defaulReportMode = int(controller.CollectFromManagementCluster)
)

func main() {
	scheme, err := controller.InitScheme()
	if err != nil {
		os.Exit(1)
	}

	controller.InitMaps()

	klog.InitFlags(nil)

	initFlags(pflag.CommandLine)
	pflag.CommandLine.SetNormalizeFunc(cliflag.WordSepNormalizeFunc)
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.Parse()

	ctrl.SetLogger(klog.Background())

	reportMode = controller.ReportMode(tmpReportMode)

	ctrlOptions := ctrl.Options{
		Scheme:                 scheme,
		HealthProbeBindAddress: probeAddr,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		WebhookServer: webhook.NewServer(
			webhook.Options{
				Port: webhookPort,
			}),
		Cache: cache.Options{
			SyncPeriod: &syncPeriod,
		},
	}

	restConfig := ctrl.GetConfigOrDie()
	restConfig.QPS = restConfigQPS
	restConfig.Burst = restConfigBurst

	mgr, err := ctrl.NewManager(restConfig, ctrlOptions)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Setup the context that's going to be used in controllers and for the manager.
	ctx := ctrl.SetupSignalHandler()

	logsettings.RegisterForLogSettings(ctx,
		libsveltosv1alpha1.ComponentShardController, ctrl.Log.WithName("log-setter"),
		ctrl.GetConfigOrDie())

	if err = (&controller.SveltosClusterReconciler{
		Config:             mgr.GetConfig(),
		Client:             mgr.GetClient(),
		Scheme:             mgr.GetScheme(),
		AgentInMgmtCluster: agentInMgmtCluster,
		ReportMode:         reportMode,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "SveltosCluster")
		os.Exit(1)
	}

	go capiWatchers(ctx, mgr, setupLog)

	//+kubebuilder:scaffold:builder

	setupChecks(mgr)

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func initFlags(fs *pflag.FlagSet) {
	fs.StringVar(&metricsAddr, "metrics-bind-address", ":8080",
		"The address the metric endpoint binds to.")

	fs.StringVar(&probeAddr, "health-probe-bind-address", ":8081",
		"The address the probe endpoint binds to.")

	fs.BoolVar(&agentInMgmtCluster,
		"agent-in-mgmt-cluster",
		false,
		"flag which is passed to sveltos deployments created for a cluster shard")

	fs.IntVar(&tmpReportMode,
		"report-mode",
		defaulReportMode,
		"flag which is passed to sveltos deployments created for a cluster shard")

	const defautlRestConfigQPS = 20
	fs.Float32Var(&restConfigQPS, "kube-api-qps", defautlRestConfigQPS,
		fmt.Sprintf("Maximum queries per second from the controller client to the Kubernetes API server. Defaults to %d",
			defautlRestConfigQPS))

	const defaultRestConfigBurst = 30
	fs.IntVar(&restConfigBurst, "kube-api-burst", defaultRestConfigBurst,
		fmt.Sprintf("Maximum number of queries that should be allowed in one burst from the controller client to the Kubernetes API server. Default %d",
			defaultRestConfigBurst))

	const defaultWebhookPort = 9443
	fs.IntVar(&webhookPort, "webhook-port", defaultWebhookPort,
		"Webhook Server port")

	const defaultSyncPeriod = 10
	fs.DurationVar(&syncPeriod, "sync-period", defaultSyncPeriod*time.Minute,
		fmt.Sprintf("The minimum interval at which watched resources are reconciled (e.g. 15m). Default: %d minutes",
			defaultSyncPeriod))
}

func setupChecks(mgr ctrl.Manager) {
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}
}

func capiWatchers(ctx context.Context, mgr ctrl.Manager, logger logr.Logger) {
	const maxRetries = 20
	retries := 0
	for {
		capiPresent, err := isCAPIInstalled(ctx, mgr.GetClient())
		if err != nil {
			if retries < maxRetries {
				logger.Info(fmt.Sprintf("failed to verify if CAPI is present: %v", err))
				time.Sleep(time.Second)
			}
			retries++
		} else {
			if !capiPresent {
				setupLog.V(logsettings.LogInfo).Info("CAPI currently not present. Starting CRD watcher")
				go crd.WatchCustomResourceDefinition(ctx, mgr.GetConfig(), capiCRDHandler, setupLog)
			} else {
				setupLog.V(logsettings.LogInfo).Info("CAPI present.")
				if err = (&controller.ClusterReconciler{
					Config:             mgr.GetConfig(),
					Client:             mgr.GetClient(),
					Scheme:             mgr.GetScheme(),
					AgentInMgmtCluster: agentInMgmtCluster,
					ReportMode:         reportMode,
				}).SetupWithManager(mgr); err != nil {
					setupLog.Error(err, "unable to create controller", "controller", "Cluster")
					os.Exit(1)
				}
			}
			return
		}
	}
}

// isCAPIInstalled returns true if CAPI is installed, false otherwise
func isCAPIInstalled(ctx context.Context, c client.Client) (bool, error) {
	clusterCRD := &apiextensionsv1.CustomResourceDefinition{}

	err := c.Get(ctx, types.NamespacedName{Name: "clusters.cluster.x-k8s.io"}, clusterCRD)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

// capiCRDHandler restarts process if a CAPI CRD is updated
func capiCRDHandler(gvk *schema.GroupVersionKind) {
	if gvk.Group == clusterv1.GroupVersion.Group {
		if killErr := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); killErr != nil {
			panic("kill -TERM failed")
		}
	}
}
