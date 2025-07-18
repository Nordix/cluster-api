# Repository Layout

This page covers the repository structure and details about the directories in Cluster API.

```
cluster-api
└───.github
└───api
└───bootstrap
└───cmd
│   │   clusterctl
└───config
└───controllers
└───controlplane
└───dev
└───docs
└───errors
└───exp
└───feature
└───hack
└───internal
└───logos
└───scripts
└───test
└───util
└───version
└───webhooks
└───main.go
└───Makefile
```

### GitHub

[~/.github](https://github.com/kubernetes-sigs/cluster-api/tree/main/.github)

Contains GitHub workflow configuration and templates for Pull requests, bug reports etc.

### API

[~/api](https://github.com/kubernetes-sigs/cluster-api/tree/main/api)

This folder is used to store types and their related resources present in CAPI core. It includes things like API types, spec/status definitions, condition types, simple webhook implementation, autogenerated, deepcopy and conversion files. Some examples of Cluster API types defined in this package include Cluster, ClusterClass, Machine, MachineSet, MachineDeployment and MachineHealthCheck.

API folder has subfolders for each supported API version.

### Bootstrap

[~/bootstrap](https://github.com/kubernetes-sigs/cluster-api/tree/main/bootstrap)

This folder contains  Cluster API bootstrap provider Kubeadm (CABPK) which is a reference implementation of a Cluster API bootstrap provider. This folder contains the types and controllers responsible for generating a cloud-init or ignition configuration to turn a Machine into a Kubernetes Node. It is built and deployed as an independent provider alongside the Cluster API controller manager.

### ControlPlane

[~/controlplane](https://github.com/kubernetes-sigs/cluster-api/tree/main/controlplane)

This folder contains a reference implementation of a Cluster API Control Plane provider - KubeadmControlPlane. This package contains the API types and controllers required to instantiate and manage a Kubernetes control plane. It is built and deployed as an independent provider alongside the Cluster API controller manager.

### Cluster API Provider Docker

[~/test/infrastructure/docker](https://github.com/kubernetes-sigs/cluster-api/tree/main/test/infrastructure/docker)

This folder contains a reference implementation of an infrastructure provider for the Cluster API project using Docker. This provider is intended for development purposes only.

### Clusterctl CLI

[~/cmd/clusterctl](https://github.com/kubernetes-sigs/cluster-api/tree/main/cmd/clusterctl)

This folder contains Clusterctl, a CLI that can be used to deploy Cluster API and providers, generate cluster manifests, read the status of a cluster, and much more.

### Manifest Generation

[~/config](https://github.com/kubernetes-sigs/cluster-api/tree/main/config)

This is a Kubernetes manifest folder containing application resource configuration as kustomize YAML definitions. These are generated from other folders in the repo using `make generate-manifests`

Some of the subfolders are:
* [~/config/certmanager](https://github.com/kubernetes-sigs/cluster-api/tree/main/config/certmanager) - It contains manifests like self-signed issuer CR and certificate CR useful for cert manager.

* [~/config/crd](https://github.com/kubernetes-sigs/cluster-api/tree/main/config/crd) - It contains CRDs generated from types defined in [api](#api) folder

* [~/config/manager](https://github.com/kubernetes-sigs/cluster-api/tree/main/config/manager) - It contains manifest for the deployment of core Cluster API manager.

* [~/config/rbac](https://github.com/kubernetes-sigs/cluster-api/tree/main/config/rbac) - Manifests for RBAC resources generated from kubebuilder markers defined in controllers.

* [~/config/webhook](https://github.com/kubernetes-sigs/cluster-api/tree/main/config/webhook) - Manifest for webhooks generated from the markers defined in the web hook implementations present in [api](#api) folder.

Note: Additional `config` containing manifests can be found in the packages for [KubeadmControlPlane](#controlplane), [KubeadmBoostrap](#bootstrap) and [Cluster API Provider Docker](#cluster-api-provider-docker).

### Controllers

[~/internal](https://github.com/kubernetes-sigs/cluster-api/tree/main/internal)

This folder contains resources which are not meant to be used directly by users of Cluster API e.g. the implementation of controllers is present in [~/internal/controllers](https://github.com/kubernetes-sigs/cluster-api/tree/main/internal/controllers) directory so that we can make changes in controller implementation without breaking users. This allows us to keep our api surface smaller and move faster.

[~/controllers](https://github.com/kubernetes-sigs/cluster-api/tree/main/controllers)

This folder contains reconciler types which provide access to CAPI controllers present in [~/internal/controllers](https://github.com/kubernetes-sigs/cluster-api/tree/main/internal/controllers) directory to our users. These types can be used by users to run any of the Cluster API controllers in an external program.

### Documentation

[~/docs](https://github.com/kubernetes-sigs/cluster-api/tree/main/docs)

This folder is a place for proposals, developer release guidelines and the Cluster API book.

[~/logos](https://github.com/kubernetes-sigs/cluster-api/tree/main/logos)

Cluster API related logos and artwork

### Tools

[~/hack](https://github.com/kubernetes-sigs/cluster-api/tree/main/hack)

This folder has scripts used for building, testing and developer workflow.

[~/scripts](https://github.com/kubernetes-sigs/cluster-api/tree/main/scripts)

This folder consists of CI scripts related to setup, build and e2e tests. These are mostly called by CI jobs.

[~/dev](https://github.com/kubernetes-sigs/cluster-api/tree/main/dev)

This folder has example configuration for integrating Cluster API development with tools like IDEs.

### Util, Feature and Errors

[~/util](https://github.com/kubernetes-sigs/cluster-api/tree/main/util)

This folder contains utilities which are used across multiple CAPI package. These utils are also widely imported in provider implementations and by other users of CAPI.

[~/feature](https://github.com/kubernetes-sigs/cluster-api/tree/main/feature)

This package provides feature gate management used in Cluster API as well as providers. This implementation of feature gates is shared across all providers.

[~/errors](https://github.com/kubernetes-sigs/cluster-api/tree/main/errors)

This is a place for defining errors returned by CAPI. Error types defined here can be used by users of CAPI and the providers.

### Experimental features

[~/exp](https://github.com/kubernetes-sigs/cluster-api/tree/main/exp)

This folder contains experimental features of CAPI. Experimental features are unreliable until they are promoted to the main repository. Each experimental feature is supposed to be present in a subfolder of [~/exp](https://github.com/kubernetes-sigs/cluster-api/tree/main/exp) folder e.g. ClusterResourceSet is present inside [~/exp/addons](https://github.com/kubernetes-sigs/cluster-api/tree/main/api/addons) folder. Historically, machine pool resources are not present in a sub-directory. Migrating them to a subfolder like `~/exp/machinepools` is still pending as it can potentially break existing users who are relying on existing folder structure.

CRDs for experimental features are present outside [~/exp](https://github.com/kubernetes-sigs/cluster-api/tree/main/exp) directory in [~/config](https://github.com/kubernetes-sigs/cluster-api/tree/main/config) folder. Also, these CRDs are deployed in the cluster irrespective of the feature gate value. These features can be enabled and disabled using feature gates supplied to the core Cluster API controller.

### Webhooks

The [api](#api) folder contains webhooks consisting of validators and defaults for many of the types in Cluster API.

[~/internal/webhooks](https://github.com/kubernetes-sigs/cluster-api/tree/main/internal/webhooks)

This directory contains the implementation of some of the Cluster API webhooks. The internal implementation means that the methods supplied by this package cannot be imported by external code bases.

[~/webhooks](https://github.com/kubernetes-sigs/cluster-api/tree/main/webhooks)

This folder exposes the custom webhooks present in [~internal/webhooks](#webhooks) to the users of CAPI.

Note: Additional webhook implementations can be found in the API packages for [KubeadmControlPlane](#controlplane), [KubeadmBoostrap](#bootstrap) and [Cluster API Provider Docker](#cluster-api-provider-docker).
