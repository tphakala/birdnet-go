// Package flac provides native, FFmpeg-free FLAC encoding of audio clips using
// github.com/tphakala/go-flac. The native path is selected at runtime by the
// BIRDNET_FLAC_ENCODER environment variable; when it is not selected the caller
// keeps using the FFmpeg-based exporter. EncodePCM writes a seekable FLAC file;
// EncodePCMToBuffer returns FLAC bytes in memory for callers that do not need
// finalized seek metadata.
package flac
