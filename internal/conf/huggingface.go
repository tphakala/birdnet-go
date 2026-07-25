// huggingface.go resolves the base URL used for every HuggingFace fetch:
// the model catalog, region metadata, and model file downloads.
package conf

import (
	"net/url"
	"os"
	"strings"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	// DefaultHuggingFaceEndpoint is the canonical HuggingFace host, used when
	// no override is configured.
	DefaultHuggingFaceEndpoint = "https://huggingface.co"

	// HuggingFaceEndpointEnvVar is the environment variable the HuggingFace
	// ecosystem uses to point tooling at a mirror. Users behind the Great
	// Firewall already set it to https://hf-mirror.com for the Python tooling,
	// so honouring it means no extra BirdNET-Go specific configuration.
	HuggingFaceEndpointEnvVar = "HF_ENDPOINT"

	// endpointSourceSettings labels the settings field in warning logs.
	endpointSourceSettings = "settings"
)

// ResolveHuggingFaceEndpoint returns the base URL for HuggingFace fetches.
// Resolution order, first non-empty wins:
//
//  1. configured, i.e. settings.BirdNET.HuggingFaceEndpoint
//  2. the HF_ENDPOINT environment variable
//  3. DefaultHuggingFaceEndpoint
//
// The winning value is normalized (surrounding whitespace and trailing slashes
// removed) and validated as an absolute http or https URL. A malformed value is
// logged and replaced by DefaultHuggingFaceEndpoint, so a typo degrades to the
// default host instead of failing every download with an unusable URL.
//
// The result never ends in a slash, so callers can append "/" + path.
func ResolveHuggingFaceEndpoint(configured string) string {
	raw, source := configured, endpointSourceSettings
	if strings.TrimSpace(raw) == "" {
		raw, source = os.Getenv(HuggingFaceEndpointEnvVar), HuggingFaceEndpointEnvVar
	}
	if strings.TrimSpace(raw) == "" {
		return DefaultHuggingFaceEndpoint
	}

	endpoint, err := normalizeHuggingFaceEndpoint(raw)
	if err != nil {
		GetLogger().Warn("Ignoring invalid HuggingFace endpoint, using the default host",
			logger.String("source", source),
			logger.String("endpoint", raw),
			logger.String("fallback", DefaultHuggingFaceEndpoint),
			logger.Error(err))
		return DefaultHuggingFaceEndpoint
	}
	return endpoint
}

// normalizeHuggingFaceEndpoint trims and validates an endpoint override,
// returning the canonical form without a trailing slash.
func normalizeHuggingFaceEndpoint(raw string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if trimmed == "" {
		return "", newEndpointError(raw, "endpoint is empty after trimming")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", errors.Newf("invalid HuggingFace endpoint %q: %v", raw, err).
			Component("conf").
			Category(errors.CategoryValidation).
			Context("endpoint", raw).
			Build()
	}

	switch parsed.Scheme {
	case "http", "https":
	default:
		return "", newEndpointError(raw, "endpoint must use the http or https scheme")
	}
	if parsed.Host == "" {
		return "", newEndpointError(raw, "endpoint has no host")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", newEndpointError(raw, "endpoint must not contain a query string or fragment")
	}

	// Re-serialize so the scheme case and path escaping are canonical.
	return strings.TrimRight(parsed.String(), "/"), nil
}

// newEndpointError builds a validation error for a rejected endpoint override.
func newEndpointError(raw, reason string) error {
	return errors.Newf("invalid HuggingFace endpoint %q: %s", raw, reason).
		Component("conf").
		Category(errors.CategoryValidation).
		Context("endpoint", raw).
		Build()
}
