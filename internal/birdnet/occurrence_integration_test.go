//go:build integration

package birdnet

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProcessChunkWithOccurrence tests end-to-end processing with occurrence probability calculations.
//
// Prerequisites to run this integration test:
// 1. BirdNET model files must be available:
//    - Analysis model: BirdNET_GLOBAL_6K_V2.4_MData_Model.tflite
//    - Range filter model: BirdNET_GLOBAL_6K_V2.4_MData_Model_Range.tflite
// 2. Test audio data in WAV format (3-second chunks at 48kHz sample rate)
// 3. Set environment variables:
//    - BIRDNET_MODEL_PATH=/path/to/models/directory
//    - BIRDNET_TEST_AUDIO_PATH=/path/to/test/audio/files
//    - BIRDNET_LATITUDE=52.5200 (example coordinates)
//    - BIRDNET_LONGITUDE=13.4050
//
// To run locally:
//   go test -tags=integration -v ./internal/birdnet/
//
// To run in CI (when models are available):
//   docker run -v /models:/models -v /testdata:/testdata \
//     -e BIRDNET_MODEL_PATH=/models \
//     -e BIRDNET_TEST_AUDIO_PATH=/testdata \
//     -e BIRDNET_LATITUDE=52.5200 \
//     -e BIRDNET_LONGITUDE=13.4050 \
//     birdnet-go:test go test -tags=integration -v ./internal/birdnet/
//
// Expected test data structure:
//   /testdata/
//     ├── positive/          # Audio files with known bird detections
//     │   ├── blackbird.wav  # 3-second WAV file with Turdus merula
//     │   └── robin.wav      # 3-second WAV file with Erithacus rubecula
//     └── negative/          # Audio files with no bird sounds
//         └── silence.wav    # 3-second WAV file with ambient noise only
//
// When implementing, use testify assertions:
//   - require.NoError(t, err) for critical assertions that should stop the test
//   - assert.Equal(t, expected, actual) for non-critical comparisons
//   - assert.Greater(t, actual, threshold) for confidence threshold checks
func TestProcessChunkWithOccurrence(t *testing.T) {
	t.Parallel()

	// TODO: Implement full integration test - blocked on model availability
	// Reference issue: https://github.com/tphakala/birdnet-go/issues/XXXX
	// CI job: integration-tests-with-models
	//
	// Example implementation structure:
	//   1. Load test audio: require.NoError(t, err)
	//   2. Initialize analyzer: require.NoError(t, err)
	//   3. Process chunk: require.NoError(t, err)
	//   4. Verify detections: assert.Equal(t, expectedSpecies, actualSpecies)
	//   5. Check occurrence probability: assert.Greater(t, probability, 0.0)
	t.Skip("Integration test requires BirdNET model files - see function documentation for setup instructions")

	// Suppress unused import warnings until implementation
	_ = assert.Equal
	_ = require.NoError
}