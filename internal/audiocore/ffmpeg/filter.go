package ffmpeg

// FFmpeg-based audio filter types, presets, and processing functions are
// defined in process.go:
//   - AudioFilters — filter parameter struct (denoise preset, normalize flag, gain)
//   - LoudnessStats — measured loudness statistics from the loudnorm filter
//   - BuildProcessingFilterChain — constructs the FFmpeg -af filter argument
//   - AnalyzeFileLoudness — loudness analysis pass (pass 1 of two-pass loudnorm)
//   - ProcessAudioFile — applies filters to a file and returns WAV output
//   - ProcessAudioToFile — applies filters to a file and writes WAV to disk
//   - IsValidDenoisePreset — validates a denoise preset name
//   - IsValidGainDB — validates a gain value in dB
//
// This file is intentionally empty of declarations because all FFmpeg-based
// filter logic was consolidated in process.go to avoid duplication.
