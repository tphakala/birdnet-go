// Package flac provides native, FFmpeg-free FLAC encoding of audio clips using
// github.com/tphakala/go-flac. The native path is selected at runtime by the
// BIRDNET_FLAC_ENCODER environment variable; when it is not selected the caller
// keeps using the FFmpeg-based exporter. EncodePCM writes a FLAC file (the
// detection save path); EncodePCMToBuffer returns FLAC bytes in memory (the
// BirdWeather soundscape upload path).
package flac
