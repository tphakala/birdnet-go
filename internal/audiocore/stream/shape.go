package stream

import (
	"encoding/binary"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// Channel mode names, sourced from conf so the native path cannot drift from the
// canonical config vocabulary. Downmix is the default: an empty or unrecognized
// mode averages every channel.
const (
	channelModeDownmix = string(conf.ChannelModeDownmix)
	channelModeLeft    = string(conf.ChannelModeLeft)
	channelModeRight   = string(conf.ChannelModeRight)

	bytesPerSample = 2 // interleaved s16le
)

// shapeToMono reduces interleaved little-endian s16 PCM with srcChannels to mono
// s16le, written into dst[:0] and returned (so dst's backing array is reused
// across calls). channelModeLeft and channelModeRight select one channel;
// anything else averages all channels. A mono or channel-less input is returned
// unchanged, so no work is done on the common single-channel path.
func shapeToMono(dst, src []byte, srcChannels int, mode string) []byte {
	if srcChannels <= 1 {
		return src
	}
	frameBytes := srcChannels * bytesPerSample
	nFrames := len(src) / frameBytes
	dst = dst[:0]

	sampleAt := func(frame, ch int) int16 {
		off := frame*frameBytes + ch*bytesPerSample
		return int16(binary.LittleEndian.Uint16(src[off:]))
	}
	appendSample := func(v int16) {
		dst = binary.LittleEndian.AppendUint16(dst, uint16(v))
	}

	switch mode {
	case channelModeLeft:
		for f := range nFrames {
			appendSample(sampleAt(f, 0))
		}
	case channelModeRight:
		ch := 1
		if ch >= srcChannels {
			ch = srcChannels - 1
		}
		for f := range nFrames {
			appendSample(sampleAt(f, ch))
		}
	default: // downmix
		for f := range nFrames {
			var sum int32
			for c := range srcChannels {
				sum += int32(sampleAt(f, c))
			}
			appendSample(int16(sum / int32(srcChannels)))
		}
	}
	return dst
}
