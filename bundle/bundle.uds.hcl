# Copyright 2026 Defense Unicorns
# SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

uds {
  bundle_api_version = "uds.dev/v1alpha1"
}

metadata {
  name        = "reference-package-test"
  description = "A UDS bundle for deploying Reference Package and its dependencies on a development cluster"
  version     = "dev"
}

package "postgres-operator" {
  source       = "oci://ghcr.io/uds-packages/postgres-operator:2.0.3-uds.1-upstream"
  values_files = ["values/postgres-operator.yaml"]
}

package "minio-operator" {
  source       = "oci://ghcr.io/uds-packages/minio-operator:7.1.1-uds.27-upstream"
  values_files = ["values/minio-operator.yaml"]
}

package "reference-package" {
  source       = "../zarf-package-reference-package-${sys.arch}-dev.tar.zst"
  depends_on   = [package.postgres-operator, package.minio-operator]
  values_files = ["values/reference-package.yaml"]

  signature_verification {
    verify = false
  }
}
