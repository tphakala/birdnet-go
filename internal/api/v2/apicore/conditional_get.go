package apicore

import "strings"

// MatchIfNoneMatch reports whether an If-None-Match request header matches etag,
// so a handler can answer a conditional GET with 304 Not Modified. It follows
// RFC 7232 §3.2: the "*" wildcard matches any current representation, a
// comma-separated list matches when any member matches, and a leading weak
// validator "W/" prefix on a candidate is ignored (reverse proxies and CDNs
// routinely weaken a strong ETag to W/"..."). An empty header never matches.
//
// etag is expected to be a strong, already-quoted validator (e.g. `"abc123"`).
// The weak "W/" prefix is stripped from the client candidate only, not from
// etag, so passing a weak server etag would under-match. Candidates are split on
// ",", so an (unusual) quoted ETag value containing a literal comma would not
// match; the ETag values used in this codebase are comma-free.
//
// Shared by the api/v2 models (coverage-map) and species (dictionary) handlers
// so their conditional-GET handling stays consistent.
func MatchIfNoneMatch(header, etag string) bool {
	if header == "" {
		return false
	}
	if strings.TrimSpace(header) == "*" {
		return true
	}
	for candidate := range strings.SplitSeq(header, ",") {
		if strings.TrimPrefix(strings.TrimSpace(candidate), "W/") == etag {
			return true
		}
	}
	return false
}
