// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

var (
	s3Client *minio.Client
	s3Bucket string
	s3Mu     sync.RWMutex
)

func connectObjectStorage() {
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
}

func handlePutObject(w http.ResponseWriter, r *http.Request) {
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
	if err := refreshObjectCount(r.Context()); err != nil {
		if os.Getenv("DB_LOG_LEVEL") == "debug" {
			fmt.Printf("[METRICS] Failed to refresh object count: %v\n", err)
		}
	}
	if _, err := fmt.Fprint(w, "Success"); err != nil {
		fmt.Printf("[HTTP-ERROR] /object-put response write failed: %v\n", err)
	}
}

func handleDeleteObject(w http.ResponseWriter, r *http.Request) {
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
	if err := refreshObjectCount(r.Context()); err != nil {
		if os.Getenv("DB_LOG_LEVEL") == "debug" {
			fmt.Printf("[METRICS] Failed to refresh object count: %v\n", err)
		}
	}
	if _, err := fmt.Fprint(w, "Success"); err != nil {
		fmt.Printf("[HTTP-ERROR] /object-delete response write failed: %v\n", err)
	}
}

func handleGetObject(w http.ResponseWriter, r *http.Request) {
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
	defer func() { _ = obj.Close() }()

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
}

func handleListObjects(w http.ResponseWriter, r *http.Request) {
	s3Mu.RLock()
	defer s3Mu.RUnlock()

	if s3Client == nil {
		trackRequest("/object-list", "503")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		writeJSONResponse(w, []ObjectInfo{})
		return
	}

	objects := []ObjectInfo{}
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
}

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

func refreshObjectCount(ctx context.Context) error {
	s3Mu.RLock()
	client, bucket := s3Client, s3Bucket
	s3Mu.RUnlock()
	if client == nil {
		return fmt.Errorf("object storage not connected")
	}
	return updateObjectCount(ctx, client, bucket)
}
