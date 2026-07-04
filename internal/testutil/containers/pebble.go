//go:build integration

package containers

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Pebble (https://github.com/letsencrypt/pebble) is Let's Encrypt's small ACME
// test server. This helper runs it as a testcontainer so an in-process server
// can obtain a real, CA-issued certificate end-to-end without touching the
// public Let's Encrypt service or its rate limits.
//
// Networking model: ACME validation is bidirectional.
//   - The test process (ACME client) reaches Pebble's ACME + management APIs via
//     the container's dynamically mapped host ports.
//   - Pebble (the ACME server) reaches back to the applicant's HTTP-01 and
//     TLS-ALPN-01 challenge listeners. Those listeners run in the test process on
//     the host, so Pebble must resolve the challenge host to the container host
//     (via a host-gateway ExtraHost) and connect to the exact host ports the
//     server listens on (PebbleConfig.HTTPValidationPort / TLSValidationPort).
const (
	pebbleImage = "ghcr.io/letsencrypt/pebble"
	// pebbleDefaultTag pins a known-good Pebble version rather than "latest": the
	// success signal (WaitForIssuedCertificate) matches Pebble's own log strings,
	// so a pinned image keeps that marker contract stable and the test reproducible.
	pebbleDefaultTag = "2.9.0"
	// pebbleACMEPort and pebbleMgmtPort are the container-internal ports (in the
	// testcontainers "N/tcp" form) that Pebble binds. The config listen addresses
	// below derive from these via trimming "/tcp", so the two cannot drift.
	pebbleACMEPort       = "14000/tcp"
	pebbleMgmtPort       = "15000/tcp"
	pebbleConfigPath     = "/test/config/pebble-config.json"
	pebbleStartupTimeout = 60 * time.Second

	// DefaultPebbleChallengeHost is a dotted host name usable as an ACME
	// identifier. It must not be single-label: golang.org/x/crypto/acme/autocert
	// rejects single-label names ("server name component count invalid").
	DefaultPebbleChallengeHost = "bng.test"

	// pebbleHostGateway is the ExtraHost target that resolves, from inside the
	// container, to the Docker/podman host running the test. Docker Engine 20.10+
	// and rootless podman both understand the special "host-gateway" value.
	pebbleHostGateway = "host-gateway"
)

// PebbleConfig configures a Pebble ACME test-server container.
type PebbleConfig struct {
	// ImageTag for ghcr.io/letsencrypt/pebble (default: pebbleDefaultTag).
	ImageTag string
	// ChallengeHost is the ACME identifier a certificate will be requested for.
	// Pebble resolves it to the container host (host-gateway) so it can reach the
	// applicant's challenge listeners. Must be dotted. Defaults to
	// DefaultPebbleChallengeHost.
	ChallengeHost string
	// HTTPValidationPort and TLSValidationPort are the HOST ports the applicant
	// server listens on for HTTP-01 and TLS-ALPN-01 challenges. Pebble connects to
	// ChallengeHost at these ports during validation, so they MUST equal the ports
	// the server binds on the host. Both are required.
	HTTPValidationPort int
	TLSValidationPort  int
}

// PebbleContainer wraps a running Pebble ACME test-server container.
type PebbleContainer struct {
	container    testcontainers.Container
	directoryURL string
	httpClient   *http.Client
	rootPool     *x509.CertPool
}

// pebbleConfigDoc is the on-disk Pebble configuration document.
type pebbleConfigDoc struct {
	Pebble pebbleConfigBody `json:"pebble"`
}

type pebbleConfigBody struct {
	ListenAddress           string `json:"listenAddress"`
	ManagementListenAddress string `json:"managementListenAddress"`
	Certificate             string `json:"certificate"`
	PrivateKey              string `json:"privateKey"`
	HTTPPort                int    `json:"httpPort"`
	TLSPort                 int    `json:"tlsPort"`
	OCSPResponderURL        string `json:"ocspResponderURL"`
}

// NewPebbleContainer creates and starts a Pebble ACME test-server container. The
// caller must Terminate it. HTTPValidationPort and TLSValidationPort are
// required and must match the applicant server's host listener ports.
func NewPebbleContainer(ctx context.Context, config *PebbleConfig) (*PebbleContainer, error) {
	if config == nil {
		return nil, fmt.Errorf("pebble: config is required")
	}
	if config.HTTPValidationPort == 0 || config.TLSValidationPort == 0 {
		return nil, fmt.Errorf("pebble: HTTPValidationPort and TLSValidationPort are required")
	}

	tag := config.ImageTag
	if tag == "" {
		tag = pebbleDefaultTag
	}
	challengeHost := config.ChallengeHost
	if challengeHost == "" {
		challengeHost = DefaultPebbleChallengeHost
	}

	// Point Pebble's challenge validation at the host ports the server listens on.
	// The certificate/privateKey paths are the test CA baked into the image
	// (relative to the container WORKDIR "/").
	configJSON, err := json.Marshal(pebbleConfigDoc{Pebble: pebbleConfigBody{
		ListenAddress:           "0.0.0.0:" + strings.TrimSuffix(pebbleACMEPort, "/tcp"),
		ManagementListenAddress: "0.0.0.0:" + strings.TrimSuffix(pebbleMgmtPort, "/tcp"),
		Certificate:             "test/certs/localhost/cert.pem",
		PrivateKey:              "test/certs/localhost/key.pem",
		HTTPPort:                config.HTTPValidationPort,
		TLSPort:                 config.TLSValidationPort,
		OCSPResponderURL:        "",
	}})
	if err != nil {
		return nil, fmt.Errorf("pebble: marshal config: %w", err)
	}

	req := testcontainers.ContainerRequest{
		Image:        fmt.Sprintf("%s:%s", pebbleImage, tag),
		ExposedPorts: []string{pebbleACMEPort, pebbleMgmtPort},
		Env: map[string]string{
			// Validate challenges immediately instead of the default ~5s sleep,
			// and do not randomly reject nonces: both keep the e2e fast and
			// deterministic. These are test-only accelerators.
			"PEBBLE_VA_NOSLEEP":      "1",
			"PEBBLE_WFE_NONCEREJECT": "0",
		},
		Cmd: []string{"-config", pebbleConfigPath},
		Files: []testcontainers.ContainerFile{{
			Reader:            bytes.NewReader(configJSON),
			ContainerFilePath: pebbleConfigPath,
			FileMode:          0o644,
		}},
		HostConfigModifier: func(hc *container.HostConfig) {
			// Make the ACME identifier resolve to the host so Pebble can reach the
			// applicant's challenge listeners running in the test process.
			hc.ExtraHosts = append(hc.ExtraHosts, fmt.Sprintf("%s:%s", challengeHost, pebbleHostGateway))
		},
		// Wait for BOTH the ACME and management listeners: fetchIssuerRoot hits the
		// management port (once, no retry) right after construction, so waiting only
		// for the ACME port would let a slow management listener flake construction.
		WaitingFor: wait.ForAll(
			wait.ForListeningPort(pebbleACMEPort),
			wait.ForListeningPort(pebbleMgmtPort),
		).WithDeadline(pebbleStartupTimeout),
	}

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		// GenericContainer returns a non-nil container on both create and start
		// failure (e.g. a WaitingFor timeout leaves a started container behind), so
		// terminate it to avoid a leak when the Ryuk reaper is disabled.
		if ctr != nil {
			_ = ctr.Terminate(context.Background())
		}
		return nil, fmt.Errorf("pebble: start container: %w", err)
	}

	pc, err := newPebbleFromContainer(ctx, ctr)
	if err != nil {
		_ = ctr.Terminate(context.Background())
		return nil, err
	}
	return pc, nil
}

// newPebbleFromContainer resolves the mapped endpoints, builds an HTTP client
// that trusts Pebble's self-signed API certificate, and waits for the ACME
// directory to become reachable while capturing the issuing root(s).
func newPebbleFromContainer(ctx context.Context, ctr testcontainers.Container) (*PebbleContainer, error) {
	host, err := ctr.Host(ctx)
	if err != nil {
		return nil, fmt.Errorf("pebble: container host: %w", err)
	}
	acmePort, err := ctr.MappedPort(ctx, pebbleACMEPort)
	if err != nil {
		return nil, fmt.Errorf("pebble: mapped ACME port: %w", err)
	}
	mgmtPort, err := ctr.MappedPort(ctx, pebbleMgmtPort)
	if err != nil {
		return nil, fmt.Errorf("pebble: mapped management port: %w", err)
	}

	// Pebble serves its ACME + management APIs over HTTPS with a self-signed cert
	// for "localhost", which will not match the mapped host: skip verification for
	// API calls only. The issued leaf is verified separately against rootPool.
	httpClient := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // Pebble test CA, API endpoints only
		Timeout:   15 * time.Second,
	}

	directoryURL := fmt.Sprintf("https://%s/dir", net.JoinHostPort(host, acmePort.Port()))
	mgmtRootsURL := fmt.Sprintf("https://%s/roots/0", net.JoinHostPort(host, mgmtPort.Port()))

	pc := &PebbleContainer{
		container:    ctr,
		directoryURL: directoryURL,
		httpClient:   httpClient,
	}

	if err := pc.waitDirectoryReady(ctx); err != nil {
		return nil, err
	}
	// Fetch the issuing root eagerly: it doubles as a readiness check that the
	// management API works, and exposing it (IssuerRoots) lets callers verify a
	// Pebble-issued leaf, the obvious next use of this helper.
	rootPool, err := pc.fetchIssuerRoot(ctx, mgmtRootsURL)
	if err != nil {
		return nil, err
	}
	pc.rootPool = rootPool
	return pc, nil
}

// waitDirectoryReady polls the ACME directory endpoint until it responds 200 or
// the context/deadline expires.
func (c *PebbleContainer) waitDirectoryReady(ctx context.Context) error {
	deadline := time.Now().Add(pebbleStartupTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.directoryURL, http.NoBody)
		if err != nil {
			return fmt.Errorf("pebble: build directory request: %w", err)
		}
		resp, err := c.httpClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("directory returned status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("pebble: waiting for directory: %w", ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
	return fmt.Errorf("pebble: ACME directory not ready within %s: %w", pebbleStartupTimeout, lastErr)
}

// fetchIssuerRoot retrieves Pebble's randomly-generated issuing root certificate
// and returns a pool containing it, so the test can verify served leaf certs.
func (c *PebbleContainer) fetchIssuerRoot(ctx context.Context, rootsURL string) (*x509.CertPool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rootsURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("pebble: build roots request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pebble: fetch issuing root: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pebble: fetch issuing root: status %d", resp.StatusCode)
	}
	rootPEM, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("pebble: read issuing root: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(rootPEM) {
		return nil, fmt.Errorf("pebble: issuing root PEM was not accepted")
	}
	return pool, nil
}

// DirectoryURL returns the ACME directory URL to point an ACME client at.
func (c *PebbleContainer) DirectoryURL() string { return c.directoryURL }

// APIHTTPClient returns an HTTP client that trusts Pebble's self-signed ACME and
// management API certificate. Use it as the ACME client's HTTPClient.
func (c *PebbleContainer) APIHTTPClient() *http.Client { return c.httpClient }

// IssuerRoots returns a cert pool containing Pebble's issuing root, for verifying
// certificates that Pebble issues.
func (c *PebbleContainer) IssuerRoots() *x509.CertPool { return c.rootPool }

// Pebble log markers used to confirm a challenge validated and a cert was issued.
// These are the CA's own server-side confirmation of the ACME flow. autocert
// cannot retrieve an issued cert from Pebble (Pebble omits the Location header on
// its finalize response, which x/crypto's CreateOrderCert requires; this works
// against real Let's Encrypt), so the CA-side log is the reliable success signal.
const (
	pebbleLogChallengeValidated = "set VALID by completed challenge"
	pebbleLogCertIssued         = "Issued certificate serial"
)

// WaitForIssuedCertificate blocks until Pebble's logs show that it validated a
// challenge against the applicant's listeners and issued a certificate, or the
// timeout elapses. Because a fresh Pebble instance only ever handles the single
// host the caller requests, an issued certificate necessarily belongs to that
// host. It proves the applicant's HTTP-01 / TLS-ALPN-01 challenge listeners
// answered the CA end-to-end. On timeout it returns an error that includes the
// captured logs for diagnosis.
func (c *PebbleContainer) WaitForIssuedCertificate(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastLogs string
	var lastErr error
	for time.Now().Before(deadline) {
		logs, err := c.Logs(ctx)
		if err != nil {
			lastErr = err
		} else {
			lastLogs = logs
			if strings.Contains(logs, pebbleLogChallengeValidated) &&
				strings.Contains(logs, pebbleLogCertIssued) {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("pebble: waiting for issuance: %w", ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
	// Surface the last log-read error too: without it a persistent Logs() failure
	// would print empty logs and hide why detection never succeeded. Stringify it
	// (rather than %w) so a nil error reads cleanly and it does not join the chain.
	lastErrText := "none"
	if lastErr != nil {
		lastErrText = lastErr.Error()
	}
	return fmt.Errorf("pebble: no certificate issued within %s (last log-read error: %s); logs:\n%s",
		timeout, lastErrText, lastLogs)
}

// Logs returns the Pebble container's stdout/stderr, which record ACME order and
// challenge-validation attempts. Useful for diagnosing issuance failures in CI.
func (c *PebbleContainer) Logs(ctx context.Context) (string, error) {
	rc, err := c.container.Logs(ctx)
	if err != nil {
		return "", fmt.Errorf("pebble: read logs: %w", err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		return "", fmt.Errorf("pebble: read logs: %w", err)
	}
	return string(b), nil
}

// Terminate stops and removes the Pebble container.
func (c *PebbleContainer) Terminate(ctx context.Context) error {
	if err := c.container.Terminate(ctx); err != nil {
		return fmt.Errorf("pebble: terminate container: %w", err)
	}
	return nil
}
