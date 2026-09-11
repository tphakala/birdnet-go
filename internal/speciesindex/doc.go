// Package speciesindex is the single builder and owner of the process-wide
// species-name lookup index. It folds the two historically duplicated
// name-map builders (internal/datastore/v2only and internal/api/v2) into one
// leaf package so their forward display (scientific -> common) and reverse
// search (common -> scientific) stay in lockstep by construction.
//
// A Snapshot is an immutable set of maps built once from a label list and a
// name resolver. It carries the three name maps the two builders produced,
// byte for byte, plus a set of memo maps (canonical key per label, labels per
// canonical key, label per scientific name) computed once at build time so no
// request path ever calls openfauna.CanonicalName per label.
//
// A Service owns the current Snapshot behind an atomic.Pointer for lock-free
// reads. Rebuild swaps in a new Snapshot under a build mutex, outside every
// caller lock; Snapshot returns the current one and is never nil (Empty before
// the first Rebuild). Rebuilds run only on model load, unload, reload and
// locale change, so the extra memo cost is paid off the request path.
//
// Ownership is transitional in Phase 1: the datastore and the api/v2 facade
// each own their own Service, exactly reproducing today's two independent
// builds. Phase 2a replaces both with a single orchestrator-owned Service.
//
// This package imports only leaf packages (internal/datastore,
// internal/detection, internal/openfauna); it must never import
// internal/classifier, any internal/api package, or internal/analysis, so it
// stays importable from every one of them. import_guard_test.go enforces this.
package speciesindex
