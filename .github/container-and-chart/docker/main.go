// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	ctx := context.Background()

	go connectDatabase()
	go connectObjectStorage()

	if os.Getenv("KEYCLOAK_URL") != "" {
		fmt.Println("Initializing SSO...")
		if err := initSSO(ctx); err != nil {
			fmt.Printf("WARNING: SSO failed to initialize: %v. Running in INSECURE mode.\n", err)
			ssoEnabled = false
		} else {
			fmt.Println("SSO Initialized successfully.")
			ssoEnabled = true
		}
	} else {
		fmt.Println("KEYCLOAK_URL not set. Running in INSECURE mode (SSO Disabled).")
		ssoEnabled = false
	}

	// Guest login is enabled by default; operators opt into the stricter mode by
	// setting SSO_GUEST_LOGIN_ENABLED=false (see chart/values.yaml sso.guestLoginEnabled).
	guestLoginEnabled = os.Getenv("SSO_GUEST_LOGIN_ENABLED") != "false"

	cfg := &serverConfig{
		ssoEnabled:        ssoEnabled,
		guestLoginEnabled: guestLoginEnabled,
		oidcVerifier:      oidcVerifier,
	}

	if os.Getenv("MONITORING_ENABLED") == "true" {
		http.Handle("/metrics", promhttp.Handler())
	}

	http.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		writeResponseBody(w, []byte("OK"))
	})

	http.HandleFunc("/", cfg.handleRoot)
	http.HandleFunc("/login", handleLogin)
	http.HandleFunc("/login-guest", cfg.handleGuestLogin)
	http.HandleFunc("/callback", handleCallback)
	http.HandleFunc("/logout", handleLogout)
	http.HandleFunc("/whoami", cfg.protect(cfg.handleWhoami))
	http.HandleFunc("/set", cfg.protect(handleSetValue))
	http.HandleFunc("/delete", cfg.protect(handleDeleteValue))
	http.HandleFunc("/get-all", cfg.protect(handleGetAll))
	http.HandleFunc("/object-put", cfg.protect(handlePutObject))
	http.HandleFunc("/object-delete", cfg.protect(handleDeleteObject))
	http.HandleFunc("/object-get", cfg.protect(handleGetObject))
	http.HandleFunc("/object-list", cfg.protect(handleListObjects))

	fmt.Println("Server starting on :8080...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		fmt.Printf("Server failed: %v\n", err)
		os.Exit(1)
	}
}
