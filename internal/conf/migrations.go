package conf

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
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
	urlLower := strings.ToLower(url)

	switch {
	case strings.HasPrefix(urlLower, "rtsp://"), strings.HasPrefix(urlLower, "rtsps://"):
		return StreamTypeRTSP
	case strings.HasPrefix(urlLower, "rtmp://"), strings.HasPrefix(urlLower, "rtmps://"):
		return StreamTypeRTMP
	case strings.HasPrefix(urlLower, "udp://"), strings.HasPrefix(urlLower, "rtp://"):
		return StreamTypeUDP
	case strings.HasPrefix(urlLower, "http://"), strings.HasPrefix(urlLower, "https://"):
		// Check for HLS (.m3u8) vs generic HTTP
		if strings.Contains(urlLower, ".m3u8") {
			return StreamTypeHLS
		}
		return StreamTypeHTTP
	default:
		return StreamTypeRTSP // Default to RTSP for unknown schemes
	}
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
// fallback during early config loading.
func (s *Settings) ValidateModelConfig(knownIDs map[string]bool) []string {
	var issues []string

	enabledSet := make(map[string]bool, len(s.Models.Enabled))
	for _, id := range s.Models.Enabled {
		enabledSet[strings.ToLower(id)] = true
	}

	for _, id := range s.Models.Enabled {
		if !knownIDs[strings.ToLower(id)] {
			issues = append(issues, "warning: unknown model ID in models.enabled: "+id)
		}
	}

	if enabledSet[ModelIDPerchV2] && !s.Perch.Enabled {
		issues = append(issues, "error: "+ModelIDPerchV2+" in models.enabled but perch.enabled is false")
	}

	if s.Perch.Enabled && !enabledSet[ModelIDPerchV2] {
		issues = append(issues, "warning: perch.enabled is true but '"+ModelIDPerchV2+"' is not in models.enabled")
	}

	if s.Perch.Enabled {
		if s.Perch.ModelPath == "" {
			issues = append(issues, "error: perch.enabled is true but perch.modelpath is empty")
		}
		if s.Perch.LabelPath == "" {
			issues = append(issues, "error: perch.enabled is true but perch.labelpath is empty")
		}
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
	knownIDs := map[string]bool{ModelIDBirdNET: true, ModelIDPerchV2: true, ModelIDBat: true}
	modelIssues := s.ValidateModelConfig(knownIDs)
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
