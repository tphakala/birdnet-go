//go:build integration

package streamtest

import (
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/testutil/containers"
)

// Fixture tuning for the MediaMTX-backed implementation.
const (
	// publishSettle is how long to wait after (re)starting a publisher before the
	// stream is reliably registered on the server.
	publishSettle = 3 * time.Second

	// serverStopTimeout bounds the graceful container stop.
	serverStopTimeout = 10 * time.Second

	// unreachableHostURL points at TEST-NET-1 (RFC 5737), which is guaranteed not
	// to be routable, so a connect attempt times out.
	unreachableHostURL = "rtsp://192.0.2.1:554/dead"

	// fallbackRefusedPort is a port that is almost never listening, used by
	// RefusedPortURL only when a free port cannot be obtained.
	fallbackRefusedPort = 1
)

// MediaMTXFixture is a Fixture backed by a MediaMTX container. It publishes
// synthesized tones with FFmpeg and can stop and restart the whole server.
type MediaMTXFixture struct {
	server *containers.MediaMTXContainer
}

// NewMediaMTXFixture wraps a running MediaMTX container as a contract Fixture.
func NewMediaMTXFixture(t *testing.T, server *containers.MediaMTXContainer) *MediaMTXFixture {
	t.Helper()
	require.NotNil(t, server, "MediaMTX container is required")
	return &MediaMTXFixture{server: server}
}

// toneOptions maps the neutral PublishOptions to the FFmpeg encoder names the
// publisher expects.
func toneOptions(o PublishOptions) containers.ToneOptions {
	codec := string(o.Codec)
	if o.Codec == CodecOpus {
		codec = "libopus"
	}
	return containers.ToneOptions{
		Codec:      codec,
		SampleRate: o.SampleRate,
		Channels:   o.Channels,
		WithVideo:  o.WithVideo,
	}
}

// Publish starts a publisher for the given options and returns once it has had
// time to register on the server.
func (f *MediaMTXFixture) Publish(t *testing.T, opts PublishOptions) Publication {
	t.Helper()
	if opts.Path == "" {
		opts.Path = uniquePath("pub")
	}
	p := &mediaPublication{
		opts: opts,
		url:  f.server.GetRTSPURL(opts.Path),
	}
	p.Restart(t)
	return p
}

// URLForPath returns a read URL for an arbitrary path.
func (f *MediaMTXFixture) URLForPath(path string) string {
	return f.server.GetRTSPURL(path)
}

// UnreachableHostURL returns a URL whose host cannot be routed.
func (f *MediaMTXFixture) UnreachableHostURL() string {
	return unreachableHostURL
}

// RefusedPortURL returns a URL on the live server host but a closed port.
func (f *MediaMTXFixture) RefusedPortURL() string {
	port := fallbackRefusedPort
	if free, err := containers.GetFreePort(); err == nil {
		port = free
	}
	return "rtsp://" + net.JoinHostPort(f.server.GetHost(), strconv.Itoa(port)) + "/closed"
}

// BadAuthURL returns "" because the default MediaMTX container enforces no
// authentication, so the auth_failed classification is not exercised here.
func (f *MediaMTXFixture) BadAuthURL(t *testing.T) string {
	t.Helper()
	return ""
}

// StopServer stops the whole media server.
func (f *MediaMTXFixture) StopServer(t *testing.T) {
	t.Helper()
	require.NoError(t, f.server.Stop(context.Background(), serverStopTimeout))
}

// StartServer restarts the media server and waits for it to accept connections.
func (f *MediaMTXFixture) StartServer(t *testing.T) {
	t.Helper()
	require.NoError(t, f.server.Start(context.Background()))
}

// SupportsVideo reports that MediaMTX can carry a video track.
func (f *MediaMTXFixture) SupportsVideo() bool { return true }

// mediaPublication is a single supervised FFmpeg publisher.
type mediaPublication struct {
	opts PublishOptions
	url  string
	mu   sync.Mutex
	pub  *containers.StreamPublisher
}

func (p *mediaPublication) URL() string { return p.url }

func (p *mediaPublication) Stop(t *testing.T) {
	t.Helper()
	p.mu.Lock()
	pub := p.pub
	p.pub = nil
	p.mu.Unlock()
	if pub != nil {
		pub.Stop()
	}
}

func (p *mediaPublication) Restart(t *testing.T) {
	t.Helper()
	p.Stop(t)
	pub, err := containers.PublishToneToMediaMTX(context.Background(), p.url, toneOptions(p.opts))
	require.NoError(t, err, "publisher should start")
	p.mu.Lock()
	p.pub = pub
	p.mu.Unlock()
	time.Sleep(publishSettle)
	requirePublisherAlive(t, pub)
}

// requirePublisherAlive fails fast, with the publisher's captured stderr, when
// the FFmpeg tone publisher has already exited (bad args, an unsupported codec).
// Without it a dead publisher surfaces only later as a confusing "frames should
// flow" waitForFrames timeout instead of a clear "publisher died: <stderr>".
func requirePublisherAlive(t *testing.T, pub *containers.StreamPublisher) {
	t.Helper()
	if pub.IsRunning() {
		return
	}
	stderr := strings.TrimSpace(pub.Stderr())
	if exitErr, ok := pub.ExitError(); ok && exitErr != nil {
		t.Fatalf("FFmpeg publisher exited shortly after start: %v\nstderr:\n%s", exitErr, stderr)
	}
	t.Fatalf("FFmpeg publisher exited shortly after start\nstderr:\n%s", stderr)
}
