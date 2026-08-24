#!/bin/sh
set -eu
fail() { printf 'uninstall.sh: %s\n' "$1" >&2; exit 1; }
home=${HOME:?HOME must be set}
case "$home" in /*) ;; *) fail 'HOME must be an absolute path' ;; esac
case "$home" in /|*'//'|*/|*/./*|*/.|*/../*|*/..) fail 'HOME must be a clean non-root path' ;; esac
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
xdg_root() {
    name=$1; fallback=$2
    case "$name" in
        XDG_STATE_HOME) value=${XDG_STATE_HOME:-$fallback} ;;
        XDG_CACHE_HOME) value=${XDG_CACHE_HOME:-$fallback} ;;
        XDG_DATA_HOME) value=${XDG_DATA_HOME:-$fallback} ;;
        XDG_CONFIG_HOME) value=${XDG_CONFIG_HOME:-$fallback} ;;
        *) fail 'internal XDG path error' ;;
    esac
    clean_path "$value" || fail "$name must be a clean path"
    case "$value" in /*) ;; *) fail "$name must be absolute" ;; esac
    [ "$value" != / ] || fail "$name must not be /"
    printf '%s/tarlink-data\n' "$value"
}

target=$home/.local/bin/tarlink-data
state_root=$(xdg_root XDG_STATE_HOME "$home/.local/state")
cache_root=$(xdg_root XDG_CACHE_HOME "$home/.cache")
data_root=$(xdg_root XDG_DATA_HOME "$home/.local/share")
config_root=$(xdg_root XDG_CONFIG_HOME "$home/.config")
state_marker=$state_root/install.sha256
for path in "$target" "$state_root" "$cache_root" "$data_root" "$config_root"; do safe_path "$path" || fail "unsafe managed path: $path"; done
command -v sha256sum >/dev/null 2>&1 || fail 'sha256sum is required'

if [ -e "$target" ] || [ -L "$target" ]; then
    [ -L "$target" ] && fail "canonical binary is a symlink: $target"
    [ -e "$state_marker" ] || fail "ownership marker is absent; refusing to remove $target"
    [ -f "$state_marker" ] && [ ! -L "$state_marker" ] || fail 'ownership marker is not a regular file'
    marker_digest=$(awk 'NR == 1 { v=$0; next } { bad=1 } END { if (bad || length(v) != 64 || v !~ /^[0-9a-f]+$/) exit 1; print v }' "$state_marker") || fail 'ownership marker is malformed'
    [ -f "$target" ] && [ -x "$target" ] || fail 'canonical binary is not an executable regular file'
    actual=$(sha256sum "$target" | awk '{print $1}') || fail 'could not hash canonical binary'
    [ "$actual" = "$marker_digest" ] || fail 'ownership marker does not match canonical binary'
elif [ -e "$state_marker" ] || [ -L "$state_marker" ]; then
    fail 'ownership marker exists without its canonical binary; refusing cleanup'
fi

for root in "$state_root" "$cache_root" "$data_root" "$config_root"; do
    if [ -e "$root" ] || [ -L "$root" ]; then
        [ -d "$root" ] && [ ! -L "$root" ] || fail "managed root is not a real directory: $root"
        [ -z "$(find -P "$root" -xdev -type l -print -quit)" ] || fail "managed root contains a symlink: $root"
    fi
done

[ ! -e "$target" ] || rm -f -- "$target" || fail "could not remove $target"
for root in "$state_root" "$cache_root" "$data_root" "$config_root"; do
    if [ -e "$root" ]; then rm -rf -- "$root" || fail "could not remove $root"; fi
done
printf 'TarLink Data removed successfully.\n'
