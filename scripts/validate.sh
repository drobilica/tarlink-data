#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

in_ci() {
	[ -n "${GITHUB_ACTIONS:-}" ]
}

phase() {
	if in_ci; then
		printf '::group::%s\n' "$1"
	else
		printf '==> %s\n' "$1"
	fi
}

finish_phase() {
	if in_ci; then
		printf '::endgroup::\n'
	fi
}

fail_phase() {
	if in_ci; then
		printf '::error::canonical validation failed in phase: %s (exit %s)\n' "$1" "$2"
	fi
	printf 'validation failed in phase: %s (exit %s)\n' "$1" "$2" >&2
	exit "$2"
}

check_format() {
	status=0
	files=$(gofmt -l cmd cli internal) || status=$?
	if [ "$status" -ne 0 ]; then
		return "$status"
	fi
	if [ -n "$files" ]; then
		printf 'unformatted Go files:\n%s\n' "$files" >&2
		return 1
	fi
}

phase 'Format (gofmt)'
status=0
check_format || status=$?
if [ "$status" -ne 0 ]; then
	fail_phase 'Format (gofmt)' "$status"
fi
finish_phase

phase 'Vet (go vet)'
go vet ./... || fail_phase 'Vet (go vet)' $?
finish_phase

phase 'Tests (go test)'
go test ./... || fail_phase 'Tests (go test)' $?
finish_phase

phase 'Shell syntax (sh -n)'
sh -n install.sh uninstall.sh scripts/test-install.sh scripts/test-validate.sh || fail_phase 'Shell syntax (sh -n)' $?
finish_phase

phase 'Installer tests'
./scripts/test-install.sh || fail_phase 'Installer tests' $?
finish_phase

if [ "${1:-}" != "--quick" ]; then
	phase 'Race tests (go test -race)'
	go test -race ./... || fail_phase 'Race tests (go test -race)' $?
	finish_phase
fi

phase 'Build (go build)'
CGO_ENABLED=0 go build ./... || fail_phase 'Build (go build)' $?
finish_phase
