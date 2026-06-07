// Package branding centralizes BirdNET-Go's project identity: the application
// name and the canonical repository, issue-tracker, support, and community
// URLs. Each value is resolved at call time with the precedence
//
//	BIRDNET_GO_PROJECT_* env var > ldflags-baked var > built-in default
//
// so a fork can rebrand the running binary without editing source or
// translations: bake values in at link time via -ldflags -X, or set the
// environment variables at runtime. The defaults intentionally stay in source
// (these are public identity, not secrets), so from-source builds keep pointing
// at the upstream project. This mirrors the build-time/runtime injection model
// used for the Sentry DSN in internal/telemetry.
package branding

import (
	"net/url"
	"os"
	"strings"
)

// Build-time injection targets. They are intentionally unexported (so callers
// must go through the getters, never read a possibly-empty var directly) and
// empty by default, so a plain `go build` falls through to the built-in const
// defaults below, while official and fork builds can override them at link
// time, e.g.:
//
//	-ldflags "-X 'github.com/tphakala/birdnet-go/internal/branding.projectRepoURL=https://github.com/acme/birdnet-fork'"
//
// The linker can set unexported package vars, so this mirrors the unexported
// internal/telemetry.sentryDSN injection target exactly.
var (
	// projectName is the human-readable application name.
	projectName string
	// projectRepoURL is the canonical source repository URL.
	projectRepoURL string
	// projectIssuesURL is the issue-tracker listing URL; derived from the repo
	// URL when empty.
	projectIssuesURL string
	// projectNewIssueURL is the "create a new issue" URL; derived from the repo
	// URL when empty.
	projectNewIssueURL string
	// projectSupportURL is the user-facing support URL (shown e.g. on the Home
	// Assistant device page); defaults to the repo URL when empty.
	projectSupportURL string
	// projectDiscussionsURL is the community discussions/forum URL; derived from
	// the repo URL when empty.
	projectDiscussionsURL string
	// projectReleasesURL is the release-notes/downloads URL; derived from the
	// repo URL when empty.
	projectReleasesURL string
	// projectCommunityURL is the community chat/forum URL.
	projectCommunityURL string
)

// Built-in defaults, used when neither an environment override nor a baked-in
// value is present. These are the upstream project's public identity.
const (
	defaultName         = "BirdNET-Go"
	defaultRepoURL      = "https://github.com/tphakala/birdnet-go"
	defaultCommunityURL = "https://discord.gg/gcSCFGUtsd"

	issuesPath      = "issues"
	newIssuePath    = "issues/new"
	discussionsPath = "discussions"
	releasesPath    = "releases"
)

// Environment variables that override the build-time values at runtime.
const (
	envName           = "BIRDNET_GO_PROJECT_NAME"
	envRepoURL        = "BIRDNET_GO_PROJECT_REPO_URL"
	envIssuesURL      = "BIRDNET_GO_PROJECT_ISSUES_URL"
	envNewIssueURL    = "BIRDNET_GO_PROJECT_NEW_ISSUE_URL"
	envSupportURL     = "BIRDNET_GO_PROJECT_SUPPORT_URL"
	envDiscussionsURL = "BIRDNET_GO_PROJECT_DISCUSSIONS_URL"
	envReleasesURL    = "BIRDNET_GO_PROJECT_RELEASES_URL"
	envCommunityURL   = "BIRDNET_GO_PROJECT_COMMUNITY_URL"
)

// resolve returns the first non-empty candidate among the environment variable
// named by envKey, the ldflags-baked value, and the fallback. All candidates
// are trimmed so a whitespace-only entry counts as unset.
func resolve(envKey, ldflagsVar, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
		return v
	}
	if v := strings.TrimSpace(ldflagsVar); v != "" {
		return v
	}
	return fallback
}

// joinURL appends a path suffix to a base URL, collapsing any trailing slash on
// the base so it yields the same result with or without one.
func joinURL(base, suffix string) string {
	return strings.TrimRight(base, "/") + "/" + suffix
}

// sanitizeURL strips any userinfo (the user:password@ component) from a URL so
// that credentials an operator might accidentally embed in a configured
// identity URL are never emitted in the public app-config response, outbound
// User-Agent headers, or logs. URLs without userinfo (the normal case) and
// values that do not parse as URLs are returned unchanged.
func sanitizeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = nil
	return u.String()
}

// Name returns the configured project name.
func Name() string {
	return resolve(envName, projectName, defaultName)
}

// RepoURL returns the configured source repository URL (credential-free).
func RepoURL() string {
	return sanitizeURL(resolve(envRepoURL, projectRepoURL, defaultRepoURL))
}

// IssuesURL returns the issue-tracker listing URL, derived from RepoURL when
// not explicitly configured.
func IssuesURL() string {
	return sanitizeURL(resolve(envIssuesURL, projectIssuesURL, joinURL(RepoURL(), issuesPath)))
}

// NewIssueURL returns the "create a new issue" URL, derived from RepoURL when
// not explicitly configured.
func NewIssueURL() string {
	return sanitizeURL(resolve(envNewIssueURL, projectNewIssueURL, joinURL(RepoURL(), newIssuePath)))
}

// SupportURL returns the user-facing support URL, defaulting to RepoURL when
// not explicitly configured.
func SupportURL() string {
	return sanitizeURL(resolve(envSupportURL, projectSupportURL, RepoURL()))
}

// DiscussionsURL returns the community discussions URL, derived from RepoURL
// when not explicitly configured.
func DiscussionsURL() string {
	return sanitizeURL(resolve(envDiscussionsURL, projectDiscussionsURL, joinURL(RepoURL(), discussionsPath)))
}

// ReleasesURL returns the release-notes/downloads URL, derived from RepoURL
// when not explicitly configured.
func ReleasesURL() string {
	return sanitizeURL(resolve(envReleasesURL, projectReleasesURL, joinURL(RepoURL(), releasesPath)))
}

// CommunityURL returns the community chat/forum URL.
func CommunityURL() string {
	return sanitizeURL(resolve(envCommunityURL, projectCommunityURL, defaultCommunityURL))
}
