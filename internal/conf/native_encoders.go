package conf

// Temporary runtime opt-in for the native Go AAC and MP3 encoders.
//
// AAC and MP3 clip export still run through FFmpeg by default. Setting
// BIRDNET_AAC_ENCODER=native switches AAC to the pure-Go encoder and muxer
// (go-aac plus go-m4a for .m4a); BIRDNET_MP3_ENCODER=native switches MP3 to the
// pure-Go go-mp3 encoder. Each is exercised in the field behind its own gate
// before becoming the default.
//
// Opus clip export and HLS live streaming have already earned that confidence.
// go-opus is the unconditional encoder for .opus (FFmpeg is used only as a
// fallback when go-opus cannot carry a clip's shape; see nativeOpusSelected in
// the analysis processor). HLS live streaming is served unconditionally by the
// in-process go-hls muxer; its BIRDNET_HLS_ENCODER gate and the entire FFmpeg
// HLS output path have both been removed.
//
// This lives in conf rather than in a package of its own so that every consumer
// reaches it without a new dependency edge: the export-format validation here
// and the encoder dispatch in the analysis processor already depend on conf. A
// dedicated package under audiocore would make conf import audiocore, which
// inverts the layering and widens the deliberately exact internal closure that
// internal/diagnostics guards.
//
// REMOVAL: this file is scaffolding with a planned end of life. Once a native
// encoder (AAC or MP3) has earned field confidence, delete its gate along with
// the branches that read it (exportFormatNeedsFFmpeg, selectEncoder and
// strandedWithoutEncoder); that format's native path becomes unconditional and
// its FFmpeg branch goes away with it.
//
// Nothing else depends on this file, and it deliberately holds no other logic
// so that each removal stays a mechanical edit.
const (
	// EnvNativeAACEncoder selects the native AAC encoder for .m4a clip export.
	EnvNativeAACEncoder = "BIRDNET_AAC_ENCODER"

	// EnvNativeMP3Encoder selects the native MP3 encoder for .mp3 clip export.
	EnvNativeMP3Encoder = "BIRDNET_MP3_ENCODER"
)

// NativeAACEncoderEnabled reports whether AAC clip export should use the native
// encoder.
func NativeAACEncoderEnabled() bool { return nativeSelected(EnvNativeAACEncoder) }

// NativeMP3EncoderEnabled reports whether MP3 clip export should use the native
// encoder.
func NativeMP3EncoderEnabled() bool { return nativeSelected(EnvNativeMP3Encoder) }
