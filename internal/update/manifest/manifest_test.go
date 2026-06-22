package manifest

import (
	"strings"
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
		{name: "rc suffix no dot", tag: "v0.7.0-rc2", wantChannel: ChannelBeta, wantOK: true},
		{name: "alpha suffix", tag: "v0.7.0-alpha.3", wantChannel: ChannelBeta, wantOK: true},
		{name: "beta no number", tag: "v1.2.3-beta", wantChannel: ChannelBeta, wantOK: true},
		{name: "beta multi segment", tag: "v1.2.3-beta.1.2", wantChannel: ChannelBeta, wantOK: true},
		{name: "nightly dated", tag: "nightly-20260622", wantChannel: ChannelNightly, wantOK: true},
		{name: "nightly suffixed retry", tag: "nightly-20260622-414", wantChannel: ChannelNightly, wantOK: true},
		{name: "nightly git-describe suffix", tag: "nightly-20251025-1-gec0f78e", wantChannel: ChannelNightly, wantOK: true},
		{name: "manifest tag ignored", tag: "manifest", wantChannel: "", wantOK: false},
		{name: "plain date tag ignored", tag: "20240215", wantChannel: "", wantOK: false},
		{name: "two-part version rejected", tag: "v1.2", wantChannel: "", wantOK: false},
		{name: "four-part version rejected", tag: "v1.2.3.4", wantChannel: "", wantOK: false},
		{name: "nightly too short rejected", tag: "nightly-2026", wantChannel: "", wantOK: false},
		{name: "uppercase nightly rejected", tag: "NIGHTLY-20260622", wantChannel: "", wantOK: false},
		{name: "leading whitespace rejected", tag: " v0.6.4", wantChannel: "", wantOK: false},
		{name: "trailing whitespace rejected", tag: "v0.6.4 ", wantChannel: "", wantOK: false},
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
		{name: "uppercase rejected", filename: "BIRDNET-GO-LINUX-AMD64.TAR.GZ", wantOK: false},
		{name: "signature file rejected", filename: "birdnet-go-linux-amd64.tar.gz.sig", wantOK: false},
		{name: "prefixed junk rejected", filename: "prefix-birdnet-go-linux-amd64.tar.gz", wantOK: false},
		{name: "empty rejected", filename: "", wantOK: false},
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
	tests := []struct {
		name string
		data string
		want map[string]string
	}{
		{
			name: "gnu text and lowercasing",
			data: "ABCDEF0123456789  file-a.tar.gz\n  \ndeadbeef  file-b.tar.gz\nmalformed-single-field\n",
			want: map[string]string{"file-a.tar.gz": "abcdef0123456789", "file-b.tar.gz": "deadbeef"},
		},
		{
			name: "binary marker stripped",
			data: "abc123 *file.tar.gz\n",
			want: map[string]string{"file.tar.gz": "abc123"},
		},
		{
			name: "crlf line endings",
			data: "abc123  file.tar.gz\r\n",
			want: map[string]string{"file.tar.gz": "abc123"},
		},
		{
			name: "tab separated",
			data: "abc123\tfile.tar.gz\n",
			want: map[string]string{"file.tar.gz": "abc123"},
		},
		{
			name: "bsd style skipped",
			data: "SHA256 (file.tar.gz) = abc123\n",
			want: map[string]string{},
		},
		{name: "empty input", data: "", want: map[string]string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ParseChecksums([]byte(tt.data)))
		})
	}
}

func TestExtractCritical(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "canonical marker", body: "Security fix.\n" + CriticalMarker + "\nDetails.", want: true},
		{name: "no spaces", body: "x\n<!--manifest:critical-->\ny", want: true},
		{name: "extra spaces", body: "<!--   manifest:critical   -->", want: true},
		{name: "uppercase", body: "<!-- MANIFEST:CRITICAL -->", want: true},
		{name: "absent", body: "Normal release notes.", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ExtractCritical(tt.body))
		})
	}
}

func TestExtractMinUpgradeFrom(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "basic", body: "Notes\n<!-- manifest:min-upgrade-from=v0.5.0 -->\nmore", want: "v0.5.0"},
		{name: "no spaces", body: "<!--manifest:min-upgrade-from=v1.0.0-->", want: "v1.0.0"},
		{name: "uppercase marker", body: "<!-- MANIFEST:MIN-UPGRADE-FROM=v2.0.0 -->", want: "v2.0.0"},
		{name: "first wins", body: "<!-- manifest:min-upgrade-from=v1.0.0 -->\n<!-- manifest:min-upgrade-from=v2.0.0 -->", want: "v1.0.0"},
		{name: "empty value not matched", body: "<!-- manifest:min-upgrade-from= -->", want: ""},
		{name: "absent", body: "No marker here", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ExtractMinUpgradeFrom(tt.body))
		})
	}
}

// Fuzz targets assert structural invariants over arbitrary input, catching
// edge cases that example tables miss (a known escaped-defect class for the
// repo's parsers).

func FuzzClassifyTag(f *testing.F) {
	for _, s := range []string{"v0.6.4", "nightly-20260622", "v0.7.0-beta.1", "manifest", "", "v1.2.3-beta"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, tag string) {
		channel, ok := ClassifyTag(tag) // must never panic
		if ok {
			assert.Contains(t, []string{ChannelStable, ChannelNightly, ChannelBeta}, channel)
		} else {
			assert.Empty(t, channel, "ok=false must yield an empty channel")
		}
	})
}

func FuzzParseAssetName(f *testing.F) {
	for _, s := range []string{"birdnet-go-linux-amd64-v0.6.4.tar.gz", "birdnet-go-darwin-arm64.tar.gz", "checksums.txt", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		platform, arch, ok := ParseAssetName(name) // must never panic
		if ok {
			assert.Contains(t, []string{"linux", "windows", "darwin"}, platform)
			assert.Contains(t, []string{"amd64", "arm64"}, arch)
		} else {
			assert.Empty(t, platform)
			assert.Empty(t, arch)
		}
	})
}

func FuzzParseChecksums(f *testing.F) {
	f.Add("abc  file.tar.gz\n")
	f.Add("abc *file.tar.gz\nSHA256 (x) = y\n")
	f.Fuzz(func(t *testing.T, s string) {
		m := ParseChecksums([]byte(s)) // must never panic
		for _, v := range m {
			assert.NotEmpty(t, v, "hash value must be non-empty")
			assert.Equal(t, strings.ToLower(v), v, "hash must be lowercased")
		}
	})
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
			wantErr: "is not the supported version",
		},
		{
			name:    "wrong schema version",
			m:       &Manifest{SchemaVersion: 99, Channels: map[string]*Channel{ChannelStable: {Version: "v1", Tag: "v1"}}},
			wantErr: "is not the supported version",
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
