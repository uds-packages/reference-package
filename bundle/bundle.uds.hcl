# Copyright 2024-2026 Defense Unicorns
# SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

uds {
  bundle_api_version = "uds.dev/v1alpha1"
}

metadata {
  name        = "reference-package-test"
  description = "A UDS bundle for deploying Reference Package and its dependencies on a development cluster"
  version     = "dev"
}

package "postgres_operator" {
  source       = "oci://ghcr.io/uds-packages/postgres-operator:1.15.1-uds.6-upstream"
  values_files = ["values/postgres-operator.yaml"]

  signature_verification {
    verify = false
  }
}

package "minio_operator" {
  source       = "oci://ghcr.io/uds-packages/minio-operator:7.1.1-uds.26-upstream"
  values_files = ["values/minio-operator.yaml"]

  signature_verification {
    verify = false
  }
}

package "reference_package" {
  source       = "../zarf-package-reference-package-${sys.arch}-dev.tar.zst"
  values_files = ["values/reference-package.yaml"]
  depends_on   = [package.postgres_operator, package.minio_operator]

  signature_verification {
    verify = false
  }
}
