// huggingface.go resolves the base URL for HuggingFace model file downloads.
// The model catalog is embedded in the binary, not fetched, so downloads are
// the only consumer.
package conf

import (
	"net/url"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
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
// removed) and must be an absolute http or https URL with an ASCII hostname, no
// userinfo, no query, no fragment, and no ".." path segment; see
// normalizeHuggingFaceEndpoint for why each of those is refused. Anything else
// is logged and replaced by DefaultHuggingFaceEndpoint, so a typo degrades to
// the default host instead of failing every download with an unusable URL.
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
		// The rejected value is scrubbed rather than logged verbatim: userinfo is
		// one of the things this function rejects, so the raw string can carry a
		// password that must not reach the log file or a support dump.
		GetLogger().Warn("Ignoring invalid HuggingFace endpoint, using the default host",
			logger.String("source", source),
			logger.CredentialURL("endpoint", raw),
			logger.String("fallback", DefaultHuggingFaceEndpoint),
			logger.Error(err))
		return DefaultHuggingFaceEndpoint
	}
	return endpoint
}

// normalizeHuggingFaceEndpoint trims and validates an endpoint override,
// returning the canonical form without a trailing slash.
//
// Rejection is deliberately strict: an endpoint that parses but cannot serve
// the repo layout is worse than no override, because the caller falls back to
// the default host and the user is told why, instead of every download failing
// against a URL that looks plausible.
func normalizeHuggingFaceEndpoint(raw string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if trimmed == "" {
		return "", newEndpointError(raw, "endpoint is empty after trimming")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		// url.Error formats as `parse %q: ...`, embedding the whole raw value,
		// so the reason has to be scrubbed too. Scrubbing only the error context
		// would still leak a password through the message, which is what the
		// caller logs and what the settings validator embeds in its warning.
		return "", newEndpointError(raw, privacy.ScrubCredentialURL(err.Error()))
	}

	switch parsed.Scheme {
	case "http", "https":
	default:
		return "", newEndpointError(raw, "endpoint must use the http or https scheme")
	}
	// Hostname(), not Host: "https://:8080" has a non-empty Host but no hostname,
	// and Go's dialer resolves an empty host to the local machine, which would
	// silently retarget every download at this host rather than a mirror.
	if parsed.Hostname() == "" {
		return "", newEndpointError(raw, "endpoint has no host")
	}
	// ForceQuery covers a bare trailing "?", which leaves RawQuery empty but is
	// re-emitted by String(); the repo path would then be swallowed into the
	// query string and every file would 404.
	if parsed.RawQuery != "" || parsed.ForceQuery {
		return "", newEndpointError(raw, "endpoint must not contain a query string")
	}
	if parsed.Fragment != "" {
		return "", newEndpointError(raw, "endpoint must not contain a fragment")
	}
	// Credentials would become the prefix of every download URL, be sent to the
	// mirror as an Authorization header, and land in logs and support dumps.
	// Authenticated mirrors are not a supported configuration.
	if parsed.User != nil {
		return "", newEndpointError(raw, "endpoint must not contain a username or password")
	}
	// A path prefix is supported, but ".." in it would escape the prefix once the
	// origin server normalizes the joined path.
	if hasDotDotSegment(parsed.Path) {
		return "", newEndpointError(raw, `endpoint path must not contain a ".." segment`)
	}
	// A non-ASCII host is percent-encoded by String() rather than punycoded, so
	// it can never resolve. Reject it so the documented fallback applies instead
	// of every download failing DNS.
	if !isASCII(parsed.Host) {
		return "", newEndpointError(raw, "endpoint host must be ASCII (use the punycode form)")
	}

	// Re-serialize so the scheme case and path escaping are canonical.
	return strings.TrimRight(parsed.String(), "/"), nil
}

// hasDotDotSegment reports whether an already-decoded URL path contains a ".."
// segment. Callers must pass url.URL.Path, which url.Parse has already
// percent-decoded; decoding again here would reject a legitimate literal "%"
// in the path and would treat a double-encoded segment as traversal when it is
// not.
func hasDotDotSegment(path string) bool {
	return slices.Contains(strings.Split(path, "/"), "..")
}

// isASCII reports whether s contains only ASCII characters.
func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// newEndpointError builds a validation error for a rejected endpoint override.
// The raw value is scrubbed before it becomes error context, which is reported
// to telemetry.
func newEndpointError(raw, reason string) error {
	return errors.Newf("invalid HuggingFace endpoint: %s", reason).
		Component("conf").
		Category(errors.CategoryValidation).
		Context("endpoint", privacy.ScrubCredentialURL(raw)).
		Build()
}
