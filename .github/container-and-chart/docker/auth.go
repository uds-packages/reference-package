// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

var (
	oauth2Config      *oauth2.Config
	oidcVerifier      *oidc.IDTokenVerifier
	ssoEnabled        bool
	guestLoginEnabled bool
)

func initSSO(ctx context.Context) error {
	provider, err := oidc.NewProvider(ctx, os.Getenv("KEYCLOAK_URL"))
	if err != nil {
		return err
	}

	oauth2Config = &oauth2.Config{
		ClientID:     os.Getenv("KEYCLOAK_CLIENT_ID"),
		ClientSecret: os.Getenv("KEYCLOAK_CLIENT_SECRET"),
		RedirectURL:  os.Getenv("APP_CALLBACK_URL"),
		Endpoint:     provider.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}
	oidcVerifier = provider.Verifier(&oidc.Config{ClientID: os.Getenv("KEYCLOAK_CLIENT_ID")})
	return nil
}

func (cfg *serverConfig) handleRoot(w http.ResponseWriter, r *http.Request) {
	if !cfg.ssoEnabled {
		serveApp(w)
		return
	}

	if _, err := r.Cookie("guest_mode"); err == nil {
		if !cfg.guestLoginEnabled {
			expireGuestCookie(w)
			cfg.serveLogin(w)
			return
		}
		serveApp(w)
		return
	}

	cookie, err := r.Cookie("auth_token")
	if err != nil {
		cfg.serveLogin(w)
		return
	}
	_, err = cfg.oidcVerifier.Verify(r.Context(), cookie.Value)
	if err != nil {
		cfg.serveLogin(w)
		return
	}

	serveApp(w)
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if !ssoEnabled {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	state, err := randomString(16)
	if err != nil {
		http.Error(w, "Failed to generate OAuth state", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   300,
	})
	http.Redirect(w, r, oauth2Config.AuthCodeURL(state), http.StatusFound)
}

func (cfg *serverConfig) handleGuestLogin(w http.ResponseWriter, r *http.Request) {
	if cfg.ssoEnabled && !cfg.guestLoginEnabled {
		http.Error(w, "Guest login is disabled", http.StatusForbidden)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "guest_mode",
		Value:    "true",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   3600,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

func handleCallback(w http.ResponseWriter, r *http.Request) {
	if !ssoEnabled {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	stateCookie, err := r.Cookie("oauth_state")
	if err != nil || stateCookie.Value == "" || r.URL.Query().Get("state") != stateCookie.Value {
		http.Error(w, "Invalid OAuth state", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "oauth_state", Value: "", Path: "/", MaxAge: -1})

	ctx := r.Context()
	oauth2Token, err := oauth2Config.Exchange(ctx, r.URL.Query().Get("code"))
	if err != nil {
		http.Error(w, "Failed to exchange token: "+err.Error(), http.StatusInternalServerError)
		return
	}
	rawIDToken, ok := oauth2Token.Extra("id_token").(string)
	if !ok {
		http.Error(w, "No id_token found", http.StatusInternalServerError)
		return
	}
	_, err = oidcVerifier.Verify(ctx, rawIDToken)
	if err != nil {
		http.Error(w, "Failed to verify ID Token: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "auth_token",
		Value:    rawIDToken,
		HttpOnly: true,
		Path:     "/",
		Secure:   true,
		MaxAge:   3600,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	rawIDToken := ""
	if cookie, err := r.Cookie("auth_token"); err == nil {
		rawIDToken = cookie.Value
	}

	http.SetCookie(w, &http.Cookie{Name: "auth_token", Value: "", Path: "/", MaxAge: -1})
	http.SetCookie(w, &http.Cookie{Name: "guest_mode", Value: "", Path: "/", MaxAge: -1})

	if ssoEnabled && rawIDToken != "" {
		redirectURI := os.Getenv("APP_CALLBACK_URL")
		if parsed, err := url.Parse(redirectURI); err == nil {
			redirectURI = fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
		}

		logoutURL := fmt.Sprintf("%s/protocol/openid-connect/logout?post_logout_redirect_uri=%s&id_token_hint=%s",
			strings.TrimSuffix(os.Getenv("KEYCLOAK_URL"), "/"),
			url.QueryEscape(redirectURI),
			rawIDToken,
		)

		http.Redirect(w, r, logoutURL, http.StatusFound)
		return
	}

	http.Redirect(w, r, "/", http.StatusFound)
}

func (cfg *serverConfig) handleWhoami(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if _, err := r.Cookie("guest_mode"); err == nil {
		writeJSONResponse(w, UserInfo{Username: "Guest", Type: "guest"})
		return
	}

	cookie, err := r.Cookie("auth_token")
	if err == nil {
		idToken, err := cfg.oidcVerifier.Verify(r.Context(), cookie.Value)
		if err == nil {
			var claims struct {
				Email             string `json:"email"`
				PreferredUsername string `json:"preferred_username"`
			}
			if err := idToken.Claims(&claims); err == nil {
				name := claims.PreferredUsername
				if name == "" {
					name = claims.Email
				}
				writeJSONResponse(w, UserInfo{Username: name, Type: "sso"})
				return
			}
		}
	}

	writeJSONResponse(w, UserInfo{Username: "Unknown", Type: "unknown"})
}

func (cfg *serverConfig) serveLogin(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html")
	if _, err := fmt.Fprintf(w, `
        <html>
        <body style="font-family: -apple-system, BlinkMacSystemFont, sans-serif; text-align: center; margin-top: 50px; background-color: #f9f9f9;">
            <div style="max-width: 400px; margin: auto; background: white; padding: 40px; border-radius: 12px; box-shadow: 0 4px 10px rgba(0,0,0,0.1);">
                <h2 style="color: #333;">Authentication Required</h2>
                <p style="color: #666; margin-bottom: 30px;">Welcome to the Reference Package</p>

                <div style="display: flex; flex-direction: column; gap: 15px;">
                    <a href="/login" style="background: #007bff; color: white; padding: 12px; text-decoration: none; border-radius: 6px; font-weight: bold; transition: background 0.2s;">
                        Login with SSO
                    </a>%s
                </div>
            </div>
        </body>
        </html>
    `, cfg.guestLoginButtonHTML()); err != nil {
		fmt.Printf("Failed to render login page: %v\n", err)
	}
}

func (cfg *serverConfig) guestLoginButtonHTML() string {
	if !cfg.guestLoginEnabled {
		return ""
	}
	return `
                    <a href="/login-guest" style="background: #6c757d; color: white; padding: 12px; text-decoration: none; border-radius: 6px; font-weight: bold; transition: background 0.2s;">
                        Login As Guest
                    </a>`
}

func (cfg *serverConfig) protect(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !cfg.ssoEnabled {
			next(w, r)
			return
		}
		if _, err := r.Cookie("guest_mode"); err == nil {
			if !cfg.guestLoginEnabled {
				expireGuestCookie(w)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			next(w, r)
			return
		}
		cookie, err := r.Cookie("auth_token")
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		_, err = cfg.oidcVerifier.Verify(r.Context(), cookie.Value)
		if err != nil {
			http.Error(w, "Invalid token", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func expireGuestCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: "guest_mode", Value: "", Path: "/", MaxAge: -1})
}

func randomString(length int) (string, error) {
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return base64.URLEncoding.EncodeToString(buf), nil
}
