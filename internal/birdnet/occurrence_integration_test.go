//go:build integration

package birdnet

import (
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

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
func TestProcessChunkWithOccurrence(t *testing.T) {
	t.Parallel()

	// TODO: Implement full integration test - blocked on model availability
	// Reference issue: https://github.com/tphakala/birdnet-go/issues/XXXX
	// CI job: integration-tests-with-models
	t.Skip("Integration test requires BirdNET model files - see function documentation for setup instructions")
}