package hlsmux

import (
	"testing"
	"time"
)

// Benchmarks for the per-frame encode path.
//
// This package had none, which is why the gate's deterministic perf signal was
// empty for the change that first made it reachable from production. It is now
// the project's only continuously-running in-process encoder, it targets ARM
// boards with 512 MB of RAM, and it sits on top of an external dependency that
// gets bumped routinely. A 10x encode regression from such a bump would
// otherwise ship silently.
//
// B/op is the signal worth watching, not allocs/op. The design deliberately
// allocates a fresh destination per segment (cutSegment: published bytes are
// handed to HTTP handlers that may still be reading them), so allocs/op reads 0
// only because one allocation per ~94 writes rounds down. A regression in the
// per-write path shows up in B/op first.

// benchFrame sizes one router delivery. Both shapes occur in production: the
// sound-card path delivers ~10 ms periods, the RTSP path a 32 KiB pipe read.
func benchFrame(b *testing.B, frameBytes int) {
	b.Helper()

	s, err := New(&Config{
		Codec:       AACLC(),
		SampleRate:  testRate,
		Channels:    testChannels,
		BitrateKbps: testBitrate,
	})
	if err != nil {
		b.Fatalf("new stream: %v", err)
	}
	b.Cleanup(func() {
		if err := s.Close(); err != nil {
			b.Errorf("close: %v", err)
		}
	})

	pcm := tone(frameBytes/(testChannels*bytesPerSample), testChannels, testRate, 3000)
	at := testEpoch
	step := time.Duration(len(pcm)/(testChannels*bytesPerSample)) * time.Second / testRate

	b.SetBytes(int64(len(pcm)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := s.Write(pcm, at); err != nil {
			b.Fatalf("write: %v", err)
		}
		at = at.Add(step)
	}
}

// BenchmarkStreamWrite10ms models the sound-card capture path.
func BenchmarkStreamWrite10ms(b *testing.B) {
	benchFrame(b, testRate/100*testChannels*bytesPerSample)
}

// BenchmarkStreamWrite32k models an RTSP source, whose frames arrive as
// whole pipe reads.
func BenchmarkStreamWrite32k(b *testing.B) { benchFrame(b, 32768) }

// BenchmarkPlaylistRender covers the read side, which every viewer polls once
// per target duration and which contends with the encode path for the stream
// mutex.
func BenchmarkPlaylistRender(b *testing.B) {
	s, err := New(&Config{
		Codec:       AACLC(),
		SampleRate:  testRate,
		Channels:    testChannels,
		BitrateKbps: testBitrate,
	})
	if err != nil {
		b.Fatalf("new stream: %v", err)
	}
	b.Cleanup(func() {
		if err := s.Close(); err != nil {
			b.Errorf("close: %v", err)
		}
	})

	// Fill the window so the render walks a realistic segment list.
	pcm := tone(testRate, testChannels, testRate, 3000)
	at := testEpoch
	for range 14 {
		if err := s.Write(pcm, at); err != nil {
			b.Fatalf("write: %v", err)
		}
		at = at.Add(time.Second)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = s.Playlist()
	}
}
