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

package sharding

//go:generate go run ../../generator.go

// GetAddonControllerTemplate returns the template
// for the addon-controller deployment
func GetAddonControllerTemplate() []byte {
	return addonControllerTemplate
}

// GetClassifierTemplate returns the template
// for the classifier deployment
func GetClassifierTemplate() []byte {
	return classifierTemplate
}

// GetEventManagerTemplate returns the template
// for the event-manager deployment
func GetEventManagerTemplate() []byte {
	return eventManagerTemplate
}

// GetHealthCheckManagerTemplate returns the template
// for the healthcheck-manager deployment
func GetHealthCheckManagerTemplate() []byte {
	return healthCheckManagerTemplate
}

// GetSveltosClusterManagerTemplate returns the template
// for the sveltosCluster deployment
func GetSveltosClusterManagerTemplate() []byte {
	return sveltosClusterManagerTemplate
}
