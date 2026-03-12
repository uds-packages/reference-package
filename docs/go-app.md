# Reference Package Web Application & Helm Chart

The underlying application for this UDS package is a lightweight Go web service. It features a simple user interface designed specifically to demonstrate reading and writing data to a Postgres database.

While the application code itself is relatively simple, its primary purpose is to demonstrate integration with various components within the UDS ecosystem.

## Directory Structure & Architecture

You will find the application source code and deployment manifests in the following locations:

* **Go Application Source & Dockerfile:** `.github/container-and-chart/docker`
* **Helm Charts:** `.github/container-and-chart/helm`

> [!NOTE]
> In a standard development scenario, it may make more sense for the Go App and Helm Charts to live in a `src/` directory and is built at runtime. However, the application and its Helm charts are intentially decoupled into this structure. This allows the Docker container and Helm charts to be published independently of the UDS package.
> By doing this, engineers can pull and use our container and Helm chart for their own purposes. It also provides a realistic demonstration of how a UDS package pulls external artifacts via the `common/zarf.yaml` and `.zarf.yaml` files.

## Update & Publishing Workflow

The UDS package relies on the published artifacts in GHCR. If you make changes to the Go application or the Helm chart, you must publish the new versions to GHCR before updating the UDS package. If you update the `zarf.yaml`, image tags in `<flavor>-values.yaml`, etc., before the new artifacts finish publishing, your local Zarf builds and CI pipelines will fail when trying to pull the non-existent versions.

Follow these steps when making changes to the Go application or chart:

1. Update the Go source code or adjust the Helm chart templates as needed.
2. If needed, bump the respective versions in `.github/container-and-chart/docker/version.txt` and `.github/container-and-chart/helm/Chart.yaml`.
3. Commit and push your changes. This will trigger the `.github/workflows/release-container-and-charts.yaml` GitHub Action, which builds and publishes the new container image and Helm chart to GHCR. This workflow is set to only run on PRs to the following:
```yaml
    paths:
      - ".github/container-and-chart/**"
      - ".github/workflows/release-container-and-charts.yaml"
```
4. Ensure the GitHub Action completes successfully and the new artifacts are visible in the repository's GHCR page.
