//go:build integration

package birdnet

import (
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestProcessChunkWithOccurrence(t *testing.T) {
	t.Parallel()

	// This test requires a fully initialized BirdNET instance
	// with models loaded for integration testing
	// In a real integration test, you would:
	// 1. Initialize BirdNET with test models
	// 2. Process a chunk of audio
	// 3. Verify that the returned notes have occurrence values set
	
	// TODO: Implement full integration test when models are available
	t.Skip("Integration test not yet implemented")
}