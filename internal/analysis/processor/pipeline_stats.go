package processor

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/logger"
)

const pipelineStatsInterval = 5 * time.Minute

// sourceModelKey identifies a unique source+model pair for stats tracking.
type sourceModelKey struct {
	sourceID string
	modelID  string
}

// inferenceStats holds accumulated inference statistics for one source-model pair.
type inferenceStats struct {
	inferences    int
	rawResults    int
	passedFilter  int
	maxConfidence float32
	threshold     float32
	// daylightDiscards counts detections dropped by the daylight filter in the
	// current window. Tracked here so a user who sees zero saved detections has an
	// aggregate signal that a filter is discarding them, instead of only a
	// per-detection Debug line they have to know to grep for.
	daylightDiscards int
}

// PipelineStats accumulates per-source, per-model inference statistics
// and logs a periodic summary at info level.
type PipelineStats struct {
	mu    sync.Mutex
	stats map[sourceModelKey]*inferenceStats

	displayNameFn func(sourceID string) string

	startOnce sync.Once
	cancel    context.CancelFunc
}

// NewPipelineStats creates a new stats accumulator. displayNameFn resolves
// source IDs to human-readable display names for log output.
func NewPipelineStats(displayNameFn func(string) string) *PipelineStats {
	return &PipelineStats{
		stats:         make(map[sourceModelKey]*inferenceStats),
		displayNameFn: displayNameFn,
	}
}

// RecordInference records one inference cycle with its result counts.
func (ps *PipelineStats) RecordInference(sourceID, modelID string, rawResults, passedFilter int, maxConfidence, threshold float32) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	key := sourceModelKey{sourceID: sourceID, modelID: modelID}
	s := ps.stats[key]
	if s == nil {
		s = &inferenceStats{}
		ps.stats[key] = s
	}

	s.inferences++
	s.rawResults += rawResults
	s.passedFilter += passedFilter
	if maxConfidence > s.maxConfidence {
		s.maxConfidence = maxConfidence
	}
	s.threshold = threshold
}

// RecordDaylightDiscard records one detection dropped by the daylight filter for
// the given source and model so the periodic summary can surface how many
// detections a filter is silently eating. The model id is the detection's best
// (winning) model: in a multi-model consensus config that can differ from a
// per-inference model id, so a discard may land on a different summary row than
// some of that detection's inferences. That attribution is intentional (the
// discard belongs to the model that produced the detection), and on the common
// single-model config it shares the row with the matching inferences.
func (ps *PipelineStats) RecordDaylightDiscard(sourceID, modelID string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	key := sourceModelKey{sourceID: sourceID, modelID: modelID}
	s := ps.stats[key]
	if s == nil {
		s = &inferenceStats{}
		ps.stats[key] = s
	}

	s.daylightDiscards++
}

// Start launches the periodic logging goroutine. Safe to call multiple times.
func (ps *PipelineStats) Start() {
	ps.startOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		ps.cancel = cancel
		go ps.run(ctx)
	})
}

// Stop cancels the periodic logging goroutine.
func (ps *PipelineStats) Stop() {
	if ps.cancel != nil {
		ps.cancel()
	}
}

func (ps *PipelineStats) run(ctx context.Context) {
	ticker := time.NewTicker(pipelineStatsInterval)
	defer ticker.Stop()

	log := GetLogger()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ps.logAndReset(log)
		}
	}
}

func (ps *PipelineStats) logAndReset(log logger.Logger) {
	ps.mu.Lock()
	snapshot := ps.stats
	ps.stats = make(map[sourceModelKey]*inferenceStats, len(snapshot))
	ps.mu.Unlock()

	for key, s := range snapshot {
		// Emit when the window saw any activity worth reporting: inferences, or
		// daylight-filter discards on their own. Discards usually share a window with
		// the inferences that produced them (RecordInference runs before filtering),
		// but a detection inferred at the tail of one window can be flushed and
		// discarded in the next, leaving a window with only discards; surfacing that
		// window is exactly the zero-detections-saved signal a user needs, so it must
		// not be suppressed here.
		if s.inferences == 0 && s.daylightDiscards == 0 {
			continue
		}

		sourceName := key.sourceID
		if ps.displayNameFn != nil {
			if name := ps.displayNameFn(key.sourceID); name != "" {
				sourceName = name
			}
		}

		log.Info("pipeline stats",
			logger.String("source", sourceName),
			logger.String("model", key.modelID),
			logger.Int("inferences", s.inferences),
			logger.Int("raw_results", s.rawResults),
			logger.Int("passed_filter", s.passedFilter),
			logger.Int("daylight_discards", s.daylightDiscards),
			logger.Float64("max_confidence", roundTo2(float64(s.maxConfidence))),
			logger.Float64("threshold", roundTo2(float64(s.threshold))),
			logger.Duration("period", pipelineStatsInterval),
			logger.String("operation", "pipeline_stats_report"),
		)
	}
}

func roundTo2(v float64) float64 {
	return math.Round(v*100) / 100
}
