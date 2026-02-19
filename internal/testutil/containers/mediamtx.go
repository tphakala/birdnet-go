//go:build integration

package containers

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/docker/go-connections/nat"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// MediaMTXContainer wraps a testcontainers MediaMTX media server instance.
// MediaMTX is a multi-protocol media server that automatically converts
// streams between RTSP, RTMP, HLS, WebRTC, and other protocols.
type MediaMTXContainer struct {
	container testcontainers.Container
	host      string
	rtspPort  int
	rtmpPort  int
	hlsPort   int
}

// MediaMTXConfig holds configuration for MediaMTX container creation.
type MediaMTXConfig struct {
	// ImageTag for bluenviron/mediamtx (default: "latest")
	ImageTag string
}

// DefaultMediaMTXConfig returns a MediaMTXConfig with sensible defaults.
func DefaultMediaMTXConfig() MediaMTXConfig {
	return MediaMTXConfig{
		ImageTag: "1.16.1",
	}
}

// NewMediaMTXContainer creates and starts a MediaMTX media server container.
// The container exposes RTSP (8554), RTMP (1935), and HLS (8888) ports.
// It runs in TCP-only mode to work with Docker port mapping (no --network=host needed).
// If config is nil, uses DefaultMediaMTXConfig().
func NewMediaMTXContainer(ctx context.Context, config *MediaMTXConfig) (*MediaMTXContainer, error) {
	if config == nil {
		defaultCfg := DefaultMediaMTXConfig()
		config = &defaultCfg
	}

	image := fmt.Sprintf("bluenviron/mediamtx:%s", config.ImageTag)

	req := testcontainers.ContainerRequest{
		Image: image,
		ExposedPorts: []string{
			"8554/tcp", // RTSP
			"1935/tcp", // RTMP
			"8888/tcp", // HLS
		},
		Env: map[string]string{
			// TCP-only mode: required for Docker port mapping to work correctly.
			// Without this, UDP port remapping breaks RTSP protocol negotiation.
			// Note: MTX_PROTOCOLS was deprecated in v1.16+ in favor of MTX_RTSPTRANSPORTS.
			"MTX_RTSPTRANSPORTS": "tcp",
		},
		WaitingFor: wait.ForLog("[RTSP] listener opened on :8554").
			WithStartupTimeout(30 * time.Second),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start MediaMTX container: %w", err)
	}

	// Ensure container is cleaned up if any subsequent step fails.
	var successful bool
	defer func() {
		if !successful {
			_ = container.Terminate(context.Background()) //nolint:gocritic // no *testing.T available
		}
	}()

	host, err := container.Host(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get container host: %w", err)
	}

	rtspPort, err := mappedPortInt(ctx, container, "8554")
	if err != nil {
		return nil, fmt.Errorf("failed to get RTSP port: %w", err)
	}

	rtmpPort, err := mappedPortInt(ctx, container, "1935")
	if err != nil {
		return nil, fmt.Errorf("failed to get RTMP port: %w", err)
	}

	hlsPort, err := mappedPortInt(ctx, container, "8888")
	if err != nil {
		return nil, fmt.Errorf("failed to get HLS port: %w", err)
	}

	mc := &MediaMTXContainer{
		container: container,
		host:      host,
		rtspPort:  rtspPort,
		rtmpPort:  rtmpPort,
		hlsPort:   hlsPort,
	}

	// Verify RTSP port is accepting connections
	if err := WaitForTCP(host, rtspPort, 10*time.Second); err != nil {
		return nil, fmt.Errorf("RTSP port health check failed: %w", err)
	}

	successful = true
	return mc, nil
}

// mappedPortInt extracts the integer port from a container's mapped port.
func mappedPortInt(ctx context.Context, container testcontainers.Container, port string) (int, error) {
	mapped, err := container.MappedPort(ctx, nat.Port(port))
	if err != nil {
		return 0, err
	}
	return mapped.Int(), nil
}

// GetRTSPURL returns an RTSP URL for the given stream path.
// Example: GetRTSPURL("mystream") -> "rtsp://localhost:32768/mystream"
func (c *MediaMTXContainer) GetRTSPURL(path string) string {
	return fmt.Sprintf("rtsp://%s/%s", net.JoinHostPort(c.host, strconv.Itoa(c.rtspPort)), path)
}

// GetRTMPURL returns an RTMP URL for the given stream path.
// Example: GetRTMPURL("mystream") -> "rtmp://localhost:32769/mystream"
func (c *MediaMTXContainer) GetRTMPURL(path string) string {
	return fmt.Sprintf("rtmp://%s/%s", net.JoinHostPort(c.host, strconv.Itoa(c.rtmpPort)), path)
}

// GetHLSURL returns an HLS URL for the given stream path.
// Example: GetHLSURL("mystream") -> "http://localhost:32770/mystream/index.m3u8"
func (c *MediaMTXContainer) GetHLSURL(path string) string {
	return fmt.Sprintf("http://%s/%s/index.m3u8", net.JoinHostPort(c.host, strconv.Itoa(c.hlsPort)), path)
}

// GetHost returns the container host address.
func (c *MediaMTXContainer) GetHost() string {
	return c.host
}

// GetRTSPPort returns the mapped RTSP port.
func (c *MediaMTXContainer) GetRTSPPort() int {
	return c.rtspPort
}

// GetRTSPAddress returns the host:port for RTSP connections.
func (c *MediaMTXContainer) GetRTSPAddress() string {
	return net.JoinHostPort(c.host, strconv.Itoa(c.rtspPort))
}

// Terminate stops and removes the MediaMTX container.
func (c *MediaMTXContainer) Terminate(ctx context.Context) error {
	if c.container != nil {
		return c.container.Terminate(ctx)
	}
	return nil
}
