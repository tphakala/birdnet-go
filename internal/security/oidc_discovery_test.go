package security

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/markbates/goth"
	"github.com/markbates/goth/providers/github"
	gothGoogle "github.com/markbates/goth/providers/google"
	"github.com/markbates/goth/providers/openidConnect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/testutil"
)

const (
	testOIDCClientSecret = "oidc-client-secret-must-not-be-logged"
	testOIDCRedirectURI  = "http://localhost/auth/openid-connect/callback"
)

// splitLogReaders reads the two files a split-log test logger writes.
type splitLogReaders struct {
	main     func() string
	security func() string
}

// useSplitLogFiles installs a global logger that writes the base handler (the
// stand-in for console and main log) to application.log and the security module
// to its own security.log, so a test can tell which of the two an event reached.
// logtest.Capture* cannot be used: it disables every module output, which puts
// both loggers in one buffer and hides the routing. Not for parallel tests.
func useSplitLogFiles(t *testing.T) splitLogReaders {
	t.Helper()
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "application.log")
	securityPath := filepath.Join(dir, "security.log")

	outputs := logger.DisabledModuleOutputs()
	outputs["security"] = logger.ModuleOutput{Enabled: true, FilePath: securityPath, Level: "info"}
	cl, err := logger.NewCentralLogger(&logger.LoggingConfig{
		DefaultLevel:  "info",
		Timezone:      "UTC",
		Console:       &logger.ConsoleOutput{Enabled: false},
		FileOutput:    &logger.FileOutput{Enabled: true, Path: mainPath, Level: "info"},
		ModuleOutputs: outputs,
	})
	require.NoError(t, err)
	prev := logger.Global()
	logger.SetGlobal(cl)
	t.Cleanup(func() {
		logger.SetGlobal(prev)
		_ = cl.Close()
	})

	read := func(path string) func() string {
		return func() string {
			require.NoError(t, cl.Flush())
			data, err := os.ReadFile(path) //nolint:gosec // test-controlled path in t.TempDir
			if os.IsNotExist(err) {
				return ""
			}
			require.NoError(t, err)
			return string(data)
		}
	}
	return splitLogReaders{main: read(mainPath), security: read(securityPath)}
}

func TestOIDCDiscoveryFailure_ReachesMainLogAndIsRecorded(t *testing.T) {
	logs := useSplitLogFiles(t)
	idp := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(idp.Close)

	registerProviders(t, &conf.Settings{
		Security: conf.Security{
			Host:            "localhost",
			SessionSecret:   "test-secret-that-is-long-enough-32chars",
			SessionDuration: 24 * time.Hour,
			OAuthProviders: []conf.OAuthProviderConfig{{
				Provider:     ConfigOIDC,
				Enabled:      true,
				ClientID:     "test-client",
				ClientSecret: testOIDCClientSecret,
				IssuerURL:    idp.URL,
			}},
		},
	})

	discoveryURL := idp.URL + "/.well-known/openid-configuration"
	requireOIDCFailureRecorded(t)
	requireOIDCFailureLogged(t, logs)
	mainLog, securityLog := logs.main(), logs.security()
	for name, content := range map[string]string{"main log": mainLog, "security log": securityLog} {
		assert.Contains(t, content, "OIDC discovery failed", name)
		assert.Contains(t, content, discoveryURL, name)
		assert.Contains(t, content, "404", name)
		assert.NotContains(t, content, testOIDCClientSecret, name)
	}
	assert.Contains(t, mainLog, "security.oidc", "the mirrored event must name its module")
	assert.Contains(t, mainLog, "ERROR")

	err := ProviderRegistrationError(ProviderOIDC)
	require.Error(t, err)
	assert.Contains(t, err.Error(), discoveryURL)
	assert.Contains(t, err.Error(), "404")
}

// TestOIDCDiscoveryFailure_RedactsURLUserinfo covers the two shapes of a URL in a
// net/http error: a transport failure masks the password but keeps the username,
// and a URL that does not parse is printed whole.
func TestOIDCDiscoveryFailure_RedactsURLUserinfo(t *testing.T) {
	const username = "tokenuser-must-not-be-logged"
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()

	tests := []struct {
		name     string
		issuer   string
		wantText string
	}{
		{name: "transport failure", issuer: strings.Replace(closedURL, "http://", "http://"+username+":secretpw@", 1), wantText: "connection refused"},
		{name: "unparseable URL", issuer: "http://" + username + ":secretpw@localhost:12x", wantText: errOIDCDiscoveryURLInvalid.Error()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := useSplitLogFiles(t)
			registerProviders(t, &conf.Settings{
				Security: conf.Security{
					Host:            "localhost",
					SessionSecret:   "test-secret-that-is-long-enough-32chars",
					SessionDuration: 24 * time.Hour,
					OAuthProviders: []conf.OAuthProviderConfig{{
						Provider:     ConfigOIDC,
						Enabled:      true,
						ClientID:     "test-client",
						ClientSecret: testOIDCClientSecret,
						IssuerURL:    tt.issuer,
					}},
				},
			})

			requireOIDCFailureRecorded(t)
			requireOIDCFailureLogged(t, logs)
			err := ProviderRegistrationError(ProviderOIDC)
			var urlErr *url.Error
			require.ErrorAs(t, err, &urlErr, "the redacted text must still unwrap to the net/http error")
			for name, content := range map[string]string{
				"main log":     logs.main(),
				"security log": logs.security(),
				"recorded":     err.Error(),
			} {
				assert.Contains(t, content, tt.wantText, name)
				assert.NotContains(t, content, username, name)
				assert.NotContains(t, content, "secretpw", name)
			}
		})
	}
}

func TestOIDCRetryExhausted_ReachesMainLogAndNamesBothRemedies(t *testing.T) {
	logs := useSplitLogFiles(t)

	const issuer = "https://idp.example.com/realms/home"
	logOIDCRetryExhausted(issuer, oidcDiscoveryURL(issuer), errors.NewStd("last discovery error text"), time.Minute)

	mainLog, securityLog := logs.main(), logs.security()
	for name, content := range map[string]string{"main log": mainLog, "security log": securityLog} {
		assert.Contains(t, content, issuer, name)
		assert.Contains(t, content, issuer+"/.well-known/openid-configuration", name)
		assert.Contains(t, content, "last discovery error text", name)
		assert.Contains(t, content, "correct it in the authentication settings and save", name)
		assert.Contains(t, content, "restart BirdNET-Go", name)
	}
	assert.Contains(t, mainLog, "ERROR")
	assert.Contains(t, mainLog, "security.oidc")
}

func TestProviderRegistrationError_Transitions(t *testing.T) {
	const discoveryURL = "https://idp.example/.well-known/openid-configuration"
	cause := errors.NewStd("Non-success code for Discovery URL: 404")
	newerCause := errors.NewStd("dial tcp: no such host")

	registerOIDC := func() {
		idp := mockOIDCDiscovery(t)
		op, err := openidConnect.New("id", "secret", testOIDCRedirectURI, idp.URL+"/.well-known/openid-configuration", "openid")
		require.NoError(t, err)
		goth.UseProviders(newPKCEOIDCProvider(op))
	}

	tests := []struct {
		name       string
		steps      func()
		query      string
		wantErr    error    // the returned error must wrap this; nil means the provider is registered and no error is expected
		notErr     error    // the returned error must not wrap this
		wantText   string   // substring of the error text
		wantNoText []string // substrings that must not appear in the error text
	}{
		{
			name:    "nothing recorded, nothing registered",
			steps:   func() {},
			query:   ProviderOIDC,
			wantErr: errProviderNotRegistered,
		},
		{
			name:     "recorded failure wraps the cause and names the URL",
			steps:    func() { recordOIDCDiscoveryFailure(discoveryURL, cause) },
			query:    ProviderOIDC,
			wantErr:  cause,
			wantText: discoveryURL,
		},
		{
			name: "userinfo in the discovery URL is redacted",
			steps: func() {
				recordOIDCDiscoveryFailure("https://user:secretpw@idp.example/.well-known/openid-configuration", cause)
			},
			query:      ProviderOIDC,
			wantErr:    cause,
			wantNoText: []string{"secretpw", "user"},
		},
		{
			name: "later failure replaces the earlier one",
			steps: func() {
				recordOIDCDiscoveryFailure(discoveryURL, cause)
				recordOIDCDiscoveryFailure(discoveryURL, newerCause)
			},
			query:   ProviderOIDC,
			wantErr: newerCause,
			notErr:  cause,
		},
		{
			name: "registered provider is never reported as failed",
			steps: func() {
				recordOIDCDiscoveryFailure(discoveryURL, cause)
				registerOIDC()
			},
			query: ProviderOIDC,
		},
		{
			name:    "OIDC failure is not attributed to another provider",
			steps:   func() { recordOIDCDiscoveryFailure(discoveryURL, cause) },
			query:   ProviderGoogle,
			wantErr: errProviderNotRegistered,
			notErr:  cause,
		},
		{
			name: "cleared failure reports not registered",
			steps: func() {
				recordOIDCDiscoveryFailure(discoveryURL, cause)
				clearOIDCDiscoveryFailure()
			},
			query:   ProviderOIDC,
			wantErr: errProviderNotRegistered,
		},
		{
			name:  "registered non-OIDC provider",
			steps: func() { goth.UseProviders(github.New("id", "secret", "http://localhost/cb")) },
			query: ProviderGitHub,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			goth.ClearProviders()
			clearOIDCDiscoveryFailure()
			t.Cleanup(goth.ClearProviders)
			t.Cleanup(clearOIDCDiscoveryFailure)
			tt.steps()

			err := ProviderRegistrationError(tt.query)
			if tt.wantErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tt.wantErr)
			if tt.notErr != nil {
				require.NotErrorIs(t, err, tt.notErr)
			}
			if tt.wantText != "" {
				assert.Contains(t, err.Error(), tt.wantText)
			}
			for _, absent := range tt.wantNoText {
				assert.NotContains(t, err.Error(), absent)
			}
		})
	}
}

func TestOIDCDiscoveryURL(t *testing.T) {
	tests := []struct{ name, issuer, want string }{
		{name: "without trailing slash", issuer: "https://idp.example/realms/home", want: "https://idp.example/realms/home/.well-known/openid-configuration"},
		{name: "with trailing slash", issuer: "https://idp.example/application/o/app/", want: "https://idp.example/application/o/app/.well-known/openid-configuration"},
		{name: "pasted discovery URL", issuer: "https://idp.example/realms/home/.well-known/openid-configuration", want: "https://idp.example/realms/home/.well-known/openid-configuration"},
		{name: "pasted discovery URL with trailing slash", issuer: "https://idp.example/realms/home/.well-known/openid-configuration/", want: "https://idp.example/realms/home/.well-known/openid-configuration"},
		{name: "pasted discovery URL of a root issuer", issuer: "https://idp.example/.well-known/openid-configuration", want: "https://idp.example/.well-known/openid-configuration"},
		{name: "well-known in the middle of the path", issuer: "https://idp.example/.well-known/realms/home", want: "https://idp.example/.well-known/realms/home/.well-known/openid-configuration"},
		{name: "pasted discovery URL with several trailing slashes", issuer: "https://idp.example/realms/home/.well-known/openid-configuration//", want: "https://idp.example/realms/home/.well-known/openid-configuration"},
		{name: "pasted discovery URL with a query", issuer: "https://login.example/tenant/v2.0/.well-known/openid-configuration?appid=abc", want: "https://login.example/tenant/v2.0/.well-known/openid-configuration?appid=abc"},
		{name: "issuer with a query", issuer: "https://login.example/tenant/v2.0/?appid=abc", want: "https://login.example/tenant/v2.0/.well-known/openid-configuration?appid=abc"},
		{name: "fragment is dropped", issuer: "https://idp.example/realms/home#section", want: "https://idp.example/realms/home/.well-known/openid-configuration"},
		{name: "empty fragment is dropped", issuer: "https://idp.example/realms/home#", want: "https://idp.example/realms/home/.well-known/openid-configuration"},
		{name: "empty query is dropped", issuer: "https://idp.example/realms/home?", want: "https://idp.example/realms/home/.well-known/openid-configuration"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := oidcDiscoveryURL(tt.issuer)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, got, oidcDiscoveryURL(got), "the builder must not change its own output")
		})
	}
}

func TestRedactDiscoveryErr(t *testing.T) {
	parseErr := func(raw string) error {
		_, err := url.Parse(raw)
		require.Error(t, err)
		return err
	}
	const pathlessURL = "https://idp.example?login_hint=a@b.example/.well-known/openid-configuration"

	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "masked password keeps the username", err: errors.NewStd(`Get "https://tok:***@idp.example/x": refused`), want: `Get "https://[REDACTED]@idp.example/x": refused`},
		{name: "raw at sign inside the password", err: errors.NewStd(`dial http://tok:pw@Zq7@idp.example:12x/x refused`), want: `dial http://[REDACTED]@idp.example:12x/x refused`},
		{name: "nested parse failure is replaced by a fixed sentence", err: errors.NewStd(`Get "http://idp.example/x": failed to parse Location header "https://a:p/s@h": parse "https://a:p/s@h": invalid port ":p" after host`), want: errOIDCDiscoveryURLInvalid.Error()},
		{name: "no userinfo passes through", err: errors.NewStd("Non-success code for Discovery URL: 404"), want: "Non-success code for Discovery URL: 404"},
		{
			name: "transport url.Error with a username",
			err:  &url.Error{Op: "Get", URL: "https://tok:***@idp.example/.well-known/openid-configuration", Err: errors.NewStd("dial tcp: connection refused")},
			want: `Get "https://[REDACTED]@idp.example/.well-known/openid-configuration": dial tcp: connection refused`,
		},
		{
			name: "parse url.Error is replaced by a fixed sentence",
			err:  parseErr("http://user:pa#ss@host:12x/x"),
			want: errOIDCDiscoveryURLInvalid.Error(),
		},
		{
			name: "url.Error with a pathless URL keeps the host",
			err:  &url.Error{Op: "Get", URL: pathlessURL, Err: errors.NewStd("refused")},
			want: `Get "` + pathlessURL + `": refused`,
		},
		{
			name: "transport url.Error with an opaque URL",
			err:  &url.Error{Op: "Get", URL: "https:tok:pw@idp.example/x", Err: errors.NewStd("unsupported protocol scheme")},
			want: `Get "[REDACTED]@idp.example/x": unsupported protocol scheme`,
		},
		{name: "nil cause", err: nil, want: errOIDCDiscoveryCauseUnknown.Error()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactDiscoveryErr(tt.err)
			require.Error(t, got)
			assert.Equal(t, tt.want, got.Error())
			if tt.err != nil {
				require.ErrorIs(t, got, tt.err)
			}
			assert.Equal(t, got.Error(), redactDiscoveryErr(got).Error(), "redaction must be idempotent")
		})
	}
}

// requireOIDCFailureRecorded waits for the background discovery to record a failure.
func requireOIDCFailureRecorded(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool {
		err := ProviderRegistrationError(ProviderOIDC)
		return err != nil && !errors.Is(err, errProviderNotRegistered)
	}, 10*time.Second, 20*time.Millisecond, "a failed discovery attempt must record its cause")
}

// requireOIDCFailureLogged waits for the first discovery failure to reach both
// logs: the event is written after the failure is recorded, so a read taken as
// soon as the record appears can miss it.
func requireOIDCFailureLogged(t *testing.T, logs splitLogReaders) {
	t.Helper()
	require.Eventually(t, func() bool {
		return strings.Contains(logs.main(), "OIDC discovery failed") && strings.Contains(logs.security(), "OIDC discovery failed")
	}, 10*time.Second, 20*time.Millisecond, "the discovery failure must reach the main and the security log")
}

// oidcSettings returns settings with one OIDC provider for the given issuer.
func oidcSettings(issuerURL string) *conf.Settings {
	return &conf.Settings{Security: conf.Security{
		Host: "localhost",
		OAuthProviders: []conf.OAuthProviderConfig{{
			Provider: ConfigOIDC, Enabled: true, ClientID: "test-client", ClientSecret: testOIDCClientSecret, IssuerURL: issuerURL,
		}},
	}}
}

// TestOIDCDiscoveryFailure_NilCause pins that a missing cause is recorded and
// logged as a sentinel instead of panicking or logging an empty cause.
func TestOIDCDiscoveryFailure_NilCause(t *testing.T) {
	logs := useSplitLogFiles(t)
	goth.ClearProviders()
	t.Cleanup(goth.ClearProviders)
	t.Cleanup(clearOIDCDiscoveryFailure)

	const issuer = "https://idp.example"
	require.NotPanics(t, func() { recordOIDCDiscoveryFailure(oidcDiscoveryURL(issuer), nil) })
	require.ErrorIs(t, ProviderRegistrationError(ProviderOIDC), errOIDCDiscoveryCauseUnknown)

	logOIDCRetryExhausted(issuer, oidcDiscoveryURL(issuer), nil, time.Minute)
	assert.Contains(t, logs.main(), errOIDCDiscoveryCauseUnknown.Error())
	assert.Contains(t, logs.security(), errOIDCDiscoveryCauseUnknown.Error())
}

// TestOIDCDiscoveryFailure_RedactsRedirectLocation covers a discovery server that
// redirects to a Location net/http cannot parse: the client error quotes both
// URLs as text, and the inner parse error is not part of the error chain.
func TestOIDCDiscoveryFailure_RedactsRedirectLocation(t *testing.T) {
	logs := useSplitLogFiles(t)
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://alice:pa/ss@idp.example", http.StatusFound)
	}))
	t.Cleanup(idp.Close)

	registerProviders(t, oidcSettings(idp.URL))

	requireOIDCFailureRecorded(t)
	requireOIDCFailureLogged(t, logs)
	err := ProviderRegistrationError(ProviderOIDC)
	require.Error(t, err)
	for name, content := range map[string]string{
		"main log":     logs.main(),
		"security log": logs.security(),
		"recorded":     err.Error(),
	} {
		assert.Contains(t, content, errOIDCDiscoveryURLInvalid.Error(), name)
		assert.NotContains(t, content, "pa/ss", name)
		assert.NotContains(t, content, ":pa", name)
		assert.NotContains(t, content, "alice", name)
	}
}

// TestOIDCRetryExhausted_RedactsUserinfoInTheCause pins that the exhaustion log
// redacts a cause that still carries a username, as net/http leaves it in a
// transport error, whatever the caller passed.
func TestOIDCRetryExhausted_RedactsUserinfoInTheCause(t *testing.T) {
	logs := useSplitLogFiles(t)

	const username = "tokenuser-must-not-be-logged"
	const issuer = "https://idp.example.com"
	cause := &url.Error{
		Op:  "Get",
		URL: "https://" + username + ":***@idp.example.com/.well-known/openid-configuration",
		Err: errors.NewStd("dial tcp: connection refused"),
	}
	logOIDCRetryExhausted(issuer, oidcDiscoveryURL(issuer), cause, time.Minute)

	for name, content := range map[string]string{"main log": logs.main(), "security log": logs.security()} {
		assert.Contains(t, content, "connection refused", name)
		assert.NotContains(t, content, username, name)
	}
}

// TestStartOIDCDiscovery_RedactsItsLogsAndReportsExhaustion runs the discovery
// loop to the end of its window under a fake clock. net/http keeps the username
// in the text of a transport error, so a loop that stored or logged the raw
// error would leak it, and the exhaustion branch must report the last cause.
func TestStartOIDCDiscovery_RedactsItsLogsAndReportsExhaustion(t *testing.T) {
	const (
		username = "tokenuser-must-not-be-logged"
		password = "secretpw-must-not-be-logged"
	)
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()
	issuer := strings.Replace(closedURL, "http://", "http://"+username+":"+password+"@", 1)

	logs := useSplitLogFiles(t)

	var recorded error
	synctest.Test(t, func(t *testing.T) {
		// The context is created inside the bubble so the loop's wait on it is
		// durably blocked and the fake clock can advance.
		ctx, gen := beginDiscoveryForTest(t)
		done := startOIDCDiscovery(ctx, gen, testOIDCRegistration(issuer), defaultOIDCRetryPolicy())
		<-done
		// Read the recorded failure before the bubble's cleanup resets it.
		recorded = ProviderRegistrationError(ProviderOIDC)
	})

	mainLog, securityLog := logs.main(), logs.security()
	assert.Contains(t, securityLog, "Retrying OIDC discovery")
	assert.Contains(t, securityLog, "OIDC discovery retry failed")
	require.Error(t, recorded)
	require.NotErrorIs(t, recorded, errProviderNotRegistered, "the last failure must be the recorded cause")
	assert.Contains(t, recorded.Error(), "connection refused")
	for name, content := range map[string]string{"main log": mainLog, "security log": securityLog} {
		assert.Contains(t, content, "OIDC discovery retries exhausted", name)
		assert.Contains(t, content, "connection refused", name)
		assert.NotContains(t, content, username, name)
		assert.NotContains(t, content, password, name)
	}
	assert.NotContains(t, recorded.Error(), username)
	assert.NotContains(t, recorded.Error(), password)
}

// TestStartOIDCDiscovery_RecordsEachFailure pins that the discovery loop records
// every failed attempt, not only the first: the first attempt answers 503 and
// every later one 502, so the recorded cause must change to the later status.
func TestStartOIDCDiscovery_RecordsEachFailure(t *testing.T) {
	var requests atomic.Int32
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		status := http.StatusBadGateway
		if requests.Add(1) == 1 {
			status = http.StatusServiceUnavailable
		}
		http.Error(w, "not ready", status)
	}))
	t.Cleanup(idp.Close)

	ctx, gen := beginDiscoveryForTest(t)
	ctx, cancel := context.WithCancel(ctx)
	done := startOIDCDiscovery(ctx, gen, testOIDCRegistration(idp.URL), testOIDCPolicy)
	t.Cleanup(func() {
		cancel()
		<-done
	})

	require.Eventually(t, func() bool {
		err := ProviderRegistrationError(ProviderOIDC)
		return err != nil && strings.Contains(err.Error(), "Discovery URL: 502")
	}, 10*time.Second, 10*time.Millisecond, "a failed retry attempt must replace the recorded cause")
	assert.Contains(t, ProviderRegistrationError(ProviderOIDC).Error(), idp.URL+"/.well-known/openid-configuration")
}

func TestOIDCDiscovery_ExhaustionLogsAndKeepsFailure(t *testing.T) {
	logs := useSplitLogFiles(t)
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	}))
	t.Cleanup(idp.Close)

	ctx, gen := beginDiscoveryForTest(t)
	done := startOIDCDiscovery(ctx, gen, testOIDCRegistration(idp.URL), oidcRetryPolicy{
		initialBackoff: 5 * time.Millisecond,
		maxBackoff:     10 * time.Millisecond,
		maxDuration:    50 * time.Millisecond,
	})

	testutil.WaitForChannel(t, done, testutil.LongTestTimeout, "OIDC discovery did not stop after its retry window")
	assert.Contains(t, logs.main(), "OIDC discovery retries exhausted")
	assert.Contains(t, logs.main(), `"retried_for":"50ms"`, "the log must carry the retry window of the policy")
	err := ProviderRegistrationError(ProviderOIDC)
	require.Error(t, err)
	require.NotErrorIs(t, err, errProviderNotRegistered, "the last failure must stay recorded after exhaustion")
	assert.Contains(t, err.Error(), "503")
}

// hangingIDP accepts the connection, reads the request and never answers it,
// until the client gives up or the test ends.
func hangingIDP(t *testing.T, entered chan<- struct{}) *httptest.Server {
	t.Helper()
	gate := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-gate:
		case <-r.Context().Done():
		}
	}))
	// Handlers must be released before Close waits for them.
	t.Cleanup(server.Close)
	t.Cleanup(func() { once.Do(func() { close(gate) }) })
	return server
}

// TestOIDCDiscovery_CancelStopsInFlightRequest pins that cancelling a
// generation ends a discovery request that the identity provider never answers,
// long before the client timeout, and that the cancellation is not reported as
// a discovery failure.
func TestOIDCDiscovery_CancelStopsInFlightRequest(t *testing.T) {
	logs := useSplitLogFiles(t)
	entered := make(chan struct{}, 1)
	idp := hangingIDP(t, entered)

	done := registerProviders(t, oidcSettings(idp.URL))
	testutil.WaitForChannel(t, entered, testutil.DefaultTestTimeout, "the discovery request never reached the identity provider")

	// A pass that disables OIDC supersedes the generation.
	registerProviders(t, &conf.Settings{})
	testutil.WaitForChannel(t, done, 2*time.Second, "cancelling the generation did not stop the request in flight")

	assert.NotContains(t, logs.main(), "OIDC discovery failed")
	assert.ErrorIs(t, ProviderRegistrationError(ProviderOIDC), errProviderNotRegistered,
		"a cancelled attempt must not record a failure")
}

// TestOIDCDiscovery_WindowDeadlineStopsInFlightRequest pins that the end of the
// retry window cuts a request in flight, reports the exhaustion once and keeps
// the earlier real cause instead of "context deadline exceeded".
func TestOIDCDiscovery_WindowDeadlineStopsInFlightRequest(t *testing.T) {
	logs := useSplitLogFiles(t)
	var requests atomic.Int32
	gate := make(chan struct{})
	var once sync.Once
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		select {
		case <-gate:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(idp.Close)
	t.Cleanup(func() { once.Do(func() { close(gate) }) })

	ctx, gen := beginDiscoveryForTest(t)
	start := time.Now()
	done := startOIDCDiscovery(ctx, gen, testOIDCRegistration(idp.URL), oidcRetryPolicy{
		initialBackoff: 5 * time.Millisecond,
		maxBackoff:     10 * time.Millisecond,
		maxDuration:    300 * time.Millisecond,
	})

	testutil.WaitForChannel(t, done, 2*time.Second, "the retry window did not stop the request in flight")
	assert.Less(t, time.Since(start), OIDCHTTPTimeout/2, "the window, not the client timeout, must end the attempt")
	assert.Equal(t, 1, strings.Count(logs.main(), "OIDC discovery retries exhausted"))
	err := ProviderRegistrationError(ProviderOIDC)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "503")
	assert.NotContains(t, err.Error(), "deadline exceeded")
}

// TestOIDCDiscovery_WindowEndsDuringFirstAttempt pins that when the window ends
// while the first attempt is still in flight, the cut request is recorded as the
// cause, since no real one exists, and the exhaustion is reported.
func TestOIDCDiscovery_WindowEndsDuringFirstAttempt(t *testing.T) {
	logs := useSplitLogFiles(t)
	idp := hangingIDP(t, nil)

	ctx, gen := beginDiscoveryForTest(t)
	done := startOIDCDiscovery(ctx, gen, testOIDCRegistration(idp.URL), oidcRetryPolicy{
		initialBackoff: 5 * time.Millisecond,
		maxBackoff:     10 * time.Millisecond,
		maxDuration:    100 * time.Millisecond,
	})

	testutil.WaitForChannel(t, done, 2*time.Second, "the retry window did not stop the first attempt")
	assert.Contains(t, logs.main(), "OIDC discovery retries exhausted")
	err := ProviderRegistrationError(ProviderOIDC)
	require.Error(t, err)
	require.NotErrorIs(t, err, errProviderNotRegistered, "the cut first attempt must leave a recorded cause")
}

// TestDiscoverOIDC_UsesBoundedClient pins that discovery goes through
// oidcHTTPClient: a provider that never answers is given up on when the client
// timeout passes, with no context deadline involved.
func TestDiscoverOIDC_UsesBoundedClient(t *testing.T) {
	idp := hangingIDP(t, nil)
	original := oidcHTTPClient
	oidcHTTPClient = &http.Client{Timeout: 200 * time.Millisecond}
	t.Cleanup(func() { oidcHTTPClient = original })

	finished := make(chan error, 1)
	go func() {
		_, err := discoverOIDC(t.Context(), testOIDCRegistration(idp.URL))
		finished <- err
	}()
	select {
	case err := <-finished:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("discovery was not bounded by the client timeout")
	}
}

// gatedIDP is an identity provider whose discovery document request blocks until
// release is called, then answers with a valid document or a 503.
type gatedIDP struct {
	*httptest.Server
	entered chan struct{}
	release func()
}

func newGatedIDP(t *testing.T, succeed bool) *gatedIDP {
	t.Helper()
	entered := make(chan struct{}, 4)
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		select {
		case <-gate:
		case <-r.Context().Done():
			return
		}
		if !succeed {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		writeOIDCDiscovery(w, r)
	}))
	// Registered after the server so it runs first: handlers must be released
	// before Close waits for them.
	t.Cleanup(server.Close)
	t.Cleanup(release)
	return &gatedIDP{Server: server, entered: entered, release: release}
}

// registerProviders runs a registration pass for settings and releases the gate
// in a cleanup that runs before the pass's own, so a test that fails before
// releasing cannot leave resetProviders waiting on a blocked attempt.
func (g *gatedIDP) registerProviders(t *testing.T, settings *conf.Settings) <-chan struct{} {
	t.Helper()
	done := registerProviders(t, settings)
	t.Cleanup(g.release)
	return done
}

func (g *gatedIDP) waitEntered(t *testing.T) {
	t.Helper()
	testutil.WaitForChannel(t, g.entered, testutil.DefaultTestTimeout, "the discovery request never reached the identity provider")
}

func waitDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	testutil.WaitForChannel(t, done, testutil.DefaultTestTimeout, "OIDC discovery goroutine did not exit")
}

// TestOIDCDiscovery_SupersededAttemptWritesNothing pins that an attempt of an
// older registration generation commits nothing, whatever it returns and
// whenever it returns.
func TestOIDCDiscovery_SupersededAttemptWritesNothing(t *testing.T) {
	tests := []struct {
		name         string
		lateSucceeds bool
	}{
		{name: "late success", lateSucceeds: true},
		{name: "late failure", lateSucceeds: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stale := newGatedIDP(t, tt.lateSucceeds)
			current := mockOIDCDiscovery(t)

			done1 := stale.registerProviders(t, oidcSettings(stale.URL))
			stale.waitEntered(t)

			// Pass 2 switches to a working issuer while attempt 1 is in flight.
			waitDone(t, registerProviders(t, oidcSettings(current.URL)))
			stale.release()
			waitDone(t, done1)

			provider, err := goth.GetProvider(ProviderOIDC)
			require.NoError(t, err)
			pkce, ok := provider.(*pkceOIDCProvider)
			require.True(t, ok)
			assert.Equal(t, current.URL+"/authorize", pkce.OpenIDConfig.AuthEndpoint,
				"the late result of the superseded attempt must not replace the current provider")
			assert.NoError(t, ProviderRegistrationError(ProviderOIDC),
				"the late result of the superseded attempt must not record a failure")
		})
	}

	t.Run("disabled while in flight", func(t *testing.T) {
		stale := newGatedIDP(t, true)
		done := stale.registerProviders(t, oidcSettings(stale.URL))
		stale.waitEntered(t)

		// Pass 3 disables OIDC.
		registerProviders(t, &conf.Settings{})
		stale.release()
		waitDone(t, done)

		_, err := goth.GetProvider(ProviderOIDC)
		require.Error(t, err, "a disabled provider must not be registered by a late attempt")
		assert.ErrorIs(t, ProviderRegistrationError(ProviderOIDC), errProviderNotRegistered)
	})
}

// TestFinishOIDCDiscovery_StaleGenerationWritesNothing pins the generation check
// itself: the goroutine's own context check cannot close the window between the
// end of an attempt and its commit, so the commit must refuse a stale generation.
func TestFinishOIDCDiscovery_StaleGenerationWritesNothing(t *testing.T) {
	idp := mockOIDCDiscovery(t)
	reg := testOIDCRegistration(idp.URL)
	provider, err := discoverOIDC(t.Context(), reg)
	require.NoError(t, err)

	tests := []struct {
		name string
		err  error
	}{
		{name: "late success", err: nil},
		{name: "late failure", err: errors.NewStd("late failure")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stale := beginDiscoveryForTest(t)
			_, current, _ := beginProviderRegistration(nil, testOIDCRegistration("http://other.test"))
			require.NotEqual(t, stale, current)

			p := provider
			if tt.err != nil {
				p = nil
			}
			assert.False(t, finishOIDCDiscovery(stale, reg, p, tt.err), "a stale generation must be refused")
			_, getErr := goth.GetProvider(ProviderOIDC)
			require.Error(t, getErr, "a stale commit must not register the provider")
			require.ErrorIs(t, ProviderRegistrationError(ProviderOIDC), errProviderNotRegistered, "a stale commit must not record a failure")

			assert.True(t, finishOIDCDiscovery(current, reg, p, tt.err), "the current generation must commit")
		})
	}
}

// oidcAndGoogleSettings returns settings with an OIDC provider for issuerURL and
// a Google provider with the given client secret.
func oidcAndGoogleSettings(issuerURL, googleSecret string) *conf.Settings {
	s := oidcSettings(issuerURL)
	s.Security.OAuthProviders = append(s.Security.OAuthProviders, conf.OAuthProviderConfig{
		Provider: ConfigGoogle, Enabled: true, ClientID: "g-id", ClientSecret: googleSecret,
	})
	return s
}

// TestReloadOAuthProviders_UnrelatedEditKeepsRegisteredOIDC pins that editing
// another provider carries the registered OIDC provider into the new registry:
// OIDC login is never unavailable and discovery is not repeated. A change of an
// OIDC setting still starts discovery again.
func TestReloadOAuthProviders_UnrelatedEditKeepsRegisteredOIDC(t *testing.T) {
	var requests atomic.Int32
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		writeOIDCDiscovery(w, r)
	}))
	t.Cleanup(idp.Close)

	waitDone(t, registerProviders(t, oidcAndGoogleSettings(idp.URL, "s1")))
	before, err := goth.GetProvider(ProviderOIDC)
	require.NoError(t, err)
	require.Equal(t, int32(1), requests.Load())

	done := initializeProviders(oidcAndGoogleSettings(idp.URL, "s2"))
	assert.Nil(t, done, "an unrelated edit must not start discovery")
	after, err := goth.GetProvider(ProviderOIDC)
	require.NoError(t, err, "OIDC must stay registered through an unrelated edit")
	assert.Same(t, before, after, "the registered OIDC provider must be carried over")
	google, err := goth.GetProvider(ProviderGoogle)
	require.NoError(t, err)
	assert.Equal(t, "s2", google.(*gothGoogle.Provider).Secret, "the edited provider must be rebuilt")
	assert.Equal(t, int32(1), requests.Load(), "discovery must not run again")

	// A change of an OIDC setting is not carried over.
	changed := oidcAndGoogleSettings(idp.URL, "s2")
	changed.Security.OAuthProviders[0].ClientSecret = "rotated"
	done = initializeProviders(changed)
	require.NotNil(t, done, "an OIDC change must start discovery")
	waitDone(t, done)
	assert.Equal(t, int32(2), requests.Load())
}

// TestReloadOAuthProviders_UnrelatedEditKeepsRunningDiscovery pins that a pass
// with unchanged OIDC settings leaves a discovery that is still running alone.
func TestReloadOAuthProviders_UnrelatedEditKeepsRunningDiscovery(t *testing.T) {
	idp := newGatedIDP(t, true)
	done := idp.registerProviders(t, oidcAndGoogleSettings(idp.URL, "s1"))
	idp.waitEntered(t)

	assert.Nil(t, initializeProviders(oidcAndGoogleSettings(idp.URL, "s2")), "the running discovery must be left running")
	idp.release()
	waitDone(t, done)

	_, err := goth.GetProvider(ProviderOIDC)
	require.NoError(t, err, "the original discovery must still register the provider")
	_, err = goth.GetProvider(ProviderGoogle)
	require.NoError(t, err)
}

// TestInitializeProviders_RestartsExhaustedDiscovery pins that a discovery that
// has ended without a provider (retry window over) is started again by a pass
// with the same OIDC settings, rather than treated as still running.
func TestInitializeProviders_RestartsExhaustedDiscovery(t *testing.T) {
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	}))
	t.Cleanup(idp.Close)

	// Run a real discovery to exhaustion for the registration of a pass.
	reg := testOIDCRegistration(idp.URL)
	enableOIDCRetries()
	ctx, gen, start := beginProviderRegistration(nil, reg)
	require.True(t, start)
	t.Cleanup(func() { resetProviders(t, nil) })
	waitDone(t, startOIDCDiscovery(ctx, gen, reg, oidcRetryPolicy{
		initialBackoff: time.Millisecond,
		maxBackoff:     time.Millisecond,
		maxDuration:    10 * time.Millisecond,
	}))

	// The same settings again must start a new discovery, not wait for the ended one.
	_, next, start := beginProviderRegistration(nil, testOIDCRegistration(idp.URL))
	assert.True(t, start, "an exhausted discovery must be started again")
	assert.NotEqual(t, gen, next)
}

// TestEndOIDCDiscoveryWindow pins that the end of the retry window marks the
// generation's discovery stopped in the same step, so a pass that arrives after
// the decision starts discovery again rather than finding it running.
func TestEndOIDCDiscoveryWindow(t *testing.T) {
	reg := testOIDCRegistration("http://begin.test")
	_, gen := beginDiscoveryForTest(t)

	_, same, start := beginProviderRegistration(nil, reg)
	require.False(t, start, "discovery within its window is left running")
	require.Equal(t, gen, same)

	require.True(t, endOIDCDiscoveryWindow(gen))
	_, next, start := beginProviderRegistration(nil, reg)
	assert.True(t, start, "a pass after the window ended must start discovery again")
	assert.NotEqual(t, gen, next)

	// A superseded generation does not touch the running flag of the current one.
	assert.False(t, endOIDCDiscoveryWindow(gen))
	_, again, start := beginProviderRegistration(nil, reg)
	assert.False(t, start, "the current generation's discovery must stay running")
	assert.Equal(t, next, again)
}

// TestShutdownOfEarlierServerKeepsLaterServer pins that the shutdown that an
// earlier server instance triggers late does not tear down a later instance:
// its registration, discovery and enabled state stay as they were.
func TestShutdownOfEarlierServerKeepsLaterServer(t *testing.T) {
	t.Cleanup(func() { enableOIDCRetries() })
	idp := mockOIDCDiscovery(t)

	earlier := enableOIDCRetries()
	done := registerProviders(t, oidcSettings(idp.URL)) // enables again: the later instance
	waitDone(t, done)
	before, err := goth.GetProvider(ProviderOIDC)
	require.NoError(t, err)

	shutdownOIDCRetriesFor(earlier)

	after, err := goth.GetProvider(ProviderOIDC)
	require.NoError(t, err, "a stale shutdown must not clear the later instance's registration")
	assert.Same(t, before, after)
	assert.Nil(t, initializeProviders(oidcSettings(idp.URL)), "the later instance keeps its registered provider")
	changed := oidcSettings(idp.URL)
	changed.Security.OAuthProviders[0].ClientSecret = "rotated"
	restarted := initializeProviders(changed)
	require.NotNil(t, restarted, "the later instance must still be enabled and start discovery")
	waitDone(t, restarted)

	// The instance's own shutdown still blocks new discovery.
	shutdownOIDCRetriesFor(enableOIDCRetries())
	assert.Nil(t, initializeProviders(oidcSettings(idp.URL)), "the instance's own shutdown must block discovery")
}
