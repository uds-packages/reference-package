// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleHealth(t *testing.T) {
	dbMu.Lock()
	dbPool = nil
	dbMu.Unlock()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health", nil)

	handleHealth(recorder, request)

	resp := recorder.Result()
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestHandleReadyWithoutDatabase(t *testing.T) {
	dbMu.Lock()
	dbPool = nil
	dbMu.Unlock()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/ready", nil)

	handleReady(recorder, request)

	resp := recorder.Result()
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusServiceUnavailable)
	}
}
