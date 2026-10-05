package main

import (
	"context"
	"strings"
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
	assert.False(t, stable.Prerelease)
	require.NotNil(t, stable.Docker)
	assert.Equal(t, "ghcr.io/tphakala/birdnet-go:latest", stable.Docker.ChannelTag)
	assert.Equal(t, "ghcr.io/tphakala/birdnet-go:v0.6.4", stable.Docker.GHCR)
	assert.Equal(t, "tphakala/birdnet-go:v0.6.4", stable.Docker.DockerHub)

	// Beta.
	beta := m.Channels[manifest.ChannelBeta]
	require.NotNil(t, beta)
	assert.Equal(t, "v0.7.0-beta.1", beta.Version)
	assert.True(t, beta.Prerelease)
	assert.Equal(t, "ghcr.io/tphakala/birdnet-go:beta", beta.Docker.ChannelTag)

	// Nightly: moving channel tag only, no version-pinned Docker ref.
	nightly := m.Channels[manifest.ChannelNightly]
	require.NotNil(t, nightly)
	assert.Equal(t, "nightly-20260622", nightly.Version)
	assert.True(t, nightly.Prerelease)
	require.Len(t, nightly.Assets, 1)
	assert.Equal(t, "ccc333", nightly.Assets[0].SHA256)
	assert.Equal(t, "ghcr.io/tphakala/birdnet-go:nightly", nightly.Docker.ChannelTag)
	assert.Empty(t, nightly.Docker.GHCR, "nightly must not advertise a version-pinned image")
	assert.Empty(t, nightly.Docker.DockerHub)
}

func TestBuildManifest_ChecksumDownloadErrorWarns(t *testing.T) {
	t.Parallel()
	src := &fakeSource{
		releases: []ghRelease{{
			TagName:     "v0.6.4",
			PublishedAt: time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC),
			Assets: []ghAsset{
				tarball("birdnet-go-linux-amd64-v0.6.4.tar.gz", "https://dl/amd", 100),
				{Name: "checksums.txt", BrowserDownloadURL: "https://dl/missing"}, // absent from downloads -> error
			},
		}},
		downloads: map[string][]byte{},
	}
	m, warnings, err := buildManifest(t.Context(), src, defaultOpts())
	require.NoError(t, err)
	assert.Contains(t, strings.Join(warnings, "|"), "download checksums.txt")
	assert.Empty(t, m.Channels[manifest.ChannelStable].Assets[0].SHA256)
}

func TestBuildManifest_NoBinaryAssetsWarns(t *testing.T) {
	t.Parallel()
	src := &fakeSource{
		releases: []ghRelease{{
			TagName:     "v0.6.4",
			PublishedAt: time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC),
			Assets:      []ghAsset{{Name: "README.md", BrowserDownloadURL: "https://dl/readme"}},
		}},
	}
	m, warnings, err := buildManifest(t.Context(), src, defaultOpts())
	require.NoError(t, err)
	stable := m.Channels[manifest.ChannelStable]
	require.NotNil(t, stable)
	assert.Empty(t, stable.Assets)
	assert.Equal(t, "v0.6.4", stable.Version, "channel still populated despite no binary assets")
	assert.Contains(t, strings.Join(warnings, "|"), "no recognised binary assets")
}

func TestBuildManifest_NoChannelsIsSoftError(t *testing.T) {
	t.Parallel()
	src := &fakeSource{releases: []ghRelease{
		{TagName: "manifest"},
		{TagName: "bogus", Draft: true},
	}}
	_, warnings, err := buildManifest(t.Context(), src, defaultOpts())
	require.ErrorIs(t, err, errNoChannels)
	assert.Empty(t, warnings)
}

// day returns midnight UTC on the given date.
func day(year int, month time.Month, d int) time.Time {
	return time.Date(year, month, d, 0, 0, 0, 0, time.UTC)
}

// dateRelease returns a published date release shaped like the ones the
// release workflow creates: four versioned tarballs plus checksums.txt.
func dateRelease(tag string, published time.Time) (release ghRelease, checksumsURL string, checksums []byte) {
	checksumsURL = "https://dl/" + tag + "/checksums.txt"
	var sums strings.Builder
	release = ghRelease{TagName: tag, Name: "BirdNET-Go " + tag, PublishedAt: published}
	for _, target := range []string{"linux-amd64", "linux-arm64", "windows-amd64", "darwin-arm64"} {
		name := "birdnet-go-" + target + "-" + tag + ".tar.gz"
		release.Assets = append(release.Assets, tarball(name, "https://dl/"+tag+"/"+name, 100))
		sums.WriteString("sha-" + target + "  " + name + "\n")
	}
	release.Assets = append(release.Assets, ghAsset{Name: checksumsFilename, BrowserDownloadURL: checksumsURL})
	return release, checksumsURL, []byte(sums.String())
}

func TestBuildManifest_DateReleases(t *testing.T) {
	t.Parallel()

	latest, latestURL, latestSums := dateRelease("20260823", day(2026, 8, 23))
	older, olderURL, olderSums := dateRelease("20260716", day(2026, 7, 16))
	src := &fakeSource{
		releases: []ghRelease{
			latest,
			{TagName: "manifest", Prerelease: true, PublishedAt: day(2026, 6, 22)},
			{TagName: "bogus", Draft: true, PublishedAt: day(2026, 9, 1)},
			older,
			{TagName: "nightly-20260615", Prerelease: true, PublishedAt: day(2026, 6, 15)},
			{TagName: "v0.6.4", PublishedAt: day(2025, 3, 15)},
			{TagName: "20240215", PublishedAt: day(2024, 2, 15)},
		},
		downloads: map[string][]byte{latestURL: latestSums, olderURL: olderSums},
	}

	m, warnings, err := buildManifest(t.Context(), src, defaultOpts())
	require.NoError(t, err)
	assert.Empty(t, warnings)
	require.Len(t, m.Channels, 2)

	for name, movingTag := range map[string]string{manifest.ChannelStable: "latest", manifest.ChannelNightly: "nightly"} {
		ch := m.Channels[name]
		require.NotNil(t, ch, name)
		assert.Equal(t, "20260823", ch.Version, name)
		assert.Equal(t, "20260823", ch.Tag, name)
		require.NotNil(t, ch.Docker, name)
		assert.Equal(t, "ghcr.io/tphakala/birdnet-go:20260823", ch.Docker.GHCR, name)
		assert.Equal(t, "tphakala/birdnet-go:20260823", ch.Docker.DockerHub, name)
		assert.Equal(t, "ghcr.io/tphakala/birdnet-go:"+movingTag, ch.Docker.ChannelTag, name)
		require.Len(t, ch.Assets, 4, name)
		assert.Equal(t, "darwin", ch.Assets[0].Platform, "assets sorted by platform")
		for _, a := range ch.Assets {
			assert.Equal(t, "sha-"+a.Platform+"-"+a.Arch, a.SHA256, a.Filename)
		}
	}
}

func TestBuildManifest_UnclassifiedReleaseFails(t *testing.T) {
	t.Parallel()
	valid, _, _ := dateRelease("20260823", day(2026, 8, 23))
	src := &fakeSource{releases: []ghRelease{
		valid,
		{TagName: "20261002-gabc123-dev", PublishedAt: day(2026, 10, 2)},
		{TagName: "nightly", PublishedAt: day(2026, 10, 3)},
	}}
	m, _, err := buildManifest(t.Context(), src, defaultOpts())
	require.ErrorIs(t, err, errUnclassifiedReleases)
	require.NotErrorIs(t, err, errNoChannels, "must not be treated as the soft no-channels case")
	assert.Contains(t, err.Error(), "20261002-gabc123-dev, nightly")
	assert.Nil(t, m)
}

func TestLatestPerChannel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		releases []ghRelease
		want     map[string]string // channel -> tag
	}{
		{
			name:     "timestamp tie breaks on the greater tag",
			releases: []ghRelease{{TagName: "v0.6.5", PublishedAt: day(2026, 6, 20)}, {TagName: "v0.6.4", PublishedAt: day(2026, 6, 20)}},
			want:     map[string]string{manifest.ChannelStable: "v0.6.5"},
		},
		{
			name:     "timestamp tie order reversed",
			releases: []ghRelease{{TagName: "v0.6.4", PublishedAt: day(2026, 6, 20)}, {TagName: "v0.6.5", PublishedAt: day(2026, 6, 20)}},
			want:     map[string]string{manifest.ChannelStable: "v0.6.5"},
		},
		{
			name:     "newer v release beats an older date snapshot",
			releases: []ghRelease{{TagName: "20240215", PublishedAt: day(2024, 2, 15)}, {TagName: "v0.6.4", PublishedAt: day(2025, 3, 15)}},
			want:     map[string]string{manifest.ChannelStable: "v0.6.4", manifest.ChannelNightly: "20240215"},
		},
		{
			name: "date release beats historical stable and nightly",
			releases: []ghRelease{
				{TagName: "v0.6.4", PublishedAt: day(2025, 3, 15)},
				{TagName: "nightly-20260615", PublishedAt: day(2026, 6, 15)},
				{TagName: "20260823", PublishedAt: day(2026, 8, 23)},
			},
			want: map[string]string{manifest.ChannelStable: "20260823", manifest.ChannelNightly: "20260823"},
		},
		{
			name:     "historical nightly kept without date releases",
			releases: []ghRelease{{TagName: "nightly-20260615", PublishedAt: day(2026, 6, 15)}},
			want:     map[string]string{manifest.ChannelNightly: "nightly-20260615"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			latest, unclassified := latestPerChannel(tt.releases)
			assert.Empty(t, unclassified)
			got := make(map[string]string, len(latest))
			for channel := range latest {
				got[channel] = latest[channel].TagName
			}
			assert.Equal(t, tt.want, got)
		})
	}
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
