package processor

import (
	"strings"

	"github.com/tphakala/birdnet-go/internal/analysis/noisebank"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	// noiseGateWindowOperation and noiseGateDetectionOperation are the operation
	// values of the two decision lines the gate writes: one per compared window at
	// Debug, one per detection at Info.
	noiseGateWindowOperation    = "noise_gate_window"
	noiseGateDetectionOperation = "noise_gate_detection"

	// Outcomes of a detection the gate compared, as written to the decision line.
	noiseGateOutcomeKept      = "kept"
	noiseGateOutcomeDiscarded = "discarded"

	// noiseGateOutcomeFieldCap is the most fields an outcome line carries.
	noiseGateOutcomeFieldCap = 16
)

// noiseComparison is what the gate found for one analysis window. The zero value
// means the gate did not compare the window.
type noiseComparison struct {
	compared   bool
	matched    bool
	similarity float64
}

// noiseTally accumulates the comparisons of every window that fed one pending
// detection, for the decision line written when it leaves the pending map. It only
// feeds the log. minMatch is meaningful only while matched > 0 and maxMiss only
// while compared > matched: a cosine similarity can be negative, so a zero value
// cannot stand for "unset".
type noiseTally struct {
	compared, matched int
	minMatch, maxMiss float64
}

// add folds one window's comparison into the tally. Counts, a minimum and a
// maximum give the same tally in any arrival order. A window that was not compared
// changes nothing.
func (t *noiseTally) add(c noiseComparison) {
	if !c.compared {
		return
	}
	if c.matched {
		if t.matched == 0 || c.similarity < t.minMatch {
			t.minMatch = c.similarity
		}
		t.matched++
	} else if t.compared == t.matched || c.similarity > t.maxMiss {
		t.maxMiss = c.similarity
	}
	t.compared++
}

// noiseGateLogger returns the logger of the noise example decision lines. It is
// fetched per call, like GetLogger, so a test or a reload that swaps the global
// logger is followed.
func noiseGateLogger() logger.Logger {
	return logger.Global().Module(noisebank.LogModule)
}

// SetNoiseBank sets the bank of noise examples the gate compares each analysis
// window with. Call it before Start; the processor reads the pointer without a
// lock and never expects it to change. The content of the bank may change at any
// time. A nil bank disables the gate.
func (p *Processor) SetNoiseBank(b *noisebank.Bank) {
	p.noiseBank = b
}

// SetNoiseExamples sets the service that learns noise examples from marked
// detections and keeps the bank in step with the database. Call it before Start
// and never reset it; the processor and the API read the pointer without a lock.
// The service owns the bank set by SetNoiseBank, so set both from the same
// service.
func (p *Processor) SetNoiseExamples(s *noisebank.Service) {
	p.noiseExamples = s
}

// NoiseExamples returns the service set by SetNoiseExamples, or nil when the
// install does not have one (the legacy database, or a start whose examples could
// not be loaded).
func (p *Processor) NoiseExamples() *noisebank.Service {
	return p.noiseExamples
}

// applyNoiseExampleGate tags the detections of one analysis window as unlikely when
// the window's embedding is close to a noise example the user marked. The tag is
// the same one the ultrasonic filter sets, with the noise bit and the similarity
// beside it, so the two validators compose in the pending merge and a window either
// one judged counts as judged. The gate only tags: whether a tagged detection is
// discarded is decided at flush by the Discard Unlikely setting.
//
// It does nothing without a bank or without an embedding (models that do not
// produce one, or a window the model did not embed), and an embedding is compared
// only with examples of its own embedding space. The embedding is the one the
// model already computed for the window, so the gate costs one scan of the bank per
// window and no inference.
//
// Every window it compared writes one Debug line, match or miss (enabled with the
// module level of noisebank.LogModule), and records the comparison on the window's
// detections for the Info outcome line written when a pending detection ends.
func (p *Processor) applyNoiseExampleGate(item *classifier.Results, detections []Detections) {
	if p.noiseBank == nil || len(item.Embedding) == 0 {
		return
	}
	similarity, ok := p.noiseBank.Match(item.EmbeddingSpace, item.Embedding)
	if !ok {
		return
	}

	if similarity < noisebank.MatchSimilarity {
		p.recordNoiseComparison(item, detections, float64(similarity), false)
		return
	}

	p.recordNoiseComparison(item, detections, float64(similarity), true)
	for i := range detections {
		detections[i].Result.Unlikely = true
		detections[i].Result.UnlikelySources |= detection.UnlikelySourceNoiseExamples
		detections[i].Result.NoiseSimilarity = float64(similarity)
	}
}

// recordNoiseComparison stores the comparison on every detection of the window and
// writes the window's Debug line. It never changes a verdict.
func (p *Processor) recordNoiseComparison(item *classifier.Results, detections []Detections, similarity float64, matched bool) {
	var best int
	for i := range detections {
		detections[i].noiseGate = noiseComparison{compared: true, matched: matched, similarity: similarity}
		if detections[i].Result.Confidence > detections[best].Result.Confidence {
			best = i
		}
	}
	var bestSpecies string
	var bestConfidence float64
	if len(detections) > 0 {
		bestSpecies = strings.ToLower(detections[best].Result.Species.CommonName)
		bestConfidence = detections[best].Result.Confidence
	}
	noiseGateLogger().Debug("noise examples: window compared",
		logger.Float64("similarity", similarity),
		logger.Float64("threshold", noisebank.MatchSimilarity),
		logger.Bool("matched", matched),
		logger.String("space", item.EmbeddingSpace),
		logger.Int("examples_compared", p.noiseBank.Len(item.EmbeddingSpace)),
		logger.String("source", p.getDisplayNameForSource(item.Source.ID)),
		logger.String("model_id", item.ModelID),
		logger.Int("detections", len(detections)),
		logger.String("best_species", bestSpecies),
		logger.Float64("best_confidence", bestConfidence),
		logger.String("operation", noiseGateWindowOperation))
}

// logNoiseGateOutcome writes the decision line of a pending detection that leaves
// the pending map, with what the gate found across its windows. It writes nothing
// for a detection the gate never compared. reason is the discard reason and is
// recorded only for a discard.
func (p *Processor) logNoiseGateOutcome(item *PendingDetection, speciesName, outcome, reason string) {
	tally := &item.noiseGate
	if tally.compared == 0 {
		return
	}
	result := &item.Detection.Result
	fields := append(make([]logger.Field, 0, noiseGateOutcomeFieldCap), []logger.Field{
		logger.String("species", speciesName),
		logger.String("source", p.getDisplayNameForSource(item.Source)),
		logger.String("best_model_id", item.BestModelID),
		logger.Float64("confidence", item.Confidence),
		logger.Time("detection_time", item.FirstDetected),
		logger.Bool("unlikely", result.Unlikely),
		logger.String("unlikely_sources", result.UnlikelySources.String()),
		logger.Int("hit_windows", item.Count),
		logger.Int("compared_windows", tally.compared),
		logger.Int("matched_windows", tally.matched),
		logger.Float64("threshold", noisebank.MatchSimilarity),
		logger.String("outcome", outcome),
	}...)
	if tally.matched > 0 {
		fields = append(fields, logger.Float64("min_match_similarity", tally.minMatch))
	}
	if tally.compared > tally.matched {
		fields = append(fields, logger.Float64("max_miss_similarity", tally.maxMiss))
	}
	if outcome == noiseGateOutcomeDiscarded {
		fields = append(fields, logger.String("reason", reason))
	}
	fields = append(fields, logger.String("operation", noiseGateDetectionOperation))
	noiseGateLogger().Info("noise examples: detection outcome", fields...)
}
