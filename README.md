# UDS Reference Package

Welcome to the UDS Reference Package! This repository serves as a practical, working example of a well-structured UDS Package.

Inside the `.github` directory, you will find a fully runnable Go-based web application that reads and writes to a Postgres database. However, the application's specific features are secondary. **The primary purpose of this repository is to demonstrate UDS Package architecture, layout, and best practices.** This repository can be referenced alongside the [UDS Documentation](https://uds.defenseunicorns.com/), as a reference guide for building, configuring, and testing own UDS packages.

## What This Demonstrates

This repository should aim to provide functional examples of the following:

* **Bundle Integration:** Pulling dependencies into a UDS Bundle.
* **Authentication:** Keycloak SSO configuration.
* **Observability:** Prometheus service monitoring integration.
* **Configuration:** Helm overrides and UDS Config chart templates.
* **Networking & Security:** Istio Virtual Service and Network Policy creation.
* **Testing:** Playwright UI testing.

---

## Prerequisites

This reference package is designed to be deployed on top of [UDS Core](https://github.com/defenseunicorns/uds-core). Please ensure the following dependencies are configured prior to deployment:

* **Postgres Database:** The Go application requires a Postgres instance. We recommend bringing this into your `uds-bundle` by using the `postgres-operator` UDS package.
* **Monitoring** To successfully demonstrate the monitoring features, you will need to install the `k3d-core-demo` bundle rather than the `k3d-core-slim-dev` bundle.

> [!TIP]
> `k3d-core-demo` is set as the default k3d bundle if you run `uds run default` in the root directory.

## Releases

The released packages can be found in [ghcr](https://github.com/uds-packages/reference-package/pkgs/container/reference-package).

## UDS Tasks (for local dev and CI)

*For local dev, this requires you install [uds-cli](https://github.com/defenseunicorns/uds-cli?tab=readme-ov-file#install)

> [!TIP]
> To get a list of tasks to run you can use `uds run --list`!

## Contributing

Please see the [CONTRIBUTING.md](./CONTRIBUTING.md)
