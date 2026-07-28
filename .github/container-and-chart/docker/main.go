// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/oauth2"
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

// --- Global Variables ---
var (
	// SSO State
	oauth2Config      *oauth2.Config
	oidcVerifier      *oidc.IDTokenVerifier
	ssoEnabled        bool
	guestLoginEnabled bool

	// DB State
	dbPool *pgxpool.Pool
	dbMu   sync.RWMutex

	// Object Storage State
	s3Client *minio.Client
	s3Bucket string
	s3Mu     sync.RWMutex

	// Prometheus Metrics
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

		pool, err := pgxpool.New(context.Background(), connStr)
		if err != nil {
			fmt.Printf("Failed to create Postgres pool: %v\n", err)
			dbConnectedMetric.Set(0)
			return
		}

		for {
			pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := pool.Ping(pingCtx)
			cancel()
			if err == nil {
				fmt.Println("Successfully connected to Postgres!")
				break
			}
			fmt.Printf("Postgres not available yet, retrying in 5s... (%v)\n", err)
			dbConnectedMetric.Set(0)
			time.Sleep(5 * time.Second)
		}

		if _, err := pool.Exec(context.Background(), "CREATE TABLE IF NOT EXISTS kv_store (key TEXT PRIMARY KEY, value TEXT)"); err != nil {
			fmt.Printf("Failed to initialize table: %v\n", err)
		}

		dbMu.Lock()
		dbPool = pool
		dbMu.Unlock()
		dbConnectedMetric.Set(1)

		if err := updateKVCount(context.Background(), pool); err != nil {
			fmt.Printf("Failed to initialize kv count metric: %v\n", err)
		}
	}()

	// --- 1b. Background Object Storage Connection ---
	go func() {
		endpoint := os.Getenv("S3_ENDPOINT")
		if endpoint == "" {
			fmt.Println("S3_ENDPOINT not set. Running in No-S3 mode.")
			objectStorageConnectedMetric.Set(0)
			return
		}

		bucket := os.Getenv("S3_BUCKET")
		useSSL := os.Getenv("S3_USE_SSL") == "true"

		client, err := minio.New(endpoint, &minio.Options{
			Creds:  credentials.NewStaticV4(os.Getenv("S3_ACCESS_KEY"), os.Getenv("S3_SECRET_KEY"), ""),
			Secure: useSSL,
		})
		if err != nil {
			fmt.Printf("Failed to create object storage client: %v\n", err)
			objectStorageConnectedMetric.Set(0)
			return
		}

		for {
			checkCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			exists, err := client.BucketExists(checkCtx, bucket)
			cancel()
			if err == nil && exists {
				fmt.Println("Successfully connected to object storage!")
				break
			}
			if err == nil && !exists {
				fmt.Printf("Object storage bucket %q not found yet, retrying in 5s...\n", bucket)
			} else {
				fmt.Printf("Object storage not available yet, retrying in 5s... (%v)\n", err)
			}
			objectStorageConnectedMetric.Set(0)
			time.Sleep(5 * time.Second)
		}

		s3Mu.Lock()
		s3Client = client
		s3Bucket = bucket
		s3Mu.Unlock()
		objectStorageConnectedMetric.Set(1)

		if err := updateObjectCount(context.Background(), client, bucket); err != nil {
			fmt.Printf("Failed to initialize object count metric: %v\n", err)
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

	// Guest login is enabled by default; operators opt into the stricter mode by
	// setting SSO_GUEST_LOGIN_ENABLED=false (see chart/values.yaml sso.guestLoginEnabled).
	guestLoginEnabled = os.Getenv("SSO_GUEST_LOGIN_ENABLED") != "false"

	cfg := &serverConfig{
		ssoEnabled:        ssoEnabled,
		guestLoginEnabled: guestLoginEnabled,
		oidcVerifier:      oidcVerifier,
	}

	// --- 3. HTTP Routes ---

	if os.Getenv("MONITORING_ENABLED") == "true" {
		http.Handle("/metrics", promhttp.Handler())
	}

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		writeResponseBody(w, []byte("OK"))
	})

	// Main App Page
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !ssoEnabled {
			serveApp(w)
			return
		}

		// Check Guest
		if _, err := r.Cookie("guest_mode"); err == nil {
			if !guestLoginEnabled {
				expireGuestCookie(w)
				cfg.serveLogin(w)
				return
			}
			serveApp(w)
			return
		}

		// Check SSO
		cookie, err := r.Cookie("auth_token")
		if err != nil {
			cfg.serveLogin(w)
			return
		}
		_, err = oidcVerifier.Verify(r.Context(), cookie.Value)
		if err != nil {
			cfg.serveLogin(w)
			return
		}

		serveApp(w)
	})

	// Auth Handlers
	http.HandleFunc("/login", handleLogin)
	http.HandleFunc("/login-guest", cfg.handleGuestLogin)
	http.HandleFunc("/callback", handleCallback)
	http.HandleFunc("/logout", handleLogout)

	// User Info API
	http.HandleFunc("/whoami", cfg.protect(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if _, err := r.Cookie("guest_mode"); err == nil {
			writeJSONResponse(w, UserInfo{Username: "Guest", Type: "guest"})
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
					writeJSONResponse(w, UserInfo{Username: name, Type: "sso"})
					return
				}
			}
		}

		writeJSONResponse(w, UserInfo{Username: "Unknown", Type: "unknown"})
	}))

	// API: Set Value
	http.HandleFunc("/set", cfg.protect(func(w http.ResponseWriter, r *http.Request) {
		dbMu.RLock()
		defer dbMu.RUnlock()

		if dbPool == nil {
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

		_, err := dbPool.Exec(r.Context(), `
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
	http.HandleFunc("/delete", cfg.protect(func(w http.ResponseWriter, r *http.Request) {
		dbMu.RLock()
		defer dbMu.RUnlock()

		if dbPool == nil {
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

		result, err := dbPool.Exec(r.Context(), "DELETE FROM kv_store WHERE key = $1", key)
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
	http.HandleFunc("/get-all", cfg.protect(func(w http.ResponseWriter, r *http.Request) {
		dbMu.RLock()
		defer dbMu.RUnlock()

		if dbPool == nil {
			trackRequest("/get-all", "503")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			writeJSONResponse(w, []KVPair{})
			return
		}

		// --- DB LOGGING START ---
		if os.Getenv("DB_LOG_LEVEL") == "debug" {
			fmt.Println("[DB-READ] Executing: SELECT key, value FROM kv_store ORDER BY key ASC")
		}
		// --- DB LOGGING END ---

		rows, err := dbPool.Query(r.Context(), "SELECT key, value FROM kv_store ORDER BY key ASC")
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
			var pair KVPair
			if err := rows.Scan(&pair.Key, &pair.Value); err != nil {
				trackRequest("/get-all", "500")
				if os.Getenv("DB_LOG_LEVEL") == "debug" {
					fmt.Printf("[DB-ERROR] Scan failed: %v\n", err)
				}
				http.Error(w, "Query failed", http.StatusInternalServerError)
				return
			}
			pairs = append(pairs, pair)
		}
		if err := rows.Err(); err != nil {
			trackRequest("/get-all", "500")
			if os.Getenv("DB_LOG_LEVEL") == "debug" {
				fmt.Printf("[DB-ERROR] Row iteration failed: %v\n", err)
			}
			http.Error(w, "Query failed", http.StatusInternalServerError)
			return
		}

		trackRequest("/get-all", "200")
		w.Header().Set("Content-Type", "application/json")
		writeJSONResponse(w, pairs)
	}))

	// API: Put Object
	http.HandleFunc("/object-put", cfg.protect(func(w http.ResponseWriter, r *http.Request) {
		s3Mu.RLock()
		defer s3Mu.RUnlock()

		if s3Client == nil {
			trackRequest("/object-put", "503")
			http.Error(w, "Object storage unavailable", http.StatusServiceUnavailable)
			return
		}

		key := r.FormValue("key")
		if key == "" {
			trackRequest("/object-put", "400")
			http.Error(w, "Missing key", http.StatusBadRequest)
			return
		}
		body := []byte(r.FormValue("value"))

		_, err := s3Client.PutObject(r.Context(), s3Bucket, key, bytes.NewReader(body), int64(len(body)),
			minio.PutObjectOptions{ContentType: "text/plain"})
		if err != nil {
			trackRequest("/object-put", "500")
			fmt.Printf("[S3-ERROR] /object-put failed: %v\n", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		trackRequest("/object-put", "200")
		// Refresh object count metric after successful write. Best-effort only.
		if err := refreshObjectCount(r.Context()); err != nil {
			if os.Getenv("DB_LOG_LEVEL") == "debug" {
				fmt.Printf("[METRICS] Failed to refresh object count: %v\n", err)
			}
		}
		fmt.Fprint(w, "Success")
	}))

	// API: Delete Object
	http.HandleFunc("/object-delete", cfg.protect(func(w http.ResponseWriter, r *http.Request) {
		s3Mu.RLock()
		defer s3Mu.RUnlock()

		if s3Client == nil {
			trackRequest("/object-delete", "503")
			http.Error(w, "Object storage unavailable", http.StatusServiceUnavailable)
			return
		}

		key := r.FormValue("key")
		if key == "" {
			trackRequest("/object-delete", "400")
			http.Error(w, "Missing key", http.StatusBadRequest)
			return
		}

		if err := s3Client.RemoveObject(r.Context(), s3Bucket, key, minio.RemoveObjectOptions{}); err != nil {
			trackRequest("/object-delete", "500")
			fmt.Printf("[S3-ERROR] /object-delete failed: %v\n", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		trackRequest("/object-delete", "200")
		// Refresh object count metric after successful delete. Best-effort only.
		if err := refreshObjectCount(r.Context()); err != nil {
			if os.Getenv("DB_LOG_LEVEL") == "debug" {
				fmt.Printf("[METRICS] Failed to refresh object count: %v\n", err)
			}
		}
		fmt.Fprint(w, "Success")
	}))

	// API: Get Object Contents
	http.HandleFunc("/object-get", cfg.protect(func(w http.ResponseWriter, r *http.Request) {
		s3Mu.RLock()
		defer s3Mu.RUnlock()

		if s3Client == nil {
			trackRequest("/object-get", "503")
			http.Error(w, "Object storage unavailable", http.StatusServiceUnavailable)
			return
		}

		key := r.URL.Query().Get("key")
		if key == "" {
			trackRequest("/object-get", "400")
			http.Error(w, "Missing key", http.StatusBadRequest)
			return
		}

		obj, err := s3Client.GetObject(r.Context(), s3Bucket, key, minio.GetObjectOptions{})
		if err != nil {
			trackRequest("/object-get", "500")
			fmt.Printf("[S3-ERROR] /object-get failed: %v\n", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		defer obj.Close()

		// minio-go defers the actual fetch until read, so a missing key surfaces here.
		data, err := io.ReadAll(obj)
		if err != nil {
			var minioErr minio.ErrorResponse
			if errors.As(err, &minioErr) && minioErr.Code == "NoSuchKey" {
				trackRequest("/object-get", "404")
				http.Error(w, "Object not found", http.StatusNotFound)
			} else {
				trackRequest("/object-get", "500")
				fmt.Printf("[S3-ERROR] /object-get read failed: %v\n", err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
			}
			return
		}

		trackRequest("/object-get", "200")
		w.Header().Set("Content-Type", "text/plain")
		writeResponseBody(w, data)
	}))

	// API: List Objects
	http.HandleFunc("/object-list", cfg.protect(func(w http.ResponseWriter, r *http.Request) {
		s3Mu.RLock()
		defer s3Mu.RUnlock()

		if s3Client == nil {
			trackRequest("/object-list", "503")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			writeJSONResponse(w, []ObjectInfo{})
			return
		}

		objects := []ObjectInfo{} // guarantees `[]` in JSON output
		for obj := range s3Client.ListObjects(r.Context(), s3Bucket, minio.ListObjectsOptions{Recursive: true}) {
			if obj.Err != nil {
				trackRequest("/object-list", "500")
				fmt.Printf("[S3-ERROR] /object-list failed: %v\n", obj.Err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}
			objects = append(objects, ObjectInfo{Key: obj.Key, Size: obj.Size})
		}

		trackRequest("/object-list", "200")
		w.Header().Set("Content-Type", "application/json")
		writeJSONResponse(w, objects)
	}))

	fmt.Println("Server starting on :8080...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		fmt.Printf("Server failed: %v\n", err)
		os.Exit(1)
	}
}

// --- Helper Functions ---

func trackRequest(path, status string) {
	if os.Getenv("MONITORING_ENABLED") == "true" {
		httpRequestsTotal.WithLabelValues(path, status).Inc()
	}
}

// updateKVCount queries the provided connection for the number of rows in kv_store
// and sets the kvCountMetric accordingly.
func updateKVCount(ctx context.Context, pool *pgxpool.Pool) error {
	var count int64
	row := pool.QueryRow(ctx, "SELECT COUNT(*) FROM kv_store")
	if err := row.Scan(&count); err != nil {
		return err
	}
	kvCountMetric.Set(float64(count))
	return nil
}

// refreshKVCount is a convenience wrapper that uses the global dbPool.
func refreshKVCount(ctx context.Context) error {
	dbMu.RLock()
	defer dbMu.RUnlock()
	if dbPool == nil {
		return fmt.Errorf("db not connected")
	}
	return updateKVCount(ctx, dbPool)
}

// updateObjectCount lists the bucket and sets objectCountMetric to the object count.
func updateObjectCount(ctx context.Context, client *minio.Client, bucket string) error {
	var count int64
	for obj := range client.ListObjects(ctx, bucket, minio.ListObjectsOptions{Recursive: true}) {
		if obj.Err != nil {
			return obj.Err
		}
		count++
	}
	objectCountMetric.Set(float64(count))
	return nil
}

// refreshObjectCount is a convenience wrapper that uses the global s3Client.
func refreshObjectCount(ctx context.Context) error {
	s3Mu.RLock()
	client, bucket := s3Client, s3Bucket
	s3Mu.RUnlock()
	if client == nil {
		return fmt.Errorf("object storage not connected")
	}
	return updateObjectCount(ctx, client, bucket)
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
	// Object storage is considered configured when the deployment injects
	// S3_ENDPOINT; Helm gates that env on objectStorage.endpoint being set.
	data := struct{ ObjectStorageEnabled bool }{
		ObjectStorageEnabled: os.Getenv("S3_ENDPOINT") != "",
	}
	if err := indexTmpl.Execute(w, data); err != nil {
		http.Error(w, "Failed to render page: "+err.Error(), http.StatusInternalServerError)
	}
}

func writeJSONResponse(w http.ResponseWriter, value any) {
	if err := json.NewEncoder(w).Encode(value); err != nil {
		fmt.Printf("Failed to encode JSON response: %v\n", err)
	}
}

func writeResponseBody(w http.ResponseWriter, data []byte) {
	if _, err := w.Write(data); err != nil {
		fmt.Printf("Failed to write response body: %v\n", err)
	}
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

// guestLoginButtonHTML returns the "Login As Guest" anchor when guest login is
// enabled, or an empty string when it has been disabled at deploy time.
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

// expireGuestCookie tells the browser to drop any existing guest_mode cookie.
// Used when guest login has been disabled at deploy time so that pre-existing
// guests are evicted immediately rather than waiting for the cookie's MaxAge.
func expireGuestCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: "guest_mode", Value: "", Path: "/", MaxAge: -1})
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

func handleCallback(w http.ResponseWriter, r *http.Request) {
	if !ssoEnabled {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	// Verify the OAuth state against the cookie set at /login to prevent CSRF on the callback.
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

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return base64.URLEncoding.EncodeToString(b), nil
}
