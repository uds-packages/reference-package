# Networking patterns

UDS packages declare their networking in a `Package` CR. The UDS Operator translates those declarations into Kubernetes `NetworkPolicy` and Istio resources, so package authors never write those directly.

> [!NOTE]
> For a full explanation of the networking model, see [Networking & Service Mesh](https://docs.defenseunicorns.com/core/concepts/core-features/networking/) and the [Packages CR reference](https://docs.defenseunicorns.com/core/reference/operator--crds/packages-v1alpha1-cr/).

## Standard package network block

A typical UDS package network block covers three concerns: exposing the application through a gateway, scoped intra-namespace communication, and egress to its dependencies.

```yaml
# chart/templates/uds-package.yaml
spec:
  network:

    expose:
      - service: reference-package
        selector:
          app: reference-package
        gateway: tenant                    
        host: reference-package            # resolves to reference-package.<domain>
        port: 8080

    allow:
      # Intra-namespace — scoped to known pod labels where possible
      - direction: Ingress
        selector:
          app: reference-package
        remoteSelector:
          app: reference-package

      - direction: Egress
        selector:
          app: reference-package
        remoteSelector:
          app: reference-package

      # Cross-namespace — scoped to the target namespace and pod selector
      - direction: Egress
        selector:
          app: reference-package
        remoteNamespace: postgres
        remoteSelector:
          cluster-name: pg-cluster
        port: 5432
        description: "Postgres database"
```

## Non-authservice SSO

Applications that handle OIDC natively (not through authservice) need to add the Keycloak network rules manually. The operator only generates these automatically for authservice clients.

```yaml
# chart/templates/uds-package.yaml
allow:
  # Direct backend connection to Keycloak
  - direction: Egress
    remoteNamespace: keycloak
    remoteSelector:
      app.kubernetes.io/name: keycloak
    selector:
      app: reference-package
    port: 8080
    description: "Keycloak OIDC backend"

  # Keycloak issuer URL resolves through the tenant gateway, not directly
  - direction: Egress
    remoteNamespace: istio-tenant-gateway
    remoteSelector:
      app: tenant-ingressgateway
    selector:
      app: reference-package
    port: 443
    description: "Keycloak via tenant gateway for OIDC issuer"
```

## External egress

To reach a known external host, use `remoteHost`. Each host requires its own entry; wildcards are not supported.

```yaml
# chart/templates/uds-package.yaml
allow:
  - direction: Egress
    selector:
      app: reference-package
    remoteHost: api.example.com        # exact hostname; wildcards are not supported
    port: 443
    description: "External API"
```

`remoteProtocol` defaults to `TLS` when `remoteHost` is set. Use `HTTP` for plaintext egress.

## Internal vs. external dependency toggle

Packages that support both in-cluster and external dependencies use a values flag with a Helm conditional to switch between a scoped rule and an unrestricted one:

```yaml
# chart/templates/uds-package.yaml
allow:
  - direction: Egress
    selector:
      app: reference-package
    {{- if .Values.postgres.internal }}
    remoteNamespace: {{ .Values.postgres.namespace | quote }}
    remoteSelector:
      {{ .Values.postgres.selector | toYaml | nindent 10 }}
    port: {{ .Values.postgres.port }}
    {{- else }}
    remoteGenerated: Anywhere
    {{- end }}
    description: "Postgres database"
```

Bundle deployers toggle this via the config chart:

```yaml
# bundle/uds-bundle.yaml
overrides:
  reference-package:
    uds-reference-package-config:
      values:
        - path: postgres.internal
          value: false
```

> [!IMPORTANT]
> `remoteGenerated: Anywhere` opens unrestricted egress. Prefer a specific `remoteHost` or scoped `remoteNamespace` rule wherever the target is known.

## Extending network rules at deploy time

Packages expose an `additionalNetworkAllow` key so deployers can inject rules for integrations not known at package-authoring time:

```yaml
# bundle/uds-bundle.yaml
overrides:
  reference-package:
    uds-reference-package-config:
      values:
        - path: additionalNetworkAllow
          value:
            - direction: Egress
              selector:
                app: reference-package
              remoteNamespace: my-service
              remoteSelector:
                app: my-service
              port: 8080
              description: "Custom integration"
```
