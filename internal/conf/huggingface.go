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
			logger.String("endpoint", redactEndpointUserinfo(raw)),
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
		return "", newEndpointError(raw, parseFailureReason(err))
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

// redactEndpointUserinfo returns raw with any userinfo replaced by a marker, so
// a rejected endpoint can be echoed to a log or to error context without its
// credentials.
//
// This is done by hand rather than with privacy.ScrubCredentialURL because that
// helper's patterns require at least one character before the colon, so
// "https://:password@host" passes through untouched, and they stop at the first
// "@", so a password containing a literal "@" leaks its tail. Both shapes reach
// here: this function is called precisely on values that failed validation, and
// a value that fails url.Parse cannot be normalized by parsing it.
//
// Everything from the start of the authority to the LAST "@" before a path
// delimiter is replaced, which covers both shapes without needing the value to
// parse.
//
// The authority is located without relying on "://", because the values that
// reach here are exactly the ones that failed validation: an opaque URL
// ("https:user:pw@host") and a scheme-less value ("user:pw@host") are both
// rejected, and both would otherwise be echoed with their credentials intact.
func redactEndpointUserinfo(raw string) string {
	authorityStart := 0
	switch {
	case strings.Contains(raw, "://"):
		authorityStart = strings.Index(raw, "://") + len("://")
	case strings.HasPrefix(raw, "//"):
		// Scheme-relative.
		authorityStart = len("//")
	default:
		// Opaque ("scheme:rest") or scheme-less. url.Parse treats a leading
		// run of scheme characters followed by ":" as a scheme, so skip it when
		// present; otherwise the whole string is treated as the authority.
		if i := strings.Index(raw, ":"); i > 0 && isSchemeName(raw[:i]) {
			authorityStart = i + 1
		}
	}

	// The authority ends at the first "/", "?" or "#"; an "@" after that point
	// belongs to the path and must not be treated as a userinfo delimiter.
	authorityEnd := len(raw)
	if i := strings.IndexAny(raw[authorityStart:], "/?#"); i >= 0 {
		authorityEnd = authorityStart + i
	}

	at := strings.LastIndex(raw[authorityStart:authorityEnd], "@")
	if at < 0 {
		return raw
	}
	return raw[:authorityStart] + "[REDACTED]" + raw[authorityStart+at:]
}

// isSchemeName reports whether s is shaped like a URL scheme, i.e. an ASCII
// letter followed by letters, digits, "+", "-" or "." (RFC 3986 section 3.1).
func isSchemeName(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'):
		default:
			return false
		}
	}
	return true
}

// parseFailureReason extracts the reason from a url.Parse failure without the
// URL it failed on.
//
// url.Error stringifies as `parse "<the whole raw URL>": <reason>`, so using
// err.Error() here would embed a credential-bearing value in a message that is
// logged and is quoted in the settings validation warning. Scrubbing that
// string is not sufficient: privacy.ScrubCredentialURL's patterns require at
// least one character before the colon, so an empty username
// ("https://:password@host") passes through untouched, and a password
// containing a literal "@" leaks its tail. Taking the inner error removes the
// raw value entirely rather than trying to redact it, and the inner reason
// ("invalid port ...", "invalid URL escape ...") never contains userinfo.
func parseFailureReason(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Err.Error()
	}
	// Unknown error shape: report nothing rather than risk echoing the value.
	return "value is not a valid URL"
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
		Context("endpoint", redactEndpointUserinfo(raw)).
		Build()
}
