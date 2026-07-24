// conf/validate_incomplete.go
//
// Reclassification of "switched on but never finished" settings.
//
// Every error a validator returns is fatal and blocks startup (see
// finalizeValidation in storage.go). That is correct for a value that would make
// the application behave wrongly, and wrong for a feature the user toggled on and
// never configured: such a feature cannot do anything, and refusing to boot over
// it removes the web UI that is the only practical way to undo the toggle. On a
// headless install the service then crash-loops until someone edits YAML by hand.
//
// normalizeIncompleteFeatures runs once per Load, before the validators, and
// resolves those configurations the way the running system already treats them:
//
//   - a required string left empty means the feature was never configured, so the
//     documented default is bound when the setting has one and the feature is
//     disabled when it does not;
//   - a required number left at zero means the field was never written, so the
//     documented default is applied;
//   - a value the running system already ignores, meaning an unrecognised entry in
//     a fixed set (a quiet hours mode, a solar event, a push provider type), is
//     resolved the way the runtime resolves it, which is to switch the feature off;
//   - anything else explicitly wrong (negative, out of range, an unparsable number,
//     NaN or Inf) is left alone and the existing validator still rejects it.
//
// The third rule is the one that needs justifying, since an unrecognised enum was
// typed on purpose. It is included because the runtime does not fail on those
// values either: it falls through to a default branch and the feature does nothing.
// Refusing to boot would therefore not protect anything, it would only hide the
// dead feature behind a boot loop. The fourth rule is where that reasoning stops:
// an out-of-range number does change what the code computes.
//
// Because most fatal rules are already written as "if feature.Enabled", disabling
// the feature here makes them unreachable without weakening them.
//
// It is called from Load and not from ValidateSettings on purpose. Leniency is
// wanted when reading a config file that aged into this state, where the only
// alternative is a boot loop, and is not wanted when the settings API validates an
// update: there the strict rules still apply, so a half-finished section from the
// UI is answered with an error the user can act on instead of being silently
// turned off and saved.
//
// Security rule that overrides the above: nothing here may reduce the set of
// authentications the server requires. An OAuth provider is disabled only when the
// runtime already ignores it (see normalizeOAuthProviders).
package conf

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// Component names used for the warnings recorded below. They become the prefix of
// each ValidationWarnings entry and are parsed back out in main.go.
const (
	warnComponentSecurity     = "security"
	warnComponentAudio        = "audio"
	warnComponentBirdweather  = "birdweather"
	warnComponentMQTT         = "mqtt"
	warnComponentTelemetry    = "telemetry"
	warnComponentWebServer    = "webserver"
	warnComponentNotification = "notification"
	warnComponentRealtime     = "realtime"
	warnComponentLowMemory    = "lowmemory"
	warnComponentModels       = "models"
	warnComponentStreams      = "streams"
)

// recordValidationWarning logs a non-fatal configuration finding and records it on
// the settings so it can be surfaced outside the log. The "component: message"
// shape is the one main.go splits when forwarding warnings to telemetry and to the
// notification centre.
//
// Duplicates are dropped for two reasons. Within one pass, two entities producing
// the same sentence (two audio sources sharing a name, a repeated filter message)
// should report once. Across calls it is load-bearing: ValidateSettings also records
// here for an invalid lowmemory.mode, and the settings API runs it on every save
// against a clone that carries the previously recorded warnings forward.
func (s *Settings) recordValidationWarning(component, format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	entry := component + ": " + message
	if slices.Contains(s.ValidationWarnings, entry) {
		return
	}
	GetLogger().Warn("Configuration validation warning",
		logger.String("component", component),
		logger.String("message", message))
	s.ValidationWarnings = append(s.ValidationWarnings, entry)
}

// normalizeIncompleteFeatures disables or defaults settings that are enabled but
// not configured enough to have any effect, recording a warning for each. See the
// file comment for the policy and its one security exception.
func normalizeIncompleteFeatures(s *Settings) {
	s.normalizeOAuthProviders()
	s.normalizeQuietHours()
	s.normalizeEqualizers()
	s.normalizeAudioExport()
	s.normalizeRetention()
	s.normalizeIntegrations()
	s.normalizeNotificationProviders()
	s.normalizeRealtimeFeatures()
	s.normalizeSpeciesTracking()
	s.normalizeWebServer()
}

// normalizeOAuthProviders disables OAuth providers that cannot authenticate anyone
// and warns about the ones that can but have no redirect URL to come back to.
//
// Disabling is limited to providers the runtime already refuses to use:
// GetEnabledOAuthProviders and initializeProviders both require a non-empty
// clientId and clientSecret, so clearing Enabled on such an entry changes nothing
// about which requests are authenticated - it only makes the stored state honest.
// A provider that does have credentials is never disabled here, however broken the
// rest of its configuration: it counts towards IsAuthenticationEnabled, so turning
// it off would drop authentication from a server the operator meant to protect.
func (s *Settings) normalizeOAuthProviders() {
	sec := &s.Security
	hasRedirectSource := sec.Host != "" || sec.BaseURL != ""

	for i := range sec.OAuthProviders {
		p := &sec.OAuthProviders[i]
		if !p.Enabled {
			continue
		}

		if p.ClientID == "" || p.ClientSecret == "" {
			p.Enabled = false
			s.recordValidationWarning(warnComponentSecurity,
				"OAuth provider %q is enabled but has no clientId/clientSecret, so it cannot sign anyone in; disabling it (set security.oauthProviders[].clientId and clientSecret to use it)",
				p.Provider)
			continue
		}

		// Credentials are present, so the provider stays enabled and
		// authentication stays required. Warn about what will not work.
		if reason := oauthRedirectProblem(p, hasRedirectSource); reason != "" {
			s.recordValidationWarning(warnComponentSecurity,
				"OAuth provider %q is enabled but %s, so sign-in will fail", p.Provider, reason)
		}
	}

	// The deprecated googleAuth/githubAuth/microsoftAuth blocks are migrated into
	// OAuthProviders on load, and MigrateOAuthConfig skips the whole migration when
	// that array already has entries, while migrateLegacyProvider skips an
	// individual block with no clientId. A block that did not become an array entry
	// is therefore inert: no code outside migration uses these fields to
	// authenticate anyone, and the remaining readers only redact their secrets. Say
	// so rather than rejecting the file.
	legacy := []struct {
		key      string
		name     string
		provider SocialProvider
	}{
		{"security.googleauth", providerGoogle, sec.GoogleAuth},
		{"security.githubauth", providerGitHub, sec.GithubAuth},
		{"security.microsoftauth", providerMicrosoft, sec.MicrosoftAuth},
	}
	for _, l := range legacy {
		if !l.provider.Enabled {
			continue
		}
		migrated := slices.ContainsFunc(sec.OAuthProviders, func(p OAuthProviderConfig) bool {
			return p.Provider == l.name
		})
		if !migrated {
			s.recordValidationWarning(warnComponentSecurity,
				"%s is enabled but was not migrated into security.oauthProviders, so it signs nobody in; configure the provider under security.oauthProviders instead",
				l.key)
		}
	}
}

// oauthRedirectProblem describes why a provider has no usable redirect URL, or
// returns "" when it has one. It mirrors what initializeProviders actually
// computes: an explicit redirectUri is used verbatim, and only an absent one is
// built from security.host or security.baseUrl.
//
// Testing merely for an empty redirectUri would miss the common case. The
// deprecated googleAuth and githubAuth blocks default redirecturi to "/settings"
// (see defaults.go), and MigrateOAuthConfig copies that into the array as-is, so a
// migrated provider usually carries a non-empty but relative value that no OAuth
// provider can redirect back to. That shape used to be caught by the fatal
// host-required rule; without this check it would degrade to silence.
func oauthRedirectProblem(p *OAuthProviderConfig, hasOrigin bool) string {
	if p.RedirectURI == "" {
		if hasOrigin {
			return ""
		}
		return "no redirect URL can be built; set security.host or security.baseUrl, or security.oauthProviders[].redirectUri"
	}
	parsed, err := url.Parse(p.RedirectURI)
	if err != nil || parsed.Host == "" || (parsed.Scheme != SchemeHTTPS && parsed.Scheme != SchemeHTTP) {
		return "its redirectUri is not an absolute http(s) URL, so the provider has nowhere to send the user back to"
	}
	return ""
}

// normalizeQuietHours disables quiet hours blocks that cannot produce a window.
//
// The scheduler already treats them as "not quiet": isInQuietHours returns false
// for an unknown mode and for an unparsable HH:MM. Solar mode with an unrecognised
// event is the exception that makes disabling better than tolerating - there
// getSolarEventTime returns the zero time, which would silence audio around
// midnight rather than around the intended sun event.
//
// Offsets outside the supported range are deliberately left to the fatal
// validator: zero is a valid offset, so an out-of-range value was typed on purpose
// and it does shift a real window.
func (s *Settings) normalizeQuietHours() {
	audio := &s.Realtime.Audio

	s.normalizeQuietHoursBlock(&audio.QuietHours, "realtime.audio.quietHours")
	for i := range audio.Sources {
		s.normalizeQuietHoursBlock(&audio.Sources[i].QuietHours,
			fmt.Sprintf("audio source %q quiet hours", audio.Sources[i].Name))
	}
	for i, stream := range s.Realtime.RTSP.AllStreams() {
		s.normalizeQuietHoursBlock(&stream.QuietHours,
			fmt.Sprintf("stream %d (%q) quiet hours", i+1, stream.Name))
	}
}

// normalizeQuietHoursBlock disables one quiet hours configuration when it is
// enabled but incomplete, describing it with the given config path.
func (s *Settings) normalizeQuietHoursBlock(qh *QuietHoursConfig, context string) {
	if !qh.Enabled {
		return
	}

	disable := func(format string, args ...any) {
		qh.Enabled = false
		s.recordValidationWarning(warnComponentAudio, "%s %s", context, fmt.Sprintf(format, args...))
	}

	switch {
	case qh.Mode == "":
		disable("is enabled but no mode is set; disabling it (set mode to %q or %q to use it)",
			QuietHoursModeFixed, QuietHoursModeSolar)
	case !ValidQuietHoursModes[qh.Mode]:
		disable("is enabled with unrecognised mode %q; disabling it (valid modes are %q and %q)",
			qh.Mode, QuietHoursModeFixed, QuietHoursModeSolar)
	case qh.Mode == QuietHoursModeFixed && !isValidClockTime(qh.StartTime):
		disable("is enabled but its start time %q is not HH:MM; disabling it", qh.StartTime)
	case qh.Mode == QuietHoursModeFixed && !isValidClockTime(qh.EndTime):
		disable("is enabled but its end time %q is not HH:MM; disabling it", qh.EndTime)
	case qh.Mode == QuietHoursModeSolar && !ValidSolarEvents[qh.StartEvent]:
		disable("is enabled but its start event %q is not %q or %q; disabling it",
			qh.StartEvent, SolarEventSunrise, SolarEventSunset)
	case qh.Mode == QuietHoursModeSolar && !ValidSolarEvents[qh.EndEvent]:
		disable("is enabled but its end event %q is not %q or %q; disabling it",
			qh.EndEvent, SolarEventSunrise, SolarEventSunset)
	}
}

// isValidClockTime reports whether a string parses as an HH:MM clock time.
func isValidClockTime(value string) bool {
	_, err := time.Parse(quietHoursTimeLayout, value)
	return err == nil
}

// eqFilterUsesQ reports whether a filter type derives its coefficients from Q.
// The width-based types (BandPass, BandReject, Peaking) take a bandwidth in Hz
// instead and never read Q, so a zero there is the normal state rather than an
// unfinished one. See buildFilter in internal/audiocore/equalizer.
func eqFilterUsesQ(filterType string) bool {
	switch filterType {
	case "LowPass", "HighPass", "AllPass", "LowShelf", "HighShelf":
		return true
	default:
		return false
	}
}

// normalizeEqualizers replaces a filter Q of exactly zero with the standard
// default. Zero is the never-written value rather than a choice, and for the
// Q-based filters it is also dangerous: their constructors compute sin(w0)/(2*q)
// with no guard, so a zero would make every coefficient non-finite and poison the
// audio.
//
// Two deliberate asymmetries with the validators:
//
// Filter sets are normalized whether or not the equalizer is enabled, even though
// StreamConfig.Validate and AudioSourceConfig.Validate now skip a disabled one.
// validateEQFilters demands a positive Q from every filter type, so leaving a
// stored zero in place would mean the user's later "enable" is rejected over a
// field the settings UI never offered them. Nothing is warned about for a disabled
// set, though: a notification about a switched-off feature is noise.
//
// Every other invalid Q, and every invalid frequency, stays fatal. A frequency of
// zero is the sharpest case: it makes w0 zero, which zeroes the whole numerator, so
// a LowPass at frequency 0 outputs silence rather than doing nothing.
func (s *Settings) normalizeEqualizers() {
	audio := &s.Realtime.Audio

	s.normalizeEQFilters(&audio.Equalizer, "realtime.audio.equalizer")
	for i := range audio.Sources {
		src := &audio.Sources[i]
		if src.Equalizer != nil {
			s.normalizeEQFilters(src.Equalizer, fmt.Sprintf("audio source %q equalizer", src.Name))
		}
	}
	for i, stream := range s.Realtime.RTSP.AllStreams() {
		if stream.Equalizer != nil {
			s.normalizeEQFilters(stream.Equalizer, fmt.Sprintf("stream %d (%q) equalizer", i+1, stream.Name))
		}
	}
}

// normalizeEQFilters applies the default Q factor to filters whose q is zero,
// reporting only the ones where Q is a setting the user could have filled in.
func (s *Settings) normalizeEQFilters(eq *EqualizerSettings, configPath string) {
	for i := range eq.Filters {
		f := &eq.Filters[i]
		if f.Q != 0 {
			continue
		}
		f.Q = DefaultEQQFactor
		if eq.Enabled && eqFilterUsesQ(f.Type) {
			s.recordValidationWarning(warnComponentAudio,
				"%s filter %d (%s) has no Q factor set; using the default %.3f",
				configPath, i+1, f.Type, DefaultEQQFactor)
		}
	}
}

// normalizeIntegrations disables integrations whose mandatory endpoint or
// credential was never filled in, and defaults the telemetry listen address.
func (s *Settings) normalizeIntegrations() {
	// BirdWeather uploads nothing without a usable station ID.
	// ValidateBirdweatherSettings rejects the same two shapes, which is what the
	// settings API should keep doing; clearing Enabled here means the load path
	// reaches that validator with the integration already switched off.
	bw := &s.Realtime.Birdweather
	if bw.Enabled {
		switch {
		case bw.ID == "":
			bw.Enabled = false
			s.recordValidationWarning(warnComponentBirdweather,
				"BirdWeather is enabled but no station ID is set; disabling uploads (set realtime.birdweather.id to use it)")
		case !birdweatherIDPattern.MatchString(bw.ID):
			bw.Enabled = false
			s.recordValidationWarning(warnComponentBirdweather,
				"BirdWeather is enabled but the station ID is not 24 alphanumeric characters; disabling uploads")
		}
	}

	// An MQTT client with no broker or no topic publishes nothing.
	mqtt := &s.Realtime.MQTT
	if mqtt.Enabled {
		switch {
		case strings.TrimSpace(mqtt.Broker) == "":
			mqtt.Enabled = false
			s.recordValidationWarning(warnComponentMQTT,
				"MQTT is enabled but no broker URL is set; disabling it (set realtime.mqtt.broker to use it)")
		case strings.TrimSpace(mqtt.Topic) == "":
			mqtt.Enabled = false
			s.recordValidationWarning(warnComponentMQTT,
				"MQTT is enabled but no topic is set; disabling it (set realtime.mqtt.topic to use it)")
		case mqtt.RetrySettings.Enabled && mqtt.RetrySettings.BackoffMultiplier == 0:
			mqtt.RetrySettings.BackoffMultiplier = DefaultRetryBackoffMultiplier
			s.recordValidationWarning(warnComponentMQTT,
				"MQTT retries are enabled but no backoff multiplier is set; using the default %.1f",
				DefaultRetryBackoffMultiplier)
		}
	}

	// An empty listen address is the never-written value and the endpoint has a
	// documented default, so bind it rather than refusing to start. Bound whether
	// or not telemetry is on, for the same reason the equalizer Q is: viper drops
	// this nested default when the parent key is present, and leaving the blank in
	// place would make a later enable from the settings UI fail on a field the
	// operator never saw. Only an enabled endpoint is worth reporting, though.
	telemetry := &s.Realtime.Telemetry
	if telemetry.Listen == "" {
		telemetry.Listen = DefaultTelemetryListen
		if telemetry.Enabled {
			s.recordValidationWarning(warnComponentTelemetry,
				"telemetry is enabled but no listen address is set; using the default %s", DefaultTelemetryListen)
		}
	}
}

// normalizeAudioExport fills in the export fields that a half-written export:
// block leaves at zero. realtime.audio.export is the section users hand-edit most,
// and viper drops a nested default whenever the parent key is present but the child
// is not, so an unfinished edit here is the likeliest way to meet this whole class
// of failure.
func (s *Settings) normalizeAudioExport() {
	export := &s.Realtime.Audio.Export
	if !export.Enabled {
		return
	}

	if export.Length == 0 {
		export.Length = DefaultAudioExportLength
		s.recordValidationWarning(warnComponentAudio,
			"audio export is enabled but no clip length is set; using the default %d seconds",
			DefaultAudioExportLength)
	}

	switch export.Type {
	case AudioExportTypeAAC, AudioExportTypeOPUS, AudioExportTypeMP3:
		if export.Bitrate == "" {
			export.Bitrate = DefaultAudioExportBitrate
			s.recordValidationWarning(warnComponentAudio,
				"audio export is enabled with the lossy format %s but no bitrate is set; using the default %s",
				export.Type, DefaultAudioExportBitrate)
		}
	}

	// Only targetLufs needs this: zero is outside its supported range, while zero
	// is a legal true peak and a legal loudness range.
	if export.Normalization.Enabled && export.Normalization.TargetLUFS == 0 {
		export.Normalization.TargetLUFS = DefaultNormalizationTargetLUFS
		s.recordValidationWarning(warnComponentAudio,
			"audio normalization is enabled but no target loudness is set; using the default %.1f LUFS",
			DefaultNormalizationTargetLUFS)
	}
}

// normalizeRetention fills in the retention limit belonging to the selected
// policy. An empty policy already means "no cleanup", so only the two active
// policies can be left half-configured.
func (s *Settings) normalizeRetention() {
	retention := &s.Realtime.Audio.Export.Retention
	switch retention.Policy {
	case RetentionPolicyAge:
		if retention.MaxAge == "" {
			retention.MaxAge = DefaultRetentionMaxAge
			s.recordValidationWarning(warnComponentAudio,
				"age-based retention is selected but no maximum age is set; using the default %s",
				DefaultRetentionMaxAge)
		}
	case RetentionPolicyUsage:
		if retention.MaxUsage == "" {
			retention.MaxUsage = DefaultRetentionMaxUsage
			s.recordValidationWarning(warnComponentAudio,
				"usage-based retention is selected but no maximum usage is set; using the default %s",
				DefaultRetentionMaxUsage)
		}
	}
}

// normalizeNotificationProviders disables push providers that are missing the one
// field their type cannot work without. A provider that cannot deliver anything is
// exactly the case where refusing to start costs a user their detections for no
// safety gain.
func (s *Settings) normalizeNotificationProviders() {
	push := &s.Notification.Push
	if !push.Enabled {
		return
	}

	for i := range push.Providers {
		p := &push.Providers[i]
		if !p.Enabled {
			continue
		}

		disable := func(format string, args ...any) {
			p.Enabled = false
			s.recordValidationWarning(warnComponentNotification,
				"push provider %q %s", p.Name, fmt.Sprintf(format, args...))
		}

		if reason := pushProviderProblem(p); reason != "" {
			disable("%s; disabling it", reason)
			continue
		}
		if strings.EqualFold(p.Type, pushProviderWebhook) {
			s.normalizeWebhookProvider(p, disable)
		}
	}
}

// normalizeWebhookProvider disables a webhook provider whose endpoint has no URL to
// post to, or declares an auth scheme without the credential it needs. Sending the
// request unauthenticated, or to nowhere, is not an option, so the provider is
// switched off and reported. The zero-endpoints case is handled by
// pushProviderProblem alongside the other per-type requirements.
func (s *Settings) normalizeWebhookProvider(p *PushProviderConfig, disable func(string, ...any)) {
	for i := range p.Endpoints {
		endpoint := &p.Endpoints[i]
		if strings.TrimSpace(endpoint.URL) == "" {
			disable("endpoint %d has no URL; disabling the provider", i)
			return
		}
		if reason := missingWebhookAuthSecret(&endpoint.Auth); reason != "" {
			disable("endpoint %d cannot authenticate: %s; disabling the provider", i, reason)
			return
		}
	}
}

// pushProviderProblem returns a description of the field a push provider's type
// cannot work without and does not have, or "" when the provider is configured. It
// is the single definition of "configured enough to deliver", shared by the fatal
// validator and by the normalization pass that switches an unusable provider off.
func pushProviderProblem(p *PushProviderConfig) string {
	switch strings.ToLower(p.Type) {
	case "":
		return "has no type set; set type to script, shoutrrr, or webhook"
	case pushProviderScript:
		if strings.TrimSpace(p.Command) == "" {
			return "is a script provider with no command"
		}
	case pushProviderShoutrrr:
		if len(p.URLs) == 0 {
			return "is a shoutrrr provider with no URLs"
		}
	case pushProviderWebhook:
		if len(p.Endpoints) == 0 {
			return "is a webhook provider with no endpoints"
		}
	default:
		return fmt.Sprintf("has unknown type %q", p.Type)
	}
	return ""
}

// missingWebhookAuthSecret returns a description of why an endpoint's auth
// configuration cannot be used, or "" when it can. An unrecognised auth type counts:
// the sender rejects it outright (see push_webhook.go), so the endpoint is dead in
// the same way a provider with an unrecognised type is, and the file's policy on
// values the runtime already ignores applies to both.
func missingWebhookAuthSecret(auth *WebhookAuthConfig) string {
	blank := func(values ...string) bool {
		for _, v := range values {
			if strings.TrimSpace(v) != "" {
				return false
			}
		}
		return true
	}

	switch strings.ToLower(auth.Type) {
	case webhookAuthBearer:
		if blank(auth.Token, auth.TokenFile) {
			return "no token or token_file is set"
		}
	case webhookAuthBasic:
		if blank(auth.User, auth.UserFile) {
			return "no user or user_file is set"
		}
		if blank(auth.Pass, auth.PassFile) {
			return "no pass or pass_file is set"
		}
	case webhookAuthCustom:
		if blank(auth.Header) {
			return "no header is set"
		}
		if blank(auth.Value, auth.ValueFile) {
			return "no value or value_file is set"
		}
	case "", webhookAuthNone:
		// No credential to check.
	default:
		return "its auth type is not one of bearer, basic, custom or none"
	}
	return ""
}

// normalizeRealtimeFeatures applies the documented default to enabled features
// whose interval or window was left at zero. Zero never means "immediately" or
// "no window" for any of these, it means the key was never written, so the
// alternative to defaulting is refusing to start over a field the user never saw.
// Non-zero values outside the supported range are left to the fatal validators.
func (s *Settings) normalizeRealtimeFeatures() {
	if sl := &s.Realtime.Audio.SoundLevel; sl.Enabled && sl.Interval == 0 {
		sl.Interval = DefaultSoundLevelInterval
		s.recordValidationWarning(warnComponentAudio,
			"sound level monitoring is enabled but no interval is set; using the default %d seconds",
			DefaultSoundLevelInterval)
	}

	if dt := &s.Realtime.DynamicThreshold; dt.Enabled && dt.ValidHours == 0 {
		dt.ValidHours = DefaultDynamicThresholdValidHours
		s.recordValidationWarning(warnComponentRealtime,
			"dynamic threshold is enabled but validHours is not set; using the default %d hours",
			DefaultDynamicThresholdValidHours)
	}

}

// normalizeSpeciesTracking applies the documented defaults to the species tracking
// windows and reset dates. Split out from normalizeRealtimeFeatures so its early
// returns cannot silently skip an unrelated feature added after it.
func (s *Settings) normalizeSpeciesTracking() {
	st := &s.Realtime.SpeciesTracking
	if !st.Enabled {
		return
	}
	if st.NewSpeciesWindowDays == 0 {
		st.NewSpeciesWindowDays = DefaultNewSpeciesWindowDays
		s.recordValidationWarning(warnComponentRealtime,
			"species tracking is enabled but no new-species window is set; using the default %d days",
			DefaultNewSpeciesWindowDays)
	}
	if st.SyncIntervalMinutes == 0 {
		st.SyncIntervalMinutes = DefaultSpeciesSyncIntervalMinutes
		s.recordValidationWarning(warnComponentRealtime,
			"species tracking is enabled but no sync interval is set; using the default %d minutes",
			DefaultSpeciesSyncIntervalMinutes)
	}

	// The month and the day are defaulted independently. Filling in both whenever
	// the month is missing would discard a reset day the user did write, which is
	// the one thing this whole pass exists not to do.
	if yt := &st.YearlyTracking; yt.Enabled {
		if yt.ResetMonth == 0 {
			yt.ResetMonth = DefaultYearlyTrackingResetMonth
			s.recordValidationWarning(warnComponentRealtime,
				"yearly tracking is enabled but no reset month is set; using month %d",
				DefaultYearlyTrackingResetMonth)
		}
		if yt.ResetDay == 0 {
			yt.ResetDay = DefaultYearlyTrackingResetDay
			s.recordValidationWarning(warnComponentRealtime,
				"yearly tracking is enabled but no reset day is set; using day %d",
				DefaultYearlyTrackingResetDay)
		}
		if yt.WindowDays == 0 {
			yt.WindowDays = DefaultYearlyTrackingWindowDays
			s.recordValidationWarning(warnComponentRealtime,
				"yearly tracking is enabled but no window is set; using the default %d days",
				DefaultYearlyTrackingWindowDays)
		}
	}

	seasonal := &st.SeasonalTracking
	if !seasonal.Enabled {
		return // nothing follows; keep any new feature out of this function
	}
	// An empty season map is filled in rather than treated as a reason to switch
	// the feature off. GetDefaultSeasons is the documented default, and it is the
	// same source Load uses for the hemisphere repair it applies after validation -
	// which this state never reached before, since validateSeasonalTrackingSettings
	// rejected an empty map outright. Disabling instead would also put the feature
	// out of reach of that repair, which is gated on Enabled and LocationConfigured.
	if len(seasonal.Seasons) == 0 {
		seasonal.Seasons = GetDefaultSeasons(s.BirdNET.Latitude)
		s.recordValidationWarning(warnComponentRealtime,
			"seasonal tracking is enabled but no seasons are defined; using the defaults for the configured hemisphere")
	}
	if seasonal.WindowDays == 0 {
		seasonal.WindowDays = DefaultSeasonalTrackingWindowDays
		s.recordValidationWarning(warnComponentRealtime,
			"seasonal tracking is enabled but no window is set; using the default %d days",
			DefaultSeasonalTrackingWindowDays)
	}
}

// normalizeWebServer binds the default port when the web server is enabled with
// none set. The web UI is the recovery path for every other misconfiguration, so
// refusing to start because its own port is blank is the least useful failure the
// application can produce.
func (s *Settings) normalizeWebServer() {
	if s.WebServer.Enabled && s.WebServer.Port == "" {
		s.WebServer.Port = DefaultWebServerPort
		s.recordValidationWarning(warnComponentWebServer,
			"the web server is enabled but no port is set; using the default %s", DefaultWebServerPort)
	}
}
