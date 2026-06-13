// Package flac provides native, FFmpeg-free FLAC encoding of detection audio
// clips using github.com/tphakala/go-flac. The native path is selected at
// runtime by the BIRDNET_FLAC_ENCODER environment variable; when it is not
// selected the caller keeps using the FFmpeg-based exporter. This package is
// scoped to the detection save path (raw PCM to FLAC) only.
package flac
