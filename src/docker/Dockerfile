# Copyright 2026 Defense Unicorns
# SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

FROM golang:1.25-alpine AS builder
WORKDIR /app
# Copy dependency files first
COPY go.mod go.sum ./
RUN go mod download

COPY main.go index.html ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o hello-world main.go

FROM scratch
COPY --from=builder /app/hello-world /hello-world

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
ENTRYPOINT ["/hello-world"]
