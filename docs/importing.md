## Importing a config

Bootstrapping `.stevedore.yaml` from a setup you already have.

`stevedore init` can bootstrap `.stevedore.yaml` from a setup you already have
instead of scanning Dockerfiles:

- `--from goreleaser` reads a `.goreleaser.yaml`'s `dockers:` blocks, merging
  per-arch entries into multi-arch images.
- `--from bake` resolves a docker-bake target set (`docker buildx bake --print`).
- `--from services --file <dir>` reads a directory of per-service manifests —
  the monorepo convention of one YAML per service — and scaffolds one image
  each.

For `--from services`, the default manifest shape is:

```yaml
# .platform/services/api.yaml
name: api                        # → id
image: ghcr.io/acme/api          # → repositories (string or list)
dockerfile: docker/Dockerfile    # → dockerfile
target: runtime                  # → target
project: src/Api/Api.csproj      # → build_args: ["PROJECT=…"]
sourcePaths:                     # → paths (change detection)
  - src/Api/**
  - src/Shared/**
```

Manifest schemas vary by org, so the field names are remappable: `--map
field=key` (fields: `id`, `repositories`, `dockerfile`, `context`, `target`,
`paths`) and `--map-build-arg ARG=key` (replaces the default
`PROJECT=project`). Keys are dotted paths, so nested manifests work too:

```sh
stevedore init --from services --file .platform/services \
  --map id=service --map paths=source_paths \
  --map-build-arg BUILD_PROJECT=build.project
```
