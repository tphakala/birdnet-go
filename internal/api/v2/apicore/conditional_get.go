package apicore

import "strings"

// Conditional-request header names. Echo defines neither of them.
const (
	// HeaderETag is the response header carrying an entity tag.
	HeaderETag = "ETag"
	// HeaderIfMatch is the request header that makes a write conditional on the
	// current entity tag.
	HeaderIfMatch = "If-Match"
)

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

// MatchIfMatch reports whether an If-Match request header matches etag, so a
// handler can refuse a write built on a state that has since changed with 412
// Precondition Failed. It follows RFC 9110 section 13.1.1: the "*" wildcard
// matches any current representation and a comma-separated list matches when any
// member matches. Unlike MatchIfNoneMatch it uses strong comparison, so a
// candidate with a weak "W/" prefix never matches. An empty header never matches;
// a handler that wants "no header means no precondition" checks for the empty
// header itself.
//
// etag is expected to be a strong, already-quoted validator. Candidates are split
// on ",", so a quoted ETag value containing a literal comma would not match; the
// ETag values used in this codebase are comma-free.
func MatchIfMatch(header, etag string) bool {
	if header == "" {
		return false
	}
	if strings.TrimSpace(header) == "*" {
		return true
	}
	for candidate := range strings.SplitSeq(header, ",") {
		if strings.TrimSpace(candidate) == etag {
			return true
		}
	}
	return false
}
