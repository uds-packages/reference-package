// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// canaryHandler is a stand-in for a protected route body. It writes 200 OK
// when reached so tests can distinguish "middleware passed" from "middleware
// short-circuited".
func canaryHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// guestCookie returns a synthesized cookie header matching the one set by
// handleGuestLogin, for tests that need to simulate a returning guest.
func guestCookie() *http.Cookie {
	return &http.Cookie{Name: "guest_mode", Value: "true"}
}

// setCookieClears reports whether the response instructs the client to drop
// the guest_mode cookie (Max-Age=0 or negative).
func setCookieClears(resp *http.Response, name string) bool {
	for _, cookie := range resp.Cookies() {
		if cookie.Name == name && cookie.MaxAge < 0 {
			return true
		}
	}
	return false
}

func TestHandleGuestLogin(t *testing.T) {
	tests := []struct {
		name                 string
		ssoEnabled           bool
		guestLoginEnabled    bool
		expectedStatus       int
		expectRedirectToRoot bool
	}{
		{
			name:                 "sso off allows guest login regardless of flag",
			ssoEnabled:           false,
			guestLoginEnabled:    false,
			expectedStatus:       http.StatusFound,
			expectRedirectToRoot: true,
		},
		{
			name:                 "sso on with guest enabled sets cookie and redirects",
			ssoEnabled:           true,
			guestLoginEnabled:    true,
			expectedStatus:       http.StatusFound,
			expectRedirectToRoot: true,
		},
		{
			name:              "sso on with guest disabled returns forbidden",
			ssoEnabled:        true,
			guestLoginEnabled: false,
			expectedStatus:    http.StatusForbidden,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := &serverConfig{
				ssoEnabled:        testCase.ssoEnabled,
				guestLoginEnabled: testCase.guestLoginEnabled,
			}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/login-guest", nil)

			cfg.handleGuestLogin(recorder, request)

			resp := recorder.Result()
			defer resp.Body.Close()

			if resp.StatusCode != testCase.expectedStatus {
				t.Fatalf("status: got %d, want %d", resp.StatusCode, testCase.expectedStatus)
			}
			if testCase.expectRedirectToRoot && resp.Header.Get("Location") != "/" {
				t.Errorf("expected Location: /, got %q", resp.Header.Get("Location"))
			}
		})
	}
}

func TestProtectMiddleware(t *testing.T) {
	tests := []struct {
		name                string
		ssoEnabled          bool
		guestLoginEnabled   bool
		attachGuestCookie   bool
		expectedStatus      int
		expectCookieCleared bool
	}{
		{
			name:              "sso off bypasses all auth checks",
			ssoEnabled:        false,
			guestLoginEnabled: false,
			expectedStatus:    http.StatusOK,
		},
		{
			name:              "sso on with guest cookie and flag enabled reaches handler",
			ssoEnabled:        true,
			guestLoginEnabled: true,
			attachGuestCookie: true,
			expectedStatus:    http.StatusOK,
		},
		{
			name:                "sso on with guest cookie but flag disabled returns 401 and evicts cookie",
			ssoEnabled:          true,
			guestLoginEnabled:   false,
			attachGuestCookie:   true,
			expectedStatus:      http.StatusUnauthorized,
			expectCookieCleared: true,
		},
		{
			name:              "sso on without any auth cookie returns 401",
			ssoEnabled:        true,
			guestLoginEnabled: true,
			expectedStatus:    http.StatusUnauthorized,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := &serverConfig{
				ssoEnabled:        testCase.ssoEnabled,
				guestLoginEnabled: testCase.guestLoginEnabled,
			}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/protected", nil)
			if testCase.attachGuestCookie {
				request.AddCookie(guestCookie())
			}

			cfg.protect(canaryHandler)(recorder, request)

			resp := recorder.Result()
			defer resp.Body.Close()

			if resp.StatusCode != testCase.expectedStatus {
				t.Fatalf("status: got %d, want %d", resp.StatusCode, testCase.expectedStatus)
			}
			if testCase.expectCookieCleared && !setCookieClears(resp, "guest_mode") {
				t.Errorf("expected response to expire guest_mode cookie, got Set-Cookie headers: %v", resp.Header.Values("Set-Cookie"))
			}
		})
	}
}

func TestServeLoginRendersGuestButtonWhenEnabled(t *testing.T) {
	tests := []struct {
		name              string
		guestLoginEnabled bool
		expectGuestButton bool
	}{
		{
			name:              "guest login enabled renders the guest anchor",
			guestLoginEnabled: true,
			expectGuestButton: true,
		},
		{
			name:              "guest login disabled hides the guest anchor",
			guestLoginEnabled: false,
			expectGuestButton: false,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := &serverConfig{
				ssoEnabled:        true,
				guestLoginEnabled: testCase.guestLoginEnabled,
			}
			recorder := httptest.NewRecorder()

			cfg.serveLogin(recorder)

			body := recorder.Body.String()
			// The SSO button must always be present regardless of the guest flag
			// so users still have a way to authenticate.
			if !strings.Contains(body, `href="/login"`) {
				t.Errorf("SSO login anchor missing from login page")
			}
			hasGuestButton := strings.Contains(body, `href="/login-guest"`)
			if hasGuestButton != testCase.expectGuestButton {
				t.Errorf("guest button presence: got %v, want %v", hasGuestButton, testCase.expectGuestButton)
			}
		})
	}
}
