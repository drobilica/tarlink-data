# TarLink Data

TarLink Data resolves exact external application data from recipes selected by the user. It materializes that data in its own managed XDG data root. TarLink Data is a separate program: it does not modify TarLink, use TarLink's private state, wire data into applications, or bundle recipes.

## Install

Install the Linux amd64 release from the official repository:

```sh
curl -fsSL https://raw.githubusercontent.com/drobilica/tarlink-data/main/install.sh | sh
```

The installer verifies the release checksum and records ownership of `~/.local/bin/tarlink-data`. It refuses to replace an installation it does not own. Run `uninstall.sh` from this repository to remove the binary and TarLink Data's own XDG config, cache, state, and managed data; configured sources and recipe directories are not removed.

## Workflow

1. Choose or write recipes and configure local directories or HTTPS indexes.
2. Provide installed-application JSON on standard input, commonly from TarLink.
3. Run `tarlink-data sync` to resolve and materialize required files.

```sh
tarlink installed --json | tarlink-data sync
tarlink-data sync --dry-run
tarlink-data sync --json
tarlink-data upgrade
tarlink-data --version
```

TarLink is only one possible producer of the installed-application input. TarLink Data does not read or manage TarLink's private state. `sync` reads at most 1 MiB from standard input and accepts this versioned JSON contract:

```json
{
  "version": 1,
  "applications": [
    { "id": "example-app", "version": "1.0" }
  ]
}
```

Version 1 accepts only those fields and requires non-empty, unique application IDs and versions. Unsupported contract versions and malformed input fail clearly.

## Configuration

`$XDG_CONFIG_HOME/tarlink-data/config.yaml` (or `~/.config/tarlink-data/config.yaml`) contains user-selected recipe catalogs and sources:

```yaml
version: 1
recipes:
  - ~/.config/tarlink-data/recipes
sources:
  - /mnt/example-data
  - https://example.invalid/data/index.json
```

Minimal synthetic recipe:

```yaml
version: 1
recipes:
  - app: example-app
    files:
      - target: required-data.bin
        accepted:
          - size: 4
            sha256: 3a6eb0790f39ac87c94f3856b2dd2c5d110e6811602261a9a923d3bb23adc8b7
```

Local sources are directories that TarLink Data indexes. HTTPS sources point to JSON indexes whose relative paths resolve beside the index. Indexes only locate candidates; SHA-256 establishes identity, and every copied or downloaded file is independently verified. No recipes or proprietary application data are included.

## Locations

- Config: `$XDG_CONFIG_HOME/tarlink-data/config.yaml` or `~/.config/tarlink-data/config.yaml`
- Cache: `$XDG_CACHE_HOME/tarlink-data/` or `~/.cache/tarlink-data/`
- State and ownership: `$XDG_STATE_HOME/tarlink-data/` or `~/.local/state/tarlink-data/`
- Managed data: `$XDG_DATA_HOME/tarlink-data/apps/<app>/` or `~/.local/share/tarlink-data/apps/<app>/`

Passive release checks use only the cache hot path, approximately once per day, and report available stable releases on stderr. Network failures are silent. `dev` builds do not check for updates.

## Development

```sh
./scripts/validate.sh --quick
./scripts/validate.sh
```

Production builds are pure Go with `CGO_ENABLED=0`.
