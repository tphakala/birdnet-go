package transcode

import (
	"context"
)

// ProbeInfo is the stream configuration a clip's headers declare.
type ProbeInfo struct {
	SampleRate int // Hz, as DecodeMono reads it (48000 for Opus)
	Channels   int
}

// ProbeFile reports whether DecodeMono can open path. It runs the same file
// checks (regular file, size bound) and opens the same decoder with the same
// sample-rate and channel bounds, returning the same errors except those only
// decoding can find (the decoded-size bound), but decodes no audio: it reads
// what each decoder needs to establish its configuration (the headers; the
// first MP3 frame; the Opus page index). A nil error does not mean the clip
// holds audio or is complete: a header-only or truncated data section is found
// only by decoding. The open itself does not observe ctx.
func ProbeFile(ctx context.Context, path string) (ProbeInfo, error) {
	if err := ctx.Err(); err != nil {
		return ProbeInfo{}, err
	}
	f, _, err := openSourceFile(path)
	if err != nil {
		return ProbeInfo{}, err
	}
	defer func() { _ = f.Close() }()

	src, err := openSourceChannels(f, path, maxAnalysisChannels)
	if err != nil {
		return ProbeInfo{}, err
	}
	return ProbeInfo{SampleRate: src.sampleRate, Channels: src.channels}, nil
}
