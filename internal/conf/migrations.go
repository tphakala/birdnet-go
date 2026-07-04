package conf

import (
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/spf13/viper"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// persistMigration saves the config file after a successful migration.
func persistMigration(settings *Settings, label string) {
	configFile := viper.ConfigFileUsed()
	if configFile == "" {
		return
	}
	if err := SaveYAMLConfig(configFile, settings); err != nil {
		GetLogger().Warn("Failed to save migrated "+label+" config", logger.Error(err))
	} else {
		GetLogger().Info("Saved migrated "+label+" configuration", logger.String("path", configFile))
	}
}

// migrateStreamEnabledDefaults materializes missing enabled fields for legacy
// RTSP stream configs before viper unmarshals them into the strongly typed
// settings struct.
func migrateStreamEnabledDefaults() bool {
	normalizedStreams, migrated := normalizeRTSPStreamEnabledDefaults(viper.Get("realtime.rtsp.streams"))
	if !migrated {
		return false
	}

	viper.Set("realtime.rtsp.streams", normalizedStreams)
	return true
}

// ensureSessionSecret backfills and persists security.sessionsecret for older
// configs that predate the field so session signing remains stable across
// restarts. Unlike the security package's temporary runtime fallback, this
// updates the loaded settings and best-effort saves the generated secret.
func ensureSessionSecret(settings *Settings) error {
	if settings.Security.SessionSecret != "" {
		return nil
	}

	sessionSecret, err := GenerateRandomSecret()
	if err != nil {
		return errors.New(err).
			Component("conf").
			Category(errors.CategoryConfiguration).
			Context("operation", "generate_session_secret").
			Build()
	}

	settings.Security.SessionSecret = sessionSecret

	// Also set it in viper so it gets saved to config file
	viper.Set("security.sessionsecret", sessionSecret)

	// Log that we generated a new session secret
	GetLogger().Info("Generated new SessionSecret for existing configuration")

	// Save the updated config back to file to persist the generated secret
	// This ensures the secret remains the same across restarts
	configFile := viper.ConfigFileUsed()
	if configFile == "" {
		return nil
	}

	if err := SaveYAMLConfig(configFile, settings); err != nil {
		// Log the error but don't fail - the generated secret will work for this session
		GetLogger().Warn("Failed to save generated SessionSecret to config file", logger.Error(err))
		return nil
	}

	// Set secure file permissions after saving
	if err := os.Chmod(configFile, 0o600); err != nil {
		GetLogger().Warn("Failed to set secure permissions on config file", logger.Error(err))
	}

	return nil
}

// migrateLegacyProvider converts a legacy SocialProvider to the new OAuthProviderConfig format.
// Returns nil if the legacy provider is not configured (no ClientID).
func migrateLegacyProvider(providerName string, legacy SocialProvider) *OAuthProviderConfig {
	if legacy.ClientID == "" {
		return nil
	}
	return &OAuthProviderConfig{
		Provider:     providerName,
		Enabled:      legacy.Enabled,
		ClientID:     legacy.ClientID,
		ClientSecret: legacy.ClientSecret,
		RedirectURI:  legacy.RedirectURI,
		UserID:       legacy.UserId,
	}
}

// MigrateTLSConfig migrates the legacy AutoTLS boolean to the new TLSMode field.
// If TLSMode is already set, migration is skipped (already migrated or user has
// explicitly configured the new field). Returns true if migration occurred.
func (s *Settings) MigrateTLSConfig() bool {
	// Skip if TLSMode is already set (already migrated or explicitly configured)
	if s.Security.TLSMode != TLSModeNone {
		return false
	}

	// Migrate AutoTLS=true to TLSMode="autotls"
	if s.Security.AutoTLS {
		s.Security.TLSMode = TLSModeAutoTLS
		return true
	}

	return false
}

// MigrateOAuthConfig migrates legacy OAuth configuration (GoogleAuth, GithubAuth, MicrosoftAuth)
// to the new OAuthProviders array format. This migration:
// - Skips if OAuthProviders already has entries (already migrated)
// - Only migrates providers that have a ClientID configured
// - Preserves all settings from the legacy format
// - Returns true if migration occurred, false if skipped
func (s *Settings) MigrateOAuthConfig() bool {
	// Skip if already migrated (new array has entries)
	if len(s.Security.OAuthProviders) > 0 {
		return false
	}

	// Define legacy providers to migrate
	legacyProviders := []struct {
		name   string
		config SocialProvider
	}{
		{"google", s.Security.GoogleAuth},
		{"github", s.Security.GithubAuth},
		{"microsoft", s.Security.MicrosoftAuth},
	}

	var migrated bool
	for _, legacy := range legacyProviders {
		if cfg := migrateLegacyProvider(legacy.name, legacy.config); cfg != nil {
			s.Security.OAuthProviders = append(s.Security.OAuthProviders, *cfg)
			migrated = true
			GetLogger().Info("Migrated OAuth configuration to new format", logger.String("provider", legacy.name))
		}
	}

	if migrated {
		GetLogger().Info("OAuth configuration migration complete", logger.String("note", "Legacy fields will be ignored"))
	}

	return migrated
}

// inferStreamType detects the stream type from URL scheme.
// Returns StreamTypeRTSP as default for unknown schemes.
func inferStreamType(url string) string {
	// Delegate to the strict scheme classifier and add the legacy default:
	// unknown schemes fall back to RTSP (streamTypeForURL returns isStream=false
	// for them). Sharing one scheme switch keeps the two classifiers from drifting.
	if streamType, ok := streamTypeForURL(url); ok {
		return streamType
	}
	return StreamTypeRTSP // Default to RTSP for unknown schemes
}

// MigrateRTSPConfig migrates legacy URLs []string to Streams []StreamConfig.
// This migration:
// - Skips if Streams already has entries (already migrated)
// - Only migrates if URLs has data
// - Trims whitespace and skips empty URLs
// - Infers stream type from URL scheme
// - Preserves the global Transport setting for RTSP/RTMP streams
// - Returns true if migration occurred, false if skipped
func (s *Settings) MigrateRTSPConfig() bool {
	rtsp := &s.Realtime.RTSP

	// Skip if already migrated (new format has streams)
	if len(rtsp.Streams) > 0 {
		return false
	}

	// Skip if no legacy URLs to migrate
	if len(rtsp.URLs) == 0 {
		return false
	}

	// Get global transport, default to tcp
	globalTransport := rtsp.Transport
	if globalTransport == "" {
		globalTransport = DefaultTransport
	}

	// Preallocate streams slice with capacity and track seen URLs for deduplication
	rtsp.Streams = make([]StreamConfig, 0, len(rtsp.URLs))
	seenURLs := make(map[string]bool)
	streamIndex := 0

	// Migrate each URL to StreamConfig
	for _, rawURL := range rtsp.URLs {
		// Trim whitespace and skip empty URLs
		url := strings.TrimSpace(rawURL)
		if url == "" {
			continue
		}

		// Skip duplicate URLs to ensure valid configuration
		if seenURLs[url] {
			continue
		}
		seenURLs[url] = true
		streamIndex++

		// Infer stream type from URL scheme
		streamType := inferStreamType(url)

		// Only apply transport setting to RTSP/RTMP types where it makes sense
		transport := ""
		if streamType == StreamTypeRTSP || streamType == StreamTypeRTMP {
			transport = globalTransport
		}

		stream := StreamConfig{
			Name:      fmt.Sprintf("Stream %d", streamIndex),
			URL:       url,
			Enabled:   true,
			Type:      streamType,
			Transport: transport,
		}
		rtsp.Streams = append(rtsp.Streams, stream)
	}

	// If no valid URLs were found, don't mark as migrated
	if len(rtsp.Streams) == 0 {
		return false
	}

	// Clear legacy fields
	rtsp.URLs = nil
	rtsp.Transport = ""

	GetLogger().Info("Migrated RTSP configuration to new streams format",
		logger.Int("stream_count", len(rtsp.Streams)))

	return true
}

// MigrateAudioSourceConfig migrates the legacy single audio source string
// (realtime.audio.source) to the new multi-source array format (realtime.audio.sources).
// This follows the same pattern as MigrateRTSPConfig.
func (s *Settings) MigrateAudioSourceConfig() bool {
	audio := &s.Realtime.Audio

	// Skip if already migrated (new format has sources) OR nothing to migrate.
	// Note: Viper unmarshals explicit `sources: []` as empty slice (not nil),
	// so we check both conditions to avoid re-migrating.
	if len(audio.Sources) > 0 || strings.TrimSpace(audio.Source) == "" {
		return false
	}

	// Create one AudioSourceConfig from the legacy scalar
	audio.Sources = []AudioSourceConfig{{
		Name:       "Sound Card 1",
		Device:     strings.TrimSpace(audio.Source),
		Gain:       0,
		QuietHours: audio.QuietHours, // Carry over global quiet hours
	}}

	// Clear legacy field
	audio.Source = ""

	GetLogger().Info("Migrated audio source to new multi-source format",
		logger.Int("source_count", 1))

	return true
}

// MigrateSourceModels migrates the legacy singular Model field to the new
// Models list on AudioSourceConfig and StreamConfig. Sources with neither
// Model nor Models set default to ["birdnet"]. Returns true if any migration
// occurred.
func (s *Settings) MigrateSourceModels() bool {
	migrated := false

	for i := range s.Realtime.Audio.Sources {
		src := &s.Realtime.Audio.Sources[i]
		if len(src.Models) > 0 {
			continue
		}
		if src.Model != "" {
			src.Models = []string{src.Model}
			src.Model = ""
		} else {
			src.Models = []string{ModelIDBirdNET}
		}
		migrated = true
	}

	for _, stream := range s.Realtime.RTSP.AllStreams() {
		if len(stream.Models) > 0 {
			continue
		}
		stream.Models = []string{ModelIDBirdNET}
		migrated = true
	}

	return migrated
}

// normalizeRTSPStreamEnabledDefaults materializes enabled=true for legacy raw
// RTSP stream entries that predate the field. It operates on Viper's raw data
// before unmarshal so StreamConfig can keep a plain bool.
func normalizeRTSPStreamEnabledDefaults(rawStreams any) ([]any, bool) {
	streams, ok := rawStreams.([]any)
	if !ok {
		return nil, false
	}

	normalized := make([]any, len(streams))
	migrated := false

	for i, rawStream := range streams {
		streamMap, ok := rawStream.(map[string]any)
		if !ok {
			normalized[i] = rawStream
			continue
		}
		if val, exists := streamMap["enabled"]; exists && val != nil {
			normalized[i] = rawStream
			continue
		}

		copied := make(map[string]any, len(streamMap)+1)
		for key, value := range streamMap {
			copied[key] = value
		}
		copied["enabled"] = true
		normalized[i] = copied
		migrated = true
	}

	if !migrated {
		return nil, false
	}

	return normalized, true
}

// ValidateModelConfig checks model-related configuration for errors and
// warnings. Returns a slice of warning/error strings. Fatal errors are
// prefixed with "error:"; non-fatal issues with "warning:".
// knownIDs is the set of recognized config-level model identifiers; callers
// should pass classifier.KnownConfigIDs() when available, or a hardcoded
// fallback during early config loading. When checkSourceRefs is true,
// sources and streams are validated against models.enabled; set to false
// during early loading before ScanInstalled has synced the enabled list.
func (s *Settings) ValidateModelConfig(knownIDs map[string]bool, checkSourceRefs bool) []string {
	var issues []string

	for _, id := range s.Models.Enabled {
		if !knownIDs[strings.ToLower(id)] {
			issues = append(issues, "warning: unknown model ID in models.enabled: "+id)
		}
	}

	// During early config loading, models.enabled is not yet synchronized
	// with gallery-installed models (ScanInstalled runs later), so these
	// checks produce false positives. The orchestrator re-validates with
	// the authoritative model list after startup sync is complete.
	if checkSourceRefs {
		enabledSet := make(map[string]bool, len(s.Models.Enabled))
		for _, id := range s.Models.Enabled {
			enabledSet[strings.ToLower(id)] = true
		}
		for i := range s.Realtime.Audio.Sources {
			src := &s.Realtime.Audio.Sources[i]
			for _, modelID := range src.Models {
				if !enabledSet[strings.ToLower(modelID)] {
					issues = append(issues, "warning: source \""+src.Name+"\" references model \""+modelID+"\" not in models.enabled")
				}
			}
		}
		for _, stream := range s.Realtime.RTSP.AllStreams() {
			for _, modelID := range stream.Models {
				if !enabledSet[strings.ToLower(modelID)] {
					issues = append(issues, "warning: stream \""+stream.Name+"\" references model \""+modelID+"\" not in models.enabled")
				}
			}
		}
	}

	return issues
}

// applyModelValidation runs ValidateModelConfig and either returns an error
// for fatal issues or appends warnings to ValidationWarnings. All fatal
// errors are collected and returned together so the user can fix them in
// one pass.
func (s *Settings) applyModelValidation() error {
	// Default known IDs - matches classifier.KnownConfigIDs() at compile time.
	// This fallback is used during config loading before the classifier package
	// is available. The orchestrator re-validates with the authoritative list.
	knownIDs := map[string]bool{ModelIDBirdNET: true, ModelIDPerchV2: true, ModelIDBat: true, ModelIDBSG: true}
	modelIssues := s.ValidateModelConfig(knownIDs, false)
	var fatalErrors []string
	for _, issue := range modelIssues {
		if strings.HasPrefix(issue, "error:") {
			fatalErrors = append(fatalErrors, strings.TrimPrefix(issue, "error: "))
		} else {
			GetLogger().Warn("model configuration issue", logger.String("issue", issue))
			s.ValidationWarnings = append(s.ValidationWarnings, issue)
		}
	}
	if len(fatalErrors) > 0 {
		return errors.Newf("model configuration: %s", strings.Join(fatalErrors, "; ")).
			Category(errors.CategoryValidation).
			Build()
	}
	return nil
}

// MigrateLocationConfigured sets LocationConfigured to true for existing configs
// that have non-zero coordinates but predate the explicit flag. This provides
// backward compatibility so that existing users don't lose location-dependent
// features after upgrading. New installations start with LocationConfigured=false
// until the user explicitly sets coordinates (even 0,0).
// Returns true when the flag was flipped (caller is responsible for persistence).
func (s *Settings) MigrateLocationConfigured() bool {
	if s.BirdNET.LocationConfigured {
		return false
	}

	if s.BirdNET.Latitude == 0 && s.BirdNET.Longitude == 0 {
		return false
	}

	s.BirdNET.LocationConfigured = true
	return true
}

// streamTypeForURL strictly classifies device as a network stream URL. It
// returns a StreamType* constant and true only for recognized stream schemes;
// unlike inferStreamType it never defaults an unknown or scheme-less value to
// RTSP, so sound-card device names (hw:, plughw:, sysdefault, default, dsnoop,
// "Loopback", bare names) and file paths classify as ("", false). Scheme
// matching is case-insensitive and leading/trailing whitespace is trimmed
// first.
func streamTypeForURL(device string) (streamType string, isStream bool) {
	lower := strings.ToLower(strings.TrimSpace(device))

	switch {
	case strings.HasPrefix(lower, "rtsp://"), strings.HasPrefix(lower, "rtsps://"):
		return StreamTypeRTSP, true
	case strings.HasPrefix(lower, "rtmp://"), strings.HasPrefix(lower, "rtmps://"):
		return StreamTypeRTMP, true
	case strings.HasPrefix(lower, "udp://"), strings.HasPrefix(lower, "rtp://"):
		return StreamTypeUDP, true
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"):
		if strings.Contains(lower, ".m3u8") {
			return StreamTypeHLS, true
		}
		return StreamTypeHTTP, true
	default:
		return "", false
	}
}

// ReconcileMisplacedAudioSources moves network stream URLs that were
// misconfigured under realtime.audio.sources (meant for local sound cards)
// into realtime.rtsp.streams, where the runtime opens them with FFmpeg. A
// stream URL left in audio.sources is otherwise opened as an ALSA device,
// which fails and breaks live audio.
//
// For each audio source whose device is a recognized stream URL:
//   - If no rtsp.streams entry already has that URL, a new StreamConfig is
//     appended carrying the source's per-source overrides (gain, models,
//     equalizer, quiet hours) and the source entry is removed.
//   - If an rtsp.streams entry already has that URL, non-default per-source
//     fields are lightly merged into the existing stream without overwriting
//     values the stream already sets, and the duplicate source entry is
//     removed.
//
// Credentials embedded in a URL are preserved verbatim in the config;
// sanitized URLs are used only in the recorded ValidationWarnings. The
// function is idempotent: once every stream URL has been relocated, a second
// call finds nothing to do and returns false. Returns true when at least one
// source entry was moved or merged and removed.
func (s *Settings) ReconcileMisplacedAudioSources() bool {
	sources := s.Realtime.Audio.Sources
	if len(sources) == 0 {
		return false
	}

	changed := false
	survivors := make([]AudioSourceConfig, 0, len(sources))

	for i := range sources {
		src := &sources[i]
		device := strings.TrimSpace(src.Device)
		streamType, isStream := streamTypeForURL(device)
		if !isStream {
			survivors = append(survivors, *src)
			continue
		}

		if existing := s.findStreamByURL(device); existing != nil {
			s.mergeSourceIntoStream(src, existing, device)
		} else {
			s.appendStreamFromSource(src, device, streamType)
		}

		if src.SampleRate > 0 {
			s.ValidationWarnings = append(s.ValidationWarnings,
				fmt.Sprintf("audio source %q sample rate %d Hz was not carried to stream %q: stream sample rate is auto-detected",
					src.Name, src.SampleRate, privacy.SanitizeStreamUrl(device)))
		}

		changed = true
	}

	if !changed {
		return false
	}

	s.Realtime.Audio.Sources = survivors

	GetLogger().Info("Reconciled misplaced stream URLs from audio.sources into rtsp.streams",
		logger.Int("stream_count", len(s.Realtime.RTSP.Streams)),
		logger.Int("remaining_sources", len(survivors)))

	return true
}

// findStreamByURL returns a pointer to the first configured stream whose URL
// matches the given already-trimmed URL, or nil when none match. The compare
// trims whitespace on the stored URL so cosmetic differences do not defeat
// deduplication.
func (s *Settings) findStreamByURL(url string) *StreamConfig {
	for i := range s.Realtime.RTSP.Streams {
		if strings.TrimSpace(s.Realtime.RTSP.Streams[i].URL) == url {
			return &s.Realtime.RTSP.Streams[i]
		}
	}
	return nil
}

// appendStreamFromSource creates a new StreamConfig from a misplaced audio
// source and appends it to the RTSP streams. Credentials in url are preserved
// verbatim; only the ValidationWarnings note uses a sanitized URL. The stream
// name is made unique and length-bounded so the migration never produces a
// config the stream validator would reject at load time.
func (s *Settings) appendStreamFromSource(src *AudioSourceConfig, url, streamType string) {
	name := s.uniqueStreamName(src.Name)

	// Transport only applies to connection-oriented stream types.
	transport := ""
	if streamType == StreamTypeRTSP || streamType == StreamTypeRTMP {
		transport = DefaultTransport
	}

	s.Realtime.RTSP.Streams = append(s.Realtime.RTSP.Streams, StreamConfig{
		Name:       name,
		URL:        url,
		Enabled:    true,
		Type:       streamType,
		Transport:  transport,
		Gain:       src.Gain,
		Equalizer:  src.Equalizer,
		QuietHours: src.QuietHours,
		Models:     src.Models,
	})

	s.ValidationWarnings = append(s.ValidationWarnings,
		fmt.Sprintf("moved misplaced stream URL %q from realtime.audio.sources to realtime.rtsp.streams as %q",
			privacy.SanitizeStreamUrl(url), name))
}

// uniqueStreamName derives a stream name from the requested source name that
// the stream validator will accept at load time: unique (case-insensitively)
// among the already-configured streams and within MaxStreamNameLength. An empty
// requested name falls back to "Stream N" (N based on the current stream count).
// An over-long name is truncated on a UTF-8 rune boundary; a name that collides
// gets an incrementing " (2)", " (3)" suffix (with the base truncated further to
// keep room for the suffix) until a free name is found. A misplaced source name
// may be up to MaxAudioSourceNameLength, longer than a stream name is allowed to
// be, so both the length clamp and the dedup are needed to keep
// ReconcileMisplacedAudioSources from producing a name the validator would
// reject with a fatal error at load time.
func (s *Settings) uniqueStreamName(requested string) string {
	base := strings.TrimSpace(requested)
	if base == "" {
		base = fmt.Sprintf("Stream %d", len(s.Realtime.RTSP.Streams)+1)
	}
	candidate := truncateStreamName(base, MaxStreamNameLength)
	for n := 2; s.streamNameTaken(candidate); n++ {
		suffix := fmt.Sprintf(" (%d)", n)
		candidate = truncateStreamName(base, MaxStreamNameLength-len(suffix)) + suffix
	}
	return candidate
}

// truncateStreamName shortens name to at most maxBytes bytes on a UTF-8 rune
// boundary (never splitting a multi-byte rune), trimming any trailing spaces
// left by the cut. Stream names are byte-length-limited by the validator, so
// this keeps a long source name from yielding a name the validator rejects.
func truncateStreamName(name string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(name) <= maxBytes {
		return name
	}
	// range over a string yields the byte index at each rune start; keep the
	// largest start that still fits so the cut never lands inside a rune.
	end := 0
	for i := range name {
		if i > maxBytes {
			break
		}
		end = i
	}
	return strings.TrimRight(name[:end], " ")
}

// streamNameTaken reports whether any configured stream already uses name,
// compared case-insensitively with surrounding whitespace trimmed, matching the
// duplicate-name rule the stream validator enforces.
func (s *Settings) streamNameTaken(name string) bool {
	for i := range s.Realtime.RTSP.Streams {
		if strings.EqualFold(strings.TrimSpace(s.Realtime.RTSP.Streams[i].Name), name) {
			return true
		}
	}
	return false
}

// mergeSourceIntoStream lightly merges non-default per-source overrides from a
// misplaced audio source into an existing stream that already has the same URL.
// Stream values that are already set are never overwritten. A ValidationWarnings
// note records which fields were applied and which were kept as-is because the
// stream already carried a non-default value.
func (s *Settings) mergeSourceIntoStream(src *AudioSourceConfig, stream *StreamConfig, url string) {
	var applied, kept []string

	if src.Gain != 0 {
		if stream.Gain == 0 {
			stream.Gain = src.Gain
			applied = append(applied, "gain")
		} else {
			kept = append(kept, "gain")
		}
	}

	if len(src.Models) > 0 {
		if len(stream.Models) == 0 {
			stream.Models = src.Models
			applied = append(applied, "models")
		} else {
			kept = append(kept, "models")
		}
	}

	if src.Equalizer != nil {
		if stream.Equalizer == nil {
			stream.Equalizer = src.Equalizer
			applied = append(applied, "equalizer")
		} else {
			kept = append(kept, "equalizer")
		}
	}

	// QuietHours merges only when the struct is safely comparable to its zero
	// value. If QuietHoursConfig ever gains a non-comparable field, skip the
	// merge and record it rather than risking a reflect panic or losing data.
	switch {
	case !quietHoursComparable():
		kept = append(kept, "quietHours (QuietHoursConfig not comparable)")
	case reflect.ValueOf(src.QuietHours).IsZero():
		// Source has no quiet-hours override to contribute.
	case reflect.ValueOf(stream.QuietHours).IsZero():
		stream.QuietHours = src.QuietHours
		applied = append(applied, "quietHours")
	default:
		kept = append(kept, "quietHours")
	}

	s.ValidationWarnings = append(s.ValidationWarnings,
		formatReconcileMergeWarning(privacy.SanitizeStreamUrl(url), stream.Name, applied, kept))
}

// quietHoursComparable reports whether QuietHoursConfig can be compared to its
// zero value. Merging quiet hours relies on a zero-value check; if the struct
// ever gains a non-comparable field this returns false so the merge is skipped
// rather than risking a runtime panic.
func quietHoursComparable() bool {
	return reflect.TypeOf(QuietHoursConfig{}).Comparable()
}

// formatReconcileMergeWarning builds the ValidationWarnings note for a duplicate
// source merged into an existing stream. sanitizedURL must already be stripped
// of credentials.
func formatReconcileMergeWarning(sanitizedURL, streamName string, applied, kept []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "merged misplaced audio source for stream URL %q into existing stream %q", sanitizedURL, streamName)
	if len(applied) > 0 {
		fmt.Fprintf(&b, "; applied: %s", strings.Join(applied, ", "))
	}
	if len(kept) > 0 {
		fmt.Fprintf(&b, "; kept existing stream values for: %s", strings.Join(kept, ", "))
	}
	return b.String()
}
