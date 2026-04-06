#!/usr/bin/env bash
set -euo pipefail

go install github.com/google/go-licenses@latest
go mod download

GO_LICENSES_BIN="${GOBIN:-$(go env GOPATH)/bin}/go-licenses"
GO_LICENSES_GOOS="${GO_LICENSES_GOOS:-linux}"
GO_LICENSES_GOARCH="${GO_LICENSES_GOARCH:-amd64}"

# Generate attribution against a stable target so local macOS runs match CI.
GOOS="$GO_LICENSES_GOOS" GOARCH="$GO_LICENSES_GOARCH" GOROOT=$(go env GOROOT) \
  "$GO_LICENSES_BIN" report ./... --template hack/attribution.tmpl > ATTRIBUTION.md
