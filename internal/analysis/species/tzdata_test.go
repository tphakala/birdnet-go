//go:build test
// +build test

package species

// Import tzdata to embed timezone database for portable timezone tests.
// This ensures time.LoadLocation works in minimal containers without OS tzdata.
// See: https://pkg.go.dev/time/tzdata
import _ "time/tzdata"
