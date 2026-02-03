# UDS Package Reference Package

This package is designed to serve as a reference of what a UDS Package may look like. This package is not intended to be *functional* and should only serve to show what the layout of a UDS Package can look like. 

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

## Releases

The released packages can be found in [ghcr](https://github.com/uds-packages/reference-package/pkgs/container/reference-package).

## UDS Tasks (for local dev and CI)

*For local dev, this requires you install [uds-cli](https://github.com/defenseunicorns/uds-cli?tab=readme-ov-file#install)

> [!TIP]
> To get a list of tasks to run you can use `uds run --list`!

## Contributing

Please see the [CONTRIBUTING.md](./CONTRIBUTING.md)
