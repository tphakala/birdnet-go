// Package flac provides native, FFmpeg-free FLAC encoding of audio clips using
// github.com/tphakala/go-flac. It is the sole FLAC encoder for the detection-save
// and BirdWeather soundscape-upload paths. EncodePCM writes a seekable FLAC file;
// EncodePCMToBuffer returns FLAC bytes in memory for callers that do not need
// finalized seek metadata.
package flac
