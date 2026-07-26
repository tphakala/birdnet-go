// useragent.go: the outbound User-Agent shared by every HTTP path in this
// package.
//
// This used to live in wikipedia.go under a Wikimedia-robot-policy comment, but
// the provider-agnostic image download in filecache.go depends on it too, so it
// belongs in a neutral file.
package imageprovider

import (
	"fmt"
	"runtime"
	"sync/atomic"

	"github.com/tphakala/birdnet-go/internal/branding"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	// User-Agent constants following Wikimedia robot policy
	// https://foundation.wikimedia.org/wiki/Policy:Wikimedia_Foundation_User-Agent_Policy
	// The contact URL is resolved at runtime from the branding package (see
	// buildUserAgent) so forks identify themselves rather than the upstream repo.
	//
	// Do not "correct" userAgentName to the hyphenated project name. Wikimedia's
	// edge refuses any User-Agent whose leading token is "birdnet-go",
	// case-insensitively, on both upload.wikimedia.org and api.php, even when
	// the header is otherwise fully policy-compliant. The hyphen-less spelling
	// is the one that works.
	userAgentName    = "BirdNETGo"
	userAgentLibrary = "Go-HTTP-Client"
)

// cachedAppUserAgent memoizes the User-Agent. It is an atomic.Pointer rather
// than a sync.OnceValue because conf.Setting() can return nil, and Version is
// published after startup: latching at first call could pin a version-less
// string for the process lifetime. Only a non-empty version is memoized, so an
// early call does not poison the value.
var cachedAppUserAgent atomic.Pointer[string]

// currentAppVersion returns the running application version. It is empty both when
// conf.Setting() returns nil (the config failed to load) and when Version has not been
// published yet; callers only need "not yet usable", so the two are equivalent here.
func currentAppVersion() string {
	if settings := conf.Setting(); settings != nil {
		return settings.Version
	}
	return ""
}

// buildUserAgent constructs a user-agent string that complies with Wikimedia's
// robot policy.
func buildUserAgent(appVersion string) string {
	if appVersion == "" {
		appVersion = "unknown"
	}

	goVersion := runtime.Version()

	// Format: BirdNETGo/1.0.0 (https://github.com/tphakala/birdnet-go) Go-HTTP-Client/go1.21.0
	return fmt.Sprintf("%s/%s (%s) %s/%s",
		userAgentName, appVersion, branding.RepoURL(), userAgentLibrary, goVersion)
}

// appUserAgent returns the User-Agent for every outbound request this package
// makes, both the MediaWiki API calls and the image byte downloads.
//
// Wikimedia answers 403 to Go's default "Go-http-client/1.1", so the byte fetch
// needs the same policy-compliant header the API path sends. Resolving it here
// also makes both paths self-heal: the API provider used to latch the string at
// construction, so one built before main.go published Version sent
// "BirdNETGo/unknown" for the whole process lifetime.
func appUserAgent() string {
	if ua := cachedAppUserAgent.Load(); ua != nil {
		return *ua
	}
	version := currentAppVersion()
	ua := buildUserAgent(version)
	if version != "" {
		// CompareAndSwap rather than Store so concurrent first callers publish once.
		cachedAppUserAgent.CompareAndSwap(nil, &ua)
	}
	return ua
}

// logUserAgentValidation logs the constructed user-agent for debugging purposes.
func logUserAgentValidation(appVersion string) {
	GetLogger().Debug("Wikipedia user-agent validation",
		logger.String("provider", wikiProviderName),
		logger.String("user_agent", buildUserAgent(appVersion)),
		logger.String("complies_with_policy", "https://foundation.wikimedia.org/wiki/Policy:User-Agent_policy"),
		logger.String("contains_app_name", userAgentName),
		logger.String("contains_version", appVersion),
		logger.String("contains_contact", branding.RepoURL()),
		logger.String("contains_library", userAgentLibrary),
		logger.String("go_version", runtime.Version()))
}
