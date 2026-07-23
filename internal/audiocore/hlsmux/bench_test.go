package hlsmux

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	// benchFramesPerSecond is how many capture buffers a second of audio is
	// delivered in when a benchmark needs a source running at real time. A
	// hundred models the ~10 ms period a sound card hands over.
	benchFramesPerSecond = 100

	// benchToneHz is the test signal. Real signal rather than silence, because
	// an encoder handed pure zeros produces degenerate frames and a
	// correspondingly unrepresentative segment size.
	benchToneHz = 3000

	// benchPollInterval paces each viewer in BenchmarkStreamWriteUnderPoll. It
	// is chosen so that the largest audience in benchViewerCounts generates
	// enough polls to keep a serialising lock busy most of the time, while
	// leaving the machine far from CPU saturation: 64 viewers at 200 us is
	// 320k polls a second, which is most of a core's worth of work when a poll
	// costs microseconds under a shared mutex and negligible when it does not.
	//
	// The rate a ticker at this period actually achieves is well under the
	// nominal one, because 200 us is at the scheduler's practical floor and
	// dropped ticks are silent. BenchmarkStreamWriteUnderPoll therefore reports
	// the achieved polls/s as a metric, so a starved run is visible rather than
	// indistinguishable from a healthy one.
	benchPollInterval = 200 * time.Microsecond

	// benchClaimBatch is how many iterations a viewer claims from the shared
	// counter at once in benchPoll. See the comment there: at one claim per
	// iteration the counter costs more than the operation being measured.
	benchClaimBatch = 512
)

// benchViewerCounts is the audience axis. It is the axis that matters on the
// read side and the one no other benchmark here varies: the encoder is per
// source, so one muxer serves every viewer of a stream and encode cost does not
// grow with audience at all. Playlist polling is the only path that does.
var benchViewerCounts = []int{1, 2, 4, 8, 16, 64}

// newBenchStream builds a stream over the shipping AAC codec, so write-side
// timings stay comparable with the deployed measurements rather than with a
// fake whose cost is arbitrary.
func newBenchStream(b *testing.B) *Stream {
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
	return s
}

// fillWindow feeds enough audio to fill the playlist window and returns the
// timestamp the next write should carry. A read benchmark against an empty
// window would measure a header and nothing else.
func fillWindow(b *testing.B, s *Stream) time.Time {
	b.Helper()

	pcm := tone(testRate, testChannels, testRate, benchToneHz)
	at := testEpoch
	// One second per write and DefaultSegmentDuration per segment, plus a
	// couple of extra seconds so the window is certainly full rather than one
	// cut short of it.
	seconds := DefaultWindowSize*int(DefaultSegmentDuration/time.Second) + 2
	for range seconds {
		if err := s.Write(pcm, at); err != nil {
			b.Fatalf("write: %v", err)
		}
		at = at.Add(time.Second)
	}
	return at
}

// Benchmarks for the per-frame encode path.
//
// This package had none, which is why the gate's deterministic perf signal was
// empty for the change that first made it reachable from production. It is now
// the project's only continuously-running in-process encoder, it targets ARM
// boards with 512 MB of RAM, and it sits on top of an external dependency that
// gets bumped routinely. A 10x encode regression from such a bump would
// otherwise ship silently.
//
// Read the per-write numbers knowing that a segment cut is amortised into them,
// and that the cut is not cheap in allocation terms even though it is cheap in
// time. Each cut allocates the published segment (one, deliberately: those
// bytes go to HTTP handlers that may still be reading them, so the arena can
// never be reused), the copy-on-write window slice, the snapshot, and the
// playlist render, for roughly 16 allocations and 1 KB beyond the segment
// itself, once per segment per stream.
//
// So allocs/op is NOT uniformly zero here and a non-zero value is not by itself
// a regression: BenchmarkStreamWrite32k reports 2 because a 32 KiB write is
// 8192 samples against a ~95000-sample segment, so a cut lands every dozen
// writes. What to watch is a change in the amortised value at a fixed shape,
// and B/op on the shapes that cut rarely.

// benchFrame sizes one router delivery. Both shapes occur in production: the
// sound-card path delivers ~10 ms periods, the RTSP path a 32 KiB pipe read.
func benchFrame(b *testing.B, frameBytes int) {
	b.Helper()

	s := newBenchStream(b)
	pcm := tone(frameBytes/(testChannels*bytesPerSample), testChannels, testRate, benchToneHz)
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

// BenchmarkStreamWriteSegmentCut isolates the segment-cadence allocation spike
// that BenchmarkStreamWrite* cannot see, by carrying one segment's worth of
// audio per write so that every iteration cuts almost exactly one segment.
//
// The per-segment allocation is by design and must not be driven to zero:
// cutSegment hands its destination to HTTP handlers that may still be reading
// it, so the arena can never be reused. What this pins is its SIZE, which the
// segBufHint pre-sizing is what keeps at one allocation instead of the eight a
// nil destination would grow through.
func BenchmarkStreamWriteSegmentCut(b *testing.B) {
	s := newBenchStream(b)

	samples := int(DefaultSegmentDuration * testRate / time.Second)
	pcm := tone(samples, testChannels, testRate, benchToneHz)
	at := testEpoch

	b.SetBytes(int64(len(pcm)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := s.Write(pcm, at); err != nil {
			b.Fatalf("write: %v", err)
		}
		at = at.Add(DefaultSegmentDuration)
	}
}

// BenchmarkPlaylistRender is the uncontended cost of one playlist poll.
//
// The name is now a misnomer and is kept deliberately: nothing renders here any
// more, because the playlist is rendered once per segment cut and a poll is an
// atomic load. Keeping the name is what lets benchstat pair this against the
// pre-change measurement, where it really did render. Quote THIS benchmark for
// what a poll costs; BenchmarkPlaylistPoll answers a different question (how
// that cost behaves as the audience grows) and carries its harness in the
// number.
func BenchmarkPlaylistRender(b *testing.B) {
	s := newBenchStream(b)
	fillWindow(b, s)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = s.Playlist()
	}
}

// benchPoll measures one playlist poll with viewers polling concurrently, which
// is the only shape that shows what an audience costs. A single-goroutine
// render benchmark cannot: the question is not what a render costs but what N
// readers cost each other, and what they cost against a source that is encoding
// at the same time.
func benchPoll(b *testing.B, viewers int, writing bool) {
	b.Helper()

	s := newBenchStream(b)
	at := fillWindow(b, s)

	if writing {
		startBenchWriter(b, s, at)
	}

	// A shared counter rather than an even split of b.N, so every viewer stays
	// alive for the whole run and the contention level really is `viewers`
	// throughout instead of decaying as the faster goroutines retire.
	//
	// Claimed in batches, which is not a micro-optimisation but the difference
	// between measuring the muxer and measuring this loop. A poll is now a
	// single atomic load of a few nanoseconds, while claiming one iteration at
	// a time is a LOCK XADD on a line contended by every viewer, which costs
	// several times more. Per-iteration claiming made this benchmark report
	// 6-27 ns/op for an operation that measures 0.6 ns uncontended, i.e. over
	// 90 percent harness, and left it unable to tell a tenfold regression from
	// a tenfold win.
	var remaining atomic.Int64
	remaining.Store(int64(b.N))

	b.ReportAllocs()
	b.ResetTimer()

	var pollers sync.WaitGroup
	for range viewers {
		pollers.Go(func() {
			for {
				claimed := remaining.Add(-benchClaimBatch)
				if claimed <= -benchClaimBatch {
					return
				}
				n := benchClaimBatch
				if claimed < 0 {
					n = benchClaimBatch + int(claimed)
				}
				for range n {
					_ = s.Playlist()
				}
			}
		})
	}
	pollers.Wait()
}

// startBenchWriter runs a source encoding into s at real time until the
// benchmark ends.
//
// Real time, not flat out: a writer with no pacing would hold the mutex
// continuously and compete for CPU, measuring a situation that never occurs. A
// live source occupies a low single-digit duty cycle, and it is the
// interference at that duty cycle which the deferral of this optimisation was
// argued on.
func startBenchWriter(b *testing.B, s *Stream, at time.Time) {
	b.Helper()

	pcm := tone(testRate/benchFramesPerSecond, testChannels, testRate, benchToneHz)
	step := time.Second / benchFramesPerSecond
	stop := make(chan struct{})

	var writer sync.WaitGroup
	writer.Go(func() {
		tick := time.NewTicker(step)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				if err := s.Write(pcm, at); err != nil {
					b.Errorf("write: %v", err)
					return
				}
				at = at.Add(step)
			}
		}
	})

	// Registered after newBenchStream's cleanup, so LIFO order stops the writer
	// before the stream it writes to is closed.
	b.Cleanup(func() {
		close(stop)
		writer.Wait()
	})
}

// BenchmarkPlaylistPoll measures the read path on its own, which is what every
// viewer pays once per segment.
func BenchmarkPlaylistPoll(b *testing.B) {
	for _, viewers := range benchViewerCounts {
		b.Run("viewers="+strconv.Itoa(viewers), func(b *testing.B) {
			benchPoll(b, viewers, false)
		})
	}
}

// BenchmarkPlaylistPollUnderWrite adds a source encoding at real time beside
// the viewers, so the read side and the write side contend for the stream mutex
// exactly as they do in production.
func BenchmarkPlaylistPollUnderWrite(b *testing.B) {
	for _, viewers := range benchViewerCounts {
		b.Run("viewers="+strconv.Itoa(viewers), func(b *testing.B) {
			benchPoll(b, viewers, true)
		})
	}
}

// startBenchPollers runs viewers polling the playlist on benchPollInterval
// until the benchmark ends, and returns the total poll count so the caller can
// report the rate actually achieved.
//
// The count is not bookkeeping. A ticker at benchPollInterval drops ticks
// silently under load, so the nominal rate is an upper bound the run may fall
// far short of, and a starved run would otherwise look exactly like a healthy
// one in the output.
func startBenchPollers(b *testing.B, s *Stream, viewers int) *atomic.Int64 {
	b.Helper()

	var polls atomic.Int64
	stop := make(chan struct{})
	var pollers sync.WaitGroup
	for range viewers {
		pollers.Go(func() {
			tick := time.NewTicker(benchPollInterval)
			defer tick.Stop()
			local := int64(0)
			defer func() { polls.Add(local) }()
			for {
				select {
				case <-stop:
					return
				case <-tick.C:
					_ = s.Playlist()
					local++
				}
			}
		})
	}
	b.Cleanup(func() {
		close(stop)
		pollers.Wait()
	})
	return &polls
}

// BenchmarkStreamWriteUnderPoll measures the capture goroutine's cost while
// viewers poll, which is the coupling that matters most on this path. Write
// runs on the audio thread, where added latency is not lost throughput but a
// dropped buffer, and every poll used to take the same mutex Write needs.
//
// The pollers are rate limited rather than run flat out, and the rate is the
// measurement. Flat-out pollers do not measure this: with a mutex in the way
// they park and consume almost no CPU, and without one they spin at tens of
// millions of polls a second and saturate every core, so removing the mutex
// scores as a large regression on a machine with fewer cores than viewers. That
// is an artefact of an infinite poll rate, not of the muxer. Pacing separates
// the two, leaving lock interference as the only thing that varies.
//
// benchPollInterval is still thousands of times faster than the roughly 0.5 Hz
// per viewer hls.js actually uses, so this remains a stress bound and not a
// production shape. The mean poll rate was never the risk; polls arriving
// together are.
func BenchmarkStreamWriteUnderPoll(b *testing.B) {
	for _, viewers := range benchViewerCounts {
		b.Run("viewers="+strconv.Itoa(viewers), func(b *testing.B) {
			s := newBenchStream(b)
			at := fillWindow(b, s)
			polls := startBenchPollers(b, s, viewers)

			pcm := tone(testRate/benchFramesPerSecond, testChannels, testRate, benchToneHz)
			step := time.Second / benchFramesPerSecond

			b.SetBytes(int64(len(pcm)))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if err := s.Write(pcm, at); err != nil {
					b.Fatalf("write: %v", err)
				}
				at = at.Add(step)
			}
			b.StopTimer()

			// The load this run actually applied, not the load it asked for.
			b.ReportMetric(float64(polls.Load())/b.Elapsed().Seconds(), "polls/s")
		})
	}
}

// BenchmarkSegmentLookup measures the window's linear scan at the shipping
// window size and at two larger ones, to establish empirically where the scan
// stops being free rather than arguing about it.
//
// It looks up the NEWEST segment, which is both the worst case for a scan that
// starts at the oldest and what a client keeping up with the live edge actually
// requests.
func BenchmarkSegmentLookup(b *testing.B) {
	for _, size := range []int{DefaultWindowSize, 60, 600} {
		b.Run("window="+strconv.Itoa(size), func(b *testing.B) {
			r := newSegmentWindow(size)
			for i := range size {
				r.push(&Segment{Seq: uint64(i)}) //nolint:gosec // loop bound is a small positive literal
			}
			newest := uint64(size - 1) //nolint:gosec // as above

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, ok := findSegment(r.retained(), newest); !ok {
					b.Fatal("newest segment must be retained")
				}
			}
		})
	}
}
