package audiocore

import (
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// statsLogInterval is the number of frames between periodic stats log lines.
// At 48kHz / 1024 samples per frame (~46 frames/s), 10000 frames is ~3.6 min.
const statsLogInterval int64 = 10000

// routeStats tracks per-frame timing for a single route. All fields are
// updated atomically so the drainer goroutine does not need any additional
// synchronization. Values are nanosecond totals accumulated over
// statsLogInterval frames.
type routeStats struct {
	frames       atomic.Int64
	resampleNs   atomic.Int64
	processingNs atomic.Int64
	writeNs      atomic.Int64
	totalNs      atomic.Int64
	maxTotalNs   atomic.Int64
}

// record adds one frame's timing to the accumulators.
func (s *routeStats) record(resample, processing, write, total time.Duration) {
	s.resampleNs.Add(int64(resample))
	s.processingNs.Add(int64(processing))
	s.writeNs.Add(int64(write))
	s.totalNs.Add(int64(total))
	for {
		cur := s.maxTotalNs.Load()
		ns := int64(total)
		if ns <= cur {
			break
		}
		if s.maxTotalNs.CompareAndSwap(cur, ns) {
			break
		}
	}
}

// checkAndLog emits a debug log and resets counters when the interval is
// reached. The caller must call this after record().
func (s *routeStats) checkAndLog(log logger.Logger, sourceID, consumerID string) {
	n := s.frames.Add(1)
	if n%statsLogInterval != 0 {
		return
	}
	total := s.totalNs.Swap(0)
	resample := s.resampleNs.Swap(0)
	processing := s.processingNs.Swap(0)
	write := s.writeNs.Swap(0)
	maxTotal := s.maxTotalNs.Swap(0)

	avgUs := total / statsLogInterval / 1000
	maxUs := maxTotal / 1000

	log.Debug("route frame stats",
		logger.String("source_id", sourceID),
		logger.String("consumer_id", consumerID),
		logger.Int64("frames", statsLogInterval),
		logger.Int64("avg_frame_us", avgUs),
		logger.Int64("max_frame_us", maxUs),
		logger.Int64("avg_resample_us", resample/statsLogInterval/1000),
		logger.Int64("avg_processing_us", processing/statsLogInterval/1000),
		logger.Int64("avg_write_us", write/statsLogInterval/1000),
	)
}
