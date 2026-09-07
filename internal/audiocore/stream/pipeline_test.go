package stream

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	audiostream "github.com/tphakala/go-audio-stream"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
)

// fakeBytePool is a deterministic bytePool for asserting FrameRef accounting.
type fakeBytePool struct {
	size       int
	gets, puts int
}

func (p *fakeBytePool) Get() []byte    { p.gets++; return make([]byte, p.size) }
func (p *fakeBytePool) Put(buf []byte) { p.puts++ }

// frameCollector records dispatched frames, optionally retaining their FrameRef
// to model a router route that holds the frame past the OnFrame return.
type frameCollector struct {
	retain bool
	frames []audiocore.AudioFrame
	data   [][]byte
}

func (c *frameCollector) onFrame(f audiocore.AudioFrame) { //nolint:gocritic // hugeParam: must match dispatchFunc's by-value signature
	if c.retain {
		f.Ref.Retain()
	}
	d := make([]byte, len(f.Data))
	copy(d, f.Data)
	c.data = append(c.data, d)
	c.frames = append(c.frames, f)
}

func pcmL16(rate, ch int) (audiostream.Codec, audiostream.AudioFormat) {
	return audiostream.CodecL16{ClockRate: rate, Channels: ch},
		audiostream.AudioFormat{Kind: audiostream.KindPCMS16LE, SampleRate: rate, Channels: ch}
}

func TestPipeline_setCodecClearsMetadataOnError(t *testing.T) {
	t.Parallel()
	spec := &audiocore.StreamSpec{SourceID: "s1", SampleRate: 48000, Channels: 1, ChannelMode: channelModeDownmix, BitDepth: 16}
	p := newPipeline(spec, 8, nil, func(audiocore.AudioFrame) {}, nil)

	// A supported codec publishes its label.
	codec, format := pcmL16(48000, 1)
	require.NoError(t, p.setCodec(codec, format))
	got, _, _ := p.codecInfo()
	require.Equal(t, "l16", got)

	// A later unsupported codec fails; the stale label and geometry must clear so
	// the health snapshot does not keep reporting a codec that is no longer active.
	require.Error(t, p.setCodec(audiostream.CodecFLAC{}, audiostream.AudioFormat{}))
	got, rate, ch := p.codecInfo()
	assert.Empty(t, got, "codec label cleared after a failed setCodec")
	assert.Zero(t, rate, "source rate cleared after a failed setCodec")
	assert.Zero(t, ch, "source channels cleared after a failed setCodec")
}

func TestPipeline_chunksAndDispatchesMonoPassthrough(t *testing.T) {
	pool := &fakeBytePool{size: 8}
	col := &frameCollector{}
	spec := &audiocore.StreamSpec{SourceID: "s1", SourceName: "Feeder", SampleRate: 48000, Channels: 1, ChannelMode: channelModeDownmix, BitDepth: 16}
	p := newPipeline(spec, 8, pool, col.onFrame, nil)
	codec, format := pcmL16(48000, 1)
	require.NoError(t, p.setCodec(codec, format))

	in := make([]byte, 20)
	for i := range in {
		in[i] = byte(i)
	}
	require.NoError(t, p.process(in))

	require.Len(t, col.frames, 2, "two full 8-byte chunks emit while filling")
	for _, f := range col.frames {
		assert.Len(t, f.Data, 8, "full chunk is exactly chunkBytes")
		assert.Equal(t, "s1", f.SourceID)
		assert.Equal(t, "Feeder", f.SourceName)
		assert.Equal(t, 48000, f.SampleRate)
		assert.Equal(t, 16, f.BitDepth)
		assert.Equal(t, 1, f.Channels)
		assert.NotNil(t, f.Ref, "pooled chunk carries a FrameRef")
		assert.False(t, f.Timestamp.IsZero())
	}

	p.flush()
	require.Len(t, col.frames, 3, "flush emits the buffered remainder")
	assert.Len(t, col.data[2], 4, "remainder is the trailing 4 bytes")

	var got []byte
	for _, d := range col.data {
		got = append(got, d...)
	}
	assert.Equal(t, in, got, "content is preserved across chunk boundaries")
}

func TestPipeline_frameRefReturnsChunkOnRouterDrop(t *testing.T) {
	pool := &fakeBytePool{size: 8}
	col := &frameCollector{retain: false} // router enqueued nothing (all dropped)
	spec := &audiocore.StreamSpec{SourceID: "s1", SampleRate: 48000, Channels: 1, BitDepth: 16}
	p := newPipeline(spec, 8, pool, col.onFrame, nil)
	codec, format := pcmL16(48000, 1)
	require.NoError(t, p.setCodec(codec, format))

	require.NoError(t, p.process(make([]byte, 8)))
	require.Len(t, col.frames, 1)
	assert.Equal(t, 1, pool.puts, "a dropped frame returns its pooled chunk exactly once")
}

func TestPipeline_frameRefHeldUntilConsumerReleases(t *testing.T) {
	pool := &fakeBytePool{size: 8}
	col := &frameCollector{retain: true} // one route holds the frame
	spec := &audiocore.StreamSpec{SourceID: "s1", SampleRate: 48000, Channels: 1, BitDepth: 16}
	p := newPipeline(spec, 8, pool, col.onFrame, nil)
	codec, format := pcmL16(48000, 1)
	require.NoError(t, p.setCodec(codec, format))

	require.NoError(t, p.process(make([]byte, 8)))
	require.Len(t, col.frames, 1)
	assert.Equal(t, 0, pool.puts, "chunk is not returned while a consumer holds the ref")

	col.frames[0].Ref.Release()
	assert.Equal(t, 1, pool.puts, "chunk returns once the consumer releases")
}

func TestPipeline_unpooledLeavesRefNil(t *testing.T) {
	col := &frameCollector{}
	spec := &audiocore.StreamSpec{SourceID: "s1", SampleRate: 48000, Channels: 1, BitDepth: 16}
	p := newPipeline(spec, 8, nil, col.onFrame, nil)
	codec, format := pcmL16(48000, 1)
	require.NoError(t, p.setCodec(codec, format))

	require.NoError(t, p.process(make([]byte, 8)))
	require.Len(t, col.frames, 1)
	assert.Nil(t, col.frames[0].Ref, "unpooled dispatch leaves Ref nil")
}

func TestPipeline_resamplesToTargetRate(t *testing.T) {
	pool := &fakeBytePool{size: 4096}
	col := &frameCollector{}
	spec := &audiocore.StreamSpec{SourceID: "s1", SampleRate: 48000, Channels: 1, BitDepth: 16}
	p := newPipeline(spec, 4096, pool, col.onFrame, nil)
	codec, format := pcmL16(24000, 1)
	require.NoError(t, p.setCodec(codec, format))

	in := make([]byte, 4800) // 2400 samples at 24 kHz mono
	require.NoError(t, p.process(in))
	p.flush()

	require.NotNil(t, p.resampler, "a rate mismatch builds a resampler")
	assert.Equal(t, 24000, p.resampler.FromRate())
	assert.Equal(t, 48000, p.resampler.ToRate())

	var total int
	for _, f := range col.frames {
		total += len(f.Data)
		assert.Equal(t, 48000, f.SampleRate, "dispatched frames carry the target rate")
	}
	assert.Greater(t, total, len(in), "upsampling 24k to 48k yields more bytes")
}

// BenchmarkPipelineProcess measures the steady-state per-frame allocations of
// the decode->shape->chunk->dispatch hot path on the zero-copy PCM passthrough
// path, for both the pooled and unpooled dispatch. It locks the allocation
// budget so a regression (a mis-sized scratch buffer, an added per-frame
// allocation) is visible in b.ReportAllocs output.
func BenchmarkPipelineProcess(b *testing.B) {
	run := func(b *testing.B, pool bytePool) {
		b.Helper()
		spec := &audiocore.StreamSpec{SourceID: "s1", SampleRate: 48000, Channels: 1, BitDepth: 16}
		// onFrame models a router with no live route: it drops the frame, so the
		// producer's own FrameRef release returns the pooled chunk each iteration.
		p := newPipeline(spec, 4096, pool, func(audiocore.AudioFrame) {}, nil)
		codec, format := pcmL16(48000, 1)
		require.NoError(b, p.setCodec(codec, format))

		frame := make([]byte, 4096) // one full chunk of mono s16le
		b.ReportAllocs()
		for b.Loop() {
			if err := p.process(frame); err != nil {
				b.Fatal(err)
			}
		}
	}

	b.Run("pooled", func(b *testing.B) {
		bufMgr := buffer.NewManager(audiocore.GetLogger())
		run(b, bufMgr.BytePoolFor(4096))
	})
	b.Run("unpooled", func(b *testing.B) { run(b, nil) })
}
