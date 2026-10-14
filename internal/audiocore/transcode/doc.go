// Package transcode decodes, filters, time-expands and re-encodes stored audio
// clips for the on-demand media endpoints of API v2, entirely in Go. It needs no
// external FFmpeg binary.
//
// The pipelines are Decode (content-sniffed, size and rate bounded), ApplyFilters
// (denoise, then loudness normalization, then user gain), TimeExpand (audible bats
// playback) and Encode (WAV, FLAC, MP3, AAC and Opus through the same encoder
// wrappers clip export uses). ExtractClip, ProcessToWAV and AudibleBats compose
// them for the three request shapes.
//
// Sources are untrusted: every decode, discard, denoise and resample loop checks
// the context per chunk, sizes are computed and checked before buffers are
// allocated, and unsupported or undecodable input ends in a typed sentinel error
// (ErrUnsupportedSource, ErrHEAACSource, ErrUndecodableSource, ErrAudioTooLong,
// ErrEmptyRange) that callers map to an HTTP status.
package transcode
