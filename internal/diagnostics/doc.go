// Package diagnostics provides a durable, filesystem-backed boot and
// lifecycle event journal that survives database loss. It records one
// full "boot" snapshot per startup plus thin lifecycle events
// (db_fresh_created, migration, consolidation, config_defaulted,
// shutdown) and diffs consecutive boots to surface anomalies such as a
// lost database (GitHub #3956).
//
// The journal lives at <configDir>/diagnostics/journal.jsonl, one JSON
// record per line. All writes are best-effort: failures are logged at
// WARN and never block or crash startup.
//
// Import policy: this package may import conf, logger, privacy, sysinfo,
// and errors ONLY. It must NEVER import internal/telemetry (heavy,
// wrong-direction dependency; callers that already import telemetry
// report the returned anomalies themselves), internal/datastore,
// internal/support, internal/analysis, or internal/app, so those
// packages can depend on it without an import cycle. Conversely,
// internal/conf must never import this package.
package diagnostics
