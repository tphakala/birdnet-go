package system

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/update/manifest"
)

func TestComputeUpdateStatus(t *testing.T) {
	t.Parallel()

	const (
		buildBeforeRelease = "2026-06-15T00:00:00Z"
		buildAfterRelease  = "2026-07-05T00:00:00Z"
	)
	m := &manifest.Manifest{
		Channels: map[string]*manifest.Channel{
			manifest.ChannelStable: {
				Version:    "v0.7.0",
				ReleaseURL: "https://example.test/v0.7.0",
				ReleasedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
				Critical:   true,
			},
		},
	}

	t.Run("unclassifiable version is treated as a dev build", func(t *testing.T) {
		t.Parallel()
		got := computeUpdateStatus("Development Build", "", m)
		assert.True(t, got.IsDevBuild)
		assert.False(t, got.UpdateAvailable)
	})

	t.Run("nil manifest is unavailable but still resolves the channel", func(t *testing.T) {
		t.Parallel()
		got := computeUpdateStatus("v0.6.4", buildBeforeRelease, nil)
		assert.Equal(t, manifest.ChannelStable, got.Channel)
		assert.True(t, got.Unavailable)
		assert.False(t, got.UpdateAvailable)
	})

	t.Run("a build older than the release is flagged available", func(t *testing.T) {
		t.Parallel()
		got := computeUpdateStatus("v0.6.4", buildBeforeRelease, m)
		assert.True(t, got.UpdateAvailable)
		assert.Equal(t, "v0.7.0", got.LatestVersion)
		assert.Equal(t, "https://example.test/v0.7.0", got.ReleaseURL)
		assert.True(t, got.Critical)
	})

	t.Run("a build newer than the release reports no update", func(t *testing.T) {
		t.Parallel()
		got := computeUpdateStatus("v0.6.4", buildAfterRelease, m)
		assert.False(t, got.UpdateAvailable)
		assert.False(t, got.Critical)
	})

	t.Run("a missing build date falls back to a version mismatch", func(t *testing.T) {
		t.Parallel()
		got := computeUpdateStatus("v0.6.4", "", m)
		assert.True(t, got.UpdateAvailable)
	})

	t.Run("running the latest reports no update", func(t *testing.T) {
		t.Parallel()
		got := computeUpdateStatus("v0.7.0", buildAfterRelease, m)
		assert.False(t, got.UpdateAvailable)
		assert.False(t, got.Critical)
		assert.Equal(t, "v0.7.0", got.LatestVersion)
	})

	t.Run("channel absent from manifest is unavailable", func(t *testing.T) {
		t.Parallel()
		got := computeUpdateStatus("nightly-20260101", "", m)
		assert.Equal(t, manifest.ChannelNightly, got.Channel)
		assert.True(t, got.Unavailable)
		assert.False(t, got.UpdateAvailable)
	})
}
