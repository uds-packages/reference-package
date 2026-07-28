// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	dbPool *pgxpool.Pool
	dbMu   sync.RWMutex
)

func connectDatabase() {
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
}

func handleSetValue(w http.ResponseWriter, r *http.Request) {
	dbMu.RLock()
	defer dbMu.RUnlock()

	if dbPool == nil {
		trackRequest("/set", "503")
		http.Error(w, "Database unavailable", http.StatusServiceUnavailable)
		return
	}

	key := r.FormValue("key")
	val := r.FormValue("value")

	if os.Getenv("DB_LOG_LEVEL") == "debug" {
		fmt.Printf("[DB-WRITE] Executing: INSERT INTO kv_store (key, value) VALUES ('%s', '%s') ON CONFLICT UPDATE\n", key, val)
	}

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
	if err := refreshKVCount(r.Context()); err != nil {
		if os.Getenv("DB_LOG_LEVEL") == "debug" {
			fmt.Printf("[METRICS] Failed to refresh kv count: %v\n", err)
		}
	}
	if _, err := fmt.Fprint(w, "Success"); err != nil {
		fmt.Printf("[HTTP-ERROR] /set response write failed: %v\n", err)
	}
}

func handleDeleteValue(w http.ResponseWriter, r *http.Request) {
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

	if os.Getenv("DB_LOG_LEVEL") == "debug" {
		fmt.Printf("[DB-WRITE] Executing: DELETE FROM kv_store WHERE key = '%s'\n", key)
	}

	result, err := dbPool.Exec(r.Context(), "DELETE FROM kv_store WHERE key = $1", key)
	if err != nil {
		trackRequest("/delete", "500")
		if os.Getenv("DB_LOG_LEVEL") == "debug" {
			fmt.Printf("[DB-ERROR] Delete failed: %v\n", err)
		}
		http.Error(w, "DB Error: "+err.Error(), 500)
		return
	}

	if result.RowsAffected() == 0 {
		trackRequest("/delete", "404")
		http.Error(w, "Key not found", http.StatusNotFound)
		return
	}

	trackRequest("/delete", "200")
	if err := refreshKVCount(r.Context()); err != nil {
		if os.Getenv("DB_LOG_LEVEL") == "debug" {
			fmt.Printf("[METRICS] Failed to refresh kv count: %v\n", err)
		}
	}

	if _, err := fmt.Fprint(w, "Success"); err != nil {
		fmt.Printf("[HTTP-ERROR] /delete response write failed: %v\n", err)
	}
}

func handleGetAll(w http.ResponseWriter, r *http.Request) {
	dbMu.RLock()
	defer dbMu.RUnlock()

	if dbPool == nil {
		trackRequest("/get-all", "503")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		writeJSONResponse(w, []KVPair{})
		return
	}

	if os.Getenv("DB_LOG_LEVEL") == "debug" {
		fmt.Println("[DB-READ] Executing: SELECT key, value FROM kv_store ORDER BY key ASC")
	}

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
}

func updateKVCount(ctx context.Context, pool *pgxpool.Pool) error {
	var count int64
	row := pool.QueryRow(ctx, "SELECT COUNT(*) FROM kv_store")
	if err := row.Scan(&count); err != nil {
		return err
	}
	kvCountMetric.Set(float64(count))
	return nil
}

func refreshKVCount(ctx context.Context) error {
	dbMu.RLock()
	defer dbMu.RUnlock()
	if dbPool == nil {
		return fmt.Errorf("db not connected")
	}
	return updateKVCount(ctx, dbPool)
}
