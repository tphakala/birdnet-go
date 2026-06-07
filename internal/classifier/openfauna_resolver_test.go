package classifier

import "github.com/tphakala/birdnet-go/internal/openfauna"

// Compile-time check that openfauna.Resolver implements NameResolver. The adapter
// lives in internal/openfauna (alongside the data it wraps); this assertion keeps
// it conformant without classifier importing it in non-test code (wiring is a
// later step). If the interface drifts, the classifier test build fails here.
var _ NameResolver = (*openfauna.Resolver)(nil)
