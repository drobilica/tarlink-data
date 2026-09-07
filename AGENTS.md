# TarLink Data project policy

## Scope

TarLink Data is a separate, narrow Unix-like program that resolves external application data using user-selected recipes and sources. TarLink and tarlink-registry must not contain TarLink Data recipes. Implement only requested functionality; avoid speculative abstractions.

## Implementation

- Production code is pure Go with `CGO_ENABLED=0`.
- Do not use `os/exec`, `unsafe`, `plugin`, C, arbitrary commands, hooks, scripts as runtime extension points, plugins, daemons, telemetry, databases, SQLite, watchers, SMB/NFS protocol implementations, archive extraction, fuzzy matching, partial hashing, or symlink/hardlink placement semantics.
- Mounted SMB/NFS paths are ordinary filesystem sources.

## Trust and security

- SHA-256 is the sole identity of required data; filenames are never identity and size is filtering/validation metadata.
- Filesystem and HTTPS indexes are advisory locators, not trust anchors. Every materialized file is SHA-256 verified while copying/downloading.
- Recipe targets are relative and confined to each app's TarLink Data root. Reject traversal and symlink escape, never silently overwrite unknown data, keep HTTPS HTTPS-only across bounded redirects and bounded reads, and publish only after verification.
- Caches are disposable and never authoritative.

## Project and legal boundary

- Do not include proprietary application-data recipes, ROM/BIOS hashes, copyrighted filenames, game-to-ROM mappings, or real proprietary games. Tests use generated synthetic bytes and fake app names.
- Pre-1.0, prefer clean changes over compatibility layers; do not add migrations or legacy parsing unless requested.

## Git

Agents never commit, push, tag, or release unless explicitly authorized. Never touch unrelated user work.

## Validation

The canonical commands are `./scripts/validate.sh` and the quick loop `./scripts/validate.sh --quick`. Security-sensitive changes require explicit hostile/failure-path tests.
