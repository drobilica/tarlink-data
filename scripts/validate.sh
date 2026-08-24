#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

files=$(gofmt -l cmd cli internal)
if [ -n "$files" ]; then
  printf '%s\n' "$files" >&2
  exit 1
fi
go vet ./...
go test ./...
sh -n install.sh uninstall.sh scripts/test-install.sh
./scripts/test-install.sh
if [ "${1:-}" != "--quick" ]; then
  go test -race ./...
fi
CGO_ENABLED=0 go build ./...
