// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package main

import (
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	dbConnectedMetric = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "app_database_connected",
		Help: "Binary status of database connection (1 = connected, 0 = disconnected)",
	})
	objectStorageConnectedMetric = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "app_object_storage_connected",
		Help: "Binary status of object storage connection (1 = connected, 0 = disconnected)",
	})
	objectCountMetric = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "reference_package_object_count",
		Help: "Current number of objects stored in the configured bucket",
	})
	kvCountMetric = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "reference_package_kv_count",
		Help: "Current number of key/value pairs stored in kv_store",
	})
	httpRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "app_http_requests_total",
		Help: "Total number of HTTP requests by path and status",
	}, []string{"path", "status"})
)

func trackRequest(path, status string) {
	if os.Getenv("MONITORING_ENABLED") == "true" {
		httpRequestsTotal.WithLabelValues(path, status).Inc()
	}
}
