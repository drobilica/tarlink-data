#!/bin/sh
set -eu

repository='https://github.com/drobilica/tarlink-data'
asset='tarlink-data-linux-amd64'
fail() { printf 'install.sh: %s\n' "$1" >&2; exit 1; }

case "$(uname -s)" in Linux) ;; *) fail 'Linux is the only supported operating system' ;; esac
case "$(uname -m)" in x86_64|amd64) ;; *) fail 'unsupported architecture (expected amd64/x86_64)' ;; esac
home=${HOME:?HOME must be set}
case "$home" in /*) ;; *) fail 'HOME must be an absolute path' ;; esac

clean_path() { case "$1" in ''|*'//'|*/|*/./*|*/.|./*|.|*/../*|*/..|../*|..) return 1 ;; esac; }
safe_path() {
    path=$1; clean_path "$path" || return 1; case "$path" in /*) ;; *) return 1 ;; esac
    current=/ remainder=$path
    while [ "$remainder" != / ]; do
        remainder=${remainder#/}; component=${remainder%%/*}
        if [ "$component" = "$remainder" ]; then remainder=/; else remainder=/${remainder#*/}; fi
        [ -n "$component" ] || return 1
        [ "$current" = / ] && current=/$component || current=$current/$component
        [ ! -L "$current" ] || return 1
    done
}

case "$home" in *'//'|*/|*/./*|*/.|*/../*|*/..) fail 'HOME must be a clean path' ;; esac
bin_dir=$home/.local/bin; target=$bin_dir/tarlink-data
state_home=${XDG_STATE_HOME:-$home/.local/state}
clean_path "$state_home" || fail 'XDG_STATE_HOME must be a clean path'
safe_path "$bin_dir" || fail 'canonical binary path contains a symlink'
safe_path "$state_home" || fail 'XDG_STATE_HOME contains a symlink'
marker_dir=$state_home/tarlink-data; marker=$marker_dir/install.sha256
safe_path "$marker_dir" || fail 'ownership path contains a symlink'
command -v curl >/dev/null 2>&1 || fail 'curl is required'
command -v sha256sum >/dev/null 2>&1 || fail 'sha256sum is required'

read_marker() {
    test -f "$marker" && test ! -L "$marker" || fail "ownership marker is not a regular file: $marker"
    marker_digest=$(awk 'NR == 1 { v=$0; next } { bad=1 } END { if (bad || length(v) != 64 || v !~ /^[0-9a-f]+$/) exit 1; print v }' "$marker") || fail "ownership marker is malformed: $marker"
}
verify_owned() {
    test ! -L "$target" || fail "canonical binary must not be a symlink: $target"
    test -f "$target" && test -x "$target" || fail "canonical binary is not an executable regular file: $target"
    read_marker; actual=$(sha256sum "$target" | awk '{print $1}') || fail "could not hash $target"
    test "$actual" = "$marker_digest" || fail "ownership marker does not match $target"
}
if [ -e "$target" ] || [ -L "$target" ]; then verify_owned; elif [ -e "$marker" ] || [ -L "$marker" ]; then fail 'ownership marker exists without its canonical binary'; fi

usage() { printf 'Usage: %s [RELEASE]\n' "$0" >&2; exit 2; }
[ "$#" -le 1 ] || usage
release=${TARLINK_DATA_VERSION:-latest}; [ "$#" -eq 0 ] || release=$1
case "$release" in ''|*[!A-Za-z0-9._-]*) fail 'release must contain only letters, numbers, dots, underscores, and hyphens' ;; esac
if [ "$release" = latest ]; then release_path=latest/download; else release_path="download/$release"; fi
base_url="$repository/releases/$release_path"

mkdir -p "$bin_dir" || fail "could not create $bin_dir"
safe_path "$bin_dir" || fail 'canonical binary directory contains a symlink'
tmp_dir=$(mktemp -d "$bin_dir/.tarlink-data-install.XXXXXXXX") || fail 'could not create private temporary directory'
marker_tmp= previous_target=0 new_target=0 marker_published=0 preserve_tmp=0
rollback() {
    ok=1
    if [ "$new_target" -eq 1 ] && [ -f "$target" ] && [ ! -L "$target" ]; then rm -f -- "$target" || ok=0; fi
    if [ "$previous_target" -eq 1 ] && [ "$ok" -eq 1 ] && [ ! -e "$target" ] && [ ! -L "$target" ]; then mv -- "$tmp_dir/previous" "$target" || ok=0; fi
    [ "$ok" -eq 1 ]
}
cleanup() {
    status=$?
    if [ "$status" -ne 0 ]; then
        [ -z "$marker_tmp" ] || rm -f -- "$marker_tmp" || status=1
        if [ "$marker_published" -eq 0 ] && { [ "$previous_target" -eq 1 ] || [ "$new_target" -eq 1 ]; }; then rollback || { preserve_tmp=1; printf 'install.sh: previous installation was not restored; temporary files remain at %s\n' "$tmp_dir" >&2; }; fi
    fi
    [ "$preserve_tmp" -eq 1 ] || rm -rf -- "$tmp_dir" || status=1
    exit "$status"
}
trap cleanup EXIT; trap 'exit 1' HUP INT TERM
download() { curl -q --fail --location --max-redirs 5 --connect-timeout 15 --max-time 600 --proto '=https' --proto-redir '=https' --tlsv1.2 --output "$2" "$1"; }
checksums=$tmp_dir/SHA256SUMS; binary=$tmp_dir/$asset
download "$base_url/SHA256SUMS" "$checksums" || fail 'could not download SHA256SUMS'
download "$base_url/$asset" "$binary" || fail "could not download $asset"
test -s "$binary" || fail 'downloaded binary is empty'
expected=$(awk -v asset="$asset" 'NF == 2 && $2 == asset && length($1) == 64 && $1 !~ /[^0-9a-f]/ { if (found) duplicate=1; found=1; value=$1 } END { if (!found || duplicate) exit 1; print value }' "$checksums") || fail "SHA256SUMS has no unique checksum for $asset"
actual=$(sha256sum "$binary" | awk '{print $1}')
test "$actual" = "$expected" || fail "SHA-256 verification failed for $asset"
chmod 0755 "$binary"
mkdir -p -m 0700 "$marker_dir" || fail 'could not create ownership directory'
safe_path "$marker_dir" || fail 'ownership directory contains a symlink'
marker_tmp=$(mktemp "$marker_dir/.install.sha256.XXXXXXXX") || fail 'could not create temporary ownership marker'
printf '%s\n' "$actual" > "$marker_tmp"; chmod 0600 "$marker_tmp"
if [ -e "$target" ] || [ -L "$target" ]; then verify_owned; mv -- "$target" "$tmp_dir/previous" || fail 'could not stage previous installation'; previous_target=1; fi
mv -- "$binary" "$target" || fail "could not publish $target"; new_target=1
mv -- "$marker_tmp" "$marker" || fail 'could not publish ownership marker'; marker_tmp=; marker_published=1
printf 'TarLink Data installed successfully at %s\n' "$target"
