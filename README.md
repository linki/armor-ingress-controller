# armor-ingress-controller
[![Build Status](https://travis-ci.org/linki/armor-ingress-controller.svg?branch=master)](https://travis-ci.org/linki/armor-ingress-controller)
[![Coverage Status](https://coveralls.io/repos/github/linki/armor-ingress-controller/badge.svg?branch=master)](https://coveralls.io/github/linki/armor-ingress-controller?branch=master)
[![Go Report Card](https://goreportcard.com/badge/github.com/linki/armor-ingress-controller)](https://goreportcard.com/report/github.com/linki/armor-ingress-controller)

A Kubernetes Ingress Controller for  LabStack's [Armor](https://github.com/labstack/armor)

# Purpose

Expose Kubernetes services via Ingress objects without the need for AWS ELBs or GCE Load Balancers.

# Why

`Armor` is easy to use and gets certificates from [Let's Encrypt](https://letsencrypt.org/) automatically.

# How

* Run `armor` as a `DaemonSet` and backed by a `ConfigMap` in your cluster, listening on ports 80 and 443 on the host.
* Start this controller that watches your Ingress objects, creates a corresponding `armor` config file, updates the `ConfigMap` and restarts `armor`.

## Armor

There are example manifests in the [manifests](manifests) folder that you can use.

Start `armor` in your cluster:

```console
$ kubectl create -f manifests/armor.yaml
daemonset "armor" created
```

Each of your nodes will run an instance of `armor` and each instance will listen on port 80 and 443. Make sure nothing else is listening on these ports and allow public traffic to reach your nodes.

## Armor Ingress Controller

Then deploy the ingress controller:

```console
$ kubectl create -f manifests/controller.yaml
deployment "armor-controller" created
```

It periodically synchronizes your Ingress descriptions with the configuration for `armor`.

# Usage

Create Ingress objects [as usual](https://kubernetes.io/docs/user-guide/ingress) and annotate each Ingress that you want this controller to synchronize with `kubernetes.io/ingress.class: "armor"`.

`Armor` will be configured to automatically redirect any HTTP request to the corresponding HTTPS endpoint. On first request it will get a certificate from Let's Encrypt for the request's hostname. You need to setup global DNS records to point to your cluster so that Let's Encrypt can verify that you are the owner of the domain.

Setup A records for the hostnames you picked in your Ingress to resolve to at least one or all of the public IPs of the nodes where your `armor` pods are running. If you picked the `DaemonSet` approach from above then each instance will run exactly one copy of `armor`.

If you don't want to manage these A records yourself you can use [mate](https://github.com/zalando-incubator/mate) or [dns-controller](https://github.com/kubernetes/kops/tree/master/dns-controller) to take care of that for you.

# Caveats

This is by far not production-ready, because:

* The controller doesn't support the full specification of Ingress. Only path-ignoring hostname-based routing is currently supported. For instance, this allows you to forward any HTTP traffic targeted at `foo.example.com` to service `foo` and `bar.example.com` to service `bar`.
* `Armor` cannot update its configuration at runtime. Therefore, after updating the `ConfigMap` each pod needs to be restarted. As `DaemonSet`s currently [don't supporting rolling updates](https://github.com/kubernetes/kubernetes/pull/31693) the controller takes care of that by killing them all at once.

# Contributing

There's an extensive test suite for the controller code which should make it easy to add functionality without breaking anything. Feel free to create issues or submit pull requests.
