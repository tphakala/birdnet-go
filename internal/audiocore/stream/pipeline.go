package stream

import (
	"fmt"
	"sync/atomic"
	"time"

	audiostream "github.com/tphakala/go-audio-stream"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/resample"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// bytePool is the pooled-slice source the pipeline draws dispatched chunks from.
// buffer.BytePool satisfies it; a nil pool means unpooled (plain allocation and
// a nil FrameRef), matching the FFmpeg producer without a wired buffer manager.
type bytePool interface {
	Get() []byte
	Put(buf []byte)
}

// dispatchFunc receives each assembled AudioFrame; it is the manager's frame
// callback, which forwards into the audio router.
type dispatchFunc func(audiocore.AudioFrame)

// pipeline is the per-source decode, shape, resample, chunk, and dispatch hot
// path. It runs entirely on the supervisor's reader goroutine inside OnFrame,
// so it is not safe for concurrent use and owns reusable scratch buffers so the
// decode and shape stages are allocation-free. The dispatched chunk's byte slice
// is drawn from a pool and reused, but each dispatch still allocates its FrameRef
// and release closure; driving that to zero would need a FrameRef free-list and
// is left as a follow-up.
type pipeline struct {
	sourceID    string
	sourceName  string
	targetRate  int
	channelMode string
	chunkBytes  int
	pool        bytePool // nil => unpooled
	onFrame     dispatchFunc
	log         logger.Logger

	dec       frameDecoder
	resampler *resample.Resampler
	monoBuf   []byte // reused mono s16le scratch for the downmix stage

	chunk    []byte // the pooled slice currently being filled, or nil
	chunkLen int

	// Observability, published for the health snapshot. The reader goroutine is
	// the sole writer; the snapshot reads them from another goroutine, so they are
	// atomics rather than mutex-guarded (a lock on the per-frame path is avoided).
	codecLabel  atomic.Pointer[string]
	srcRate     atomic.Int32
	srcChannels atomic.Int32
}

// newPipeline builds a pipeline for one source. pool may be nil for the unpooled
// path. log may be nil, in which case the package logger is used.
func newPipeline(spec *audiocore.StreamSpec, chunkBytes int, pool bytePool, onFrame dispatchFunc, log logger.Logger) *pipeline {
	if log == nil {
		log = audiocore.GetLogger()
	}
	return &pipeline{
		sourceID:    spec.SourceID,
		sourceName:  spec.SourceName,
		targetRate:  spec.SampleRate,
		channelMode: spec.ChannelMode,
		chunkBytes:  chunkBytes,
		pool:        pool,
		onFrame:     onFrame,
		log:         log,
	}
}

// setCodec builds the frame decoder for a track's codec and discards any prior
// resampler so the next frame rebuilds it for the new geometry. It is called at
// session start and on an OnCodecUpdate. An unsupported codec returns a terminal
// error the caller maps to stream health.
func (p *pipeline) setCodec(codec audiostream.Codec, format audiostream.AudioFormat) error {
	dec, err := newFrameDecoder(codec, format)
	if err != nil {
		// Clear the decoder so a failed (re)resolution does not keep feeding the
		// new bitstream into the previous decoder: process drops frames while
		// p.dec is nil until a valid codec is resolved (e.g. via OnCodecUpdate).
		// Clear the published geometry too, so the health snapshot does not report
		// a codec and rate that are no longer active.
		p.dec = nil
		p.codecLabel.Store(nil)
		p.srcRate.Store(0)
		p.srcChannels.Store(0)
		if p.resampler != nil {
			_ = p.resampler.Close()
			p.resampler = nil
		}
		return err
	}
	p.dec = dec
	label := codecName(codec)
	p.codecLabel.Store(&label)
	// Reset the geometry until the first decoded frame of the new codec repopulates
	// it, so a snapshot between the codec change and the first frame does not report
	// the previous codec's rate.
	p.srcRate.Store(0)
	p.srcChannels.Store(0)
	if p.resampler != nil {
		_ = p.resampler.Close()
		p.resampler = nil
	}
	return nil
}

// process decodes one depacketized frame and dispatches the resulting audio as
// pooled chunks. A frame that arrives before the codec is resolved is dropped.
func (p *pipeline) process(data []byte) error {
	if p.dec == nil {
		return nil
	}
	pcm, rate, channels, err := p.dec.decodeFrame(data)
	if err != nil {
		return err
	}
	// Publish the codec's native geometry (before resampling) for observability.
	p.srcRate.Store(int32(rate))
	p.srcChannels.Store(int32(channels))
	if len(pcm) == 0 {
		return nil
	}

	var mono []byte
	if channels <= 1 {
		mono = pcm
	} else {
		p.monoBuf = shapeToMono(p.monoBuf, pcm, channels, p.channelMode)
		mono = p.monoBuf
	}

	out, err := p.resampleTo(mono, rate)
	if err != nil {
		return err
	}
	p.chunkAndDispatch(out)
	return nil
}

// resampleTo resamples mono s16le from rate to the target rate, rebuilding the
// resampler if the input rate changed. When the rates already match it returns
// the input unchanged so the common case does no work.
func (p *pipeline) resampleTo(mono []byte, rate int) ([]byte, error) {
	if rate == p.targetRate {
		return mono, nil
	}
	if p.resampler == nil || p.resampler.FromRate() != rate {
		if p.resampler != nil {
			_ = p.resampler.Close()
		}
		r, err := resample.NewResampler(rate, p.targetRate)
		if err != nil {
			return nil, fmt.Errorf("build resampler %d->%d: %w", rate, p.targetRate, err)
		}
		p.resampler = r
	}
	out, err := p.resampler.ResampleInto(mono)
	if err != nil {
		return nil, fmt.Errorf("resample %d->%d: %w", rate, p.targetRate, err)
	}
	return out, nil
}

// chunkAndDispatch copies mono s16le PCM into pooled chunkBytes slices, emitting
// each slice as it fills. Because chunkBytes and every PCM length are even, a
// sample is never split across a chunk boundary.
func (p *pipeline) chunkAndDispatch(pcm []byte) {
	for len(pcm) > 0 {
		if p.chunk == nil {
			p.chunk = p.newChunk()
			p.chunkLen = 0
		}
		n := copy(p.chunk[p.chunkLen:p.chunkBytes], pcm)
		p.chunkLen += n
		pcm = pcm[n:]
		if p.chunkLen == p.chunkBytes {
			p.dispatch(p.chunk[:p.chunkLen], p.chunk)
			p.chunk = nil
			p.chunkLen = 0
		}
	}
}

// flush dispatches the partial chunk (if any) so nothing stays buffered when a
// session ends or the stream stops. It is a no-op when no chunk is in flight.
func (p *pipeline) flush() {
	switch {
	case p.chunkLen > 0:
		p.dispatch(p.chunk[:p.chunkLen], p.chunk)
	case p.chunk != nil && p.pool != nil:
		p.pool.Put(p.chunk)
	}
	p.chunk = nil
	p.chunkLen = 0
}

// close flushes any buffered audio and releases the resampler. It is called at
// stream stop and manager shutdown.
func (p *pipeline) close() {
	p.flush()
	if p.resampler != nil {
		_ = p.resampler.Close()
		p.resampler = nil
	}
}

// newChunk returns a fresh chunk slice: a pooled one when a pool is wired, or a
// plain allocation otherwise (the unpooled path never reuses a slice because the
// consumer's lifetime is GC-governed with a nil FrameRef).
func (p *pipeline) newChunk() []byte {
	if p.pool != nil {
		return p.pool.Get()
	}
	return make([]byte, p.chunkBytes)
}

// dispatch builds the AudioFrame for one chunk and hands it to onFrame. When the
// chunk is pooled it carries a FrameRef whose release returns owner to the pool;
// the producer releases its own reference after onFrame returns, so the slice is
// reclaimed once every retaining consumer has released too.
func (p *pipeline) dispatch(data, owner []byte) {
	frame := audiocore.AudioFrame{
		SourceID:   p.sourceID,
		SourceName: p.sourceName,
		Data:       data,
		SampleRate: p.targetRate,
		BitDepth:   16,
		Channels:   1,
		Timestamp:  time.Now(),
	}
	if p.pool == nil {
		p.onFrame(frame)
		return
	}
	frame.Ref = audiocore.NewFrameRef(func() { p.pool.Put(owner) })
	p.onFrame(frame)
	frame.Ref.Release()
}

// codecInfo returns the current source codec label and the codec's native sample
// rate and channel count (before resampling and downmix), for the health
// snapshot. Values are zero until the first frame resolves the codec. Safe for
// concurrent use.
func (p *pipeline) codecInfo() (codec string, sampleRate, channels int) {
	if lbl := p.codecLabel.Load(); lbl != nil {
		codec = *lbl
	}
	return codec, int(p.srcRate.Load()), int(p.srcChannels.Load())
}
