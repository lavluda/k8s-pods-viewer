#!/usr/bin/env bash
set -euo pipefail

go install github.com/google/go-licenses@latest
go mod download

GO_LICENSES_BIN="${GOBIN:-$(go env GOPATH)/bin}/go-licenses"
GOROOT=$(go env GOROOT) "$GO_LICENSES_BIN" report ./... --template hack/attribution.tmpl > ATTRIBUTION.md
