#!/usr/bin/env bash
set -euo pipefail

go test "$@" ./cmd/... ./internal/...
