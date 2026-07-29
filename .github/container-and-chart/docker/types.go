// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package main

import (
	_ "embed"
	"html/template"

	"github.com/coreos/go-oidc/v3/oidc"
)

//go:embed index.html
var indexHTML string

var indexTmpl = template.Must(template.New("index").Parse(indexHTML))

type KVPair struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type UserInfo struct {
	Username string `json:"username"`
	Type     string `json:"type"`
}

type ObjectInfo struct {
	Key  string `json:"key"`
	Size int64  `json:"size"`
}

// serverConfig captures the auth-related runtime configuration for handlers
// that need to be unit-testable. Handlers close over a *serverConfig so tests
// can construct their own instance per case rather than mutating package
// globals. In production main() builds one instance from environment values.
type serverConfig struct {
	ssoEnabled        bool
	guestLoginEnabled bool
	oidcVerifier      *oidc.IDTokenVerifier
}
