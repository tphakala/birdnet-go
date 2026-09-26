// Package streamtest is an importable test-support package that characterizes
// BirdNET-Go's network stream ingest at the manager seam.
//
// It exists so the observable behaviour of the current FFmpeg ingest path can be
// pinned by a single suite before the native go-audio-stream producer is built,
// and so that same suite can then be run unchanged against the native producer
// to prove parity. Production code never imports this package.
//
// The suite is producer-agnostic. RunManagerContract drives any producer through
// the narrow Manager interface, reading health through the Health interface and
// obtaining live streams and server control through the Fixture interface. A
// producer participates by supplying a ManagerFactory that adapts its concrete
// manager to Manager, plus a Fixture; the FFmpeg producer's adapter and a
// MediaMTX-backed Fixture live in the integration-tagged tests.
//
// The signal-analysis helpers (DominantFrequency, RMSDBFS) are pure and unit
// tested; the MediaMTX-backed Fixture (fixture_mediamtx.go) is behind the
// integration build tag because it needs a container runtime.
package streamtest
