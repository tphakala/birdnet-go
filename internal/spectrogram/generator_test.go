package spectrogram

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/audiotemp"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// TestNewGenerator tests generator creation
func TestNewGenerator(t *testing.T) {
	env := setupTestEnv(t)

	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))
	require.NotNil(t, gen, "NewGenerator() returned nil")
	assert.Equal(t, env.Settings, gen.settings, "NewGenerator() did not set settings correctly")
	assert.Equal(t, env.SFS, gen.sfs, "NewGenerator() did not set sfs correctly")
}

// TestGenerator_EnsureOutputDirectory tests directory creation
func TestGenerator_EnsureOutputDirectory(t *testing.T) {
	env := setupTestEnv(t)

	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	// Test creating a nested directory
	outputPath := filepath.Join(env.TempDir, "subdir", "test.png")
	err := gen.ensureOutputDirectory(outputPath)
	require.NoError(t, err, "ensureOutputDirectory() error")

	// Verify directory was created
	outputDir := filepath.Dir(outputPath)
	_, err = os.Stat(outputDir)
	assert.False(t, os.IsNotExist(err), "ensureOutputDirectory() did not create directory: %v", outputDir)
}

// TestGenerator_EnsureOutputDirectory_PathTraversal tests path validation
func TestGenerator_EnsureOutputDirectory_PathTraversal(t *testing.T) {
	env := setupTestEnv(t)

	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	// Test path traversal attempt
	outputPath := filepath.Join(env.TempDir, "..", "escape", "test.png")
	err := gen.ensureOutputDirectory(outputPath)
	assert.Error(t, err, "ensureOutputDirectory() should reject path traversal")
}

// assertNoSpectrogramTemp fails if any "<outputPath>.<pid>.<seq>.temp" sidecar
// temp from an interrupted or raced render was left behind next to outputPath.
// outputPath must not contain glob metacharacters (callers pass t.TempDir paths).
func assertNoSpectrogramTemp(t *testing.T, outputPath string) {
	t.Helper()
	leftover, err := filepath.Glob(outputPath + ".*" + audiotemp.Ext)
	require.NoError(t, err)
	assert.Empty(t, leftover, "spectrogram temp files must not be left behind")
}

// TestGenerator_GenerateFromPCM_FailureLeavesNoPartialOutput covers a render
// that fails (4 bytes of PCM are too short to draw): the entry point returns an
// error and publishes nothing, leaving neither a final .png nor a temp sidecar.
func TestGenerator_GenerateFromPCM_FailureLeavesNoPartialOutput(t *testing.T) {
	t.Parallel()
	env := setupTestEnv(t)
	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	outputPath := filepath.Join(env.TempDir, "fail.png")
	err := gen.GenerateFromPCM(t.Context(), []byte{0, 1, 2, 3}, outputPath, 400, false, 0)

	require.Error(t, err)
	assert.NoFileExists(t, outputPath, "a failed render must not leave a partial final .png")
	assertNoSpectrogramTemp(t, outputPath)
}

// TestGenerator_GenerateFromFile_FailureLeavesNoPartialOutput is the file-input
// counterpart: a WAV whose bytes cannot be decoded fails the render, so nothing
// is published.
func TestGenerator_GenerateFromFile_FailureLeavesNoPartialOutput(t *testing.T) {
	t.Parallel()
	env := setupTestEnv(t)
	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	audioPath := filepath.Join(env.TempDir, "in.wav")
	require.NoError(t, os.WriteFile(audioPath, []byte("fake audio"), 0o600))
	outputPath := filepath.Join(env.TempDir, "fail.png")

	err := gen.GenerateFromFile(t.Context(), audioPath, outputPath, 400, false)

	require.Error(t, err)
	assert.NoFileExists(t, outputPath, "a failed render must not leave a partial final .png")
	assertNoSpectrogramTemp(t, outputPath)
}

// TestGenerator_GenerateFromPCM_AtomicWrite verifies the success path: the final
// spectrogram is published and the temp sidecar is removed. It asserts the
// post-conditions (final exists, non-empty, no leftover temp); it does not, and a
// unit test cannot cheaply, prove the no-partial-window invariant directly.
func TestGenerator_GenerateFromPCM_AtomicWrite(t *testing.T) {
	t.Parallel()
	env := setupTestEnv(t)
	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	// 0.5s of 16-bit mono PCM with a non-constant signal so the render has content.
	// 251 is a prime, so the byte ramp never settles into a constant value.
	pcm := make([]byte, defaultSampleRate) // defaultSampleRate bytes = 0.5s of int16 mono
	for i := range pcm {
		pcm[i] = byte(i % 251)
	}

	outputPath := filepath.Join(env.TempDir, "ok.png")
	err := gen.GenerateFromPCM(t.Context(), pcm, outputPath, 400, true, defaultSampleRate)
	require.NoError(t, err)

	info, statErr := os.Stat(outputPath)
	require.NoError(t, statErr, "the final spectrogram must exist after a successful render")
	assert.Positive(t, info.Size(), "the published spectrogram must be non-empty")
	assertNoSpectrogramTemp(t, outputPath)
}

// TestGenerateFromFile_Validation tests input validation for GenerateFromFile.
func TestGenerateFromFile_Validation(t *testing.T) {
	env := setupTestEnv(t)

	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	tests := []struct {
		name       string
		audioPath  string
		outputPath string
		width      int
		wantErr    bool
		errContain string
	}{
		{
			name:       "empty output path",
			audioPath:  filepath.Join(env.TempDir, "test.wav"),
			outputPath: "",
			width:      400,
			wantErr:    true,
			errContain: "output path is empty",
		},
		{
			name:       "relative output path",
			audioPath:  filepath.Join(env.TempDir, "test.wav"),
			outputPath: "relative/path/test.png",
			width:      400,
			wantErr:    true,
			errContain: "output path must be absolute",
		},
		{
			name:       "zero width",
			audioPath:  filepath.Join(env.TempDir, "test.wav"),
			outputPath: filepath.Join(env.TempDir, "test.png"),
			width:      0,
			wantErr:    true,
			errContain: "width must be positive",
		},
		{
			name:       "negative width",
			audioPath:  filepath.Join(env.TempDir, "test.wav"),
			outputPath: filepath.Join(env.TempDir, "test.png"),
			width:      -100,
			wantErr:    true,
			errContain: "width must be positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := gen.GenerateFromFile(t.Context(), tt.audioPath, tt.outputPath, tt.width, false)

			if tt.wantErr {
				require.Error(t, err, "expected error for invalid input")
				assert.Contains(t, err.Error(), tt.errContain, "error should contain expected message")
			} else {
				assert.NoError(t, err, "expected no error for valid input")
			}
		})
	}
}

// TestGenerateFromPCM_Validation tests input validation for GenerateFromPCM.
func TestGenerateFromPCM_Validation(t *testing.T) {
	env := setupTestEnv(t)

	gen := NewGenerator(env.Settings, env.SFS, logger.Global().Module("spectrogram.test"))

	tests := []struct {
		name       string
		pcmData    []byte
		outputPath string
		width      int
		wantErr    bool
		errContain string
	}{
		{
			name:       "empty output path",
			pcmData:    []byte{0, 1, 2, 3},
			outputPath: "",
			width:      400,
			wantErr:    true,
			errContain: "output path is empty",
		},
		{
			name:       "relative output path",
			pcmData:    []byte{0, 1, 2, 3},
			outputPath: "relative/path/test.png",
			width:      400,
			wantErr:    true,
			errContain: "output path must be absolute",
		},
		{
			name:       "zero width",
			pcmData:    []byte{0, 1, 2, 3},
			outputPath: filepath.Join(env.TempDir, "test.png"),
			width:      0,
			wantErr:    true,
			errContain: "width must be positive",
		},
		{
			name:       "negative width",
			pcmData:    []byte{0, 1, 2, 3},
			outputPath: filepath.Join(env.TempDir, "test.png"),
			width:      -100,
			wantErr:    true,
			errContain: "width must be positive",
		},
		{
			name:       "empty PCM data",
			pcmData:    []byte{},
			outputPath: filepath.Join(env.TempDir, "test.png"),
			width:      400,
			wantErr:    true,
			errContain: "PCM data is empty",
		},
		{
			name:       "nil PCM data",
			pcmData:    nil,
			outputPath: filepath.Join(env.TempDir, "test.png"),
			width:      400,
			wantErr:    true,
			errContain: "PCM data is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := gen.GenerateFromPCM(t.Context(), tt.pcmData, tt.outputPath, tt.width, false, 0)

			if tt.wantErr {
				require.Error(t, err, "expected error for invalid input")
				assert.Contains(t, err.Error(), tt.errContain, "error should contain expected message")
			} else {
				assert.NoError(t, err, "expected no error for valid input")
			}
		})
	}
}

// TestProfileForModelType verifies model type to frequency profile mapping.
// Bat models resolve to the bat profile (native rate, no resample);
// every other model type falls back to bird defaults.
func TestProfileForModelType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		modelType    string
		wantResample int
		wantSuffix   string
	}{
		{"bird model", "bird", 24000, ""},
		{"bat model uses bat profile", "bat", 0, "bat-v3"},
		{"multi model defaults to bird", "multi", 24000, ""},
		{"empty defaults to bird", "", 24000, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := ProfileForModelType(tt.modelType)
			assert.Equal(t, tt.wantResample, p.ResampleRate)
			assert.Equal(t, tt.wantSuffix, ProfileSuffix(p))
		})
	}
}

// TestProfileSuffix verifies the cache-key token derived from a frequency profile:
// bat profiles get a versioned "bat-v3" token (bumped when bat renders moved to the
// clip's native rate so 0-128 kHz renders are not served from cache); bird defaults stay
// empty for backward compatibility with existing cached spectrogram filenames.
func TestProfileSuffix(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "bat-v3", ProfileSuffix(BatProfile()), "bat profile should map to the versioned bat token")
	assert.Empty(t, ProfileSuffix(BirdProfile()), "bird profile should map to an empty token")
	assert.Equal(t, "bat-v3", ProfileSuffix(ProfileForModelType("bat")))
	assert.Empty(t, ProfileSuffix(ProfileForModelType("bird")))
}

// TestSupersededSuffixesForToken verifies which older bat tokens a current
// token replaces.
func TestSupersededSuffixesForToken(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"bat-v2", "bat"}, SupersededSuffixesForToken("bat-v3"))
	assert.Empty(t, SupersededSuffixesForToken("bat-v2"))
	assert.Empty(t, SupersededSuffixesForToken(""))
	assert.Empty(t, SupersededSuffixesForToken("unknown"))
}
