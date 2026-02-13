// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package main

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/oauth2"
)

//go:embed index.html
var indexHTML string

type KVPair struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type UserInfo struct {
	Username string `json:"username"`
	Type     string `json:"type"`
}

// --- Global Variables ---
var (
	// SSO State
	oauth2Config *oauth2.Config
	oidcVerifier *oidc.IDTokenVerifier
	ssoEnabled   bool

	// DB State
	dbConn *pgx.Conn
	dbMu   sync.RWMutex

	// Prometheus Metrics
	dbConnectedMetric = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "app_database_connected",
		Help: "Binary status of database connection (1 = connected, 0 = disconnected)",
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

func main() {
	ctx := context.Background()

	// --- 1. Background Database Connection ---
	go func() {
		connStr := os.Getenv("DATABASE_URL")
		if connStr == "" {
			fmt.Println("DATABASE_URL not set. Running in No-DB mode.")
			dbConnectedMetric.Set(0)
			return
		}

		for {
			conn, err := pgx.Connect(context.Background(), connStr)
			if err == nil {
				fmt.Println("Successfully connected to Postgres!")

				// Initialize table
				_, err = conn.Exec(context.Background(), "CREATE TABLE IF NOT EXISTS kv_store (key TEXT PRIMARY KEY, value TEXT)")
				if err != nil {
					fmt.Printf("Failed to initialize table: %v\n", err)
				}

				dbMu.Lock()
				dbConn = conn
				dbMu.Unlock()
				dbConnectedMetric.Set(1)
				// Initialize KV count metric from DB
				if err := updateKVCount(context.Background(), conn); err != nil {
					fmt.Printf("Failed to initialize kv count metric: %v\n", err)
				}
				break
			}
			fmt.Printf("Postgres not available yet, retrying in 5s... (%v)\n", err)
			dbConnectedMetric.Set(0)
			time.Sleep(5 * time.Second)
		}
	}()

	// --- 2. SSO Setup ---
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

	// --- 3. HTTP Routes ---

	if os.Getenv("MONITORING_ENABLED") == "true" {
		http.Handle("/metrics", promhttp.Handler())
	}

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Main App Page
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !ssoEnabled {
			serveApp(w)
			return
		}

		// Check Guest
		if _, err := r.Cookie("guest_mode"); err == nil {
			serveApp(w)
			return
		}

		// Check SSO
		cookie, err := r.Cookie("auth_token")
		if err != nil {
			serveLogin(w)
			return
		}
		_, err = oidcVerifier.Verify(r.Context(), cookie.Value)
		if err != nil {
			serveLogin(w)
			return
		}

		serveApp(w)
	})

	// Auth Handlers
	http.HandleFunc("/login", handleLogin)
	http.HandleFunc("/login-guest", handleGuestLogin)
	http.HandleFunc("/callback", handleCallback)
	http.HandleFunc("/logout", handleLogout)

	// User Info API
	http.HandleFunc("/whoami", protect(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if _, err := r.Cookie("guest_mode"); err == nil {
			json.NewEncoder(w).Encode(UserInfo{Username: "Guest", Type: "guest"})
			return
		}

		cookie, err := r.Cookie("auth_token")
		if err == nil {
			idToken, err := oidcVerifier.Verify(r.Context(), cookie.Value)
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
					json.NewEncoder(w).Encode(UserInfo{Username: name, Type: "sso"})
					return
				}
			}
		}

		json.NewEncoder(w).Encode(UserInfo{Username: "Unknown", Type: "unknown"})
	}))

	// API: Set Value
	http.HandleFunc("/set", protect(func(w http.ResponseWriter, r *http.Request) {
		dbMu.RLock()
		defer dbMu.RUnlock()

		if dbConn == nil {
			trackRequest("/set", "503")
			http.Error(w, "Database unavailable", http.StatusServiceUnavailable)
			return
		}

		key := r.FormValue("key")
		val := r.FormValue("value")

		// --- DB LOGGING START ---
		if os.Getenv("DB_LOG_LEVEL") == "debug" {
			fmt.Printf("[DB-WRITE] Executing: INSERT INTO kv_store (key, value) VALUES ('%s', '%s') ON CONFLICT UPDATE\n", key, val)
		}
		// --- DB LOGGING END ---

		_, err := dbConn.Exec(r.Context(), `
            INSERT INTO kv_store (key, value) VALUES ($1, $2)
            ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
			key, val)
		if err != nil {
			trackRequest("/set", "500")
			if os.Getenv("DB_LOG_LEVEL") == "debug" {
				fmt.Printf("[DB-ERROR] Write failed: %v\n", err)
			}
			http.Error(w, "DB Error: "+err.Error(), 500)
			return
		}

		trackRequest("/set", "200")
		// Refresh kv count metric after successful write. Best-effort only.
		if err := refreshKVCount(r.Context()); err != nil {
			if os.Getenv("DB_LOG_LEVEL") == "debug" {
				fmt.Printf("[METRICS] Failed to refresh kv count: %v\n", err)
			}
		}
		fmt.Fprint(w, "Success")
	}))

	// API: Delete Value
	http.HandleFunc("/delete", protect(func(w http.ResponseWriter, r *http.Request) {
		dbMu.RLock()
		defer dbMu.RUnlock()

		if dbConn == nil {
			trackRequest("/delete", "503")
			http.Error(w, "Database unavailable", http.StatusServiceUnavailable)
			return
		}

		key := r.FormValue("key")
		if key == "" {
			trackRequest("/delete", "400")
			http.Error(w, "Missing key", http.StatusBadRequest)
			return
		}

		// --- DB LOGGING START ---
		if os.Getenv("DB_LOG_LEVEL") == "debug" {
			fmt.Printf("[DB-WRITE] Executing: DELETE FROM kv_store WHERE key = '%s'\n", key)
		}
		// --- DB LOGGING END ---

		result, err := dbConn.Exec(r.Context(), "DELETE FROM kv_store WHERE key = $1", key)
		if err != nil {
			trackRequest("/delete", "500")
			if os.Getenv("DB_LOG_LEVEL") == "debug" {
				fmt.Printf("[DB-ERROR] Delete failed: %v\n", err)
			}
			http.Error(w, "DB Error: "+err.Error(), 500)
			return
		}

		// If no rows affected, return 404
		if result.RowsAffected() == 0 {
			trackRequest("/delete", "404")
			http.Error(w, "Key not found", http.StatusNotFound)
			return
		}

		trackRequest("/delete", "200")
		// Refresh kv count metric after successful delete. Best-effort only.
		if err := refreshKVCount(r.Context()); err != nil {
			if os.Getenv("DB_LOG_LEVEL") == "debug" {
				fmt.Printf("[METRICS] Failed to refresh kv count: %v\n", err)
			}
		}

		fmt.Fprint(w, "Success")
	}))

	// API: Get All Values
	http.HandleFunc("/get-all", protect(func(w http.ResponseWriter, r *http.Request) {
		dbMu.RLock()
		defer dbMu.RUnlock()

		if dbConn == nil {
			trackRequest("/get-all", "503")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode([]KVPair{})
			return
		}

		// --- DB LOGGING START ---
		if os.Getenv("DB_LOG_LEVEL") == "debug" {
			fmt.Println("[DB-READ] Executing: SELECT key, value FROM kv_store ORDER BY key ASC")
		}
		// --- DB LOGGING END ---

		rows, err := dbConn.Query(r.Context(), "SELECT key, value FROM kv_store ORDER BY key ASC")
		if err != nil {
			trackRequest("/get-all", "500")
			if os.Getenv("DB_LOG_LEVEL") == "debug" {
				fmt.Printf("[DB-ERROR] Read failed: %v\n", err)
			}
			http.Error(w, "Query failed", 500)
			return
		}
		defer rows.Close()

		var pairs []KVPair
		for rows.Next() {
			var p KVPair
			rows.Scan(&p.Key, &p.Value)
			pairs = append(pairs, p)
		}

		trackRequest("/get-all", "200")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(pairs)
	}))

	fmt.Println("Server starting on :8080...")
	http.ListenAndServe(":8080", nil)
}

// --- Helper Functions ---

func trackRequest(path, status string) {
	if os.Getenv("MONITORING_ENABLED") == "true" {
		httpRequestsTotal.WithLabelValues(path, status).Inc()
	}
}

// updateKVCount queries the provided connection for the number of rows in kv_store
// and sets the kvCountMetric accordingly.
func updateKVCount(ctx context.Context, conn *pgx.Conn) error {
	var count int64
	row := conn.QueryRow(ctx, "SELECT COUNT(*) FROM kv_store")
	if err := row.Scan(&count); err != nil {
		return err
	}
	kvCountMetric.Set(float64(count))
	return nil
}

// refreshKVCount is a convenience wrapper that uses the global dbConn.
func refreshKVCount(ctx context.Context) error {
	dbMu.RLock()
	defer dbMu.RUnlock()
	if dbConn == nil {
		return fmt.Errorf("db not connected")
	}
	return updateKVCount(ctx, dbConn)
}

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

func serveApp(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprint(w, indexHTML)
}

func serveLogin(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprint(w, `
        <html>
        <body style="font-family: -apple-system, BlinkMacSystemFont, sans-serif; text-align: center; margin-top: 50px; background-color: #f9f9f9;">
            <div style="max-width: 400px; margin: auto; background: white; padding: 40px; border-radius: 12px; box-shadow: 0 4px 10px rgba(0,0,0,0.1);">
                <h2 style="color: #333;">Authentication Required</h2>
                <p style="color: #666; margin-bottom: 30px;">Welcome to the Reference Package</p>
                
                <div style="display: flex; flex-direction: column; gap: 15px;">
                    <a href="/login" style="background: #007bff; color: white; padding: 12px; text-decoration: none; border-radius: 6px; font-weight: bold; transition: background 0.2s;">
                        Login with SSO
                    </a>
                    <a href="/login-guest" style="background: #6c757d; color: white; padding: 12px; text-decoration: none; border-radius: 6px; font-weight: bold; transition: background 0.2s;">
                        Login As Guest
                    </a>
                </div>
            </div>
        </body>
        </html>
    `)
}

func protect(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !ssoEnabled {
			next(w, r)
			return
		}
		if _, err := r.Cookie("guest_mode"); err == nil {
			next(w, r)
			return
		}
		cookie, err := r.Cookie("auth_token")
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		_, err = oidcVerifier.Verify(r.Context(), cookie.Value)
		if err != nil {
			http.Error(w, "Invalid token", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func handleGuestLogin(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "guest_mode",
		Value:    "true",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   3600,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

// Updated Logout Logic
func handleLogout(w http.ResponseWriter, r *http.Request) {
	// 1. Grab the ID token before we delete the cookie
	rawIDToken := ""
	if cookie, err := r.Cookie("auth_token"); err == nil {
		rawIDToken = cookie.Value
	}

	// 2. Clear Local Cookies
	http.SetCookie(w, &http.Cookie{Name: "auth_token", Value: "", Path: "/", MaxAge: -1})
	http.SetCookie(w, &http.Cookie{Name: "guest_mode", Value: "", Path: "/", MaxAge: -1})

	// 3. If SSO is enabled and we have a token, we must call Keycloak's logout endpoint
	if ssoEnabled && rawIDToken != "" {
		// Construct the "Return to App" URL (Base URL of your app)
		// We derive this from the callback URL environment variable
		// e.g., "https://reference-package.uds.dev/callback" -> "https://reference-package.uds.dev"
		redirectURI := os.Getenv("APP_CALLBACK_URL")
		if u, err := url.Parse(redirectURI); err == nil {
			redirectURI = fmt.Sprintf("%s://%s", u.Scheme, u.Host)
		}

		// Keycloak standard logout endpoint:
		// <KEYCLOAK_URL>/protocol/openid-connect/logout?post_logout_redirect_uri=<APP_URL>&id_token_hint=<TOKEN>
		logoutURL := fmt.Sprintf("%s/protocol/openid-connect/logout?post_logout_redirect_uri=%s&id_token_hint=%s",
			strings.TrimSuffix(os.Getenv("KEYCLOAK_URL"), "/"), // Ensure no double slashes
			url.QueryEscape(redirectURI),
			rawIDToken,
		)

		http.Redirect(w, r, logoutURL, http.StatusFound)
		return
	}

	// If Guest or SSO disabled, just go back to home
	http.Redirect(w, r, "/", http.StatusFound)
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if !ssoEnabled {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	state := randomString(16)
	http.Redirect(w, r, oauth2Config.AuthCodeURL(state), http.StatusFound)
}

func handleCallback(w http.ResponseWriter, r *http.Request) {
	if !ssoEnabled {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
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

func randomString(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}
