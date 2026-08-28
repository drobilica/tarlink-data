#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/tarlink-data-validate-test.XXXXXXXX")
trap 'rm -rf "$tmp"' EXIT

mkdir -p "$tmp/bin"

cat >"$tmp/bin/gofmt" <<'EOF'
#!/bin/sh
printf '%s\n' 'cmd/tarlink-data/bad.go'
EOF
chmod 0755 "$tmp/bin/gofmt"

status=0
env -u GITHUB_ACTIONS PATH="$tmp/bin:$PATH" \
	"$root/scripts/validate.sh" --quick >"$tmp/local.out" 2>"$tmp/local.err" || status=$?
test "$status" = 1
grep -F '==> Format (gofmt)' "$tmp/local.out" >/dev/null
grep -F 'unformatted Go files:' "$tmp/local.err" >/dev/null
grep -F 'cmd/tarlink-data/bad.go' "$tmp/local.err" >/dev/null
grep -F 'validation failed in phase: Format (gofmt) (exit 1)' "$tmp/local.err" >/dev/null
if grep -q '^::' "$tmp/local.out"; then
	printf '%s\n' 'local validation must not emit GitHub Actions workflow commands' >&2
	exit 1
fi

status=0
env GITHUB_ACTIONS=true PATH="$tmp/bin:$PATH" \
	"$root/scripts/validate.sh" --quick >"$tmp/ci.out" 2>"$tmp/ci.err" || status=$?
test "$status" = 1
grep -F '::group::Format (gofmt)' "$tmp/ci.out" >/dev/null
grep -F '::error::canonical validation failed in phase: Format (gofmt) (exit 1)' "$tmp/ci.out" >/dev/null
grep -F 'validation failed in phase: Format (gofmt) (exit 1)' "$tmp/ci.err" >/dev/null

cat >"$tmp/bin/gofmt" <<'EOF'
#!/bin/sh
exit 2
EOF
chmod 0755 "$tmp/bin/gofmt"

status=0
env -u GITHUB_ACTIONS PATH="$tmp/bin:$PATH" \
	"$root/scripts/validate.sh" --quick >"$tmp/crash.out" 2>"$tmp/crash.err" || status=$?
test "$status" = 2
grep -F 'validation failed in phase: Format (gofmt) (exit 2)' "$tmp/crash.err" >/dev/null

cat >"$tmp/bin/gofmt" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod 0755 "$tmp/bin/gofmt"
cat >"$tmp/bin/go" <<'EOF'
#!/bin/sh
case "$1" in
	vet) exit 0 ;;
	test) exit 3 ;;
	*) exit 0 ;;
esac
EOF
chmod 0755 "$tmp/bin/go"

status=0
env -u GITHUB_ACTIONS PATH="$tmp/bin:$PATH" \
	"$root/scripts/validate.sh" --quick >"$tmp/status.out" 2>"$tmp/status.err" || status=$?
test "$status" = 3
grep -F '==> Tests (go test)' "$tmp/status.out" >/dev/null
grep -F 'validation failed in phase: Tests (go test) (exit 3)' "$tmp/status.err" >/dev/null

printf '%s\n' 'validation script tests passed'
