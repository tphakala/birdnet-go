// Package useragent builds the outbound User-Agent headers BirdNET-Go sends to
// third-party services, chiefly Wikimedia (the MediaWiki action API and the
// upload.wikimedia.org image host).
//
// It exists because two packages — internal/imageprovider and
// internal/guideprovider — each need a policy-compliant header for Wikimedia,
// and each had grown its own copy of "resolve the version, resolve the contact
// URL, format a string". The version and the contact URL are the parts that must
// not drift: the contact URL comes from internal/branding so a fork advertises
// its own operator (which is exactly what Wikimedia's policy relies on), and the
// version must be read at call time because main.go publishes it after startup.
//
// Two FORMS are exported rather than one, because both are empirically load-
// bearing against the Wikimedia edge and neither can be substituted for the
// other without re-testing against a live service:
//
//   - Product: "BirdNETGo/1.2.3 (https://…) Go-HTTP-Client/go1.26.0".
//     Do not "correct" ProductName to the hyphenated project name: the edge
//     refuses any User-Agent whose LEADING token is "birdnet-go",
//     case-insensitively, on both upload.wikimedia.org and api.php, even when
//     the header is otherwise fully policy-compliant.
//   - PoliteBot: "Mozilla/5.0 (compatible; BirdNET-Go/1.2.3; +https://…)".
//     UA-policy enforcement (phab T400119) answers 403 to a bare
//     "App/1.0 (url)" agent; the standard polite-bot wrapper is accepted. The
//     hyphenated name is safe here because it is not the leading token.
//
// Policy: https://foundation.wikimedia.org/wiki/Policy:Wikimedia_Foundation_User-Agent_Policy
package useragent

import (
	"fmt"
	"runtime"
	"sync/atomic"

	"github.com/tphakala/birdnet-go/internal/branding"
	"github.com/tphakala/birdnet-go/internal/conf"
)

const (
	// ProductName is the leading product token of the Product form. See the
	// package doc: the hyphen-less spelling is the one the Wikimedia edge accepts.
	ProductName = "BirdNETGo"
	// LibraryToken is the trailing library product of the Product form.
	LibraryToken = "Go-HTTP-Client"
	// PoliteBotName is the application token inside the PoliteBot comment.
	PoliteBotName = "BirdNET-Go"
	// UnknownVersion stands in when the running version is not yet published.
	UnknownVersion = "unknown"
)

// cached memoizes one built header. It is an atomic.Pointer rather than a
// sync.OnceValue because conf.Setting() can return nil and Version is published
// after startup: latching at first call could pin a version-less string for the
// process lifetime. Only a non-empty version is memoized, so an early call does
// not poison the value.
type cached struct {
	value atomic.Pointer[string]
	build func(version string) string
}

// get returns the memoized header, building it on first use and re-building on
// every call until the running version is known.
func (c *cached) get() string {
	if ua := c.value.Load(); ua != nil {
		return *ua
	}
	version := AppVersion()
	ua := c.build(version)
	if version != "" {
		// CompareAndSwap rather than Store so concurrent first callers publish once.
		c.value.CompareAndSwap(nil, &ua)
	}
	return ua
}

var (
	productCache   = cached{build: BuildProduct}
	politeBotCache = cached{build: BuildPoliteBot}
)

// AppVersion returns the running application version, or "" when it is not yet
// usable. That covers both conf.Setting() returning nil (the config failed to
// load) and Version not having been published yet; callers only need "not yet
// usable", so the two are equivalent here.
func AppVersion() string {
	if settings := conf.Setting(); settings != nil {
		return settings.Version
	}
	return ""
}

// BuildProduct formats the product-comment-product User-Agent for an explicit
// version. An empty version becomes UnknownVersion. Exported for tests and for
// callers that log the header for a version other than the running one.
func BuildProduct(appVersion string) string {
	if appVersion == "" {
		appVersion = UnknownVersion
	}
	// Format: BirdNETGo/1.0.0 (https://github.com/tphakala/birdnet-go) Go-HTTP-Client/go1.26.0
	return fmt.Sprintf("%s/%s (%s) %s/%s",
		ProductName, appVersion, branding.RepoURL(), LibraryToken, runtime.Version())
}

// Product returns the product-comment-product User-Agent for the running
// version, memoized once that version is known.
func Product() string { return productCache.get() }

// BuildPoliteBot formats the polite-bot User-Agent for an explicit version. An
// empty version becomes UnknownVersion.
func BuildPoliteBot(appVersion string) string {
	if appVersion == "" {
		appVersion = UnknownVersion
	}
	// Format: Mozilla/5.0 (compatible; BirdNET-Go/1.0.0; +https://github.com/tphakala/birdnet-go)
	return fmt.Sprintf("Mozilla/5.0 (compatible; %s/%s; +%s)",
		PoliteBotName, appVersion, branding.RepoURL())
}

// PoliteBot returns the polite-bot User-Agent for the running version, memoized
// once that version is known.
func PoliteBot() string { return politeBotCache.get() }
