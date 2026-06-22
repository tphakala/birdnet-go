package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/update/manifest"
)

// fakeSource is an in-memory releaseSource for tests.
type fakeSource struct {
	releases  []ghRelease
	downloads map[string][]byte
	listErr   error
}

func (f *fakeSource) ListReleases(_ context.Context, _ string) ([]ghRelease, error) {
	return f.releases, f.listErr
}

func (f *fakeSource) Download(_ context.Context, url string) ([]byte, error) {
	if data, ok := f.downloads[url]; ok {
		return data, nil
	}
	return nil, assert.AnError
}

func tarball(name, url string, size int64) ghAsset {
	return ghAsset{Name: name, Size: size, BrowserDownloadURL: url}
}

func defaultOpts() *buildOptions {
	return &buildOptions{
		Repo:           "tphakala/birdnet-go",
		GHCRImage:      "ghcr.io/tphakala/birdnet-go",
		DockerHubImage: "tphakala/birdnet-go",
		GeneratedAt:    time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC),
		MaxNotesLen:    50000,
	}
}

func TestBuildManifest_AllChannels(t *testing.T) {
	t.Parallel()

	stableChecksumsURL := "https://dl/stable/checksums.txt"
	nightlyChecksumsURL := "https://dl/nightly/checksums.txt"
	betaChecksumsURL := "https://dl/beta/checksums.txt"

	src := &fakeSource{
		releases: []ghRelease{
			// Newer stable should win over the older one.
			{
				TagName: "v0.6.3", PublishedAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
				Assets: []ghAsset{tarball("birdnet-go-linux-amd64-v0.6.3.tar.gz", "https://dl/old", 1)},
			},
			{
				TagName: "v0.6.4", Name: "BirdNET-Go v0.6.4",
				Body:        "Stable notes.\n" + manifest.CriticalMarker + "\n<!-- manifest:min-upgrade-from=v0.5.0 -->",
				PublishedAt: time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC),
				HTMLURL:     "https://github.com/tphakala/birdnet-go/releases/tag/v0.6.4",
				Assets: []ghAsset{
					tarball("birdnet-go-linux-arm64-v0.6.4.tar.gz", "https://dl/s-arm", 200),
					tarball("birdnet-go-linux-amd64-v0.6.4.tar.gz", "https://dl/s-amd", 100),
					{Name: "checksums.txt", BrowserDownloadURL: stableChecksumsURL},
					{Name: "README.md", BrowserDownloadURL: "https://dl/readme"},
				},
			},
			// Beta.
			{
				TagName: "v0.7.0-beta.1", Prerelease: true,
				PublishedAt: time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC),
				Assets: []ghAsset{
					tarball("birdnet-go-linux-amd64-v0.7.0-beta.1.tar.gz", "https://dl/beta", 50),
					{Name: "checksums.txt", BrowserDownloadURL: betaChecksumsURL},
				},
			},
			// Nightly (no version suffix in asset names).
			{
				TagName: "nightly-20260622", Prerelease: true,
				PublishedAt: time.Date(2026, 6, 22, 1, 0, 0, 0, time.UTC),
				Assets: []ghAsset{
					tarball("birdnet-go-linux-amd64.tar.gz", "https://dl/n-amd", 300),
					{Name: "checksums.txt", BrowserDownloadURL: nightlyChecksumsURL},
				},
			},
			// The manifest release itself must be ignored.
			{TagName: "manifest", PublishedAt: time.Date(2026, 6, 22, 2, 0, 0, 0, time.UTC)},
			// A draft must be ignored even if newer.
			{TagName: "v0.6.5", Draft: true, PublishedAt: time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)},
		},
		downloads: map[string][]byte{
			stableChecksumsURL: []byte(
				"aaa111  birdnet-go-linux-amd64-v0.6.4.tar.gz\n" +
					"bbb222  birdnet-go-linux-arm64-v0.6.4.tar.gz\n"),
			nightlyChecksumsURL: []byte("ccc333  birdnet-go-linux-amd64.tar.gz\n"),
			betaChecksumsURL:    []byte("ddd444  birdnet-go-linux-amd64-v0.7.0-beta.1.tar.gz\n"),
		},
	}

	m, warnings, err := buildManifest(t.Context(), src, defaultOpts())
	require.NoError(t, err)
	require.NoError(t, m.Validate())
	assert.Empty(t, warnings, "all assets have checksums")

	require.Len(t, m.Channels, 3)
	assert.Equal(t, manifest.SchemaVersion, m.SchemaVersion)
	assert.Equal(t, "tphakala/birdnet-go", m.Repo)

	// Stable: newest wins, critical + min-upgrade parsed, assets sorted, hashes mapped.
	stable := m.Channels[manifest.ChannelStable]
	require.NotNil(t, stable)
	assert.Equal(t, "v0.6.4", stable.Version)
	assert.True(t, stable.Critical)
	assert.Equal(t, "v0.5.0", stable.MinUpgradeFrom)
	require.Len(t, stable.Assets, 2, "README and checksums excluded")
	assert.Equal(t, "amd64", stable.Assets[0].Arch, "sorted: amd64 before arm64")
	assert.Equal(t, "aaa111", stable.Assets[0].SHA256)
	assert.Equal(t, "bbb222", stable.Assets[1].SHA256)
	require.NotNil(t, stable.Docker)
	assert.Equal(t, "ghcr.io/tphakala/birdnet-go:latest", stable.Docker.ChannelTag)
	assert.Equal(t, "ghcr.io/tphakala/birdnet-go:v0.6.4", stable.Docker.GHCR)
	assert.Equal(t, "tphakala/birdnet-go:v0.6.4", stable.Docker.DockerHub)

	// Beta.
	beta := m.Channels[manifest.ChannelBeta]
	require.NotNil(t, beta)
	assert.Equal(t, "v0.7.0-beta.1", beta.Version)
	assert.Equal(t, "ghcr.io/tphakala/birdnet-go:beta", beta.Docker.ChannelTag)

	// Nightly.
	nightly := m.Channels[manifest.ChannelNightly]
	require.NotNil(t, nightly)
	assert.Equal(t, "nightly-20260622", nightly.Version)
	require.Len(t, nightly.Assets, 1)
	assert.Equal(t, "ccc333", nightly.Assets[0].SHA256)
	assert.Equal(t, "ghcr.io/tphakala/birdnet-go:nightly", nightly.Docker.ChannelTag)
}

func TestBuildManifest_MissingChecksumsWarns(t *testing.T) {
	t.Parallel()
	src := &fakeSource{
		releases: []ghRelease{
			{
				TagName:     "v0.6.4",
				PublishedAt: time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC),
				Assets:      []ghAsset{tarball("birdnet-go-linux-amd64-v0.6.4.tar.gz", "https://dl/amd", 100)},
			},
		},
	}
	m, warnings, err := buildManifest(t.Context(), src, defaultOpts())
	require.NoError(t, err)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "no checksum")
	assert.Empty(t, m.Channels[manifest.ChannelStable].Assets[0].SHA256)
}

func TestBuildManifest_NoMatchingReleases(t *testing.T) {
	t.Parallel()
	src := &fakeSource{releases: []ghRelease{{TagName: "manifest"}, {TagName: "random"}}}
	_, _, err := buildManifest(t.Context(), src, defaultOpts())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no releases matched")
}

func TestBuildManifest_DockerOmittedWhenImagesEmpty(t *testing.T) {
	t.Parallel()
	opts := defaultOpts()
	opts.GHCRImage = ""
	opts.DockerHubImage = ""
	src := &fakeSource{
		releases: []ghRelease{
			{TagName: "v0.6.4", PublishedAt: time.Now(), Assets: []ghAsset{tarball("birdnet-go-linux-amd64-v0.6.4.tar.gz", "https://dl/amd", 100)}},
		},
	}
	m, _, err := buildManifest(t.Context(), src, opts)
	require.NoError(t, err)
	assert.Nil(t, m.Channels[manifest.ChannelStable].Docker)
}
