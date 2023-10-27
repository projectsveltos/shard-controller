[![CI](https://github.com/projectsveltos/shard-controller/actions/workflows/main.yaml/badge.svg)](https://github.com/projectsveltos/shard-controller/actions)
[![Go Report Card](https://goreportcard.com/badge/github.com/projectsveltos/shard-controller)](https://goreportcard.com/report/github.com/projectsveltos/shard-controller)
[![Slack](https://img.shields.io/badge/join%20slack-%23projectsveltos-brighteen)](https://join.slack.com/t/projectsveltos/shared_invite/zt-1hraownbr-W8NTs6LTimxLPB8Erj8Q6Q)
[![License](https://img.shields.io/badge/license-Apache-blue.svg)](LICENSE)
[![Twitter Follow](https://img.shields.io/twitter/follow/projectsveltos?style=social)](https://twitter.com/projectsveltos)

# Sveltos: Kubernetes add-on controller

<img src="https://raw.githubusercontent.com/projectsveltos/sveltos/main/docs/assets/logo.png" width="200">

# Useful links

- Projectsveltos [documentation](https://projectsveltos.github.io/sveltos/)
- [Quick Start](https://projectsveltos.github.io/sveltos/quick_start/)

# What is the Projectsveltos?
Sveltos is a Kubernetes add-on controller that simplifies the deployment and management of add-ons and applications across multiple clusters. It runs in the management cluster and can programmatically deploy and manage add-ons and applications on any cluster in the fleet, including the management cluster itself. Sveltos supports a variety of add-on formats, including Helm charts, raw YAML, Kustomize, Carvel ytt, and Jsonnet.

![Kubernetes add-on deployment](https://github.com/projectsveltos/sveltos/blob/main/docs/assets/addons_deployment.gif)