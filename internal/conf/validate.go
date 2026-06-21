// conf/validate.go

package conf

import (
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// MinSoundLevelInterval is the minimum sound level interval in seconds to prevent excessive CPU usage
const MinSoundLevelInterval = 5

// DefaultCleanupCheckInterval is the default disk cleanup check interval in minutes
const DefaultCleanupCheckInterval = 15

// Valid retention policy values
const (
	RetentionPolicyNone  = "none"  // No retention cleanup
	RetentionPolicyAge   = "age"   // Age-based retention cleanup
	RetentionPolicyUsage = "usage" // Disk usage-based retention cleanup
)

// validRetentionPolicies contains all valid retention policy values
var validRetentionPolicies = []string{
	RetentionPolicyNone,
	RetentionPolicyAge,
	RetentionPolicyUsage,
}

// htmlTagPattern matches HTML tags for sanitization.
var htmlTagPattern = regexp.MustCompile(`<[^>]*>`)

// sanitizeStringField strips HTML tags from a string field as defense in depth.
// Returns the sanitized string.
func sanitizeStringField(s string) string {
	return htmlTagPattern.ReplaceAllString(s, "")
}

// Precompiled regular expressions for validation
var (
	// birdweatherIDPattern validates Birdweather ID format (24 alphanumeric characters)
	birdweatherIDPattern = regexp.MustCompile(`^[a-zA-Z0-9]{24}$`)

	// gpsCoordPattern matches the GPS-coordinate-as-device-string
	// misconfiguration seen in the wild. The leading colon is optional:
	// the originally reported case was `:45.5,-120.5` (an ALSA-style
	// device prefix with the PCM name replaced by coordinates), but the
	// naked variant `45.5,-120.5` is equally nonsensical as an audio
	// device. At least one of the two numbers must contain a decimal
	// point so that ALSA shorthand device names like `:2,0` (equivalent
	// to `hw:2,0`) are not falsely rejected. Real GPS coordinates are
	// virtually always fractional; pure-integer pairs like `45,120` are
	// valid ALSA card/subdevice selectors and must be allowed through.
	// Optional whitespace around the comma is allowed because users
	// frequently paste coordinates copied from map services with a
	// space after the separator (`45.5, -120.5`). Detecting this shape
	// at validation time lets us point the user at the right field with
	// a single clear error instead of emitting recurring telemetry.
	gpsCoordPattern = regexp.MustCompile(`^:?[+-]?\d+\.\d+\s*,\s*[+-]?\d+(\.\d+)?$|^:?[+-]?\d+(\.\d+)?\s*,\s*[+-]?\d+\.\d+$`)
)

// ValidAudioModels contains recognized AI model identifiers.
// Empty string is also valid (defaults to birdnet).
var ValidAudioModels = map[string]bool{
	"":             true, // default (birdnet)
	ModelIDBirdNET: true,
	ModelIDPerchV2: true,
	ModelIDBat:     true,
	ModelIDBSG:     true,
}

// ValidationError is the set of fatal validation findings produced by
// ValidateSettings. Every entry in Errors blocks startup: severity is decided
// structurally by whether a validator returned an error, never by inspecting
// the message text. Non-fatal configuration findings use a separate channel,
// Settings.ValidationWarnings, recorded during config migration (see
// applyModelValidation), not derived from the text of a fatal error.
type ValidationError struct {
	Errors []string
}

// Error returns a string representation of the validation errors
func (ve ValidationError) Error() string {
	return fmt.Sprintf("Validation errors: %v", ve.Errors)
}

// logValidationWarning logs a validation warning both locally and for telemetry without returning an error.
func logValidationWarning(err error, validationType, warningType string) {
	// Log locally so administrators can see config issues in their logs
	log := logger.Global().Module("conf")
	log.Warn("Configuration validation warning",
		logger.String("validation_type", validationType),
		logger.String("warning", warningType),
		logger.Error(err))

	// Create an enhanced error for telemetry tracking
	_ = errors.New(err).
		Category(errors.CategoryValidation).
		Context("validation_type", validationType).
		Context("warning", warningType).
		Build()
}

// ValidationResult captures validation outcomes without side effects.
// Used by pure validation functions to return validation state, errors, warnings,
// and normalized/transformed configuration.
type ValidationResult struct {
	Valid      bool     // Overall validation result
	Errors     []string // Validation errors (fatal)
	Warnings   []string // Non-fatal warnings
	Normalized any      // Normalized/transformed config (type matches input)
}

// numeric covers all Go numeric types for range validation.
type numeric interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 |
		~float32 | ~float64
}

// checkRange appends an error to result if value is outside [lo, hi].
// For float types, NaN and Inf are rejected because NaN comparisons
// silently return false, letting invalid values bypass range checks.
func checkRange[T numeric](result *ValidationResult, value, lo, hi T, msg string) {
	if f := float64(value); math.IsNaN(f) || math.IsInf(f, 0) {
		result.Valid = false
		result.Errors = append(result.Errors, msg)
		return
	}
	if value < lo || value > hi {
		result.Valid = false
		result.Errors = append(result.Errors, msg)
	}
}

// extractNormalized extracts and type-asserts the Normalized field from a
// ValidationResult. Returns an error if the field is nil or the wrong type.
func extractNormalized[T any](result ValidationResult, funcName string) (*T, error) {
	if result.Normalized == nil {
		return nil, errors.Newf("internal error: %s returned nil Normalized", funcName).
			Category(errors.CategoryValidation).
			Context("validation_type", "type-assertion").
			Build()
	}
	normalized, ok := result.Normalized.(*T)
	if !ok {
		return nil, errors.Newf("internal error: %s returned unexpected type %T", funcName, result.Normalized).
			Category(errors.CategoryValidation).
			Context("validation_type", "type-assertion").
			Build()
	}
	if normalized == nil {
		return nil, errors.Newf("internal error: %s returned typed nil Normalized", funcName).
			Category(errors.CategoryValidation).
			Context("validation_type", "type-assertion").
			Build()
	}
	return normalized, nil
}

// firstValidationError wraps the first error from a ValidationResult into an
// EnhancedError with the given validation type. Returns nil when valid.
func firstValidationError(result ValidationResult, validationType string) error {
	if result.Valid {
		return nil
	}
	if len(result.Errors) == 0 {
		return errors.Newf("validation failed with no error details").
			Category(errors.CategoryValidation).
			Context("validation_type", validationType).
			Build()
	}
	return errors.Newf("%s", result.Errors[0]).
		Category(errors.CategoryValidation).
		Context("validation_type", validationType).
		Build()
}

// ValidateSettings validates the entire Settings struct
func ValidateSettings(settings *Settings) error {
	ve := ValidationError{}

	// Sanitize Main.Name: strip HTML tags as defense in depth
	settings.Main.Name = sanitizeStringField(settings.Main.Name)

	// Validate low-memory mode (non-fatal). Canonicalize case/whitespace so a
	// value like "On" or " off " keeps the operator's intent; normalize genuinely
	// invalid values to "auto" with a warning.
	switch normalized := strings.ToLower(strings.TrimSpace(settings.LowMemory.Mode)); normalized {
	case "", LowMemoryModeAuto, LowMemoryModeOn, LowMemoryModeOff:
		settings.LowMemory.Mode = normalized
	default:
		settings.ValidationWarnings = append(settings.ValidationWarnings,
			fmt.Sprintf("invalid lowmemory.mode %q; using %q", settings.LowMemory.Mode, LowMemoryModeAuto))
		settings.LowMemory.Mode = LowMemoryModeAuto
	}

	// Default empty BirdNET locale to "en" so downstream label loading
	// always has a valid locale to work with.
	if settings.BirdNET.Locale == "" {
		settings.BirdNET.Locale = "en"
	}

	// Validate BirdNET settings
	if err := validateBirdNETSettings(&settings.BirdNET); err != nil {
		ve.Errors = append(ve.Errors, err.Error())
	}

	// Validate WebServer settings
	if err := validateWebServerSettings(&settings.WebServer); err != nil {
		ve.Errors = append(ve.Errors, err.Error())
	}

	// Validate Security settings
	if err := validateSecuritySettings(&settings.Security); err != nil {
		ve.Errors = append(ve.Errors, err.Error())
	}

	// Validate Realtime settings
	if err := validateRealtimeSettings(&settings.Realtime); err != nil {
		ve.Errors = append(ve.Errors, err.Error())
	}

	// Validate Birdweather settings
	if err := validateBirdweatherSettings(&settings.Realtime.Birdweather); err != nil {
		ve.Errors = append(ve.Errors, err.Error())
	}

	// Validate Audio settings
	if err := validateAudioSettings(&settings.Realtime.Audio); err != nil {
		ve.Errors = append(ve.Errors, err.Error())
	}

	// Validate Retention settings (policy, maxAge, maxUsage)
	if err := validateRetentionSettings(&settings.Realtime.Audio.Export.Retention); err != nil {
		ve.Errors = append(ve.Errors, err.Error())
	}

	// Validate Dashboard settings
	if err := validateDashboardSettings(&settings.Realtime.Dashboard); err != nil {
		ve.Errors = append(ve.Errors, err.Error())
	}

	// Validate Weather settings
	if err := validateWeatherSettings(&settings.Realtime.Weather); err != nil {
		ve.Errors = append(ve.Errors, err.Error())
	}

	// Validate Species Tracking settings
	if err := validateSpeciesTrackingSettings(&settings.Realtime.SpeciesTracking); err != nil {
		ve.Errors = append(ve.Errors, err.Error())
	}

	// Validate Notification settings
	if err := validateNotificationSettings(&settings.Notification); err != nil {
		ve.Errors = append(ve.Errors, err.Error())
	}

	// Validate Embeddings settings
	if err := validateEmbeddingsSettings(&settings.Embeddings); err != nil {
		ve.Errors = append(ve.Errors, err.Error())
	}

	// If there are any errors, return the ValidationError
	if len(ve.Errors) > 0 {
		return ve
	}
	return nil
}
