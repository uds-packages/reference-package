# Configuration

The Reference Package is configured using the [application's Helm chart](https://github.com/uds-packages/reference-package/tree/main/.github/container-and-chart/helm/chart), alongside the `uds-reference-package` UDS config chart.

## Bundle Overrides

Use bundle overrides in `bundle/uds-bundle.yaml` to configure the Database, SSO, and Monitoring.

```yaml
overrides:
  reference-package:
    reference-package:
      values:
        - path: database
          value:
            secretName: "reference-package-postgres"
            secretKey: "PASSWORD"
        - path: sso
          value:
            enabled: true
            secretName: reference-package-sso
        - path: monitoring
          value:
            enabled: true

```

## UDS Config Chart Values

### PostgreSQL Database

The underlying Go application requires a database connection string provided via a Kubernetes secret.

If you are using the [uds-package-postgres-operator](https://github.com/uds-packages/postgres-operator) in your bundle, the `uds-reference-package-config` chart will create the secret, via the below values:

```yaml
postgres:
  username: "reference"
  # Note: Specifying password as anything other than "" will not use the existingSecret
  password: ""
  existingSecret:
    name: "reference-package.reference-package.pg-cluster.credentials.postgresql.acid.zalan.do"
    passwordKey: password
    usernameKey: username
  host: "pg-cluster.postgres.svc.cluster.local"
  dbName: "reference"
  # Example: "?connect_timeout=10&sslmode=disable"
  connectionOptions: "?sslmode=disable"
  # Set to false to use external postgres
  internal: true
  selector:
    cluster-name: pg-cluster
  namespace: postgres
  port: 5432
```

### Single Sign-On

Setting `sso.enabled: true` in the UDS config chart overrides tells the package to generate an SSO secret.

This relies on the UDS Operator's built-in secret templating. You can read more about how this works in the [UDS SSO Templating Documentation](https://uds.defenseunicorns.com/reference/configuration/single-sign-on/sso-templating/).

### Monitoring

Setting `monitoring.enabled: true` configures the package to expose metrics to Prometheus. More information can be found here: [Monitoring and Metrics](https://uds.defenseunicorns.com/reference/configuration/observability/monitoring-metrics/)

## Package Custom Resources (CR)

For further information regarding the UDS Package Custom Resource (CR), defined in the `chart/templates/uds-package.yaml`, the full specification can be found in the [UDS Package CR Documentation](https://uds.defenseunicorns.com/reference/configuration/custom-resources/exemptions-v1alpha1-cr/).
