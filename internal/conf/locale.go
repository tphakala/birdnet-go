// conf/locale.go contains all locales application supports

package conf

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// DefaultFallbackLocale is the default locale used when the requested locale is not supported
// This should match the original configuration default that was "en-uk"
const DefaultFallbackLocale = "en-uk"

// Label file configurations for different model versions
type LabelConfig struct {
	FilePattern string // Pattern for label files, e.g. "BirdNET_GLOBAL_6K_V2.4_Labels_%s.txt"
	BasePath    string // Base path for the label files, e.g. "V2.4/"
}

// ModelLabelMapping maps model versions to their corresponding label configurations
var ModelLabelMapping = map[string]LabelConfig{
	"BirdNET_GLOBAL_6K_V2.4": {
		FilePattern: "BirdNET_GLOBAL_6K_V2.4_Labels_%s.txt",
		BasePath:    "V2.4/",
	},
}

// IMPORTANT: When adding or modifying locale entries, also update the locale_codes and locale_names arrays
// in the configure_locale() function in install.sh to keep installation options in sync with app capabilities.

// LocaleCodeMapping maps 2-letter codes to file identifiers for the V2.4 format
var LocaleCodeMapping = map[string]string{
	"af":    "af",
	"ar":    "ar",
	"bg":    "bg",
	"ca":    "ca",
	"cs":    "cs",
	"da":    "da",
	"de":    "de",
	"el":    "el",
	"en-uk": "en_uk",
	"en-us": "en_us",
	"es":    "es",
	"et":    "et_ee",
	"fi":    "fi",
	"fr":    "fr",
	"he":    "he",
	"hi-in": "hi_in", // Hindi (India)
	"hr":    "hr",
	"hu":    "hu",
	"id":    "id", // Using proper ISO code for Indonesia
	"is":    "is",
	"it":    "it",
	"ja":    "ja",
	"ko":    "ko",
	"lt":    "lt",
	"lv":    "lv_lv",
	"ml":    "ml",
	"nl":    "nl",
	"no":    "no",
	"pl":    "pl",
	"pt":    "pt_PT",
	"pt-br": "pt_BR",
	"pt-pt": "pt_PT",
	"ro":    "ro",
	"ru":    "ru",
	"sk":    "sk",
	"sl":    "sl",
	"sr":    "sr",
	"sv":    "sv",
	"th":    "th",
	"tr":    "tr",
	"uk":    "uk",
	"vi-vn": "vi_vn", // Vietnamese (Vietnam)
	"zh":    "zh",
}

// LocaleCodes holds human-readable names for locale codes
var LocaleCodes = map[string]string{
	"af":    "Afrikaans",
	"ar":    "Arabic",
	"bg":    "Bulgarian",
	"pt-br": "Brazilian Portuguese",
	"ca":    "Catalan",
	"cs":    "Czech",
	"zh":    "Chinese",
	"hr":    "Croatian",
	"da":    "Danish",
	"nl":    "Dutch",
	"el":    "Greek",
	"en-uk": "English (UK)",
	"en-us": "English (US)",
	"et":    "Estonian",
	"fi":    "Finnish",
	"fr":    "French",
	"de":    "German",
	"he":    "Hebrew",
	"hi-in": "Hindi", // Hindi (India)
	"hu":    "Hungarian",
	"is":    "Icelandic",
	"id":    "Indonesian",
	"it":    "Italian",
	"ja":    "Japanese",
	"ko":    "Korean",
	"lv":    "Latvian",
	"lt":    "Lithuanian",
	"ml":    "Malayalam",
	"no":    "Norwegian",
	"pl":    "Polish",
	"pt":    "Portuguese",
	"pt-pt": "Portuguese (Portugal)",
	"ro":    "Romanian",
	"ru":    "Russian",
	"sr":    "Serbian",
	"sk":    "Slovak",
	"sl":    "Slovenian",
	"es":    "Spanish",
	"sv":    "Swedish",
	"th":    "Thai",
	"tr":    "Turkish",
	"uk":    "Ukrainian",
	"vi-vn": "Vietnamese", // Vietnamese (Vietnam)
}

// GetLabelFilename returns the appropriate label filename for the given model version and locale code
func GetLabelFilename(modelVersion, localeCode string) (string, error) {
	config, exists := ModelLabelMapping[modelVersion]
	if !exists {
		return "", errors.Newf("unsupported model version: %s", modelVersion).
			Category(errors.CategoryValidation).
			Context("validation_type", "model-version-support").
			Context("model_version", modelVersion).
			Build()
	}

	// Get the file identifier for the locale code
	fileLocale, exists := LocaleCodeMapping[localeCode]
	if !exists {
		return "", errors.Newf("unsupported locale code for model %s: %s", modelVersion, localeCode).
			Category(errors.CategoryValidation).
			Context("validation_type", "locale-code-support").
			Context("model_version", modelVersion).
			Context("locale_code", localeCode).
			Build()
	}

	return config.BasePath + fmt.Sprintf(config.FilePattern, fileLocale), nil
}

// NormalizeLocale normalizes the input locale string and matches it to a known locale code or full name.
// If the locale is not supported, it falls back to DefaultFallbackLocale.
func NormalizeLocale(inputLocale string) (string, error) {
	originalInput := inputLocale
	inputLocale = strings.ToLower(inputLocale)

	// Check if it's already a valid locale code
	if _, exists := LocaleCodes[inputLocale]; exists {
		return inputLocale, nil
	}

	// Try to match by full name
	for code, fullName := range LocaleCodes {
		if strings.EqualFold(fullName, inputLocale) {
			return code, nil
		}
	}

	// Fall back to DefaultFallbackLocale if the locale is not supported
	// Get the human-readable name for the fallback locale
	fallbackName, exists := LocaleCodes[DefaultFallbackLocale]
	if !exists {
		fallbackName = DefaultFallbackLocale // fallback to code if name not found
	}

	// Create detailed error with available locales for debugging
	availableLocales := slices.Collect(maps.Keys(LocaleCodes))

	return DefaultFallbackLocale, errors.Newf("locale '%s' not supported, falling back to %s", originalInput, fallbackName).
		Category(errors.CategoryValidation).
		Context("validation_type", "locale-normalization").
		Context("input_locale", originalInput).
		Context("fallback_locale", DefaultFallbackLocale).
		Context("available_locales_sample", availableLocales[:5]).
		Build()
}
