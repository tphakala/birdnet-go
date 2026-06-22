package main

import (
	"context"
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

	latest := latestPerChannel(releases)
	if len(latest) == 0 {
		return nil, nil, fmt.Errorf("no releases matched a known channel in %s", opts.Repo)
	}

	m := &manifest.Manifest{
		SchemaVersion: manifest.SchemaVersion,
		GeneratedAt:   opts.GeneratedAt.UTC(),
		Repo:          opts.Repo,
		Channels:      make(map[string]*manifest.Channel, len(latest)),
	}

	var warnings []string
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
		if cur, exists := latest[channel]; !exists || r.PublishedAt.After(cur.PublishedAt) {
			latest[channel] = r
		}
	}
	return latest
}

// buildChannel converts a single GitHub release into a manifest Channel.
func buildChannel(ctx context.Context, src releaseSource, opts *buildOptions, channelName string, r *ghRelease) (channel *manifest.Channel, warnings []string) {
	notes := strings.TrimSpace(r.Body)
	if opts.MaxNotesLen > 0 && len(notes) > opts.MaxNotesLen {
		notes = notes[:opts.MaxNotesLen]
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

	checksums := map[string]string{}
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
	d := &manifest.Docker{}
	if opts.GHCRImage != "" {
		d.GHCR = opts.GHCRImage + ":" + version
		d.ChannelTag = opts.GHCRImage + ":" + channelMovingTag(channelName)
	}
	if opts.DockerHubImage != "" {
		d.DockerHub = opts.DockerHubImage + ":" + version
	}
	return d
}

// channelMovingTag returns the moving Docker tag users pull to track a channel.
func channelMovingTag(channelName string) string {
	switch channelName {
	case manifest.ChannelStable:
		return "latest"
	case manifest.ChannelNightly:
		return "nightly"
	case manifest.ChannelBeta:
		return "beta"
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
