//go:build ruleguard

package gorules

import "github.com/quasilyte/go-ruleguard/dsl"

// SettingsPublishThroughWriter keeps runtime settings changes on the one path
// that is safe against concurrent writers.
//
// Bad pattern:
//
//	current := conf.GetSettings()
//	updated := conf.CloneSettings(current)
//	// ... mutate updated ...
//	conf.StoreSettings(updated)
//
// Good pattern:
//
//	_, _, err := conf.UpdateSettings(func(current, updated *conf.Settings) error {
//		// ... mutate updated ...
//		return nil
//	})
//
// conf.StoreSettings publishes without any lock, so a writer that read its
// baseline earlier can overwrite a change another writer (a settings save, a
// model install, a range filter rebuild) published in between, in memory and,
// because SaveSettings persists the published snapshot, on disk. conf.UpdateSettings
// reads the baseline and publishes under one process-wide writer lock.
//
// Test files and the test-support packages that seed or clear the snapshot
// (internal/conf/conftest, internal/security/securitytest) are exempt. Calls
// inside package conf are unqualified and do not match, which is intended: Load
// publishes the first snapshot there.
func SettingsPublishThroughWriter(m dsl.Matcher) {
	m.Import("github.com/tphakala/birdnet-go/internal/conf")

	m.Match(
		`conf.StoreSettings($_)`,
	).
		Where(!m.File().Name.Matches(`_test\.go$`) &&
			!m.File().PkgPath.Matches(`internal/(conf/conftest|security/securitytest)$`)).
		Report("publish settings through conf.UpdateSettings so the baseline is read under the settings writer lock")
}
