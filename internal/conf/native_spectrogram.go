package conf

// Runtime opt-in for the native Go spectrogram renderer.
//
// Clip spectrograms are drawn by the sox binary by default (with FFmpeg piped in
// for formats sox cannot read and as a fallback). Setting
// BIRDNET_SPECTROGRAM_RENDERER=native switches rendering to the pure-Go renderer
// in internal/spectrogram/render, exercised in the field behind this gate before
// it becomes the default. While the gate is on, a native failure is returned to
// the caller and never falls back to sox or FFmpeg.
//
// The gate is read live per call. An environment variable cannot change inside a
// running process, so a flip takes effect at the next service start (Docker
// --env, systemd drop-in).
//
// REMOVAL: at promotion this inverts to a SoxSpectrogramForced() reading the
// value "sox", native becomes the default, and the sox and ffmpeg spectrogram
// paths are deleted.
const EnvNativeSpectrogramRenderer = "BIRDNET_SPECTROGRAM_RENDERER"

// NativeSpectrogramEnabled reports whether spectrograms should be drawn by the
// pure-Go renderer instead of sox. Only the value "native" (case-insensitive,
// whitespace trimmed) enables it; unset or any other value keeps sox. Read live
// per call.
func NativeSpectrogramEnabled() bool { return nativeSelected(EnvNativeSpectrogramRenderer) }
