package conf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// warningsText joins the recorded validation warnings so a test can assert on the
// text without caring which entry carried it.
func warningsText(s *Settings) string {
	return strings.Join(s.ValidationWarnings, "\n")
}

// TestNormalizeOAuthProviders_DisablesProviderWithoutCredentials verifies that a
// provider switched on without a client ID or secret is turned off rather than
// rejected. Disabling it is safe precisely because the runtime already ignores that
// shape, which is also why the GetEnabledOAuthProviders assertion below holds by
// construction for these inputs: it records the invariant rather than testing it.
// The assertion with teeth is in
// TestNormalizeOAuthProviders_KeepsCredentialedProviderEnabled, which fails if this
// pass ever disables a provider that does count as an authentication method.
func TestNormalizeOAuthProviders_DisablesProviderWithoutCredentials(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		provider OAuthProviderConfig
	}{
		{
			name:     "no credentials at all",
			provider: OAuthProviderConfig{Provider: "google", Enabled: true},
		},
		{
			name:     "client ID without secret",
			provider: OAuthProviderConfig{Provider: "github", Enabled: true, ClientID: "id"},
		},
		{
			name:     "secret without client ID",
			provider: OAuthProviderConfig{Provider: "microsoft", Enabled: true, ClientSecret: "secret"},
		},
		{
			name:     "OIDC with neither credentials nor issuer",
			provider: OAuthProviderConfig{Provider: providerOIDC, Enabled: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &Settings{}
			s.Security.OAuthProviders = []OAuthProviderConfig{tt.provider}

			normalizeIncompleteFeatures(s)

			assert.False(t, s.Security.OAuthProviders[0].Enabled,
				"a provider that cannot authenticate anyone must be left disabled")
			assert.Empty(t, s.GetEnabledOAuthProviders(),
				"a half-configured provider must never be consulted during auth")
			assert.Contains(t, warningsText(s), "clientId/clientSecret")
			require.NoError(t, ValidateSettings(s))
		})
	}
}

// TestNormalizeOAuthProviders_KeepsCredentialedProviderEnabled verifies the other
// half of the security rule: a provider that does have credentials is never
// disabled by normalization, however broken the rest of its configuration. It
// counts towards IsAuthenticationEnabled, so switching it off here would drop
// authentication from a server the operator meant to protect.
//
// Such a provider with no usable redirect URL is the one shape this change does
// NOT downgrade to a warning: it forces authentication on while making sign-in
// impossible, so validateOAuthRedirects rejects it and startup stops with the field
// named. Booting instead would leave an instance nobody can sign in to, with the
// warning that explains why sitting behind the login that cannot complete.
func TestNormalizeOAuthProviders_KeepsCredentialedProviderEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		provider    OAuthProviderConfig
		expectFatal string
	}{
		{
			name:        "no redirect source",
			provider:    OAuthProviderConfig{Provider: "google", Enabled: true, ClientID: "id", ClientSecret: "secret"},
			expectFatal: "no redirect URL can be built",
		},
		{
			name:        "relative redirect URI, the shape a migrated legacy block carries",
			provider:    OAuthProviderConfig{Provider: providerGoogle, Enabled: true, ClientID: "id", ClientSecret: "secret", RedirectURI: "/settings"},
			expectFatal: "not an absolute http(s) URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &Settings{}
			s.Security.OAuthProviders = []OAuthProviderConfig{tt.provider}

			normalizeIncompleteFeatures(s)

			assert.True(t, s.Security.OAuthProviders[0].Enabled,
				"a credentialed provider must stay enabled so authentication is still required")
			assert.Equal(t, []string{tt.provider.Provider}, s.GetEnabledOAuthProviders(),
				"authentication must not be silently removed")

			err := ValidateSettings(s)
			require.Error(t, err,
				"a provider that forces auth on but cannot complete a sign-in must stay fatal")
			assert.Contains(t, err.Error(), tt.expectFatal)
		})
	}
}

// TestValidateOAuthRedirects_ScopedToLockOutShape pins the boundaries of the fatal
// rule, so it cannot drift back into the blunt host requirement it replaced.
//
// The deleted security-oauth-host rule rejected any enabled social provider without
// security.host or security.baseUrl, even one carrying a perfectly good absolute
// redirectUri. Only the absence of a usable redirect is fatal now, and only when the
// provider has the credentials that make authentication mandatory.
func TestValidateOAuthRedirects_ScopedToLockOutShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		security Security
		wantErr  bool
	}{
		{
			name: "absolute redirectUri without host or baseUrl is accepted",
			security: Security{OAuthProviders: []OAuthProviderConfig{
				{Provider: providerGoogle, Enabled: true, ClientID: "id", ClientSecret: "secret", RedirectURI: "https://example.com/callback"},
			}},
		},
		{
			name: "host supplies the redirect source",
			security: Security{Host: "example.com", OAuthProviders: []OAuthProviderConfig{
				{Provider: providerGitHub, Enabled: true, ClientID: "id", ClientSecret: "secret"},
			}},
		},
		{
			name: "no credentials is inert, not fatal",
			security: Security{OAuthProviders: []OAuthProviderConfig{
				{Provider: providerGoogle, Enabled: true, RedirectURI: "/settings"},
			}},
		},
		{
			name: "disabled provider is never consulted",
			security: Security{OAuthProviders: []OAuthProviderConfig{
				{Provider: providerGoogle, ClientID: "id", ClientSecret: "secret", RedirectURI: "/settings"},
			}},
		},
		{
			name: "credentialed provider with no usable redirect is fatal",
			security: Security{OAuthProviders: []OAuthProviderConfig{
				{Provider: providerMicrosoft, Enabled: true, ClientID: "id", ClientSecret: "secret", RedirectURI: "/settings"},
			}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateOAuthRedirects(&tt.security)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

// TestNormalizeOAuthProviders_LegacyFieldsAreReportedNotRejected verifies the
// exact configuration from the field report: a deprecated googleAuth block left
// enabled with no client ID. migrateLegacyProvider skips it, so nothing at runtime
// reads it, and the old rule made that inert leftover fatal.
func TestNormalizeOAuthProviders_LegacyFieldsAreReportedNotRejected(t *testing.T) {
	t.Parallel()

	s := &Settings{}
	s.Security.GoogleAuth = SocialProvider{Enabled: true}
	s.Security.GithubAuth = SocialProvider{Enabled: true}

	normalizeIncompleteFeatures(s)

	require.NoError(t, ValidateSettings(s))
	assert.Empty(t, s.GetEnabledOAuthProviders(),
		"a legacy block with no clientId is never migrated, so it authenticates nobody")
	assert.Contains(t, warningsText(s), "security.googleauth")
	assert.Contains(t, warningsText(s), "security.githubauth")
}

// TestNormalizeOAuthProviders_LeavesCompleteConfigurationAlone guards against a
// blanket downgrade touching configurations that are fine.
func TestNormalizeOAuthProviders_LeavesCompleteConfigurationAlone(t *testing.T) {
	t.Parallel()

	s := &Settings{}
	s.Security.Host = "birdnet.example.com"
	s.Security.OAuthProviders = []OAuthProviderConfig{
		{Provider: "google", Enabled: true, ClientID: "id", ClientSecret: "secret"},
	}

	normalizeIncompleteFeatures(s)

	assert.True(t, s.Security.OAuthProviders[0].Enabled)
	assert.Equal(t, []string{"google"}, s.GetEnabledOAuthProviders())
	assert.Empty(t, s.ValidationWarnings)
}

// TestNormalizeOAuthProviders_LeavesDisabledProviderAlone verifies that a provider
// the operator deliberately switched off is neither re-examined nor warned about.
func TestNormalizeOAuthProviders_LeavesDisabledProviderAlone(t *testing.T) {
	t.Parallel()

	s := &Settings{}
	s.Security.OAuthProviders = []OAuthProviderConfig{
		{Provider: providerGoogle, Enabled: false},
		{Provider: providerOIDC, Enabled: false, ClientID: "id", ClientSecret: "secret"},
	}

	normalizeIncompleteFeatures(s)

	assert.Empty(t, s.ValidationWarnings, "a disabled provider is not an unfinished one")
	assert.Empty(t, s.GetEnabledOAuthProviders())
}

// TestNormalizeOAuthProviders_AcceptsCompleteOIDC verifies that a fully configured
// OIDC provider draws no warning. Without this, a regression that always reported an
// issuer problem would tell every healthy install that sign-in is broken.
func TestNormalizeOAuthProviders_AcceptsCompleteOIDC(t *testing.T) {
	t.Parallel()

	s := &Settings{}
	s.Security.Host = "birdnet.example.com"
	s.Security.OAuthProviders = []OAuthProviderConfig{
		{Provider: providerOIDC, Enabled: true, ClientID: "id", ClientSecret: "secret", IssuerURL: "https://idp.example.com"},
	}

	normalizeIncompleteFeatures(s)

	assert.Empty(t, s.ValidationWarnings)
	assert.Equal(t, []string{providerOIDC}, s.GetEnabledOAuthProviders())
	require.NoError(t, ValidateSettings(s))
}

// TestNormalizeOAuthProviders_WarnsForEveryLegacyBlock verifies all three deprecated
// blocks are covered, including microsoftAuth, and that a block whose provider did
// reach security.oauthProviders is not reported as ignored.
func TestNormalizeOAuthProviders_WarnsForEveryLegacyBlock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		apply      func(s *Settings)
		expectKey  string
		expectWarn bool
	}{
		{
			name:       "google",
			apply:      func(s *Settings) { s.Security.GoogleAuth = SocialProvider{Enabled: true} },
			expectKey:  "security.googleauth",
			expectWarn: true,
		},
		{
			name:       "github",
			apply:      func(s *Settings) { s.Security.GithubAuth = SocialProvider{Enabled: true} },
			expectKey:  "security.githubauth",
			expectWarn: true,
		},
		{
			name:       "microsoft",
			apply:      func(s *Settings) { s.Security.MicrosoftAuth = SocialProvider{Enabled: true} },
			expectKey:  "security.microsoftauth",
			expectWarn: true,
		},
		{
			name: "legacy block that was migrated is not reported",
			apply: func(s *Settings) {
				s.Security.GoogleAuth = SocialProvider{Enabled: true, ClientID: "id", ClientSecret: "secret"}
				s.Security.OAuthProviders = []OAuthProviderConfig{
					{Provider: providerGoogle, Enabled: true, ClientID: "id", ClientSecret: "secret", RedirectURI: "https://app.example.com/cb"},
				}
			},
			expectKey:  "security.googleauth",
			expectWarn: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &Settings{}
			tt.apply(s)

			normalizeIncompleteFeatures(s)

			if tt.expectWarn {
				assert.Contains(t, warningsText(s), tt.expectKey)
			} else {
				assert.NotContains(t, warningsText(s), tt.expectKey)
			}
		})
	}
}

// TestNormalizeOAuthProviders_LegacyCredentialedBlockStaysFatalAfterMigration covers
// the one shape the deleted host rule was doing real work for: a legacy block with
// credentials and no redirect source. After migration it becomes an array entry that
// forces authentication on while being unable to complete a sign-in, so it must stay
// fatal rather than degrade to a warning nobody can reach.
func TestNormalizeOAuthProviders_LegacyCredentialedBlockStaysFatalAfterMigration(t *testing.T) {
	t.Parallel()

	s := &Settings{}
	s.Security.GoogleAuth = SocialProvider{Enabled: true, ClientID: "id", ClientSecret: "secret"}
	require.True(t, s.MigrateOAuthConfig())

	normalizeIncompleteFeatures(s)

	assert.Equal(t, []string{providerGoogle}, s.GetEnabledOAuthProviders(),
		"a credentialed provider keeps requiring authentication")

	err := ValidateSettings(s)
	require.Error(t, err, "a migrated legacy block with no redirect source must not boot")
	assert.Contains(t, err.Error(), "no redirect URL can be built")
}

// TestNormalizeQuietHours_DisablesUnusableBlocks verifies that a quiet hours block
// which cannot produce a window is switched off. The scheduler already treats each
// of these as "not quiet", so nothing about the running system changes.
func TestNormalizeQuietHours_DisablesUnusableBlocks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		quietHours    QuietHoursConfig
		expectWarning string
	}{
		{
			name:          "enabled with no mode",
			quietHours:    QuietHoursConfig{Enabled: true},
			expectWarning: "no mode is set",
		},
		{
			name:          "unrecognised mode",
			quietHours:    QuietHoursConfig{Enabled: true, Mode: "nocturnal"},
			expectWarning: "unrecognised mode",
		},
		{
			name:          "fixed mode with empty times",
			quietHours:    QuietHoursConfig{Enabled: true, Mode: QuietHoursModeFixed},
			expectWarning: "start time",
		},
		{
			name:          "fixed mode with unparsable end time",
			quietHours:    QuietHoursConfig{Enabled: true, Mode: QuietHoursModeFixed, StartTime: "22:00", EndTime: "6am"},
			expectWarning: "end time",
		},
		{
			name:          "solar mode with no events",
			quietHours:    QuietHoursConfig{Enabled: true, Mode: QuietHoursModeSolar},
			expectWarning: "start event",
		},
		{
			name:          "solar mode with unrecognised end event",
			quietHours:    QuietHoursConfig{Enabled: true, Mode: QuietHoursModeSolar, StartEvent: SolarEventSunset, EndEvent: "moonrise"},
			expectWarning: "end event",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := createMinimalValidSettings()
			s.Realtime.Audio.QuietHours = tt.quietHours

			normalizeIncompleteFeatures(s)

			assert.False(t, s.Realtime.Audio.QuietHours.Enabled, "quiet hours must be switched off")
			assert.Contains(t, warningsText(s), tt.expectWarning)
			require.NoError(t, ValidateSettings(s))
		})
	}
}

// TestNormalizeQuietHours_CoversSourcesAndStreams verifies the sweep reaches every
// place a quiet hours block can live, not only the global one. The stream case is
// the one from the field report.
func TestNormalizeQuietHours_CoversSourcesAndStreams(t *testing.T) {
	t.Parallel()

	s := createMinimalValidSettings()
	s.Realtime.Audio.Sources = []AudioSourceConfig{
		{Name: "Sound Card 1", Device: testAudioDeviceSysdefault, QuietHours: QuietHoursConfig{Enabled: true}},
	}
	s.Realtime.RTSP.Streams = []StreamConfig{
		{Name: "Garden", URL: "rtsp://cam.local/stream", Type: StreamTypeRTSP, QuietHours: QuietHoursConfig{Enabled: true}},
	}

	normalizeIncompleteFeatures(s)

	assert.False(t, s.Realtime.Audio.Sources[0].QuietHours.Enabled)
	assert.False(t, s.Realtime.RTSP.Streams[0].QuietHours.Enabled)
	assert.Contains(t, warningsText(s), `audio source "Sound Card 1"`)
	assert.Contains(t, warningsText(s), `stream 1 ("Garden")`)
	require.NoError(t, ValidateSettings(s))
}

// TestNormalizeQuietHours_KeepsOutOfRangeOffsetsFatal guards the boundary of the
// downgrade. An offset outside the supported range is an explicitly typed value
// that shifts a real suppression window, not a field that was never filled in, so
// it must still be rejected.
func TestNormalizeQuietHours_KeepsOutOfRangeOffsetsFatal(t *testing.T) {
	t.Parallel()

	s := createMinimalValidSettings()
	s.Realtime.Audio.QuietHours = QuietHoursConfig{
		Enabled:     true,
		Mode:        QuietHoursModeSolar,
		StartEvent:  SolarEventSunset,
		EndEvent:    SolarEventSunrise,
		StartOffset: MaxQuietHoursOffset + 1,
	}

	normalizeIncompleteFeatures(s)

	assert.True(t, s.Realtime.Audio.QuietHours.Enabled, "a complete configuration must not be disabled")
	err := ValidateSettings(s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quiet hours start offset must be between")
}

// TestNormalizeEqualizers_AppliesDefaultQ verifies that a filter with no Q factor
// gets the standard one. This is not only a startup fix: NewLowPass computes
// sin(w0)/(2*q) without guarding q, so a zero reaching the audio path would make
// every coefficient infinite.
func TestNormalizeEqualizers_AppliesDefaultQ(t *testing.T) {
	t.Parallel()

	s := createMinimalValidSettings()
	s.Realtime.Audio.Equalizer = EqualizerSettings{
		Enabled: true,
		Filters: []EqualizerFilter{
			{Type: "LowPass", Frequency: 15000},
			{Type: "HighPass", Frequency: 100},
		},
	}
	s.Realtime.Audio.Sources = []AudioSourceConfig{
		{
			Name:      "Sound Card 1",
			Device:    testAudioDeviceSysdefault,
			Equalizer: &EqualizerSettings{Enabled: true, Filters: []EqualizerFilter{{Type: "LowPass", Frequency: 12000}}},
		},
	}
	s.Realtime.RTSP.Streams = []StreamConfig{
		{
			Name:      "Garden",
			URL:       "rtsp://cam.local/stream",
			Type:      StreamTypeRTSP,
			Equalizer: &EqualizerSettings{Enabled: true, Filters: []EqualizerFilter{{Type: "HighPass", Frequency: 200}}},
		},
	}

	normalizeIncompleteFeatures(s)

	assert.InDelta(t, DefaultEQQFactor, s.Realtime.Audio.Equalizer.Filters[0].Q, 0.0001)
	assert.InDelta(t, DefaultEQQFactor, s.Realtime.Audio.Equalizer.Filters[1].Q, 0.0001)
	assert.InDelta(t, DefaultEQQFactor, s.Realtime.Audio.Sources[0].Equalizer.Filters[0].Q, 0.0001)
	assert.InDelta(t, DefaultEQQFactor, s.Realtime.RTSP.Streams[0].Equalizer.Filters[0].Q, 0.0001)
	assert.Contains(t, warningsText(s), "no Q factor set")
	require.NoError(t, ValidateSettings(s))
}

// TestNormalizeEqualizers_NormalizesDisabledSetsSilently covers the file's most
// contested carve-out, in both halves: a switched-off equalizer still has its zero Q
// rewritten, so a later enable is not rejected over a field the settings UI never
// offered, and it produces no warning, so a feature the user turned off does not
// generate a notification.
func TestNormalizeEqualizers_NormalizesDisabledSetsSilently(t *testing.T) {
	t.Parallel()

	s := createMinimalValidSettings()
	s.Realtime.Audio.Equalizer = EqualizerSettings{
		Enabled: false,
		Filters: []EqualizerFilter{{Type: "LowPass", Frequency: 15000}},
	}

	normalizeIncompleteFeatures(s)

	assert.InDelta(t, DefaultEQQFactor, s.Realtime.Audio.Equalizer.Filters[0].Q, 0.0001,
		"a stored zero would make the user's later enable fail validation")
	assert.Empty(t, s.ValidationWarnings, "a switched-off equalizer is not worth a notification")
	require.NoError(t, ValidateSettings(s))
}

// TestNormalizeEqualizers_SilentForWidthBasedFilters verifies that the width-based
// filter types, which ignore Q entirely, are normalized without being reported. The
// validator still demands a positive Q from every type, so the value must be filled
// in, but telling the user a Q default was applied to a filter that has no Q setting
// would be misleading.
func TestNormalizeEqualizers_SilentForWidthBasedFilters(t *testing.T) {
	t.Parallel()

	for _, filterType := range []string{"BandPass", "BandReject", "Peaking"} {
		t.Run(filterType, func(t *testing.T) {
			t.Parallel()
			assert.False(t, eqFilterUsesQ(filterType))

			s := createMinimalValidSettings()
			s.Realtime.Audio.Equalizer = EqualizerSettings{
				Enabled: true,
				Filters: []EqualizerFilter{{Type: filterType, Frequency: 1000, Width: 100}},
			}

			normalizeIncompleteFeatures(s)

			assert.InDelta(t, DefaultEQQFactor, s.Realtime.Audio.Equalizer.Filters[0].Q, 0.0001)
			assert.Empty(t, s.ValidationWarnings)
			require.NoError(t, ValidateSettings(s))
		})
	}
}

// TestNormalizeIntegrations_BindsListenWhenTelemetryDisabled covers the other
// deliberate carve-out: the listen address is bound even when telemetry is off, so
// enabling it from the settings UI later is not rejected over a blank the operator
// never saw, but a disabled endpoint is not reported.
func TestNormalizeIntegrations_BindsListenWhenTelemetryDisabled(t *testing.T) {
	t.Parallel()

	s := createMinimalValidSettings()
	s.Realtime.Telemetry = TelemetrySettings{Enabled: false, Listen: ""}

	normalizeIncompleteFeatures(s)

	assert.Equal(t, DefaultTelemetryListen, s.Realtime.Telemetry.Listen)
	assert.Empty(t, s.ValidationWarnings)
}

// TestNormalizeIntegrations_DefaultsMQTTBackoffMultiplier verifies the retry
// multiplier is filled in rather than rejected. Zero is never a usable multiplier,
// so it can only mean the key was never written.
func TestNormalizeIntegrations_DefaultsMQTTBackoffMultiplier(t *testing.T) {
	t.Parallel()

	s := createMinimalValidSettings()
	s.Realtime.MQTT = MQTTSettings{
		Enabled:       true,
		Broker:        "tcp://localhost:1883",
		Topic:         "birdnet",
		RetrySettings: RetrySettings{Enabled: true},
	}

	normalizeIncompleteFeatures(s)

	assert.True(t, s.Realtime.MQTT.Enabled)
	assert.InDelta(t, DefaultRetryBackoffMultiplier, s.Realtime.MQTT.RetrySettings.BackoffMultiplier, 0.0001)
	assert.Contains(t, warningsText(s), "no backoff multiplier is set")
	require.NoError(t, ValidateSettings(s))
}

// TestNormalizeEqualizers_KeepsInvalidValuesFatal guards the boundary: only an
// exact zero means "never written". Anything else could reach the biquad and
// change what gets analysed, and the audio engine does not defend against it.
func TestNormalizeEqualizers_KeepsInvalidValuesFatal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		filter      EqualizerFilter
		expectError string
	}{
		{name: "negative Q", filter: EqualizerFilter{Type: "LowPass", Frequency: 15000, Q: -1},
			expectError: "invalid Q factor"},
		{name: "Q above maximum", filter: EqualizerFilter{Type: "LowPass", Frequency: 15000, Q: MaxEQQ + 1},
			expectError: "Q factor 101.0 exceeds maximum"},
		{name: "zero frequency", filter: EqualizerFilter{Type: "LowPass", Q: 0.7},
			expectError: "invalid frequency"},
		{name: "frequency above maximum", filter: EqualizerFilter{Type: "LowPass", Frequency: MaxEQFrequency + 1, Q: 0.7},
			expectError: "exceeds maximum"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := createMinimalValidSettings()
			s.Realtime.Audio.Equalizer = EqualizerSettings{Enabled: true, Filters: []EqualizerFilter{tt.filter}}

			normalizeIncompleteFeatures(s)

			err := ValidateSettings(s)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectError)
		})
	}
}

// TestValidateEqualizer_SkipsDisabledFilterSets verifies that filters belonging to
// an equalizer that is switched off are not validated. BuildFilterChain returns nil
// for a disabled equalizer, so those values never reach the audio path.
func TestValidateEqualizer_SkipsDisabledFilterSets(t *testing.T) {
	t.Parallel()

	broken := &EqualizerSettings{Enabled: false, Filters: []EqualizerFilter{{Type: "LowPass", Frequency: 15000, Q: -1}}}

	source := AudioSourceConfig{Name: "Sound Card 1", Device: testAudioDeviceSysdefault, Equalizer: broken}
	assert.NoError(t, source.Validate())

	stream := StreamConfig{Name: "Garden", URL: "rtsp://cam.local/stream", Type: StreamTypeRTSP, Equalizer: broken}
	assert.NoError(t, stream.Validate())
}

// TestNormalizeIntegrations_DisablesUnusableIntegrations verifies that an
// integration with no endpoint or credential is switched off with a warning.
func TestNormalizeIntegrations_DisablesUnusableIntegrations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		apply         func(s *Settings)
		stillEnabled  func(s *Settings) bool
		expectWarning string
	}{
		{
			name:          "BirdWeather without station ID",
			apply:         func(s *Settings) { s.Realtime.Birdweather = BirdweatherSettings{Enabled: true} },
			stillEnabled:  func(s *Settings) bool { return s.Realtime.Birdweather.Enabled },
			expectWarning: "no station ID is set",
		},
		{
			name:          "BirdWeather with malformed station ID",
			apply:         func(s *Settings) { s.Realtime.Birdweather = BirdweatherSettings{Enabled: true, ID: "short"} },
			stillEnabled:  func(s *Settings) bool { return s.Realtime.Birdweather.Enabled },
			expectWarning: "24 alphanumeric characters",
		},
		{
			name:          "MQTT without broker",
			apply:         func(s *Settings) { s.Realtime.MQTT = MQTTSettings{Enabled: true, Topic: "birdnet"} },
			stillEnabled:  func(s *Settings) bool { return s.Realtime.MQTT.Enabled },
			expectWarning: "no broker URL is set",
		},
		{
			name:          "MQTT without topic",
			apply:         func(s *Settings) { s.Realtime.MQTT = MQTTSettings{Enabled: true, Broker: "tcp://localhost:1883"} },
			stillEnabled:  func(s *Settings) bool { return s.Realtime.MQTT.Enabled },
			expectWarning: "no topic is set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := createMinimalValidSettings()
			tt.apply(s)

			normalizeIncompleteFeatures(s)

			assert.False(t, tt.stillEnabled(s), "an integration that cannot connect must be switched off")
			assert.Contains(t, warningsText(s), tt.expectWarning)
			require.NoError(t, ValidateSettings(s))
		})
	}
}

// TestNormalizeIntegrations_DefaultsListenAndPort verifies that an address left
// blank falls back to the documented default instead of blocking startup. The web
// server matters most here: it is the recovery path for every other setting.
func TestNormalizeIntegrations_DefaultsListenAndPort(t *testing.T) {
	t.Parallel()

	s := createMinimalValidSettings()
	s.Realtime.Telemetry = TelemetrySettings{Enabled: true}
	s.WebServer.Enabled = true
	s.WebServer.Port = ""

	normalizeIncompleteFeatures(s)

	assert.Equal(t, DefaultTelemetryListen, s.Realtime.Telemetry.Listen)
	assert.Equal(t, DefaultWebServerPort, s.WebServer.Port)
	assert.Contains(t, warningsText(s), "no listen address is set")
	assert.Contains(t, warningsText(s), "no port is set")
	require.NoError(t, ValidateSettings(s))
}

// TestNormalizeNotificationProviders_DisablesUndeliverableProviders verifies that a
// push provider missing the one field its type needs is switched off. Losing
// notifications is the same outcome as before; losing every detection because the
// process will not start is not.
func TestNormalizeNotificationProviders_DisablesUndeliverableProviders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		provider      PushProviderConfig
		expectWarning string
	}{
		{
			name:          "no type",
			provider:      PushProviderConfig{Name: "unnamed", Enabled: true},
			expectWarning: "has no type set",
		},
		{
			name:          "unknown type",
			provider:      PushProviderConfig{Name: "legacy", Enabled: true, Type: "pushover"},
			expectWarning: "unknown type",
		},
		{
			name:          "script without command",
			provider:      PushProviderConfig{Name: "runner", Enabled: true, Type: pushProviderScript},
			expectWarning: "no command",
		},
		{
			name:          "shoutrrr without URLs",
			provider:      PushProviderConfig{Name: "shout", Enabled: true, Type: pushProviderShoutrrr},
			expectWarning: "no URLs",
		},
		{
			name:          "webhook without endpoints",
			provider:      PushProviderConfig{Name: "hook", Enabled: true, Type: pushProviderWebhook},
			expectWarning: "no endpoints",
		},
		{
			name: "webhook endpoint with bearer auth and no token",
			provider: PushProviderConfig{
				Name: "hook", Enabled: true, Type: pushProviderWebhook,
				Endpoints: []WebhookEndpointConfig{{
					URL:  "https://example.com/hook",
					Auth: WebhookAuthConfig{Type: webhookAuthBearer},
				}},
			},
			expectWarning: "no token or token_file is set",
		},
		{
			name: "webhook endpoint with basic auth and no password",
			provider: PushProviderConfig{
				Name: "hook", Enabled: true, Type: pushProviderWebhook,
				Endpoints: []WebhookEndpointConfig{{
					URL:  "https://example.com/hook",
					Auth: WebhookAuthConfig{Type: webhookAuthBasic, User: "birdnet"},
				}},
			},
			expectWarning: "no pass or pass_file is set",
		},
		{
			name: "webhook endpoint with an auth type the sender cannot use",
			provider: PushProviderConfig{
				Name: "hook", Enabled: true, Type: pushProviderWebhook,
				Endpoints: []WebhookEndpointConfig{{
					URL:  "https://example.com/hook",
					Auth: WebhookAuthConfig{Type: "mtls"},
				}},
			},
			expectWarning: "auth type is not one of",
		},
		{
			name: "webhook endpoint with no URL",
			provider: PushProviderConfig{
				Name: "hook", Enabled: true, Type: pushProviderWebhook,
				Endpoints: []WebhookEndpointConfig{{Auth: WebhookAuthConfig{}}},
			},
			expectWarning: "has no URL",
		},
		{
			name: "webhook endpoint with custom auth and no value",
			provider: PushProviderConfig{
				Name: "hook", Enabled: true, Type: pushProviderWebhook,
				Endpoints: []WebhookEndpointConfig{{
					URL:  "https://example.com/hook",
					Auth: WebhookAuthConfig{Type: webhookAuthCustom, Header: "X-Token"},
				}},
			},
			expectWarning: "no value or value_file is set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := createMinimalValidSettings()
			s.Notification.Push.Enabled = true
			s.Notification.Push.Providers = []PushProviderConfig{tt.provider}

			normalizeIncompleteFeatures(s)

			assert.False(t, s.Notification.Push.Providers[0].Enabled, "an undeliverable provider must be switched off")
			assert.Contains(t, warningsText(s), tt.expectWarning)
			require.NoError(t, ValidateSettings(s))
		})
	}
}

// TestNormalizeNotificationProviders_KeepsWorkingProvidersEnabled verifies the
// direction that matters most: a provider that CAN deliver is never switched off.
// Without it, a regression that disabled every webhook would cost users their alerts
// silently.
func TestNormalizeNotificationProviders_KeepsWorkingProvidersEnabled(t *testing.T) {
	t.Parallel()

	endpoint := func(auth WebhookAuthConfig) WebhookEndpointConfig {
		return WebhookEndpointConfig{URL: "https://example.com/hook", Auth: auth}
	}

	tests := []struct {
		name     string
		provider PushProviderConfig
	}{
		{
			name:     "script with a command",
			provider: PushProviderConfig{Name: "runner", Enabled: true, Type: pushProviderScript, Command: "/usr/local/bin/notify"},
		},
		{
			name:     "shoutrrr with a URL",
			provider: PushProviderConfig{Name: "shout", Enabled: true, Type: pushProviderShoutrrr, URLs: []string{"ntfy://ntfy.sh/birds"}},
		},
		{
			name: "webhook with no auth",
			provider: PushProviderConfig{Name: "hook", Enabled: true, Type: pushProviderWebhook,
				Endpoints: []WebhookEndpointConfig{endpoint(WebhookAuthConfig{})}},
		},
		{
			name: "webhook with auth type none",
			provider: PushProviderConfig{Name: "hook", Enabled: true, Type: pushProviderWebhook,
				Endpoints: []WebhookEndpointConfig{endpoint(WebhookAuthConfig{Type: webhookAuthNone})}},
		},
		{
			name: "webhook with complete bearer auth",
			provider: PushProviderConfig{Name: "hook", Enabled: true, Type: pushProviderWebhook,
				Endpoints: []WebhookEndpointConfig{endpoint(WebhookAuthConfig{Type: webhookAuthBearer, Token: "t"})}},
		},
		{
			name: "webhook with complete basic auth",
			provider: PushProviderConfig{Name: "hook", Enabled: true, Type: pushProviderWebhook,
				Endpoints: []WebhookEndpointConfig{endpoint(WebhookAuthConfig{Type: webhookAuthBasic, User: "u", Pass: "p"})}},
		},
		{
			name: "webhook with complete custom auth",
			provider: PushProviderConfig{Name: "hook", Enabled: true, Type: pushProviderWebhook,
				Endpoints: []WebhookEndpointConfig{endpoint(WebhookAuthConfig{Type: webhookAuthCustom, Header: "X-Token", Value: "v"})}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := createMinimalValidSettings()
			s.Notification.Push.Enabled = true
			s.Notification.Push.Providers = []PushProviderConfig{tt.provider}

			normalizeIncompleteFeatures(s)

			assert.True(t, s.Notification.Push.Providers[0].Enabled,
				"a provider that can deliver must not be switched off")
			assert.Empty(t, s.ValidationWarnings)
		})
	}
}

// TestNormalizeIncompleteFeatures_LeavesConfiguredValuesAlone verifies the pass only
// fills in what was never written. A regression that defaulted unconditionally would
// rebind a telemetry endpoint the operator pinned to loopback onto every interface,
// which no "the empty value gets a default" test can detect.
func TestNormalizeIncompleteFeatures_LeavesConfiguredValuesAlone(t *testing.T) {
	t.Parallel()

	s := createMinimalValidSettings()
	s.Realtime.Telemetry = TelemetrySettings{Enabled: true, Listen: "127.0.0.1:9100"}
	s.WebServer.Enabled = true
	s.WebServer.Port = "9000"
	s.Realtime.Audio.SoundLevel = SoundLevelSettings{Enabled: true, Interval: 30}
	s.Realtime.DynamicThreshold = DynamicThresholdSettings{Enabled: true, Trigger: 0.9, Min: 0.2, ValidHours: 12}
	s.Realtime.SpeciesTracking = SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 21,
		SyncIntervalMinutes:  15,
		YearlyTracking:       YearlyTrackingSettings{Enabled: true, ResetMonth: 4, ResetDay: 15, WindowDays: 30},
		SeasonalTracking: SeasonalTrackingSettings{
			Enabled: true, WindowDays: 14,
			Seasons: map[string]Season{"spring": {StartMonth: 3, StartDay: 20}},
		},
	}
	s.Realtime.Audio.Export = ExportSettings{Enabled: true, Type: AudioExportTypeOPUS, Bitrate: "128k", Length: 30}

	normalizeIncompleteFeatures(s)

	assert.Equal(t, "127.0.0.1:9100", s.Realtime.Telemetry.Listen)
	assert.Equal(t, "9000", s.WebServer.Port)
	assert.Equal(t, 30, s.Realtime.Audio.SoundLevel.Interval)
	assert.Equal(t, 12, s.Realtime.DynamicThreshold.ValidHours)
	assert.Equal(t, 21, s.Realtime.SpeciesTracking.NewSpeciesWindowDays)
	assert.Equal(t, 15, s.Realtime.SpeciesTracking.SyncIntervalMinutes)
	assert.Equal(t, 4, s.Realtime.SpeciesTracking.YearlyTracking.ResetMonth)
	assert.Equal(t, 15, s.Realtime.SpeciesTracking.YearlyTracking.ResetDay)
	assert.Equal(t, 30, s.Realtime.SpeciesTracking.YearlyTracking.WindowDays)
	assert.Equal(t, 14, s.Realtime.SpeciesTracking.SeasonalTracking.WindowDays)
	assert.Equal(t, "128k", s.Realtime.Audio.Export.Bitrate)
	assert.Equal(t, 30, s.Realtime.Audio.Export.Length)
	assert.Empty(t, s.ValidationWarnings, "nothing here was left unconfigured")
}

// TestNormalizeSpeciesTracking_DefaultsResetDayIndependently verifies that a reset
// day the user did write survives a missing reset month. Filling in both from the
// month's absence would discard a value the user set, which is the one thing this
// pass exists not to do.
func TestNormalizeSpeciesTracking_DefaultsResetDayIndependently(t *testing.T) {
	t.Parallel()

	t.Run("month missing, day kept", func(t *testing.T) {
		t.Parallel()
		s := createMinimalValidSettings()
		s.Realtime.SpeciesTracking = SpeciesTrackingSettings{
			Enabled:        true,
			YearlyTracking: YearlyTrackingSettings{Enabled: true, ResetDay: 15},
		}

		normalizeIncompleteFeatures(s)

		yt := s.Realtime.SpeciesTracking.YearlyTracking
		assert.Equal(t, DefaultYearlyTrackingResetMonth, yt.ResetMonth)
		assert.Equal(t, 15, yt.ResetDay, "a reset day the user set must survive")
		require.NoError(t, ValidateSettings(s))
	})

	t.Run("day missing, month kept", func(t *testing.T) {
		t.Parallel()
		s := createMinimalValidSettings()
		s.Realtime.SpeciesTracking = SpeciesTrackingSettings{
			Enabled:        true,
			YearlyTracking: YearlyTrackingSettings{Enabled: true, ResetMonth: 9},
		}

		normalizeIncompleteFeatures(s)

		yt := s.Realtime.SpeciesTracking.YearlyTracking
		assert.Equal(t, 9, yt.ResetMonth, "a reset month the user set must survive")
		assert.Equal(t, DefaultYearlyTrackingResetDay, yt.ResetDay)
		require.NoError(t, ValidateSettings(s))
	})
}

// TestNormalizeAudioExport_AppliesDefaults covers the section users hand-edit most,
// where viper drops a nested default whenever the parent key is present.
func TestNormalizeAudioExport_AppliesDefaults(t *testing.T) {
	t.Parallel()

	s := createMinimalValidSettings()
	s.Realtime.Audio.Export = ExportSettings{
		Enabled:       true,
		Type:          AudioExportTypeOPUS,
		Normalization: NormalizationSettings{Enabled: true},
	}
	s.Realtime.Audio.Export.Retention = RetentionSettings{Policy: RetentionPolicyAge}

	normalizeIncompleteFeatures(s)

	export := s.Realtime.Audio.Export
	assert.Equal(t, DefaultAudioExportLength, export.Length)
	assert.Equal(t, DefaultAudioExportBitrate, export.Bitrate)
	assert.InDelta(t, DefaultNormalizationTargetLUFS, export.Normalization.TargetLUFS, 0.0001)
	assert.Equal(t, DefaultRetentionMaxAge, export.Retention.MaxAge)
	require.NoError(t, ValidateSettings(s))
}

// TestValidateNotificationSettings_IgnoresDisabledUnknownProvider verifies that a
// leftover provider entry with a type this build no longer knows does not block
// startup while it is switched off.
func TestValidateNotificationSettings_IgnoresDisabledUnknownProvider(t *testing.T) {
	t.Parallel()

	n := &NotificationConfig{}
	n.Push.Enabled = true
	n.Push.Providers = []PushProviderConfig{{Name: "legacy", Enabled: false, Type: "pushover"}}

	assert.NoError(t, validateNotificationSettings(n))
}

// TestNormalizeRealtimeFeatures_AppliesDefaults verifies that an interval or window
// left at zero gets the documented default. Zero never means "no window" for any of
// these, so the alternative is refusing to start over a field the user never saw.
func TestNormalizeRealtimeFeatures_AppliesDefaults(t *testing.T) {
	t.Parallel()

	s := createMinimalValidSettings()
	s.Realtime.Audio.SoundLevel = SoundLevelSettings{Enabled: true}
	s.Realtime.DynamicThreshold = DynamicThresholdSettings{Enabled: true, Trigger: 0.9, Min: 0.2}
	s.Realtime.SpeciesTracking = SpeciesTrackingSettings{
		Enabled:          true,
		YearlyTracking:   YearlyTrackingSettings{Enabled: true},
		SeasonalTracking: SeasonalTrackingSettings{Enabled: true, Seasons: map[string]Season{"spring": {StartMonth: 3, StartDay: 20}}},
	}

	normalizeIncompleteFeatures(s)

	assert.Equal(t, DefaultSoundLevelInterval, s.Realtime.Audio.SoundLevel.Interval)
	assert.Equal(t, DefaultDynamicThresholdValidHours, s.Realtime.DynamicThreshold.ValidHours)
	assert.Equal(t, DefaultNewSpeciesWindowDays, s.Realtime.SpeciesTracking.NewSpeciesWindowDays)
	assert.Equal(t, DefaultSpeciesSyncIntervalMinutes, s.Realtime.SpeciesTracking.SyncIntervalMinutes)
	assert.Equal(t, DefaultYearlyTrackingResetMonth, s.Realtime.SpeciesTracking.YearlyTracking.ResetMonth)
	assert.Equal(t, DefaultYearlyTrackingResetDay, s.Realtime.SpeciesTracking.YearlyTracking.ResetDay)
	assert.Equal(t, DefaultYearlyTrackingWindowDays, s.Realtime.SpeciesTracking.YearlyTracking.WindowDays)
	assert.Equal(t, DefaultSeasonalTrackingWindowDays, s.Realtime.SpeciesTracking.SeasonalTracking.WindowDays)
	require.NoError(t, ValidateSettings(s))
}

// TestNormalizeRealtimeFeatures_KeepsExplicitOutOfRangeFatal guards the boundary
// between "never written" and "written wrongly".
func TestNormalizeRealtimeFeatures_KeepsExplicitOutOfRangeFatal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		apply       func(s *Settings)
		expectError string
	}{
		{
			expectError: "sound level interval must be at least",
			name:        "sound level interval below the CPU floor",
			apply: func(s *Settings) {
				s.Realtime.Audio.SoundLevel = SoundLevelSettings{Enabled: true, Interval: MinSoundLevelInterval - 1}
			},
		},
		{
			expectError: "dynamic threshold validHours must be positive",
			name:        "negative dynamic threshold validHours",
			apply: func(s *Settings) {
				s.Realtime.DynamicThreshold = DynamicThresholdSettings{Enabled: true, Trigger: 0.9, Min: 0.2, ValidHours: -1}
			},
		},
		{
			expectError: "species tracking window days must be between",
			name:        "species tracking window out of range",
			apply: func(s *Settings) {
				s.Realtime.SpeciesTracking = SpeciesTrackingSettings{
					Enabled: true, NewSpeciesWindowDays: 400, SyncIntervalMinutes: 60,
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := createMinimalValidSettings()
			tt.apply(s)

			normalizeIncompleteFeatures(s)

			err := ValidateSettings(s)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectError)
		})
	}
}

// TestNormalizeRealtimeFeatures_FillsMissingSeasons verifies that an empty season
// map is populated from the configured latitude rather than used as a reason to
// switch seasonal tracking off. Disabling would also remove the repair Load already
// performs after validation, since that is gated on the feature being enabled.
func TestNormalizeRealtimeFeatures_FillsMissingSeasons(t *testing.T) {
	t.Parallel()

	s := createMinimalValidSettings()
	s.BirdNET.Latitude = -33.9 // southern hemisphere, so the defaults are not the northern ones
	s.Realtime.SpeciesTracking = SpeciesTrackingSettings{
		Enabled:          true,
		SeasonalTracking: SeasonalTrackingSettings{Enabled: true},
	}

	normalizeIncompleteFeatures(s)

	seasonal := s.Realtime.SpeciesTracking.SeasonalTracking
	assert.True(t, seasonal.Enabled, "seasonal tracking must stay on")
	assert.Equal(t, GetDefaultSeasons(-33.9), seasonal.Seasons)
	assert.Contains(t, warningsText(s), "no seasons are defined")
	require.NoError(t, ValidateSettings(s))
}

// TestRecordValidationWarning_Deduplicates verifies that repeated normalization of
// an unfixed configuration cannot grow the warning slice without bound.
func TestRecordValidationWarning_Deduplicates(t *testing.T) {
	t.Parallel()

	s := createMinimalValidSettings()
	s.Realtime.MQTT = MQTTSettings{Enabled: true, Topic: "birdnet"}

	normalizeIncompleteFeatures(s)
	s.Realtime.MQTT.Enabled = true
	normalizeIncompleteFeatures(s)

	assert.Len(t, s.ValidationWarnings, 1)
}

// TestLoad_AcceptsPreviouslyRejectedConfig is the regression for the reported
// failure, exercised through the path the server actually takes. This config.yaml
// produced four fatal errors at once, so conf.Load returned an error, main exited
// 1, and the service restarted forever with no UI in which to correct any of them.
// Every one of the four settings is switched on and unfinished, none can affect
// analysis, and together they must no longer stop the server from starting.
//
// Not parallel: Load mutates ConfigPath, viper, and the published settings.
func TestLoad_AcceptsPreviouslyRejectedConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	// 1. OAuth providers toggled on with no credentials and no host or baseUrl.
	// 2. Stream quiet hours enabled with an unset mode and empty times.
	// 3. BirdWeather enabled with no station ID.
	// 4. Two global equalizer filters with no Q factor.
	configYAML := `
security:
  host: ""
  baseurl: ""
  googleauth:
    enabled: true
    clientid: ""
    clientsecret: ""
  githubauth:
    enabled: true
    clientid: ""
    clientsecret: ""
realtime:
  birdweather:
    enabled: true
    id: ""
  rtsp:
    streams:
      - name: "Sound Card 1"
        url: "rtsp://192.168.1.10/stream"
        type: "rtsp"
        quietHours:
          enabled: true
          mode: ""
          startTime: ""
          endTime: ""
  audio:
    equalizer:
      enabled: true
      filters:
        - type: "LowPass"
          frequency: 15000
          q: 0
        - type: "HighPass"
          frequency: 100
          q: 0
`
	require.NoError(t, os.WriteFile(configPath, []byte(configYAML), 0o600))

	oldPath := ConfigPath
	// Clone rather than keep the live pointer: GetSettings returns the published
	// settings, so a snapshot taken by reference would follow any mutation the test
	// makes and restore the mutated state instead of the original.
	oldSettings := CloneSettings(GetSettings())
	t.Cleanup(func() {
		ConfigPath = oldPath
		viper.Reset()
		StoreSettings(oldSettings)
	})
	// Reset viper before loading: initViper does not clear it, so a viper.Set
	// override left by an earlier sequential test would outrank the config file and
	// this test would pass or fail for a reason that has nothing to do with it.
	viper.Reset()
	ConfigPath = configPath

	settings, err := Load()
	require.NoError(t, err, "a config with only inert half-configured features must load")

	// Each feature is off or defaulted rather than left silently half-on.
	assert.Empty(t, settings.GetEnabledOAuthProviders(),
		"OAuth providers without credentials must not be consulted")
	require.Len(t, settings.Realtime.RTSP.Streams, 1)
	assert.False(t, settings.Realtime.RTSP.Streams[0].QuietHours.Enabled)
	assert.False(t, settings.Realtime.Birdweather.Enabled)
	require.Len(t, settings.Realtime.Audio.Equalizer.Filters, 2)
	assert.InDelta(t, DefaultEQQFactor, settings.Realtime.Audio.Equalizer.Filters[0].Q, 0.0001)
	assert.InDelta(t, DefaultEQQFactor, settings.Realtime.Audio.Equalizer.Filters[1].Q, 0.0001)

	// And the user is told about all four, on the channel main.go forwards to
	// telemetry and the notification centre.
	warnings := warningsText(settings)
	assert.Contains(t, warnings, "security.googleauth")
	assert.Contains(t, warnings, "quiet hours")
	assert.Contains(t, warnings, "BirdWeather")
	assert.Contains(t, warnings, "Q factor")
}
