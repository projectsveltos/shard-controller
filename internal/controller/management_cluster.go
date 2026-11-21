/*
Copyright 2026. projectsveltos.io. All rights reserved.

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

var (
	sveltosNamespace     string
	sveltosAgentDisabled bool

	// how many clusters one sveltos-agent (classifier) and one drift-detection-manager (addon-controller)
	// serve in agentless mode. A value lower than 2 keeps one instance per cluster
	sveltosAgentClustersPerInstance   int
	driftDetectionClustersPerInstance int
)

func SetSveltosNamespace(ns string) {
	sveltosNamespace = ns
}

// SetSveltosAgentDisabled sets whether sveltos-agent is not deployed. When true, addon-controller and
// classifier deployments created for a shard are started with --disable-sveltos-agent, and
// event-manager and healthcheck-manager (which need sveltos-agent) are not deployed.
func SetSveltosAgentDisabled(disabled bool) {
	sveltosAgentDisabled = disabled
}

// SetSveltosAgentClustersPerInstance sets the value passed, as --agent-clusters-per-instance, to the
// classifier deployments created for a shard (agentless mode only)
func SetSveltosAgentClustersPerInstance(clustersPerInstance int) {
	sveltosAgentClustersPerInstance = clustersPerInstance
}

// SetDriftDetectionClustersPerInstance sets the value passed, as --agent-clusters-per-instance, to the
// addon-controller deployments created for a shard (agentless mode only)
func SetDriftDetectionClustersPerInstance(clustersPerInstance int) {
	driftDetectionClustersPerInstance = clustersPerInstance
}

func getSveltosAgentDisabled() bool {
	return sveltosAgentDisabled
}

func getSveltosNamespace() string {
	return sveltosNamespace
}
