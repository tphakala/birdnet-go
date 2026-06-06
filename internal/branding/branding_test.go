package branding

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Note: these tests mutate process-global state (environment variables and the
// ldflags-target package vars), so they intentionally do NOT call t.Parallel().

func TestDefaults(t *testing.T) {
	// With no environment overrides and empty (un-baked) ldflags vars, the
	// getters fall back to the built-in upstream defaults.
	assert.Equal(t, "BirdNET-Go", Name())
	assert.Equal(t, "https://github.com/tphakala/birdnet-go", RepoURL())
	assert.Equal(t, "https://github.com/tphakala/birdnet-go/issues", IssuesURL())
	assert.Equal(t, "https://github.com/tphakala/birdnet-go/issues/new", NewIssueURL())
	assert.Equal(t, "https://github.com/tphakala/birdnet-go", SupportURL())
	assert.Equal(t, "https://discord.gg/gcSCFGUtsd", CommunityURL())
}

func TestEnvOverride(t *testing.T) {
	t.Setenv(envName, "MyFork")
	t.Setenv(envRepoURL, "https://example.com/me/fork")
	t.Setenv(envCommunityURL, "https://chat.example.com")

	assert.Equal(t, "MyFork", Name())
	assert.Equal(t, "https://example.com/me/fork", RepoURL())
	// Derived values follow the env-overridden repo URL.
	assert.Equal(t, "https://example.com/me/fork/issues", IssuesURL())
	assert.Equal(t, "https://example.com/me/fork/issues/new", NewIssueURL())
	assert.Equal(t, "https://example.com/me/fork", SupportURL())
	assert.Equal(t, "https://chat.example.com", CommunityURL())
}

func TestExplicitDerivedOverride(t *testing.T) {
	// Explicit overrides win over derivation.
	t.Setenv(envIssuesURL, "https://example.com/tracker")
	t.Setenv(envNewIssueURL, "https://example.com/tracker/file")
	t.Setenv(envSupportURL, "https://support.example.com")

	assert.Equal(t, "https://example.com/tracker", IssuesURL())
	assert.Equal(t, "https://example.com/tracker/file", NewIssueURL())
	assert.Equal(t, "https://support.example.com", SupportURL())
}

func TestDerivationCollapsesTrailingSlash(t *testing.T) {
	t.Setenv(envRepoURL, "https://example.com/fork/")

	// Derived sub-paths normalize the trailing slash so there is no double slash.
	assert.Equal(t, "https://example.com/fork/issues", IssuesURL())
	assert.Equal(t, "https://example.com/fork/issues/new", NewIssueURL())
	// The repo and support URLs are returned verbatim (a trailing slash on a
	// repository URL is harmless and reflects exactly what the operator set).
	assert.Equal(t, "https://example.com/fork/", RepoURL())
	assert.Equal(t, "https://example.com/fork/", SupportURL())
}

func TestWhitespaceCountsAsUnset(t *testing.T) {
	t.Setenv(envRepoURL, "   ")
	t.Setenv(envName, "\t")

	assert.Equal(t, "https://github.com/tphakala/birdnet-go", RepoURL())
	assert.Equal(t, "BirdNET-Go", Name())
}

func TestLdflagsVarPrecedence(t *testing.T) {
	// Simulate a value baked in at link time via -ldflags -X.
	orig := projectRepoURL
	t.Cleanup(func() { projectRepoURL = orig })
	projectRepoURL = "https://baked.example.com/repo"

	assert.Equal(t, "https://baked.example.com/repo", RepoURL())
	assert.Equal(t, "https://baked.example.com/repo/issues", IssuesURL())

	// The environment variable still beats the baked-in value.
	t.Setenv(envRepoURL, "https://env.example.com/repo")
	assert.Equal(t, "https://env.example.com/repo", RepoURL())
}
