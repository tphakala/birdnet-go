package support

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRuntimeInfoProvider returns a fixed RuntimeInfo.
type fakeRuntimeInfoProvider struct{ info *RuntimeInfo }

func (f fakeRuntimeInfoProvider) CollectRuntimeInfo(_ context.Context) *RuntimeInfo { return f.info }

// runtimeInfoFromArchive collects with the given provider (nil means none) and
// returns the shipped runtime_info.json bytes.
func runtimeInfoFromArchive(t *testing.T, provider RuntimeInfoProvider) []byte {
	t.Helper()
	c := NewCollector(t.TempDir(), t.TempDir(), "sys-id", "20260716")
	if provider != nil {
		c.SetRuntimeInfoProvider(provider)
	}
	opts := CollectorOptions{IncludeSystemInfo: true, AnonymizePII: false}
	dump, err := c.Collect(t.Context(), opts)
	require.NoError(t, err)
	archive, err := c.CreateArchive(t.Context(), dump, opts)
	require.NoError(t, err)

	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	require.NoError(t, err)
	for _, f := range zr.File {
		if f.Name == runtimeInfoFileName {
			return readZipFile(t, f)
		}
	}
	require.Failf(t, "missing file", "%s not in archive", runtimeInfoFileName)
	return nil
}

// readZipFile returns the content of one archive entry.
func readZipFile(t *testing.T, f *zip.File) []byte {
	t.Helper()
	rc, err := f.Open()
	require.NoError(t, err)
	defer func() { assert.NoError(t, rc.Close()) }()
	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	return data
}

// sortedKeys returns the keys of m in order.
func sortedKeys(m map[string]any) []string {
	return slices.Sorted(maps.Keys(m))
}

// TestRuntimeInfoArchive_ShapeAndNoRawPaths pins the shipped JSON shape and that no
// raw path reaches the archive even with AnonymizePII off, which is how the CLI runs.
func TestRuntimeInfoArchive_ShapeAndNoRawPaths(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home", "someuser")
	info := &RuntimeInfo{
		Available: true,
		ONNXRuntime: &ONNXRuntimeInfo{
			Initialized: true, Version: "1.25.1", RequiredVersion: "1.25.x",
			LibraryPath: filepath.Join(home, "lib", "libonnxruntime.so.1.25.1"), LibraryConfigured: true,
		},
		OpenVINO: &OpenVINOInfo{
			CompiledIn: true, Initialized: true, LibraryPath: "libopenvino_c.so",
			Probe: OpenVINOProbeInfo{Status: "cached", Devices: []string{"CPU"}},
		},
		OrchestratorAvailable: true,
		BirdNETThreadsSetting: 2,
		Models: []ModelRuntimeInfo{{
			RegistryID: "Perch_V2", Backend: "onnx", Device: "CPU", LabelCount: 10, ConfiguredThreads: 2,
			LoadedAt:  time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC),
			ModelPath: filepath.Join(home, "models", "perch_v2.onnx"),
		}},
		RangeFilter:     &RangeFilterRuntimeInfo{Available: true, Backend: "none"},
		Sources:         []SourceModelInfo{{Kind: SourceKindAudio, Index: 0, Enabled: true, UsesDefault: true}},
		ModelsDirectory: &ModelsDirectoryInfo{Source: ModelsDirSourceDefault, Exists: true, Persistence: PersistenceNotApplicable},
	}

	data := runtimeInfoFromArchive(t, fakeRuntimeInfoProvider{info: info})

	assert.NotContains(t, string(data), "someuser")
	assert.NotContains(t, string(data), home)

	var got map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, []string{
		"available", "birdnet_threads_setting", "models", "models_directory", "onnx_runtime",
		"openvino", "orchestrator_available", "range_filter", "sources",
	}, sortedKeys(got))

	ort, ok := got["onnx_runtime"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []string{"initialized", "library_dir", "library_file", "library_source", "required_version", "version"}, sortedKeys(ort))
	assert.Equal(t, "custom", ort["library_dir"])
	assert.Equal(t, "configured", ort["library_source"])
	assert.Equal(t, "1.25.x", ort["required_version"])
	assert.Equal(t, "libonnxruntime.so.1.25.1", ort["library_file"])

	ov, ok := got["openvino"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []string{"active", "compiled_in", "initialized", "library_file", "library_source", "probe"}, sortedKeys(ov))
	assert.Equal(t, true, ov["compiled_in"])
	assert.Equal(t, "libopenvino_c.so", ov["library_file"])
	assert.Equal(t, LibrarySourceLoaderSearch, ov["library_source"])

	models, ok := got["models"].([]any)
	require.True(t, ok)
	require.Len(t, models, 1)
	model, ok := models[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []string{
		"backend", "configured_threads", "device", "generation", "label_count", "loaded_at", "model_file", "registry_id",
	}, sortedKeys(model))
	assert.Equal(t, "perch_v2.onnx", model["model_file"])
}

// TestRuntimeInfoArchive_NilProvider covers the CLI path: no running instance, so
// the file says it could not be collected instead of being missing.
func TestRuntimeInfoArchive_NilProvider(t *testing.T) {
	data := runtimeInfoFromArchive(t, nil)

	var got map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, false, got["available"])
	assert.Equal(t, runtimeInfoUnavailableReason, got["reason"])
	assert.NotContains(t, got, "models")
}

func TestLibraryLocation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		raw               string
		configured        bool
		wantFile, wantDir string
		wantSource        string
	}{
		{name: "system dir, auto", raw: "/usr/lib/libabsent-test-runtime.so", wantFile: "libabsent-test-runtime.so", wantDir: "/usr/lib", wantSource: LibrarySourceAuto},
		{name: "bare name", raw: "onnxruntime", wantFile: "onnxruntime", wantDir: "", wantSource: LibrarySourceLoaderSearch},
		{name: "configured custom dir", raw: "/opt/x/lib/libonnxruntime.so", configured: true, wantFile: "libonnxruntime.so", wantDir: LibraryDirCustom, wantSource: LibrarySourceConfigured},
		{name: "empty", raw: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			file, dir, source := libraryLocation(tt.raw, tt.configured)
			assert.Equal(t, tt.wantFile, file)
			assert.Equal(t, tt.wantDir, dir)
			assert.Equal(t, tt.wantSource, source)
		})
	}
}
