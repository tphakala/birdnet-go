package conf

import "slices"

// supportEnvAllowlist names the environment variables a support dump may
// capture verbatim, together with their values.
//
// Why this list exists at all: the native-encoder gates are env-only and are
// read live per export, so nothing in config.yaml records them. A dump that
// arrives without a clip export inside the captured log window therefore cannot
// answer whether the reporter was running a native encoder or FFmpeg, which is
// the first thing to establish while the gate is still opt-in.
//
// Coverage before this was partial and asymmetric: the deployment collector
// reads unit-file Environment= lines, so a systemd user who set a gate in their
// unit file might have it captured, while Docker and compose users, who set it
// with -e or in the compose file, had no coverage whatsoever.
//
// Why an allowlist and not a BIRDNET_ prefix scan: the process environment can
// hold credentials, and a future BIRDNET_-prefixed variable could carry one. A
// prefix scan would capture it the day it is added, silently, in a file the
// user uploads to a third party. This list only grows by someone deciding a
// specific variable is safe to disclose.
//
// The entries here are all "native" or unset, so they are not secrets. Add a
// future BIRDNET_* runtime gate here so it shows up in dumps without needing
// the collector changed again; do not add anything that identifies the
// operator or their location (the coordinate and locale variables in env.go are
// deliberately absent), and do not add anything already recorded in
// config.yaml, which the dump captures separately.
//
// Unexported and returned by copy rather than exported directly: this list is a
// privacy control, deciding what gets written into a file the user uploads to a
// third party. An exported slice can be appended to or overwritten in place by
// any package in the tree, so the "only grows by someone deciding a specific
// variable is safe to disclose" rule above would be enforced by nothing.
//
//nolint:gochecknoglobals // package-level allowlist, never mutated after init
var supportEnvAllowlist = []string{
	EnvNativeAACEncoder,
	// Historical: no longer read by any code path, because native FLAC, native
	// Opus and native HLS are now unconditional. Captured anyway, so a dump
	// distinguishes "the operator set nothing" from "the operator set a variable
	// that stopped doing anything", which otherwise look identical from the
	// outside.
	"BIRDNET_FLAC_ENCODER",
	"BIRDNET_OPUS_ENCODER",
	"BIRDNET_HLS_ENCODER",
	// Not a BIRDNET_ variable, and the one entry here whose value is withheld:
	// the name matches the "endpoint" sensitive-key pattern on its underscore
	// boundary, so a dump records that it is set but redacts what it is set to.
	// That is the point. It silently redirects every model download and is
	// recorded nowhere in config.yaml, so a dump from a user whose installs fail
	// or fetch from an unexpected host cannot otherwise distinguish "unset" from
	// "pointed at a broken mirror"; and because users set it for the HuggingFace
	// Python tooling rather than for BirdNET-Go, the value can carry credentials
	// this project never asked for.
	HuggingFaceEndpointEnvVar,
}

// SupportEnvAllowlist returns a copy of the environment variables a support dump
// may capture verbatim. A copy, so no caller can widen what a dump discloses;
// see supportEnvAllowlist for the full rationale.
func SupportEnvAllowlist() []string { return slices.Clone(supportEnvAllowlist) }
