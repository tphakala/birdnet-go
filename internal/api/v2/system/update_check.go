package system

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/update/manifest"
)

// manifestURL is the stable, never-deleted manifest asset that always resolves
// to the latest per-channel release info (see internal/update/manifest).
const manifestURL = "https://github.com/tphakala/birdnet-go/releases/download/manifest/manifest.json"

const (
	updateCheckCacheTTL  = 1 * time.Hour
	manifestFetchTimeout = 10 * time.Second
	maxManifestBytes     = 1 << 20 // 1 MiB
)

// UpdateCheckResponse reports whether a newer build is available on the running
// build's release channel. It is intentionally forgiving: when the latest
// version cannot be determined (dev build, offline, unknown channel) it reports
// UpdateAvailable=false so the UI simply shows no indicator.
type UpdateCheckResponse struct {
	CurrentVersion  string `json:"currentVersion"`
	LatestVersion   string `json:"latestVersion,omitempty"`
	LatestName      string `json:"latestName,omitempty"` // human-readable release title
	ReleasedAt      string `json:"releasedAt,omitempty"` // RFC3339 publication time
	Notes           string `json:"notes,omitempty"`      // release changelog (trimmed)
	Channel         string `json:"channel,omitempty"`
	UpdateAvailable bool   `json:"updateAvailable"`
	Critical        bool   `json:"critical,omitempty"`
	ReleaseURL      string `json:"releaseURL,omitempty"` // GitHub release page
	IsDevBuild      bool   `json:"isDevBuild"`
	// Unavailable is true when the check could not reach a verdict (offline /
	// no channel data). The UI should treat this the same as "up to date".
	Unavailable bool   `json:"unavailable,omitempty"`
	CheckedAt   string `json:"checkedAt,omitempty"`
}

// computeUpdateStatus is the pure decision core: given the running version and a
// parsed manifest, it fills in the channel comparison. It performs no I/O so it
// is directly unit-testable. A nil manifest marks the result Unavailable.
func computeUpdateStatus(current, buildDate string, m *manifest.Manifest) UpdateCheckResponse {
	resp := UpdateCheckResponse{CurrentVersion: current}

	channel, ok := manifest.ClassifyTag(current)
	if !ok {
		// Empty, "Development Build", or a locally-built binary: no channel to
		// compare against.
		resp.IsDevBuild = true
		return resp
	}
	resp.Channel = channel

	if m == nil {
		resp.Unavailable = true
		return resp
	}
	ch := m.Channels[channel]
	if ch == nil || ch.Version == "" {
		resp.Unavailable = true
		return resp
	}

	resp.LatestVersion = ch.Version
	resp.LatestName = ch.Name
	resp.Notes = ch.Notes
	resp.ReleaseURL = ch.ReleaseURL
	if !ch.ReleasedAt.IsZero() {
		resp.ReleasedAt = ch.ReleasedAt.Format(time.RFC3339)
	}
	resp.UpdateAvailable = isNewerRelease(current, buildDate, ch)
	resp.Critical = ch.Critical && resp.UpdateAvailable
	return resp
}

// isNewerRelease reports whether the channel's latest release is newer than the
// running build. Version strings span multiple formats (semver stable tags,
// date-based nightlies), so rather than parse versions it compares the build
// date against the release date: a build produced at or after the latest release
// cannot be behind it, which avoids a false "update available" when the running
// build is ahead of a stale manifest. When either timestamp is missing it falls
// back to a plain version mismatch so a genuine update is never hidden.
func isNewerRelease(current, buildDate string, ch *manifest.Channel) bool {
	if ch.Version == current {
		return false
	}
	built, err := time.Parse(time.RFC3339, buildDate)
	if err != nil || ch.ReleasedAt.IsZero() {
		return true
	}
	return built.Before(ch.ReleasedAt)
}

// manifestCache memoizes the parsed manifest so the endpoint does not hit GitHub
// on every request, and can serve a stale copy while offline.
type manifestCache struct {
	mu        sync.Mutex
	manifest  *manifest.Manifest
	fetchedAt time.Time
}

var sharedManifestCache manifestCache

// get returns a cached manifest when fresh, otherwise fetches a new one. On
// fetch failure it falls back to any previously cached manifest, and only
// returns an error when there is nothing to serve.
func (c *manifestCache) get(ctx context.Context) (*manifest.Manifest, error) {
	// Snapshot the cache under the lock, then release it before any network I/O
	// so a slow fetch never blocks other requests. A rare concurrent double-fetch
	// is harmless for a small, idempotent manifest.
	c.mu.Lock()
	cached := c.manifest
	fresh := cached != nil && time.Since(c.fetchedAt) < updateCheckCacheTTL
	c.mu.Unlock()

	if fresh {
		return cached, nil
	}

	m, err := fetchManifest(ctx)
	if err != nil {
		if cached != nil {
			return cached, nil // serve stale rather than fail
		}
		return nil, err
	}

	c.mu.Lock()
	c.manifest = m
	c.fetchedAt = time.Now()
	c.mu.Unlock()
	return m, nil
}

func fetchManifest(ctx context.Context) (*manifest.Manifest, error) {
	ctx, cancel := context.WithTimeout(ctx, manifestFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build manifest request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch manifest: unexpected status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestBytes))
	if err != nil {
		return nil, fmt.Errorf("read manifest body: %w", err)
	}

	return manifest.Parse(data)
}

// GetUpdateCheck reports whether a newer build is available for the running
// build's channel. It never fails the request on network errors; those surface
// as Unavailable=true so the UI degrades to "up to date".
func (c *Handler) GetUpdateCheck(ctx echo.Context) error {
	current, buildDate := "", ""
	if s := c.CurrentSettings(); s != nil {
		current = s.Version
		buildDate = s.BuildDate
	}

	// Only reach out to the network when the running build maps to a channel;
	// dev or unclassifiable builds skip the fetch entirely.
	var m *manifest.Manifest
	if _, ok := manifest.ClassifyTag(current); ok {
		// Populate the shared cache with a detached context so an abandoned
		// request (client disconnect or navigation) cannot cancel the fetch and
		// leave the cache empty for the next caller. fetchManifest applies its
		// own timeout.
		fetched, err := sharedManifestCache.get(context.Background())
		if err != nil {
			c.Debug("update-check: manifest unavailable: %v", err)
		} else {
			m = fetched
		}
	}

	resp := computeUpdateStatus(current, buildDate, m)
	resp.CheckedAt = time.Now().Format(time.RFC3339)
	return ctx.JSON(http.StatusOK, resp)
}
