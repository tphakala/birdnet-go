# Release Manifest

BirdNET-Go publishes a machine-readable manifest describing the latest release on
each distribution channel. It is the data source for the in-app update checker
and any external tooling that needs to know what the current builds are.

## Where it lives

The manifest is a single JSON asset on a dedicated, never-deleted `manifest`
GitHub release. The stable URL always resolves to the current manifest:

```text
https://github.com/tphakala/birdnet-go/releases/download/manifest/manifest.json
```

The `manifest` release is marked as a pre-release so it never occupies the
"Latest" slot; `/releases/latest` keeps pointing at the newest stable release.

## How it is maintained

`tools/release-manifest` (a small Go CLI) queries the GitHub Releases API, picks
the newest release on each channel, reads each release's `checksums.txt`, and
writes `manifest.json`. It runs in CI:

- as the final job of `release-build.yml` and `nightly-build.yml`, after all
  release assets exist (so the manifest never races asset uploads),
- on manual `workflow_dispatch`,
- on a daily schedule as a self-heal.

See `.github/workflows/release-manifest.yml`.

## Channels

| Channel   | Tag pattern                                         | Moving Docker tag |
| --------- | --------------------------------------------------- | ----------------- |
| `stable`  | `vX.Y.Z`                                            | `:latest`         |
| `nightly` | `nightly-YYYYMMDD` (build/git-describe suffixes ok) | `:nightly`        |
| `beta`    | `vX.Y.Z-` with an `alpha`/`beta`/`rc` pre-release   | `:beta`           |

The beta pattern accepts any SemVer pre-release identifier beginning with
`alpha`, `beta`, or `rc`, with or without a numeric or dotted suffix
(`v1.2.3-beta`, `v1.2.3-rc2`, `v1.2.3-beta.1`, `v1.2.3-rc.1.2`). A version-like
release that matches no channel is skipped with a warning rather than silently
dropped. A channel with no releases yet is omitted from `channels`.

For the `nightly` channel only the moving `channel_tag` (`:nightly`) is
published; no version-pinned Docker ref is advertised, because the nightly
dated image tag can drift from the GitHub release tag on a build retry. The
`stable` and `beta` channels carry version-pinned `ghcr`/`dockerhub` refs.

## Schema

`schema_version` is `1`. Consumers MUST check `schema_version` and tolerate
unknown fields so additive changes do not break older clients. The Go types in
`internal/update/manifest/manifest.go` are the authoritative definition.

| Field            | Type          | Notes                                 |
| ---------------- | ------------- | ------------------------------------- |
| `schema_version` | int           | Manifest schema version.              |
| `generated_at`   | RFC 3339 time | When the manifest was produced (UTC). |
| `repo`           | string        | `owner/repo` the releases come from.  |
| `channels`       | map           | Channel name to channel object.       |

Each channel object:

| Field              | Type          | Notes                                                         |
| ------------------ | ------------- | ------------------------------------------------------------- |
| `version`          | string        | Same string baked into the binary (`settings.Version`).       |
| `tag`              | string        | Git tag (usually equal to `version`).                         |
| `name`             | string        | Release title.                                                |
| `released_at`      | RFC 3339 time | Publication time (UTC).                                       |
| `prerelease`       | bool          | GitHub pre-release flag.                                      |
| `critical`         | bool          | Security-critical release (see markers below).                |
| `min_upgrade_from` | string        | Lowest version that may upgrade directly; empty for none.     |
| `release_url`      | string        | Human-facing release page.                                    |
| `notes`            | string        | Release body / changelog (length-bounded).                    |
| `docker`           | object        | `ghcr`, `dockerhub` (version-pinned), `channel_tag` (moving). |
| `assets`           | array         | Native binary tarballs, one per platform/arch.                |

Each asset:

| Field      | Type   | Notes                                                                    |
| ---------- | ------ | ------------------------------------------------------------------------ |
| `platform` | string | `linux`, `windows`, `darwin`.                                            |
| `arch`     | string | `amd64`, `arm64`.                                                        |
| `filename` | string | Asset file name.                                                         |
| `url`      | string | Direct download URL.                                                     |
| `size`     | int    | Bytes.                                                                   |
| `sha256`   | string | Lowercase hex SHA-256; empty for releases predating checksum publishing. |

## Release-note markers

Release authors can annotate a GitHub release body to influence the manifest:

- `<!-- manifest:critical -->` sets `critical: true` on that channel, signalling
  an urgent or security update.
- `<!-- manifest:min-upgrade-from=vX.Y.Z -->` sets `min_upgrade_from`.

## Example

```json
{
  "schema_version": 1,
  "generated_at": "2026-06-22T14:41:28Z",
  "repo": "tphakala/birdnet-go",
  "channels": {
    "stable": {
      "version": "v0.6.4",
      "tag": "v0.6.4",
      "name": "March 15th, 2025 release",
      "released_at": "2025-03-15T10:36:58Z",
      "prerelease": false,
      "critical": false,
      "release_url": "https://github.com/tphakala/birdnet-go/releases/tag/v0.6.4",
      "notes": "...",
      "docker": {
        "ghcr": "ghcr.io/tphakala/birdnet-go:v0.6.4",
        "dockerhub": "tphakala/birdnet-go:v0.6.4",
        "channel_tag": "ghcr.io/tphakala/birdnet-go:latest"
      },
      "assets": [
        {
          "platform": "linux",
          "arch": "amd64",
          "filename": "birdnet-go-linux-amd64-v0.6.4.tar.gz",
          "url": "https://github.com/tphakala/birdnet-go/releases/download/v0.6.4/birdnet-go-linux-amd64-v0.6.4.tar.gz",
          "size": 82091332,
          "sha256": "5f3a..."
        }
      ]
    },
    "nightly": {
      "...": "same shape, channel_tag ghcr.io/tphakala/birdnet-go:nightly"
    }
  }
}
```
