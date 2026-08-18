# Single image

One service, one Dockerfile. Signed, scanned, smoke-tested, SBOM'd.

```sh
cp .stevedore.yaml /path/to/your/repo/
# edit repositories: and the test cmd, then:
stevedore check
```

The workflow that goes with it is in [`../../docs/ci.md`](../../docs/ci.md) —
four lines plus `permissions:`.

Things worth noticing:

- `latest` is listed like any other tag, but it is **floating**: stevedore
  publishes it only on the default branch of a non-snapshot release. You do not
  need branch conditionals in your workflow to protect it.
- The scan and smoke-test gates run **before** signing. That ordering is the
  point — a signature on an image that does not start is worse than no signature.
- `sign.cosign` with no `key:` is keyless OIDC. In GitHub Actions that needs
  `permissions: id-token: write`.
