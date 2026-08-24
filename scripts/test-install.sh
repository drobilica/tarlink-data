#!/bin/sh
set -eu
unset XDG_CONFIG_HOME XDG_CACHE_HOME XDG_DATA_HOME XDG_STATE_HOME
case "$(uname -s)" in Darwin) test_tmp=/private/tmp ;; *) test_tmp=${TMPDIR:-/tmp} ;; esac
root=$(mktemp -d "$test_tmp/tarlink-data-test.XXXXXXXX")
trap 'rm -rf "$root"' EXIT
fake=$root/fake; mkdir "$fake"
printf '%s\n' '#!/bin/sh' 'case "$1" in -s) echo "${TEST_OS:-Linux}" ;; -m) echo "${TEST_ARCH:-x86_64}" ;; esac' > "$fake/uname"
printf '%s\n' '#!/bin/sh' 'set -eu' 'out= url=' 'while [ "$#" -gt 0 ]; do case "$1" in --output) out=$2; shift 2 ;; http*) url=$1; shift ;; *) shift ;; esac; done' '[ "${FAIL_DOWNLOAD:-}" != 1 ] || exit 22' 'case "$url" in */SHA256SUMS) cp "$FIXTURE/SHA256SUMS" "$out" ;; */tarlink-data-linux-amd64) cp "$FIXTURE/tarlink-data-linux-amd64" "$out" ;; *) exit 22 ;; esac' > "$fake/curl"
chmod 0755 "$fake/uname" "$fake/curl"
fixture=$root/fixture; mkdir "$fixture"
printf 'synthetic binary\n' > "$fixture/tarlink-data-linux-amd64"
hash=$(sha256sum "$fixture/tarlink-data-linux-amd64" | awk '{print $1}')
printf '%s  tarlink-data-linux-amd64\n' "$hash" > "$fixture/SHA256SUMS"
home=$root/home; mkdir -p "$home"
env PATH="$fake:$PATH" HOME="$home" FIXTURE="$fixture" ./install.sh v0.0.2
test -f "$home/.local/bin/tarlink-data"
test "$(cat "$home/.local/state/tarlink-data/install.sha256")" = "$hash"
old_hash=$hash
printf 'replacement binary\n' > "$fixture/tarlink-data-linux-amd64"
hash=$(sha256sum "$fixture/tarlink-data-linux-amd64" | awk '{print $1}')
printf '%s  tarlink-data-linux-amd64\n' "$hash" > "$fixture/SHA256SUMS"
env PATH="$fake:$PATH" HOME="$home" FIXTURE="$fixture" ./install.sh v0.0.2
test "$(cat "$home/.local/state/tarlink-data/install.sha256")" = "$hash"
printf 'modified\n' > "$home/.local/bin/tarlink-data"
if env PATH="$fake:$PATH" HOME="$home" FIXTURE="$fixture" ./install.sh v0.0.2; then exit 1; fi
printf 'replacement binary\n' > "$home/.local/bin/tarlink-data"
chmod 0755 "$home/.local/bin/tarlink-data"
if env PATH="$fake:$PATH" HOME="$home" FIXTURE="$fixture" FAIL_DOWNLOAD=1 ./install.sh v0.0.2; then exit 1; fi
test "$(cat "$home/.local/state/tarlink-data/install.sha256")" = "$hash"
mkdir -p "$home/.config/tarlink-data" "$home/.cache/tarlink-data" "$home/.local/share/tarlink-data" "$root/external-source" "$root/external-recipes"
printf 'keep\n' > "$root/external-source/file"; printf 'keep\n' > "$root/external-recipes/recipe.yaml"
env PATH="$fake:$PATH" HOME="$home" FIXTURE="$fixture" ./uninstall.sh
test ! -e "$home/.local/bin/tarlink-data"; test ! -e "$home/.local/state/tarlink-data"; test ! -e "$home/.cache/tarlink-data"; test ! -e "$home/.local/share/tarlink-data"; test ! -e "$home/.config/tarlink-data"
test -f "$root/external-source/file"; test -f "$root/external-recipes/recipe.yaml"
env PATH="$fake:$PATH" HOME="$home" FIXTURE="$fixture" ./uninstall.sh
mkdir -p "$home/.local/bin"; printf foreign > "$home/.local/bin/tarlink-data"; chmod 0755 "$home/.local/bin/tarlink-data"
if env PATH="$fake:$PATH" HOME="$home" FIXTURE="$fixture" ./uninstall.sh; then exit 1; fi
rm "$home/.local/bin/tarlink-data"
printf 'owned binary\n' > "$fixture/tarlink-data-linux-amd64"
hash=$(sha256sum "$fixture/tarlink-data-linux-amd64" | awk '{print $1}')
printf '%s  tarlink-data-linux-amd64\n' "$hash" > "$fixture/SHA256SUMS"
env PATH="$fake:$PATH" HOME="$home" FIXTURE="$fixture" ./install.sh v0.0.2
printf modified > "$home/.local/bin/tarlink-data"
if env PATH="$fake:$PATH" HOME="$home" FIXTURE="$fixture" ./uninstall.sh; then exit 1; fi
test -f "$home/.local/bin/tarlink-data"
rm -f "$home/.local/bin/tarlink-data" "$home/.local/state/tarlink-data/install.sha256"
rm -rf "$home/.local/state/tarlink-data"

printf '%s  tarlink-data-linux-amd64\n' 0000000000000000000000000000000000000000000000000000000000000000 > "$fixture/SHA256SUMS"
if env PATH="$fake:$PATH" HOME="$home" FIXTURE="$fixture" ./install.sh v0.0.2; then exit 1; fi
printf '%s\n' 'not-the-asset' > "$fixture/SHA256SUMS"
if env PATH="$fake:$PATH" HOME="$home" FIXTURE="$fixture" ./install.sh v0.0.2; then exit 1; fi
mkdir -p "$home/.local/bin"; printf foreign > "$home/.local/bin/tarlink-data"
if env PATH="$fake:$PATH" HOME="$home" FIXTURE="$fixture" ./install.sh v0.0.2; then exit 1; fi
rm -f "$home/.local/bin/tarlink-data"; ln -s /tmp "$home/.local/bin/tarlink-data"
if env PATH="$fake:$PATH" HOME="$home" FIXTURE="$fixture" ./install.sh v0.0.2; then exit 1; fi
rm -f "$home/.local/bin/tarlink-data"
if env PATH="$fake:$PATH" HOME="$home" TEST_ARCH=arm64 FIXTURE="$fixture" ./install.sh v0.0.2; then exit 1; fi
mkdir -p "$home/.config/tarlink-data"; ln -s "$root/external-source" "$home/.cache/tarlink-data"
if env PATH="$fake:$PATH" HOME="$home" FIXTURE="$fixture" ./uninstall.sh; then exit 1; fi
rm "$home/.cache/tarlink-data"
printf 'installer/uninstaller tests passed\n'
