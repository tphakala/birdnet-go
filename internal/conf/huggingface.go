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
	raw, source, hasValue := huggingFaceOverrideSource(configured)
	if !hasValue {
		return DefaultHuggingFaceEndpoint
	}

	endpoint, err := normalizeHuggingFaceEndpoint(raw)
	if err != nil {
		// The rejected value is scrubbed rather than logged verbatim: userinfo is
		// one of the things this function rejects, so the raw string can carry a
		// password that must not reach the log file or a support dump.
		GetLogger().Warn("Ignoring invalid HuggingFace endpoint, using the default host",
			logger.String("source", source),
			logger.String("endpoint", privacy.RedactURLUserinfo(raw)),
			logger.String("fallback", DefaultHuggingFaceEndpoint),
			logger.Error(err))
		return DefaultHuggingFaceEndpoint
	}
	return endpoint
}

// huggingFaceOverrideSource returns the raw HuggingFace endpoint override in
// effect and where it came from, preferring the configured settings value over
// the HuggingFaceEndpointEnvVar environment variable. hasValue is false when
// neither is set (after trimming), so the caller applies its own default. raw is
// returned untrimmed so a caller can echo the exact rejected value in a log or
// error; normalizeHuggingFaceEndpoint trims internally. Centralizing the
// settings-then-env precedence here keeps ResolveHuggingFaceEndpoint and
// ResolveHuggingFaceEndpointChain from silently diverging if the precedence
// order ever changes.
func huggingFaceOverrideSource(configured string) (raw, source string, hasValue bool) {
	raw, source = configured, endpointSourceSettings
	if strings.TrimSpace(raw) == "" {
		raw, source = os.Getenv(HuggingFaceEndpointEnvVar), HuggingFaceEndpointEnvVar
	}
	return raw, source, strings.TrimSpace(raw) != ""
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
		return "", newEndpointError(raw, parseFailureReason(trimmed, err))
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

// parseFailureCredentialsReason is the reason reported for a value that fails
// url.Parse and may carry credentials.
const parseFailureCredentialsReason = "value is not a valid URL"

// parseFailureReason extracts the reason from a url.Parse failure without the
// URL it failed on.
//
// url.Error stringifies as `parse "<the whole raw URL>": <reason>`, so using
// err.Error() here would embed a credential-bearing value in a message that is
// logged and is quoted in the settings validation warning. Taking the inner
// error removes the raw value, but the inner reason can still quote a fragment
// of it: a password containing a "/" makes url.Parse read the text before the
// "/" as host and port, and the reason is then `invalid port ":<fragment>"
// after host`. So the inner reason is returned only when raw has no "@" and
// therefore no userinfo; otherwise a fixed sentence is returned.
func parseFailureReason(raw string, err error) string {
	if !strings.Contains(raw, "@") {
		if urlErr, ok := errors.AsType[*url.Error](err); ok && urlErr.Err != nil {
			return urlErr.Err.Error()
		}
	}
	// Possible credentials or an unknown error shape: report nothing rather
	// than risk echoing the value.
	return parseFailureCredentialsReason
}

// hasDotDotSegment reports whether an already-decoded URL path contains a ".."
// segment. Callers must pass url.URL.Path, which url.Parse has already
// percent-decoded; decoding again here would reject a legitimate literal "%"
// in the path and would treat a double-encoded segment as traversal when it is
// not. It splits on '/' only, because '\' is not a URL path separator;
// hasParentDirSegment in validate_audio.go is the file-path variant that also
// splits on '\'.
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
		Context("endpoint", privacy.RedactURLUserinfo(raw)).
		Build()
}
