package manifest

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifyTag(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		tag         string
		wantChannel string
		wantOK      bool
	}{
		{name: "stable semver", tag: "v0.6.4", wantChannel: ChannelStable, wantOK: true},
		{name: "stable two digit", tag: "v1.10.0", wantChannel: ChannelStable, wantOK: true},
		{name: "beta suffix", tag: "v0.7.0-beta.1", wantChannel: ChannelBeta, wantOK: true},
		{name: "rc suffix", tag: "v0.7.0-rc2", wantChannel: ChannelBeta, wantOK: true},
		{name: "alpha suffix", tag: "v0.7.0-alpha.3", wantChannel: ChannelBeta, wantOK: true},
		{name: "nightly dated", tag: "nightly-20260622", wantChannel: ChannelNightly, wantOK: true},
		{name: "nightly suffixed retry", tag: "nightly-20260622-414", wantChannel: ChannelNightly, wantOK: true},
		{name: "manifest tag ignored", tag: "manifest", wantChannel: "", wantOK: false},
		{name: "plain date tag ignored", tag: "20240215", wantChannel: "", wantOK: false},
		{name: "empty ignored", tag: "", wantChannel: "", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotChannel, gotOK := ClassifyTag(tt.tag)
			assert.Equal(t, tt.wantOK, gotOK)
			assert.Equal(t, tt.wantChannel, gotChannel)
		})
	}
}

func TestParseAssetName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		filename     string
		wantPlatform string
		wantArch     string
		wantOK       bool
	}{
		{name: "stable with version", filename: "birdnet-go-linux-amd64-v0.6.4.tar.gz", wantPlatform: "linux", wantArch: "amd64", wantOK: true},
		{name: "nightly without version", filename: "birdnet-go-linux-arm64.tar.gz", wantPlatform: "linux", wantArch: "arm64", wantOK: true},
		{name: "windows amd64", filename: "birdnet-go-windows-amd64-v0.6.4.tar.gz", wantPlatform: "windows", wantArch: "amd64", wantOK: true},
		{name: "darwin arm64", filename: "birdnet-go-darwin-arm64.tar.gz", wantPlatform: "darwin", wantArch: "arm64", wantOK: true},
		{name: "checksums file rejected", filename: "checksums.txt", wantOK: false},
		{name: "readme rejected", filename: "README.md", wantOK: false},
		{name: "unknown arch rejected", filename: "birdnet-go-linux-riscv64.tar.gz", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			platform, arch, ok := ParseAssetName(tt.filename)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantPlatform, platform)
			assert.Equal(t, tt.wantArch, arch)
		})
	}
}

func TestParseChecksums(t *testing.T) {
	t.Parallel()
	data := []byte(
		"ABCDEF0123456789  birdnet-go-linux-amd64-v0.6.4.tar.gz\n" +
			"  \n" + // blank line skipped
			"deadbeef  birdnet-go-linux-arm64-v0.6.4.tar.gz\n" +
			"malformed-single-field\n", // skipped
	)
	got := ParseChecksums(data)
	require.Len(t, got, 2)
	assert.Equal(t, "abcdef0123456789", got["birdnet-go-linux-amd64-v0.6.4.tar.gz"], "hash lowercased")
	assert.Equal(t, "deadbeef", got["birdnet-go-linux-arm64-v0.6.4.tar.gz"])
}

func TestExtractCritical(t *testing.T) {
	t.Parallel()
	assert.True(t, ExtractCritical("Security fix.\n"+CriticalMarker+"\nDetails."))
	assert.False(t, ExtractCritical("Normal release notes."))
}

func TestExtractMinUpgradeFrom(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "v0.5.0", ExtractMinUpgradeFrom("Notes\n<!-- manifest:min-upgrade-from=v0.5.0 -->\nmore"))
	assert.Empty(t, ExtractMinUpgradeFrom("No marker here"))
}

func TestValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		m       *Manifest
		wantErr string
	}{
		{
			name:    "missing schema version",
			m:       &Manifest{Channels: map[string]*Channel{ChannelStable: {Version: "v1", Tag: "v1"}}},
			wantErr: "schema_version is required",
		},
		{
			name:    "no channels",
			m:       &Manifest{SchemaVersion: SchemaVersion, Channels: map[string]*Channel{}},
			wantErr: "at least one channel is required",
		},
		{
			name:    "nil channel",
			m:       &Manifest{SchemaVersion: SchemaVersion, Channels: map[string]*Channel{ChannelStable: nil}},
			wantErr: "entry is nil",
		},
		{
			name:    "missing version",
			m:       &Manifest{SchemaVersion: SchemaVersion, Channels: map[string]*Channel{ChannelStable: {Tag: "v1"}}},
			wantErr: "version is required",
		},
		{
			name:    "valid",
			m:       &Manifest{SchemaVersion: SchemaVersion, Channels: map[string]*Channel{ChannelStable: {Version: "v1", Tag: "v1"}}},
			wantErr: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.m.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestJSONRoundTrip(t *testing.T) {
	t.Parallel()
	original := &Manifest{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC),
		Repo:          "tphakala/birdnet-go",
		Channels: map[string]*Channel{
			ChannelStable: {
				Version:    "v0.6.4",
				Tag:        "v0.6.4",
				Name:       "BirdNET-Go v0.6.4",
				ReleasedAt: time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC),
				ReleaseURL: "https://github.com/tphakala/birdnet-go/releases/tag/v0.6.4",
				Docker:     &Docker{ChannelTag: "ghcr.io/tphakala/birdnet-go:latest"},
				Assets: []Asset{
					{Platform: "linux", Arch: "amd64", Filename: "birdnet-go-linux-amd64-v0.6.4.tar.gz", URL: "https://example/a", Size: 100, SHA256: "abc"},
				},
			},
		},
	}
	data, err := original.JSON()
	require.NoError(t, err)
	assert.Equal(t, byte('\n'), data[len(data)-1], "trailing newline")

	parsed, err := Parse(data)
	require.NoError(t, err)
	assert.Equal(t, original, parsed)
	require.NoError(t, parsed.Validate())
}
