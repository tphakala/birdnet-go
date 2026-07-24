// conf/validate_services.go

package conf

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"text/template"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/templatefuncs"
)

// Push notification provider types.
const (
	pushProviderScript   = "script"
	pushProviderShoutrrr = "shoutrrr"
	pushProviderWebhook  = "webhook"
)

// Webhook endpoint authentication types. An empty type is equivalent to
// webhookAuthNone.
const (
	webhookAuthNone   = "none"
	webhookAuthBearer = "bearer"
	webhookAuthBasic  = "basic"
	webhookAuthCustom = "custom"
)

// ValidateBirdNETSettings performs BirdNET validation without side effects.
// Returns normalized settings and any errors/warnings.
// This pure function enables testing without log output or settings mutation.
//
// The private validateBirdNETSettings() calls this and handles side effects.
func ValidateBirdNETSettings(cfg *BirdNETConfig) ValidationResult {
	if cfg == nil {
		return ValidationResult{Valid: false, Errors: []string{"BirdNET config is nil"}}
	}
	result := ValidationResult{Valid: true, Warnings: []string{}}
	normalized := *cfg

	checkRange(&result, cfg.Sensitivity, 0, 1.5, "BirdNET sensitivity must be between 0 and 1.5")
	checkRange(&result, cfg.Threshold, 0, 1, "BirdNET threshold must be between 0 and 1")
	checkRange(&result, cfg.Overlap, 0, 2.99, "BirdNET overlap value must be between 0 and 2.99 seconds")
	checkRange(&result, cfg.Longitude, -180, 180, "BirdNET longitude must be between -180 and 180")
	checkRange(&result, cfg.Latitude, -90, 90, "BirdNET latitude must be between -90 and 90")

	if cfg.Threads < 0 {
		result.Valid = false
		result.Errors = append(result.Errors, "BirdNET threads must be at least 0")
	}

	// Empty string, "latest", "legacy", or "v3" are valid
	if cfg.RangeFilter.Model != "" && cfg.RangeFilter.Model != RangeFilterModelLatest && cfg.RangeFilter.Model != RangeFilterModelLegacy && cfg.RangeFilter.Model != RangeFilterModelV3 {
		result.Valid = false
		result.Errors = append(result.Errors, "RangeFilter model must be either empty (v2 default), 'latest', 'legacy', or 'v3'")
	}

	// Backend must be one of the known preference strings when non-empty.
	// Unknown values fall back safely to auto, so this is a warning, not an error.
	if cfg.Backend != "" && cfg.Backend != BackendPrefAuto && cfg.Backend != BackendPrefONNX && cfg.Backend != BackendPrefOpenVINO {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("BirdNET backend '%s' is not recognised; must be 'auto', 'onnx', or 'openvino' - will use 'auto'", cfg.Backend))
	}

	// OpenVINODevice must be one of the known device strings when non-empty.
	// Unknown values fall back safely to auto, so this is a warning, not an error.
	if cfg.OpenVINODevice != "" && cfg.OpenVINODevice != OVDeviceAuto && cfg.OpenVINODevice != OVDeviceCPU && cfg.OpenVINODevice != OVDeviceGPU {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("BirdNET openvinodevice '%s' is not recognised; must be 'auto', 'cpu', or 'gpu' - will use 'auto'", cfg.OpenVINODevice))
	}

	checkRange(&result, cfg.RangeFilter.Threshold, 0, 1, "RangeFilter threshold must be between 0 and 1")

	// Locale validation and normalization (pure transformation)
	if cfg.Locale != "" {
		normalizedLocale, err := NormalizeLocale(cfg.Locale)
		if err != nil {
			// Locale normalization fell back to default - this is a warning, not an error
			message := fmt.Sprintf("BirdNET locale '%s' is not supported, will use fallback '%s'", cfg.Locale, normalizedLocale)
			result.Warnings = append(result.Warnings, message)
		}
		// Update the normalized locale
		normalized.Locale = normalizedLocale
	}

	result.Normalized = &normalized
	return result
}

// ValidateBirdweatherSettings performs Birdweather validation without side effects.
// Returns validation result with normalized settings.
func ValidateBirdweatherSettings(settings *BirdweatherSettings) ValidationResult {
	if settings == nil {
		return ValidationResult{Valid: false, Errors: []string{"Birdweather settings is nil"}}
	}
	result := ValidationResult{Valid: true}
	normalized := *settings

	if settings.Enabled {
		// An unusable station ID is an error here and only an error. It used to be
		// reported as an error AND as a "will be disabled" warning that cleared
		// Enabled on the normalized copy, which meant one condition carried two
		// contradictory severities: the config was rejected, and the rejection
		// claimed the integration had been switched off instead.
		//
		// The graceful half now lives in normalizeIncompleteFeatures, which runs
		// before this validator on the load path and clears Enabled, so an aged
		// config file no longer blocks startup. Keeping the error is what stops the
		// settings API from accepting a bad ID, silently switching BirdWeather off
		// and saving that back over the user's own toggle.
		switch {
		case settings.ID == "":
			result.Valid = false
			result.Errors = append(result.Errors, "Birdweather ID is required when enabled")
		case !birdweatherIDPattern.MatchString(settings.ID):
			// Validate Birdweather ID format using precompiled regex
			result.Valid = false
			result.Errors = append(result.Errors, "Invalid Birdweather ID format: must be 24 alphanumeric characters")
		}

		checkRange(&result, settings.Threshold, 0, 1, "birdweather threshold must be between 0 and 1")

		if settings.LocationAccuracy < 0 {
			result.Valid = false
			result.Errors = append(result.Errors, "birdweather location accuracy must be non-negative")
		}
	}

	result.Normalized = &normalized
	return result
}

// ValidateWebhookProvider performs webhook provider validation without side effects.
// Returns validation result with errors.
func ValidateWebhookProvider(p *PushProviderConfig) ValidationResult {
	if p == nil {
		return ValidationResult{Valid: false, Errors: []string{"webhook provider config is nil"}}
	}
	result := ValidationResult{Valid: true}

	if !p.Enabled {
		result.Normalized = p
		return result
	}

	// Webhook requires at least one endpoint
	if len(p.Endpoints) == 0 {
		result.Valid = false
		result.Errors = append(result.Errors,
			fmt.Sprintf("webhook provider '%s' requires at least one endpoint when enabled", p.Name))
		result.Normalized = p
		return result
	}

	// Validate custom template if specified.
	// Use the shared templatefuncs.Funcs so Parse() accepts custom functions
	// like "title" or "formatTime".
	if p.Template != "" {
		if _, err := template.New("validation").Funcs(templatefuncs.Funcs).Parse(p.Template); err != nil {
			result.Valid = false
			result.Errors = append(result.Errors,
				fmt.Sprintf("webhook provider '%s': invalid template syntax: %v", p.Name, err))
		}
	}

	// Validate each endpoint
	for i := range p.Endpoints {
		endpoint := &p.Endpoints[i]

		// URL is required
		if strings.TrimSpace(endpoint.URL) == "" {
			result.Valid = false
			result.Errors = append(result.Errors,
				fmt.Sprintf("webhook provider '%s' endpoint %d: URL is required", p.Name, i))
			continue
		}

		// URL must start with http:// or https://
		if !strings.HasPrefix(endpoint.URL, "http://") && !strings.HasPrefix(endpoint.URL, "https://") {
			result.Valid = false
			result.Errors = append(result.Errors,
				fmt.Sprintf("webhook provider '%s' endpoint %d: URL must start with http:// or https://", p.Name, i))
		}

		// Validate HTTP method if specified
		if endpoint.Method != "" {
			method := strings.ToUpper(endpoint.Method)
			if method != "POST" && method != "PUT" && method != "PATCH" {
				result.Valid = false
				result.Errors = append(result.Errors,
					fmt.Sprintf("webhook provider '%s' endpoint %d: method must be POST, PUT, or PATCH, got %s", p.Name, i, endpoint.Method))
			}
		}

		// Validate timeout
		if endpoint.Timeout < 0 {
			result.Valid = false
			result.Errors = append(result.Errors,
				fmt.Sprintf("webhook provider '%s' endpoint %d: timeout must be non-negative", p.Name, i))
		}
	}

	result.Normalized = p
	return result
}

// ValidateMQTTSettings performs MQTT validation.
// Trims Broker and Topic in-place before checking.
// Returns validation result with errors.
func ValidateMQTTSettings(settings *MQTTSettings) ValidationResult {
	if settings == nil {
		return ValidationResult{Valid: false, Errors: []string{"MQTT settings is nil"}}
	}
	result := ValidationResult{Valid: true}

	if !settings.Enabled {
		result.Normalized = settings
		return result
	}

	// Normalize fields in-place so downstream code sees trimmed values.
	settings.Broker = strings.TrimSpace(settings.Broker)
	settings.Topic = strings.TrimSpace(settings.Topic)

	// Check if broker is provided when enabled
	if settings.Broker == "" {
		result.Valid = false
		result.Errors = append(result.Errors, "MQTT broker URL is required when MQTT is enabled")
	}

	// Check if topic is provided when enabled
	if settings.Topic == "" {
		result.Valid = false
		result.Errors = append(result.Errors, "MQTT topic is required when MQTT is enabled")
	}

	// Validate retry settings if enabled
	if settings.RetrySettings.Enabled {
		if settings.RetrySettings.MaxRetries < 0 {
			result.Valid = false
			result.Errors = append(result.Errors, "MQTT max retries must be non-negative")
		}
		if settings.RetrySettings.InitialDelay < 0 {
			result.Valid = false
			result.Errors = append(result.Errors, "MQTT initial delay must be non-negative")
		}
		if settings.RetrySettings.MaxDelay < settings.RetrySettings.InitialDelay {
			result.Valid = false
			result.Errors = append(result.Errors, "MQTT max delay must be greater than or equal to initial delay")
		}
		if settings.RetrySettings.BackoffMultiplier <= 0 {
			result.Valid = false
			result.Errors = append(result.Errors, "MQTT backoff multiplier must be positive")
		}
	}

	result.Normalized = settings
	return result
}

// ValidateWebServerSettings performs WebServer validation without side effects.
// Returns validation result with errors.
func ValidateWebServerSettings(settings *WebServerSettings) ValidationResult {
	if settings == nil {
		return ValidationResult{Valid: false, Errors: []string{"WebServer settings is nil"}}
	}
	result := ValidationResult{Valid: true}

	if settings.Enabled {
		// Check if port is provided when enabled
		if settings.Port == "" {
			result.Valid = false
			result.Errors = append(result.Errors, "WebServer port is required when enabled")
		} else {
			// Validate port is a valid number in range 1-65535
			if port, err := strconv.Atoi(settings.Port); err != nil || port < 1 || port > 65535 {
				result.Valid = false
				result.Errors = append(result.Errors,
					fmt.Sprintf("WebServer port must be a number between 1 and 65535, got %q", settings.Port))
			}
		}
	}

	// Validate BasePath (reverse proxy subpath prefix).
	// Empty is allowed (disables basepath). Non-empty must be a safe URL-path prefix.
	if err := validateBasePath(settings.BasePath); err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, err.Error())
	}

	// Normalize LiveStream defaults: viper nested defaults can be lost when the
	// parent key (webserver:) exists in the config file but the child section
	// (livestream:) is absent. Apply compile-time defaults before range-checking.
	if settings.LiveStream.BitRate == 0 {
		settings.LiveStream.BitRate = DefaultLiveStreamBitRate
	}
	if settings.LiveStream.SegmentLength == 0 {
		settings.LiveStream.SegmentLength = DefaultLiveStreamSegmentLength
	}
	if settings.LiveStream.SampleRate == 0 {
		settings.LiveStream.SampleRate = DefaultLiveStreamSampleRate
	}
	trimmed := strings.ToLower(strings.TrimSpace(settings.LiveStream.FfmpegLogLevel))
	if trimmed == "" {
		settings.LiveStream.FfmpegLogLevel = DefaultLiveStreamFFmpegLogLevel
	} else {
		settings.LiveStream.FfmpegLogLevel = trimmed
	}

	// Validate LiveStream settings
	if settings.LiveStream.SampleRate < MinLiveStreamSampleRate || settings.LiveStream.SampleRate > MaxLiveStreamSampleRate {
		result.Valid = false
		result.Errors = append(result.Errors,
			fmt.Sprintf("LiveStream sample rate must be between %d and %d Hz, got %d",
				MinLiveStreamSampleRate, MaxLiveStreamSampleRate, settings.LiveStream.SampleRate))
	}

	if settings.LiveStream.BitRate < MinLiveStreamBitRate || settings.LiveStream.BitRate > MaxLiveStreamBitRate {
		result.Valid = false
		result.Errors = append(result.Errors,
			fmt.Sprintf("LiveStream bitrate must be between %d and %d kbps, got %d",
				MinLiveStreamBitRate, MaxLiveStreamBitRate, settings.LiveStream.BitRate))
	}

	if settings.LiveStream.SegmentLength < MinLiveStreamSegmentLength || settings.LiveStream.SegmentLength > MaxLiveStreamSegmentLength {
		result.Valid = false
		result.Errors = append(result.Errors,
			fmt.Sprintf("LiveStream segment length must be between %d and %d seconds, got %d",
				MinLiveStreamSegmentLength, MaxLiveStreamSegmentLength, settings.LiveStream.SegmentLength))
	}

	result.Normalized = settings
	return result
}

// validateBasePath verifies that a configured WebServer.BasePath is a safe URL-path prefix.
// Empty is allowed and disables subpath routing. Non-empty values must start with "/",
// contain only a restricted character set, and must not introduce path traversal,
// protocol-relative URLs, backslashes, or scheme-like sequences.
func validateBasePath(basePath string) error {
	if basePath == "" {
		return nil
	}

	if !strings.HasPrefix(basePath, "/") {
		return fmt.Errorf("WebServer basepath must start with %q, got %q", "/", basePath)
	}

	// A single "/" is meaningless as a subpath prefix — require the caller to use empty instead.
	if basePath == "/" {
		return fmt.Errorf("WebServer basepath %q is meaningless; leave empty to disable subpath routing", basePath)
	}

	// Reject dangerous sequences outright. The sequence list aligns with the
	// dangerous-pattern check in isValidBasePath (internal/api/v2/auth.go) so that
	// YAML-configured and UI-supplied basepaths share the same rejection rules.
	for _, bad := range []string{"..", "//", "\\", "://", "\n", "\r", "\x00"} {
		if strings.Contains(basePath, bad) {
			return fmt.Errorf("WebServer basepath contains disallowed sequence %q: %q", bad, basePath)
		}
	}

	// Restrict to the same alphanumeric + /_- character set accepted elsewhere.
	// This catches HTML/JS injection attempts, whitespace, and unicode surprises early.
	for _, r := range basePath {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '/', r == '-', r == '_':
			// allowed
		default:
			return fmt.Errorf("WebServer basepath contains disallowed character %q: %q", r, basePath)
		}
	}

	return nil
}

// ValidateTelemetrySettings validates the telemetry endpoint configuration.
// Returns validation result with errors if enabled and listen address is malformed.
func ValidateTelemetrySettings(settings *TelemetrySettings) ValidationResult {
	if settings == nil {
		return ValidationResult{Valid: false, Errors: []string{"telemetry settings is nil"}}
	}
	result := ValidationResult{Valid: true}

	if !settings.Enabled {
		return result
	}

	if settings.Listen == "" {
		result.Valid = false
		result.Errors = append(result.Errors, "telemetry listen address cannot be empty when enabled")
		return result
	}

	_, portStr, err := net.SplitHostPort(settings.Listen)
	if err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, fmt.Sprintf("telemetry listen address has invalid format: %v (expected 'host:port', e.g. '0.0.0.0:8090' or '[::1]:8090')", err))
		return result
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, fmt.Sprintf("telemetry listen port is not a valid number: %s", portStr))
		return result
	}

	if port < 1 || port > 65535 {
		result.Valid = false
		result.Errors = append(result.Errors, fmt.Sprintf("telemetry listen port must be between 1 and 65535, got %d", port))
	}

	return result
}

// validateBirdNETSettings validates the BirdNET-specific settings.
// This function uses ValidateBirdNETSettings internally and handles side effects
// (logging, mutation) to maintain backward compatibility.
func validateBirdNETSettings(birdnetSettings *BirdNETConfig) error {
	result := ValidateBirdNETSettings(birdnetSettings)

	normalized, err := extractNormalized[BirdNETConfig](result, "ValidateBirdNETSettings")
	if err != nil {
		return err
	}
	*birdnetSettings = *normalized

	// Handle warnings (side effects: logging)
	// Locale fallback warnings are debug-level since the fallback works correctly
	// and common locales like "en" or "en-US" always resolve to "en-uk"
	for _, warning := range result.Warnings {
		GetLogger().Debug("Configuration notice", logger.String("message", warning))
	}

	// Return errors if validation failed
	if !result.Valid {
		return errors.Newf("birdnet settings errors: %v", result.Errors).
			Category(errors.CategoryValidation).
			Context("validation_type", "birdnet-settings-collection").
			Build()
	}

	return nil
}

// validateWebServerSettings validates the WebServer-specific settings.
// This function uses ValidateWebServerSettings internally and handles error formatting
// to maintain backward compatibility.
func validateWebServerSettings(settings *WebServerSettings) error {
	return firstValidationError(ValidateWebServerSettings(settings), "webserver-settings")
}

// validateMQTTSettings validates the MQTT-specific settings.
// This function uses ValidateMQTTSettings internally and handles error formatting
// to maintain backward compatibility.
func validateMQTTSettings(settings *MQTTSettings) error {
	return firstValidationError(ValidateMQTTSettings(settings), "mqtt-settings")
}

// validateTelemetrySettings validates the telemetry-specific settings.
// This function uses ValidateTelemetrySettings internally and handles error formatting.
func validateTelemetrySettings(settings *TelemetrySettings) error {
	return firstValidationError(ValidateTelemetrySettings(settings), "telemetry-listen-address")
}

// validateBirdweatherSettings validates the Birdweather-specific settings.
// This function uses ValidateBirdweatherSettings internally and handles side effects
// (logging, mutation) to maintain backward compatibility.
func validateBirdweatherSettings(settings *BirdweatherSettings) error {
	result := ValidateBirdweatherSettings(settings)

	normalized, err := extractNormalized[BirdweatherSettings](result, "ValidateBirdweatherSettings")
	if err != nil {
		return err
	}
	*settings = *normalized

	// Return errors if validation failed
	if !result.Valid {
		// Join errors for backward compatibility
		return errors.Newf("%s", strings.Join(result.Errors, "; ")).
			Category(errors.CategoryValidation).
			Context("validation_type", "birdweather-settings").
			Build()
	}

	return nil
}

// validateNotificationSettings validates notification push configuration
func validateNotificationSettings(n *NotificationConfig) error {
	if !n.Push.Enabled {
		return nil
	}
	// Basic sanity checks
	if n.Push.MaxRetries < 0 {
		return errors.Newf("notification.push.max_retries must be >= 0").
			Category(errors.CategoryValidation).
			Context("validation_type", "notification-push-max-retries").
			Build()
	}
	if n.Push.DefaultTimeout < 0 || n.Push.RetryDelay < 0 {
		return errors.Newf("notification push durations must be non-negative").
			Category(errors.CategoryValidation).
			Context("validation_type", "notification-push-durations").
			Build()
	}
	for i := range n.Push.Providers {
		p := &n.Push.Providers[i]
		ptype := strings.ToLower(p.Type)
		switch ptype {
		case pushProviderScript:
			if p.Enabled && pushProviderProblem(p) != "" {
				return errors.Newf("script provider requires command when enabled").
					Category(errors.CategoryValidation).
					Context("validation_type", "notification-push-script-command").
					Build()
			}
		case pushProviderShoutrrr:
			if p.Enabled && pushProviderProblem(p) != "" {
				return errors.Newf("shoutrrr provider requires at least one URL when enabled").
					Category(errors.CategoryValidation).
					Context("validation_type", "notification-push-shoutrrr-urls").
					Build()
			}
			// Normalize ntfy URLs: ntfy://topic -> ntfy://ntfy.sh/topic
			normalizeNtfyURLs(p)
		case pushProviderWebhook:
			if err := validateWebhookProvider(p); err != nil {
				return err
			}
		default:
			// Only reject an unrecognised type on a provider that is switched on.
			// A disabled leftover entry (an older type name, or a half-created
			// provider) sends nothing, so it is not a reason to refuse to start.
			if !p.Enabled {
				continue
			}
			return errors.Newf("unknown push provider type: %s", p.Type).
				Category(errors.CategoryValidation).
				Context("validation_type", "notification-push-provider-type").
				Build()
		}
	}
	return nil
}

// validateWebhookProvider validates webhook provider configuration.
// This function uses ValidateWebhookProvider internally and handles error formatting
// to maintain backward compatibility. Authentication validation is still performed separately
// via validateWebhookAuth due to its complexity.
func validateWebhookProvider(p *PushProviderConfig) error {
	// Call the pure validation function
	result := ValidateWebhookProvider(p)

	// Return early if basic validation failed
	if !result.Valid {
		return errors.Newf("%s", result.Errors[0]).
			Category(errors.CategoryValidation).
			Context("validation_type", "notification-push-webhook").
			Context("provider_name", p.Name).
			Build()
	}

	// If disabled or no endpoints, no need to validate auth
	if !p.Enabled || len(p.Endpoints) == 0 {
		return nil
	}

	// Validate authentication for each endpoint
	// Note: Auth validation is kept separate as it's complex and not yet in pure version
	for i := range p.Endpoints {
		endpoint := &p.Endpoints[i]
		if err := validateWebhookAuth(&endpoint.Auth, p.Name, i); err != nil {
			return err
		}
	}

	return nil
}

// validateWebhookAuth validates webhook authentication configuration.
// Checks that required fields are provided but does NOT resolve secrets here.
// Secret resolution happens at runtime in the webhook provider.
func validateWebhookAuth(auth *WebhookAuthConfig, providerName string, endpointIndex int) error {
	authType := strings.ToLower(auth.Type)

	// Empty auth type defaults to "none" - this is valid
	if authType == "" || authType == webhookAuthNone {
		return nil
	}

	switch authType {
	case webhookAuthBearer, webhookAuthBasic, webhookAuthCustom:
		// Which credential each auth type needs is defined once, in
		// missingWebhookAuthSecret, so this rule and the normalization pass that
		// disables an unusable provider cannot drift apart.
		if reason := missingWebhookAuthSecret(auth); reason != "" {
			return errors.Newf("webhook provider '%s' endpoint %d: %s auth requires a credential, %s", providerName, endpointIndex, authType, reason).
				Category(errors.CategoryValidation).
				Context("validation_type", "notification-push-webhook-auth-"+authType).
				Context("provider_name", providerName).
				Context("endpoint_index", endpointIndex).
				Build()
		}
	default:
		return errors.Newf("webhook provider '%s' endpoint %d: unsupported auth type: %s", providerName, endpointIndex, authType).
			Category(errors.CategoryValidation).
			Context("validation_type", "notification-push-webhook-auth-type").
			Context("provider_name", providerName).
			Context("endpoint_index", endpointIndex).
			Context("auth_type", authType).
			Build()
	}

	return nil
}

// NormalizeNtfyURL fixes a bare ntfy topic URL (ntfy://topic) by inserting
// the default ntfy.sh host, producing ntfy://ntfy.sh/topic. The shoutrrr
// library interprets ntfy://topic as hostname="topic" with an empty path,
// which fails to deliver. URLs that already contain a recognizable host
// (containing a dot, a colon port, "localhost", or an IP address) are
// returned unchanged. Non-ntfy URLs are returned as-is.
func NormalizeNtfyURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "ntfy" {
		return raw
	}

	// When the URL already has a path (e.g. ntfy://host/topic), the host
	// part is unambiguous -- leave it alone.
	if u.Path != "" {
		return raw
	}

	// No path means the URL is either a bare topic (ntfy://mytopic) or a
	// host with an empty topic (ntfy://localhost). Apply a heuristic: if
	// the host portion looks like a real hostname or IP, leave it as-is.
	host := u.Hostname()
	if strings.Contains(host, ".") || host == "localhost" || net.ParseIP(host) != nil {
		return raw
	}

	// Also leave URLs with an explicit port alone (e.g. ntfy://myhost:8080).
	if u.Port() != "" {
		return raw
	}

	// It is a bare topic. Reconstruct the URL with ntfy.sh as the host.
	u.Path = "/" + u.Host
	u.Host = "ntfy.sh"
	return u.String()
}

// normalizeNtfyURLs repairs bare ntfy topic URLs in a shoutrrr provider's
// URL list so that shoutrrr receives the expected host/topic format.
func normalizeNtfyURLs(p *PushProviderConfig) {
	for i, u := range p.URLs {
		p.URLs[i] = NormalizeNtfyURL(u)
	}
}
