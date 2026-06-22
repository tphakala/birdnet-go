// Package manifest defines the schema for the BirdNET-Go release manifest: a
// machine-readable description of the most recent release on each distribution
// channel (stable, nightly, beta).
//
// The manifest is generated in CI by tools/release-manifest and published as an
// asset on a dedicated, never-deleted "manifest" GitHub release. The stable URL
//
//	https://github.com/tphakala/birdnet-go/releases/download/manifest/manifest.json
//
// always resolves to the latest manifest. The in-app update checker (a future
// feature) consumes this file to decide whether a newer build is available.
//
// This package intentionally depends only on the standard library so that both
// the CI generator and the future in-app client can import it without pulling
// in any application internals. The Go types here are the single source of
// truth for the manifest contract.
package manifest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// SchemaVersion is the current manifest schema version. Consumers MUST check
// this field and tolerate unknown fields so that additive changes (a new field,
// a new channel) do not break older clients. The version is only incremented on
// a breaking change to the existing fields.
const SchemaVersion = 1

// Channel names. A running build maps to exactly one channel based on its
// version string (see ClassifyTag).
const (
	ChannelStable  = "stable"
	ChannelNightly = "nightly"
	ChannelBeta    = "beta"
)

// CriticalMarker is the token a release author places in the GitHub release body
// to flag a release as security-critical. The generator copies this into the
// Channel.Critical field so the update checker can surface an urgent prompt.
const CriticalMarker = "<!-- manifest:critical -->"

// Manifest is the top-level document published as manifest.json.
type Manifest struct {
	// SchemaVersion is the schema version this document conforms to.
	SchemaVersion int `json:"schema_version"`
	// GeneratedAt is when the manifest was produced (UTC).
	GeneratedAt time.Time `json:"generated_at"`
	// Repo is the "owner/repo" the releases come from.
	Repo string `json:"repo"`
	// Channels maps a channel name (stable, nightly, beta) to its latest
	// release. A channel with no releases yet is omitted entirely.
	Channels map[string]*Channel `json:"channels"`
}

// Channel describes the most recent release on a single distribution channel.
type Channel struct {
	// Version is the release version string, identical to the value baked into
	// the binary at build time (e.g. "v0.6.4" or "nightly-20260622").
	Version string `json:"version"`
	// Tag is the underlying git tag of the release. Usually equal to Version,
	// but can differ for nightlies whose tag was suffixed after a retry.
	Tag string `json:"tag"`
	// Name is the human-readable release title.
	Name string `json:"name,omitempty"`
	// ReleasedAt is the release publication time (UTC).
	ReleasedAt time.Time `json:"released_at"`
	// Prerelease reports whether GitHub marks this release as a pre-release.
	Prerelease bool `json:"prerelease"`
	// Critical flags a security-critical release (see CriticalMarker).
	Critical bool `json:"critical"`
	// MinUpgradeFrom, when set, names the lowest version that may upgrade
	// directly to this release; older installs must first move to an
	// intermediate version. Empty means no constraint. Sourced from a
	// "<!-- manifest:min-upgrade-from=vX.Y.Z -->" marker in the release body.
	MinUpgradeFrom string `json:"min_upgrade_from,omitempty"`
	// ReleaseURL is the human-facing GitHub release page.
	ReleaseURL string `json:"release_url"`
	// Notes is the release body (changelog), trimmed and length-bounded.
	Notes string `json:"notes,omitempty"`
	// Docker holds the container image references for this release.
	Docker *Docker `json:"docker,omitempty"`
	// Assets lists the downloadable native binary tarballs, one per platform
	// and architecture, sorted deterministically.
	Assets []Asset `json:"assets"`
}

// Docker holds container image references for a channel.
type Docker struct {
	// GHCR is the version-pinned GitHub Container Registry reference,
	// e.g. "ghcr.io/tphakala/birdnet-go:v0.6.4".
	GHCR string `json:"ghcr,omitempty"`
	// DockerHub is the version-pinned Docker Hub reference,
	// e.g. "tphakala/birdnet-go:v0.6.4".
	DockerHub string `json:"dockerhub,omitempty"`
	// ChannelTag is the moving tag a user pulls to track this channel,
	// e.g. "ghcr.io/tphakala/birdnet-go:latest" or ":nightly". This is the
	// authoritative reference for Docker-based installs.
	ChannelTag string `json:"channel_tag,omitempty"`
}

// Asset is a single downloadable native binary tarball.
type Asset struct {
	// Platform is the target OS: "linux", "windows" or "darwin".
	Platform string `json:"platform"`
	// Arch is the target architecture: "amd64" or "arm64".
	Arch string `json:"arch"`
	// Filename is the asset's file name as attached to the release.
	Filename string `json:"filename"`
	// URL is the direct download URL for the tarball.
	URL string `json:"url"`
	// Size is the tarball size in bytes.
	Size int64 `json:"size"`
	// SHA256 is the lowercase hex SHA-256 of the tarball, sourced from the
	// release's checksums.txt. Empty if the release predates checksum publishing.
	SHA256 string `json:"sha256,omitempty"`
}

var (
	stableTagRe = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
	// betaTagRe accepts any SemVer pre-release identifier beginning with
	// alpha/beta/rc, with or without a separator and with multi-segment
	// suffixes: v1.2.3-beta, v1.2.3-rc2, v1.2.3-beta.1, v1.2.3-rc.1.2.
	betaTagRe = regexp.MustCompile(`^v\d+\.\d+\.\d+-(?:alpha|beta|rc)(?:[.-]?[0-9A-Za-z.-]+)?$`)
	// nightlyTagRe matches nightly tags by prefix and is intentionally
	// unanchored at the end: real nightly tags carry build-retry and
	// git-describe suffixes (nightly-20260622-414, nightly-20251025-1-gec0f78e)
	// that an end-anchored pattern would reject.
	nightlyTagRe = regexp.MustCompile(`^nightly-\d{8}`)
	// assetNameRe matches both the stable filename form
	// "birdnet-go-linux-amd64-v0.6.4.tar.gz" and the nightly form
	// "birdnet-go-linux-amd64.tar.gz" (no version suffix).
	assetNameRe  = regexp.MustCompile(`^birdnet-go-(linux|windows|darwin)-(amd64|arm64)(?:-.+)?\.tar\.gz$`)
	minUpgradeRe = regexp.MustCompile(`(?i)<!--\s*manifest:min-upgrade-from=([^\s>]+)\s*-->`)
	// criticalRe tolerates whitespace variations around the critical marker,
	// mirroring minUpgradeRe so a stray missing space does not silently drop
	// the flag.
	criticalRe = regexp.MustCompile(`(?i)<!--\s*manifest:critical\s*-->`)
)

// ClassifyTag maps a git tag to its distribution channel. It returns false for
// tags that belong to no channel (for example the "manifest" release's own tag),
// which the generator skips.
func ClassifyTag(tag string) (channel string, ok bool) {
	switch {
	case nightlyTagRe.MatchString(tag):
		return ChannelNightly, true
	case betaTagRe.MatchString(tag):
		return ChannelBeta, true
	case stableTagRe.MatchString(tag):
		return ChannelStable, true
	default:
		return "", false
	}
}

// ParseAssetName extracts the platform and architecture from a release tarball
// file name. It returns false for names that are not recognised binary tarballs
// (READMEs, checksum files, etc.).
func ParseAssetName(name string) (platform, arch string, ok bool) {
	m := assetNameRe.FindStringSubmatch(name)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// ParseChecksums parses the content of a checksums.txt file in the standard
// "sha256sum" format ("<hex>  <filename>") into a map of file name to hash.
// Malformed lines are skipped.
func ParseChecksums(data []byte) map[string]string {
	out := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		fields := strings.Fields(strings.TrimSpace(scanner.Text()))
		if len(fields) != 2 {
			continue
		}
		// GNU coreutils prefixes the filename with "*" in binary mode
		// (sha256sum -b); strip it so the key matches the asset name.
		name := strings.TrimPrefix(fields[1], "*")
		out[name] = strings.ToLower(fields[0])
	}
	return out
}

// ExtractCritical reports whether a release body flags the release as critical.
// It tolerates whitespace variations of CriticalMarker.
func ExtractCritical(body string) bool {
	return criticalRe.MatchString(body)
}

// ExtractMinUpgradeFrom returns the minimum upgrade-from version declared in a
// release body, or the empty string if no marker is present.
func ExtractMinUpgradeFrom(body string) string {
	m := minUpgradeRe.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return m[1]
}

// Validate performs structural validation of a manifest. It deliberately does
// not require per-asset checksums so that releases predating checksum publishing
// remain representable; the generator logs warnings for missing hashes instead.
func (m *Manifest) Validate() error {
	if m.SchemaVersion == 0 {
		return errors.New("schema_version is required")
	}
	if len(m.Channels) == 0 {
		return errors.New("at least one channel is required")
	}
	for name, ch := range m.Channels {
		if ch == nil {
			return fmt.Errorf("channel %q: entry is nil", name)
		}
		if ch.Version == "" {
			return fmt.Errorf("channel %q: version is required", name)
		}
		if ch.Tag == "" {
			return fmt.Errorf("channel %q: tag is required", name)
		}
	}
	return nil
}

// JSON marshals the manifest to indented JSON with a trailing newline.
func (m *Manifest) JSON() ([]byte, error) {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	return append(b, '\n'), nil
}

// Parse decodes a manifest from its JSON representation.
func Parse(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("unmarshal manifest: %w", err)
	}
	return &m, nil
}
