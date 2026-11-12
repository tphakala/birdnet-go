package processor

import (
	"github.com/tphakala/birdnet-go/internal/analysis/noisebank"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// SetNoiseBank sets the bank of noise examples the gate compares each analysis
// window with. Call it before Start; the processor reads the pointer without a
// lock and never expects it to change. The content of the bank may change at any
// time. A nil bank disables the gate.
func (p *Processor) SetNoiseBank(b *noisebank.Bank) {
	p.noiseBank = b
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
func (p *Processor) applyNoiseExampleGate(item *classifier.Results, detections []Detections) {
	if p.noiseBank == nil || len(item.Embedding) == 0 {
		return
	}
	similarity, ok := p.noiseBank.Match(item.EmbeddingSpace, item.Embedding)
	if !ok {
		return
	}

	if similarity < noisebank.MatchSimilarity {
		GetLogger().Debug("noise examples: window does not match",
			logger.Float64("similarity", float64(similarity)),
			logger.Float64("threshold", noisebank.MatchSimilarity),
			logger.String("space", item.EmbeddingSpace))
		return
	}

	GetLogger().Info("noise examples: detections tagged unlikely",
		logger.Float64("similarity", float64(similarity)),
		logger.Float64("threshold", noisebank.MatchSimilarity),
		logger.String("space", item.EmbeddingSpace),
		logger.Int("bank_size", p.noiseBank.Len(item.EmbeddingSpace)),
		logger.String("source", p.getDisplayNameForSource(item.Source.ID)),
		logger.Int("detections", len(detections)))
	for i := range detections {
		detections[i].Result.Unlikely = true
		detections[i].Result.UnlikelySources |= detection.UnlikelySourceNoiseExamples
		detections[i].Result.NoiseSimilarity = float64(similarity)
	}
}
