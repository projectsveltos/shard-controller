[![CI](https://github.com/projectsveltos/shard-controller/actions/workflows/main.yaml/badge.svg)](https://github.com/projectsveltos/shard-controller/actions)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/projectsveltos/shard-controller/badge)](https://scorecard.dev/viewer/?uri=github.com/projectsveltos/shard-controller)
[![CodeQL](https://github.com/projectsveltos/shard-controller/actions/workflows/codeql.yaml/badge.svg)](https://github.com/projectsveltos/shard-controller/actions/workflows/codeql.yaml)
[![Release](https://img.shields.io/github/v/release/projectsveltos/shard-controller)](https://github.com/projectsveltos/shard-controller/releases)
[![License](https://img.shields.io/badge/license-Apache-blue.svg)](LICENSE)
[![Slack](https://img.shields.io/badge/join%20slack-%23projectsveltos-brighteen)](https://join.slack.com/t/projectsveltos/shared_invite/zt-1hraownbr-W8NTs6LTimxLPB8Erj8Q6Q)
[![LinkedIn](https://custom-icon-badges.demolab.com/badge/LinkedIn-0A66C2?logo=linkedin-white&logoColor=fff)](https://www.linkedin.com/company/projectsveltos/)
[![X URL](https://img.shields.io/twitter/url/https/twitter.com/projectsveltos.svg?style=social&label=Follow%20%40projectsveltos)](https://x.com/projectsveltos)

👋 Welcome to **Projectsveltos**!

<div align="center">

| 🌐 Website | 📚 Documentation | 📅 Book a Demo | 💼 Enterprise Support | 🏢 Adopters |
|:---:|:---:|:---:|:---:|:---:|
| [Visit](https://website.projectsveltos.io) | [Get Started](https://projectsveltos.github.io/sveltos/) | [Schedule 30 min](https://cal.com/gianluca-mardente-nuclsu/30min) | [Contact Us](mailto:gianluca@projectsveltos.io) | [View List](https://website.projectsveltos.io/companies) |

</div>

<img src="https://raw.githubusercontent.com/projectsveltos/sveltos/main/docs/assets/logo.png" width="200">

## What this repository is
shard-controller implements horizontal scaling for Sveltos: it watches managed clusters for a shard annotation and automatically provisions (or removes) a dedicated set of Sveltos controller Deployments for each distinct shard key. This lets a large fleet be partitioned across multiple, independently scaled sets of controllers instead of a single one handling every cluster.

# Useful links

- Projectsveltos [documentation](https://projectsveltos.github.io/sveltos/)
- [Quick Start](https://projectsveltos.github.io/sveltos/quick_start/)

# What is the Projectsveltos?
Sveltos is a Kubernetes add-on controller that simplifies the deployment and management of add-ons and applications across multiple clusters. It runs in the management cluster and can programmatically deploy and manage add-ons and applications on any cluster in the fleet, including the management cluster itself. Sveltos supports a variety of add-on formats, including Helm charts, raw YAML, Kustomize, Carvel ytt, and Jsonnet.

![Kubernetes add-on deployment](https://github.com/projectsveltos/sveltos/blob/main/docs/assets/addons_deployment.gif)
