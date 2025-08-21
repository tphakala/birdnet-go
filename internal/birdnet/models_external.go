//go:build noembed

package birdnet

// This file is included when building with -tags noembed
// It provides empty model data variables for a non-monolithic build
// where models must be loaded from external files.

// modelData is nil when models are not embedded
var modelData []byte

// metaModelDataV1 is nil when models are not embedded
var metaModelDataV1 []byte

// metaModelDataV2 is nil when models are not embedded
var metaModelDataV2 []byte

// hasEmbeddedModels indicates whether models are embedded in the binary
// This is a var instead of const to allow test overrides
var hasEmbeddedModels = false
