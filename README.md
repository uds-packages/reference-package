# UDS Package Reference Package

This package is designed to serve as a reference of what a UDS Package may look like. This package is not intended to be *functional* and should only serve to show what the layout of a UDS Package can look like. 

The application itself is a simple web app built with Go. This can currently be found in the `src` directory. The function of the app is a page that can write and get queries from a bundled `postgres` database.

### Demonstration
The following should be demonstrated within this UDS Package:
- Dependencies Pulled into a UDS Bundle
- Keycloak SSO Configuration
- Prometheus Service Monitoring
- Helm Overrides
- UDS Config Chart Templates
- Istio Virtual Service Creation
- Network Policy Creation
- Playwright UI Testing


## Pre-requisites

The Reference Package Package expects to be deployed on top of [UDS Core](https://github.com/defenseunicorns/uds-core) with the dependencies listed below being configured prior to deployment.

#### Postgres Database
This package requires a postgres database instance. It is suggested to pull this into the `uds-bundle` by using the `postgres-operator` uds package.
#### Monitoring
To demonstrate monitoring, the `k3d-core-demo` will need to be installed instead of `k3d-core-slim-dev`.

## Releases

The released packages can be found in [ghcr](https://github.com/uds-packages/reference-package/pkgs/container/reference-package).

## UDS Tasks (for local dev and CI)

*For local dev, this requires you install [uds-cli](https://github.com/defenseunicorns/uds-cli?tab=readme-ov-file#install)

> [!TIP]
> To get a list of tasks to run you can use `uds run --list`!

## Contributing

Please see the [CONTRIBUTING.md](./CONTRIBUTING.md)
