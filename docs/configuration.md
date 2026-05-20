# Configuration

The Reference Package is configured using the [application's Helm chart](https://github.com/uds-packages/reference-package/tree/main/.github/container-and-chart/helm/chart), alongside the `uds-reference-package-config` UDS config chart.

## Bundle Overrides

Use bundle overrides to configure the Database, SSO, and Monitoring.

```yaml
# bundle/uds-bundle.yaml
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

If you are using the [uds-package-postgres-operator](https://github.com/uds-packages/postgres-operator) in your bundle, the `uds-reference-package-config` chart will create the secret via the below values:

```yaml
# chart/values.yaml
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

#### Secrets creation

The Zalando Postgres Operator uses a `{namespace}.{username}` format for the `users` key in its config. That namespace prefix determines **which namespace** the operator places the credentials secret in. Given the following bundle override on the `postgres-operator` package:

```yaml
# bundle/uds-bundle.yaml
overrides:
  postgres-operator:
    uds-postgres-config:
      values:
        - path: postgresql
          value:
            users:
              reference-package.reference-package: []
            databases:
              reference: reference-package.reference-package
```

The operator creates a secret named `reference-package.reference-package.pg-cluster.credentials.postgresql.acid.zalan.do` in the `reference-package` namespace.

`chart/templates/postgres-secret.yaml` then looks up that secret, extracts the credentials, and writes a `postgres://` connection string into `reference-package-postgres` in the same namespace. The application chart consumes it via the `database` override:

```yaml
# bundle/uds-bundle.yaml
overrides:
  reference-package:
    reference-package:
      values:
        - path: database
          value:
            secretName: "reference-package-postgres"  # must match the secret created by the config chart
            secretKey: "PASSWORD"
```

For external databases (non-operator), set `postgres.password` directly and provide the `host`, `dbName`, and connection details for your database service.

> [!NOTE]
> Setting `postgres.internal` to `false` also changes the egress rule from a scoped namespace selector to unrestricted egress. See [Networking patterns](networking-patterns.md) for details.

> [!IMPORTANT]
> You can learn more about configuring the databases and operator within the [Postgres Operator docs](https://github.com/zalando/postgres-operator/tree/master/docs).

### Single Sign-On

Setting `sso.enabled: true` on the application chart registers the SSO client with Keycloak and wires the resulting credentials into the application.

The UDS Operator reads the SSO configuration from the Package CR (`chart/templates/uds-package.yaml`) and creates a secret named `reference-package-sso` in the `reference-package` namespace. That secret contains:

- `KEYCLOAK_URL`
- `KEYCLOAK_CLIENT_ID`
- `KEYCLOAK_CLIENT_SECRET`
- `APP_CALLBACK_URL`

The application mounts the entire secret as environment variables. The `sso.secretName` value in the bundle override must match the name declared in the Package CR — both default to `reference-package-sso`.

You can read more about how UDS Operator SSO secret templating works in the [Register and customize SSO clients](https://docs.defenseunicorns.com/core/how-to-guides/identity--authorization/register-and-customize-sso-clients/) guide.

### Monitoring

Setting `monitoring.enabled: true` configures the package to expose metrics to Prometheus and creates a Grafana dashboard ConfigMap (`chart/templates/grafana-dashboard.yaml`) that Grafana auto-discovers via the `grafana_dashboard: "1"` label. More information can be found in the [Capture application metrics](https://docs.defenseunicorns.com/core/how-to-guides/monitoring--observability/capture-application-metrics/) guide.

## Package Custom Resources (CR)

For further information regarding the UDS Package Custom Resource (CR), defined in the `chart/templates/uds-package.yaml`, the full specification can be found in the [Packages CR](https://docs.defenseunicorns.com/core/reference/operator--crds/packages-v1alpha1-cr/).
