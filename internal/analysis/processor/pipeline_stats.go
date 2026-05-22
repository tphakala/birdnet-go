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
	SourceID string
	ModelID  string
}

// inferenceStats holds accumulated inference statistics for one source-model pair.
type inferenceStats struct {
	Inferences    int
	RawResults    int
	PassedFilter  int
	MaxConfidence float32
	Threshold     float32
}

// PipelineStats accumulates per-source, per-model inference statistics
// and logs a periodic summary at info level.
type PipelineStats struct {
	mu    sync.Mutex
	stats map[sourceModelKey]*inferenceStats

	displayNameFn func(sourceID string) string

	cancel context.CancelFunc
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

	key := sourceModelKey{SourceID: sourceID, ModelID: modelID}
	s := ps.stats[key]
	if s == nil {
		s = &inferenceStats{}
		ps.stats[key] = s
	}

	s.Inferences++
	s.RawResults += rawResults
	s.PassedFilter += passedFilter
	if maxConfidence > s.MaxConfidence {
		s.MaxConfidence = maxConfidence
	}
	s.Threshold = threshold
}

// Start launches the periodic logging goroutine.
func (ps *PipelineStats) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	ps.cancel = cancel

	go ps.run(ctx)
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
		if s.Inferences == 0 {
			continue
		}

		sourceName := key.SourceID
		if ps.displayNameFn != nil {
			if name := ps.displayNameFn(key.SourceID); name != "" {
				sourceName = name
			}
		}

		log.Info("pipeline stats",
			logger.String("source", sourceName),
			logger.String("model", key.ModelID),
			logger.Int("inferences", s.Inferences),
			logger.Int("raw_results", s.RawResults),
			logger.Int("passed_filter", s.PassedFilter),
			logger.Float64("max_confidence", roundTo2(float64(s.MaxConfidence))),
			logger.Float64("threshold", roundTo2(float64(s.Threshold))),
			logger.String("period", pipelineStatsInterval.String()),
		)
	}
}

func roundTo2(v float64) float64 {
	return math.Round(v*100) / 100
}
