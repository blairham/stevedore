# Monorepo with change detection

Three services, independent versions, and only the ones whose code changed get
rebuilt.

```sh
stevedore plan | jq          # what would build, and why — builds nothing
stevedore release            # marker_refs: each image vs its own last release
```

## The three change-detection modes

| Mode | Baseline | Use it for |
|------|----------|-----------|
| `change_detection.marker_refs: true` | each image's own last release ref | **CI** — stateless, no argument to pass, survives independent cadences |
| `--changed-since origin/main` | a git ref you name | PR checks |
| `--only-changed` | a content fingerprint in `dist/` | local iteration |

`marker_refs` is the one to reach for in CI. The baseline lives in git as
`refs/releases/image/<id>`, so a fresh checkout knows it, and an image that has
not changed since *its own* last release does not rebuild just because a
neighbour released.

## Scoping is the whole game

Every image here has `context: .` — the whole repo — because that is what a
monorepo Dockerfile usually needs. Change detection therefore cannot infer
anything from the context, and `paths` is what tells it the truth. Get `paths`
wrong in the tight direction and you ship a stale image; get it wrong in the
loose direction and you rebuild everything, which is where you started.

For .NET, `change_detection.resolver: dotnet` derives `paths` from the `.csproj`
`<ProjectReference>` graph transitively, so shared libraries are handled without
hand-maintaining the lists. See [`docs/monorepo.md`](../../docs/monorepo.md).

## Build-once grouping

Images with an identical build spec — same Dockerfile, context, target,
platforms, build args and labels — that differ only by destination are built
**once** and pushed to every destination in one `buildx` invocation.

`worker` and `worker-eu` above deliberately do *not* qualify: they differ by a
build arg, which makes them genuinely different images. Nothing to configure
either way; grouping is automatic, and `stevedore plan` shows the groups.
