# Releasing

Releases are automated with [release-please](https://github.com/googleapis/release-please). On every push to `main`:

1. `.github/workflows/release.yml` runs the test gate (`go vet` + `go test`), then release-please opens or updates a **Release PR** that bumps `.release-please-manifest.json` and writes `CHANGELOG.md` from Conventional Commits.
2. Merging the Release PR creates a **GitHub Release** and a `vX.Y.Z` tag, and the same workflow then publishes:
   - the container image to GHCR (`ghcr.io/<owner>/baleia:vX.Y.Z`), stamped with the release version via `-ldflags` (`internal/version`);
   - the Helm chart to `oci://ghcr.io/<owner>/charts/baleia` and as a `.tgz` asset on the Release.
3. Install the released chart with `helm install baleia oci://ghcr.io/<owner>/charts/baleia --version X.Y.Z`.
4. A failed publish can be retried with `workflow_dispatch`, passing the existing `vX.Y.Z` tag.

Commit conventions drive the version: `feat:` → minor, `fix:` → patch, `feat!:` / `BREAKING CHANGE:` → major.

Do not let a bot rewrite the Release PR body: release-please needs its marker to recognise the merged release. The workflow fails loudly if a `chore(main): release` merge produces no tag, instead of silently reopening the next release with every commit since the bootstrap SHA.
