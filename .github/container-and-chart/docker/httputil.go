// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

func serveApp(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html")
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
