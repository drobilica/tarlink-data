# TarLink Data

TarLink Data resolves exact external application data from recipes selected by the user and materializes it in TarLink Data's own managed data root. It does not modify TarLink, wire data into applications, or bundle recipes.

Build and run:

```sh
go build ./cmd/tarlink-data
tarlink list --installed --json | tarlink-data sync
```

Minimal `config.yaml`:

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

Local sources are directories that TarLink Data indexes. HTTPS sources point to a JSON source index whose relative paths resolve beside that index; indexes only locate candidates. SHA-256 establishes identity, and every copied/downloaded file is independently verified.

No recipes are bundled. Users choose recipe sources and must have the right to use the data they configure. TarLink Data never downloads arbitrary application data based solely on filenames. V1 materializes data under its own XDG data root; application-specific runtime wiring remains outside this initial release.

Locations:

- Config: `$XDG_CONFIG_HOME/tarlink-data/config.yaml` or `~/.config/tarlink-data/config.yaml`
- Cache: `$XDG_CACHE_HOME/tarlink-data/` or `~/.cache/tarlink-data/`
- Managed data: `$XDG_DATA_HOME/tarlink-data/apps/<app>/` or `~/.local/share/tarlink-data/apps/<app>/`
