package branding

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Note: these tests mutate process-global state (environment variables and the
// ldflags-target package vars), so they intentionally do NOT call t.Parallel().

// resetBrandingState makes a test hermetic against the caller's environment: it
// clears every BIRDNET_GO_PROJECT_* env var and the ldflags-target package vars
// (restoring them after the test) so the getters resolve deterministically
// regardless of any inherited env or baked-in build values.
func resetBrandingState(t *testing.T) {
	t.Helper()

	for _, key := range []string{
		envName, envRepoURL, envIssuesURL, envNewIssueURL, envSupportURL,
		envDiscussionsURL, envReleasesURL, envCommunityURL,
	} {
		t.Setenv(key, "")
	}

	origName, origRepo := projectName, projectRepoURL
	origIssues, origNewIssue := projectIssuesURL, projectNewIssueURL
	origSupport, origCommunity := projectSupportURL, projectCommunityURL
	origDiscussions, origReleases := projectDiscussionsURL, projectReleasesURL
	projectName, projectRepoURL = "", ""
	projectIssuesURL, projectNewIssueURL = "", ""
	projectSupportURL, projectCommunityURL = "", ""
	projectDiscussionsURL, projectReleasesURL = "", ""
	t.Cleanup(func() {
		projectName, projectRepoURL = origName, origRepo
		projectIssuesURL, projectNewIssueURL = origIssues, origNewIssue
		projectSupportURL, projectCommunityURL = origSupport, origCommunity
		projectDiscussionsURL, projectReleasesURL = origDiscussions, origReleases
	})
}

func TestDefaults(t *testing.T) {
	resetBrandingState(t)
	// With no environment overrides and empty (un-baked) ldflags vars, the
	// getters fall back to the built-in upstream defaults.
	assert.Equal(t, "BirdNET-Go", Name())
	assert.Equal(t, "https://github.com/tphakala/birdnet-go", RepoURL())
	assert.Equal(t, "https://github.com/tphakala/birdnet-go/issues", IssuesURL())
	assert.Equal(t, "https://github.com/tphakala/birdnet-go/issues/new", NewIssueURL())
	assert.Equal(t, "https://github.com/tphakala/birdnet-go", SupportURL())
	assert.Equal(t, "https://github.com/tphakala/birdnet-go/discussions", DiscussionsURL())
	assert.Equal(t, "https://github.com/tphakala/birdnet-go/releases", ReleasesURL())
	assert.Equal(t, "https://discord.gg/gcSCFGUtsd", CommunityURL())
}

func TestEnvOverride(t *testing.T) {
	resetBrandingState(t)
	t.Setenv(envName, "MyFork")
	t.Setenv(envRepoURL, "https://example.com/me/fork")
	t.Setenv(envCommunityURL, "https://chat.example.com")

	assert.Equal(t, "MyFork", Name())
	assert.Equal(t, "https://example.com/me/fork", RepoURL())
	// Derived values follow the env-overridden repo URL.
	assert.Equal(t, "https://example.com/me/fork/issues", IssuesURL())
	assert.Equal(t, "https://example.com/me/fork/issues/new", NewIssueURL())
	assert.Equal(t, "https://example.com/me/fork", SupportURL())
	assert.Equal(t, "https://example.com/me/fork/discussions", DiscussionsURL())
	assert.Equal(t, "https://example.com/me/fork/releases", ReleasesURL())
	assert.Equal(t, "https://chat.example.com", CommunityURL())
}

func TestExplicitDerivedOverride(t *testing.T) {
	resetBrandingState(t)
	// Explicit overrides win over derivation.
	t.Setenv(envIssuesURL, "https://example.com/tracker")
	t.Setenv(envNewIssueURL, "https://example.com/tracker/file")
	t.Setenv(envSupportURL, "https://support.example.com")
	t.Setenv(envDiscussionsURL, "https://forum.example.com")
	t.Setenv(envReleasesURL, "https://example.com/downloads")

	assert.Equal(t, "https://example.com/tracker", IssuesURL())
	assert.Equal(t, "https://example.com/tracker/file", NewIssueURL())
	assert.Equal(t, "https://support.example.com", SupportURL())
	assert.Equal(t, "https://forum.example.com", DiscussionsURL())
	assert.Equal(t, "https://example.com/downloads", ReleasesURL())
}

func TestDerivationCollapsesTrailingSlash(t *testing.T) {
	resetBrandingState(t)
	t.Setenv(envRepoURL, "https://example.com/fork/")

	// Derived sub-paths normalize the trailing slash so there is no double slash.
	assert.Equal(t, "https://example.com/fork/issues", IssuesURL())
	assert.Equal(t, "https://example.com/fork/issues/new", NewIssueURL())
	assert.Equal(t, "https://example.com/fork/discussions", DiscussionsURL())
	assert.Equal(t, "https://example.com/fork/releases", ReleasesURL())
	// The repo and support URLs are returned verbatim (a trailing slash on a
	// repository URL is harmless and reflects exactly what the operator set).
	assert.Equal(t, "https://example.com/fork/", RepoURL())
	assert.Equal(t, "https://example.com/fork/", SupportURL())
}

func TestWhitespaceCountsAsUnset(t *testing.T) {
	resetBrandingState(t)
	t.Setenv(envRepoURL, "   ")
	t.Setenv(envName, "\t")

	assert.Equal(t, "https://github.com/tphakala/birdnet-go", RepoURL())
	assert.Equal(t, "BirdNET-Go", Name())
}

func TestLdflagsVarPrecedence(t *testing.T) {
	resetBrandingState(t)
	// Simulate a value baked in at link time via -ldflags -X.
	projectRepoURL = "https://baked.example.com/repo"

	assert.Equal(t, "https://baked.example.com/repo", RepoURL())
	assert.Equal(t, "https://baked.example.com/repo/issues", IssuesURL())

	// A value baked directly into a derived getter's own var wins over the
	// repo-derived default (covers the same tier-2 plumbing for the newer vars).
	projectReleasesURL = "https://baked.example.com/downloads"
	assert.Equal(t, "https://baked.example.com/downloads", ReleasesURL())
	// Discussions has no baked value, so it still derives from the baked repo.
	assert.Equal(t, "https://baked.example.com/repo/discussions", DiscussionsURL())

	// The environment variable still beats the baked-in value.
	t.Setenv(envRepoURL, "https://env.example.com/repo")
	assert.Equal(t, "https://env.example.com/repo", RepoURL())
}

func TestSanitizesCredentials(t *testing.T) {
	resetBrandingState(t)
	// Credentials an operator accidentally embeds in a configured URL must be
	// stripped before any getter returns it, since the values feed the public
	// app-config endpoint, outbound User-Agent headers, and logs.
	t.Setenv(envRepoURL, "https://user:secret@example.com/fork")

	assert.Equal(t, "https://example.com/fork", RepoURL())
	assert.Equal(t, "https://example.com/fork/issues", IssuesURL())
	assert.Equal(t, "https://example.com/fork/issues/new", NewIssueURL())
	assert.Equal(t, "https://example.com/fork", SupportURL())
	assert.Equal(t, "https://example.com/fork/discussions", DiscussionsURL())
	assert.Equal(t, "https://example.com/fork/releases", ReleasesURL())
}
