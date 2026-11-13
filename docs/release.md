# Release

This describes how a version of acd-ctx is cut. Nothing is published until a
maintainer pushes a tag; the workflows never publish from a branch.

## Cutting a version

1. Make sure `main` is green in CI (`.github/workflows/ci.yml`).
2. Tag the commit with a semantic version prefixed by `v`:

   ```sh
   git tag -a v1.2.3 -m "acd-ctx v1.2.3"
   git push origin v1.2.3
   ```

Tags must match `v*`. A tag such as `v1.2.3` produces the version `1.2.3`
(GoReleaser strips the `v`; the image tag keeps the semver form).

## What the workflows do

`.github/workflows/release.yml` runs on a pushed `v*` tag and on
`workflow_dispatch`.

- **test** (always): `go vet`, `go test -race`, and the cross vet for darwin and
  windows with `CGO_ENABLED=0`.
- **goreleaser** (tag only): builds `acd-ctx` for linux, darwin and windows on
  amd64 and arm64 with `CGO_ENABLED=0`, sets `main.version` from the tag, and
  writes `tar.gz` archives (`zip` for windows), `checksums.txt` (sha256) and a
  changelog. The release is created as a **draft** on GitHub, so it is checked
  and published by hand. It uses only the workflow's `GITHUB_TOKEN`. No signing.
- **docker** (tag only): builds the image for `linux/amd64` and `linux/arm64`
  and pushes it to `ghcr.io/<owner>/<repo>` with the tags `1.2.3` and `1.2`.
  It uses `GITHUB_TOKEN` with `packages: write`.

A manual `workflow_dispatch` run executes the tests only. The release and image
jobs run only when the ref is a `v*` tag.

`.goreleaser.yaml` holds the build and archive settings. Check it locally with
`goreleaser check`. Do not run `goreleaser release` by hand outside a tag.

## Installing from source

`go install` with a version works only for a `v*` tag, because the Go module
proxy serves only semantic-version tags (a tag without the `v`, such as `1.0.0`,
is not a module version). `@latest` resolves to the newest `v*` tag:

```sh
go install github.com/fsvxavier/accordion-context/cmd/acd-ctx@v1.0.6
```

The toolchain must be at least the `go` version in `go.mod`.
