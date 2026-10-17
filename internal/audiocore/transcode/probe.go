package transcode

import (
	"context"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// ProbeInfo is the stream configuration a clip's headers declare.
type ProbeInfo struct {
	SampleRate int // Hz, as DecodeMono reads it (48000 for Opus)
	Channels   int
}

// ProbeFile reports whether DecodeMono can open path and read audio from it. It
// runs the same file checks (regular file, size bound) and opens the same
// decoder with the same sample-rate and channel bounds, returning the same
// errors except those only a full decode can find (the decoded-size bound). It
// decodes one frame, so a clip whose headers parse but holds no audio wraps
// ErrUndecodableSource. A nil error does not mean the clip is complete: a
// truncated data section is found only by decoding it all. The open itself does
// not observe ctx.
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
	frameBytes := src.channels * bytesPerSample
	data, err := readLimited(ctx, src.reader, frameBytes, frameBytes, frameBytes)
	if err != nil {
		return ProbeInfo{}, err
	}
	if len(data) == 0 {
		return ProbeInfo{}, wrapSentinel(ErrUndecodableSource, nil, "probe_read", "source holds no audio", errors.CategoryAudio)
	}
	return ProbeInfo{SampleRate: src.sampleRate, Channels: src.channels}, nil
}
