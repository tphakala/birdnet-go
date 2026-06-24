package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/update/manifest"
)

// checksumsFilename is the name of the aggregated checksum asset attached to
// every release by the build workflows.
const checksumsFilename = "checksums.txt"

// Moving Docker tags users pull to track a channel.
const (
	dockerTagLatest  = "latest"
	dockerTagNightly = "nightly"
	dockerTagBeta    = "beta"
)

// errNoChannels is returned when no published release matched any channel. The
// CLI treats it as a soft, non-fatal condition (nothing to publish) so a
// transient empty-release state cannot fail an otherwise-successful release
// pipeline.
var errNoChannels = errors.New("no releases matched a known channel")

// buildOptions configures manifest generation.
type buildOptions struct {
	Repo           string    // owner/repo
	GHCRImage      string    // e.g. ghcr.io/tphakala/birdnet-go
	DockerHubImage string    // e.g. tphakala/birdnet-go
	GeneratedAt    time.Time // stamped into the manifest
	MaxNotesLen    int       // 0 means unbounded
}

// buildManifest fetches releases via src, picks the newest release on each
// channel, and assembles the manifest. It returns any non-fatal warnings (for
// example a release missing checksums) alongside the manifest so the caller can
// surface them without failing the build.
func buildManifest(ctx context.Context, src releaseSource, opts *buildOptions) (*manifest.Manifest, []string, error) {
	releases, err := src.ListReleases(ctx, opts.Repo)
	if err != nil {
		return nil, nil, fmt.Errorf("list releases: %w", err)
	}

	// Surface version-like releases that matched no channel, so a mis-tagged
	// release does not vanish from the manifest with zero diagnostics.
	warnings := unclassifiedWarnings(releases)

	latest := latestPerChannel(releases)
	if len(latest) == 0 {
		return nil, warnings, fmt.Errorf("%w in %s", errNoChannels, opts.Repo)
	}

	m := &manifest.Manifest{
		SchemaVersion: manifest.SchemaVersion,
		GeneratedAt:   opts.GeneratedAt.UTC(),
		Repo:          opts.Repo,
		Channels:      make(map[string]*manifest.Channel, len(latest)),
	}

	// Iterate channels in a stable order so warnings are deterministic.
	for _, name := range sortedKeys(latest) {
		release := latest[name]
		channel, w := buildChannel(ctx, src, opts, name, &release)
		m.Channels[name] = channel
		warnings = append(warnings, w...)
	}

	if err := m.Validate(); err != nil {
		return nil, warnings, fmt.Errorf("validate manifest: %w", err)
	}
	return m, warnings, nil
}

// latestPerChannel selects, for each channel, the published release with the
// most recent publication time. Drafts and releases belonging to no channel
// (e.g. the "manifest" release itself) are ignored.
func latestPerChannel(releases []ghRelease) map[string]ghRelease {
	latest := make(map[string]ghRelease)
	for _, r := range releases {
		if r.Draft {
			continue
		}
		channel, ok := manifest.ClassifyTag(r.TagName)
		if !ok {
			continue
		}
		// Deterministic selection: newest by publish time, and on an exact
		// timestamp tie prefer the lexicographically greater tag so the result
		// does not depend on the API's (undocumented) tie ordering.
		cur, exists := latest[channel]
		if !exists || r.PublishedAt.After(cur.PublishedAt) ||
			(r.PublishedAt.Equal(cur.PublishedAt) && r.TagName > cur.TagName) {
			latest[channel] = r
		}
	}
	return latest
}

// unclassifiedWarnings reports non-draft releases whose tag looks like a version
// (starts with "v" or "nightly-") but matched no channel, e.g. a beta tagged
// with an unsupported pre-release form.
func unclassifiedWarnings(releases []ghRelease) []string {
	var warnings []string
	for i := range releases {
		r := releases[i]
		if r.Draft {
			continue
		}
		if _, ok := manifest.ClassifyTag(r.TagName); ok {
			continue
		}
		if strings.HasPrefix(r.TagName, "v") || strings.HasPrefix(r.TagName, "nightly-") {
			warnings = append(warnings, fmt.Sprintf("release %q matched no channel and was skipped", r.TagName))
		}
	}
	return warnings
}

// buildChannel converts a single GitHub release into a manifest Channel.
func buildChannel(ctx context.Context, src releaseSource, opts *buildOptions, channelName string, r *ghRelease) (channel *manifest.Channel, warnings []string) {
	notes := strings.TrimSpace(r.Body)
	// Bound the notes by rune count, not bytes, so truncation never splits a
	// multi-byte character and produces invalid UTF-8.
	if opts.MaxNotesLen > 0 {
		if runes := []rune(notes); len(runes) > opts.MaxNotesLen {
			notes = string(runes[:opts.MaxNotesLen])
		}
	}

	channel = &manifest.Channel{
		Version:        r.TagName,
		Tag:            r.TagName,
		Name:           r.Name,
		ReleasedAt:     r.PublishedAt.UTC(),
		Prerelease:     r.Prerelease,
		Critical:       manifest.ExtractCritical(r.Body),
		MinUpgradeFrom: manifest.ExtractMinUpgradeFrom(r.Body),
		ReleaseURL:     r.HTMLURL,
		Notes:          notes,
		Docker:         dockerRefs(opts, channelName, r.TagName),
	}

	var checksums map[string]string
	for i := range r.Assets {
		if r.Assets[i].Name != checksumsFilename {
			continue
		}
		data, err := src.Download(ctx, r.Assets[i].BrowserDownloadURL)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: download %s: %v", channelName, checksumsFilename, err))
			break
		}
		checksums = manifest.ParseChecksums(data)
		break
	}

	for i := range r.Assets {
		platform, arch, ok := manifest.ParseAssetName(r.Assets[i].Name)
		if !ok {
			continue
		}
		sha := checksums[r.Assets[i].Name]
		if sha == "" {
			warnings = append(warnings, fmt.Sprintf("%s: no checksum for %s", channelName, r.Assets[i].Name))
		}
		channel.Assets = append(channel.Assets, manifest.Asset{
			Platform: platform,
			Arch:     arch,
			Filename: r.Assets[i].Name,
			URL:      r.Assets[i].BrowserDownloadURL,
			Size:     r.Assets[i].Size,
			SHA256:   sha,
		})
	}
	sortAssets(channel.Assets)

	if len(channel.Assets) == 0 {
		warnings = append(warnings, fmt.Sprintf("%s: release %s has no recognised binary assets", channelName, r.TagName))
	}
	return channel, warnings
}

// dockerRefs builds the container image references for a channel.
func dockerRefs(opts *buildOptions, channelName, version string) *manifest.Docker {
	if opts.GHCRImage == "" && opts.DockerHubImage == "" {
		return nil
	}
	// The nightly dated image tag is derived from the build version, which can
	// drift from the GitHub release tag on a retry (the release tag gains a
	// "-<run_number>" suffix the image never gets). Only advertise a
	// version-pinned ref for channels whose release tag is guaranteed to match
	// the pushed image tag; nightly installs track the moving channel tag.
	pinned := channelName != manifest.ChannelNightly
	d := &manifest.Docker{}
	if opts.GHCRImage != "" {
		d.ChannelTag = opts.GHCRImage + ":" + channelMovingTag(channelName)
		if pinned {
			d.GHCR = opts.GHCRImage + ":" + version
		}
	}
	if opts.DockerHubImage != "" && pinned {
		d.DockerHub = opts.DockerHubImage + ":" + version
	}
	return d
}

// channelMovingTag returns the moving Docker tag users pull to track a channel.
func channelMovingTag(channelName string) string {
	switch channelName {
	case manifest.ChannelStable:
		return dockerTagLatest
	case manifest.ChannelNightly:
		return dockerTagNightly
	case manifest.ChannelBeta:
		return dockerTagBeta
	default:
		return channelName
	}
}

func sortAssets(assets []manifest.Asset) {
	slices.SortFunc(assets, func(a, b manifest.Asset) int {
		if a.Platform != b.Platform {
			return strings.Compare(a.Platform, b.Platform)
		}
		return strings.Compare(a.Arch, b.Arch)
	})
}

func sortedKeys(m map[string]ghRelease) []string {
	keys := slices.Collect(maps.Keys(m))
	slices.Sort(keys)
	return keys
}
