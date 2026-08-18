# Is this just X?

The honest answer to most of these is **"no, and you probably want both."**
stevedore is a *release orchestrator* for images. It does not build anything
itself — `docker buildx` does the building — and it does not deploy anything.
It owns the part between those two: deciding what to build, what to call it,
whether it is allowed to ship, and what has to be true before it does.

The analogy in the tagline is exact. GoReleaser does not compile your Go code;
`go build` does. GoReleaser decides the version, the artifact names, the
checksums, the changelog and the release. stevedore is that, for images.

## The short version

| Tool | What it is | Overlap |
|------|-----------|---------|
| **`docker buildx` / `bake`** | The builder. Bake is a declarative *build target* file. | stevedore calls buildx. Bake describes builds; stevedore describes releases. `init --from bake` imports your targets. |
| **`docker/build-push-action`** | A GitHub Action wrapping buildx. | Same layer as bake, in CI form. It builds and pushes one image; stevedore decides *which* images, *what version*, and everything after the push. |
| **`ko`** | Builds images for **Go** apps with no Dockerfile and no daemon. Very fast. | Different layer and narrower input. If your services are pure Go, ko is an excellent builder — and stevedore has no ko backend today ([open an issue](https://github.com/blairham/stevedore/issues) if you want one). |
| **GoReleaser (`dockers:`)** | Releases *binaries*; can also build images around them. | The closest overlap. See below. |
| **Kaniko / Buildah** | Daemonless builders. | Builders, not release tools. |
| **Skaffold** | Inner-loop dev and deploy to Kubernetes. | Development and deployment; stevedore is neither. |
| **release-please / semantic-release** | Version and changelog from commits. | Same idea, no images. If you already use one, `versioning.strategy: env` or `static` takes the version from it. |
| **melange / apko** | Declarative apk-based image *construction*. | A builder, and a different one from Dockerfiles. |

## Against GoReleaser's `dockers:` block

This is the real comparison, and if GoReleaser already covers you, **keep using
it** — one tool is better than two.

GoReleaser's image support is built around a binary release: it compiles Go
artifacts and then builds images that copy those artifacts in. `docker_manifests`
stitches the per-arch images into a manifest list, and you list the arches by
hand.

The differences that matter show up when images are the *primary* artifact:

- **Not Go-shaped.** stevedore's input is a Dockerfile and a context. A Python
  service, a .NET service and a Go service are the same kind of thing to it.
- **Multi-arch is one field.** `platforms: [linux/amd64, linux/arm64]`, not a
  per-arch image plus a manifest block. And when QEMU is too slow, `--split` /
  `merge` fan the same config across native runners per arch.
- **Change detection.** In a monorepo, stevedore skips images whose sources did
  not change — including against *each image's own* last release, so services on
  independent cadences do not rebuild each other.
- **Per-image versions.** Each image can version from its own registry
  repository, so `api` at 1.9.0 and `worker` at 0.3.2 coexist in one repo.
- **Gates before signing.** A vulnerability scan and a smoke test run *before*
  the signature, so a failing image is never signed.

This repo uses both, one per artifact kind: stevedore publishes the image,
GoReleaser publishes the CLI binary, the GitHub release and the Homebrew
formula. That is the intended relationship.

## Against a pile of shell in a workflow

The real incumbent. Most image pipelines are: compute a tag from the git ref,
`docker buildx build` with the right flags, maybe sign, maybe SBOM,
hand-maintain a changelog.

That works. What it costs:

- It only runs in CI. `stevedore release --snapshot --dry-run` does the same
  resolution on your laptop and prints every command without running one.
- Tag logic drifts between repos, and the floating-tag rule ("`latest` only from
  the default branch of a real release") is the one everybody gets wrong.
- Signing and SBOM are steps you can forget. Here they are defaults, and the
  gates are ordered so a bad image cannot reach them.
- Nothing validates it until it runs. `stevedore check` resolves the whole plan —
  the exact refs that would publish — without building.

## What stevedore is not

Stated plainly, because a tool that is unclear about this wastes your afternoon:

- **Not a builder.** No BuildKit reimplementation. If buildx cannot build it,
  neither can stevedore.
- **Not a deployer.** It publishes images and can POST a webhook per pushed
  digest. What happens next is your CD system's job.
- **Not a registry, a scanner, or a signer.** It orchestrates `cosign`, `syft`,
  `grype`/`trivy`, `crane`, `aws` and `gh` rather than embedding them — which is
  why the binary is ~4.5 MB instead of carrying their combined dependency trees,
  and why you can pin those tools' versions yourself.
- **Not a way to avoid learning Docker.** It assumes you have a Dockerfile that
  works.
