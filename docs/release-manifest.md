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

- as the final job of `release-build.yml`, after all release assets exist (so
  the manifest never races asset uploads),
- on manual `workflow_dispatch`,
- on a daily schedule as a self-heal.

See `.github/workflows/release-manifest.yml`.

## Channels

| Channel   | Tag pattern                                                    | Moving Docker tag |
| --------- | -------------------------------------------------------------- | ----------------- |
| `stable`  | `YYYYMMDD`; historically `vX.Y.Z`                              | `:latest`         |
| `nightly` | every `YYYYMMDD` release; historically `nightly-YYYYMMDD`      | `:nightly`        |
| `beta`    | historically `vX.Y.Z-` with an `alpha`/`beta`/`rc` pre-release | `:beta`           |

Releases are tagged with their date, `YYYYMMDD` (for example `20260823`). The
tag must be exactly eight digits starting with `20` and form a valid calendar
date; dev build versions such as `20261002-g5dc2ab881-dev` never match. A date
release feeds both `stable` and `nightly`, matching the `:nightly` image tag,
which also moves to every date release.

The `vX.Y.Z`, `vX.Y.Z-rc` and `nightly-YYYYMMDD` forms are no longer used but
still classify, so older releases stay representable. The beta pattern accepts
any SemVer pre-release identifier beginning with `alpha`, `beta`, or `rc`, with
or without a numeric or dotted suffix (`v1.2.3-beta`, `v1.2.3-rc2`,
`v1.2.3-beta.1`, `v1.2.3-rc.1.2`).

Each channel lists its newest release by publication time. A channel with no releases yet is omitted
from `channels`.

A published, non-draft release whose tag matches no channel (other than the
`manifest` release itself) fails the run: the generator exits non-zero and
names the tags, and the previously published manifest stays in place. Retag,
delete or return the mis-tagged release to draft to unblock it.

Every channel carries the moving `channel_tag` and, for every tag form except
legacy `nightly-YYYYMMDD`, version-pinned `ghcr`/`dockerhub` refs. A legacy `nightly-YYYYMMDD`
release gets only the moving tag, because its image tag could drift from the
GitHub release tag on a build retry.

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
- `<!-- manifest:min-upgrade-from=YYYYMMDD -->` sets `min_upgrade_from`.

## Example

```json
{
  "schema_version": 1,
  "generated_at": "2026-06-22T14:41:28Z",
  "repo": "tphakala/birdnet-go",
  "channels": {
    "stable": {
      "version": "20260823",
      "tag": "20260823",
      "name": "BirdNET-Go 20260823",
      "released_at": "2026-08-23T10:36:58Z",
      "prerelease": false,
      "critical": false,
      "release_url": "https://github.com/tphakala/birdnet-go/releases/tag/20260823",
      "notes": "...",
      "docker": {
        "ghcr": "ghcr.io/tphakala/birdnet-go:20260823",
        "dockerhub": "tphakala/birdnet-go:20260823",
        "channel_tag": "ghcr.io/tphakala/birdnet-go:latest"
      },
      "assets": [
        {
          "platform": "linux",
          "arch": "amd64",
          "filename": "birdnet-go-linux-amd64-20260823.tar.gz",
          "url": "https://github.com/tphakala/birdnet-go/releases/download/20260823/birdnet-go-linux-amd64-20260823.tar.gz",
          "size": 82091332,
          "sha256": "5f3a..."
        }
      ]
    },
    "nightly": {
      "...": "same release and refs, channel_tag ghcr.io/tphakala/birdnet-go:nightly"
    }
  }
}
```
