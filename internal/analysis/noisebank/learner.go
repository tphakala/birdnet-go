package noisebank

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore/transcode"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// jobPhase is where a job is in its life.
type jobPhase uint8

const (
	phaseQueued jobPhase = iota
	phaseRunning
	phaseLearned
	phaseFailed
)

// job is the learning of one detection's clip. Its fields are guarded by
// Service.mu, except the progress counters.
type job struct {
	detectionID uint
	clipName    string
	open        OpenClipFunc

	phase     jobPhase
	cancelled bool               // a verdict change or Clear ended the job's right to store
	cancel    context.CancelFunc // set while running
	failure   Reason

	done  atomic.Int64
	total atomic.Int64
}

// active reports whether the job is queued or running and still entitled to
// store examples. A nil job is not active. A job that was cancelled is not: it
// may be finishing its current window, but it will store nothing and no longer
// needs the clip file.
func (j *job) active() bool {
	return j != nil && !j.cancelled && (j.phase == phaseQueued || j.phase == phaseRunning)
}

// cancelLocked withdraws the job's right to store and stops its work. The caller
// holds Service.mu.
func (j *job) cancelLocked() {
	if j.phase == phaseLearned || j.phase == phaseFailed {
		return
	}
	j.cancelled = true
	if j.cancel != nil {
		j.cancel()
	}
}

// reported returns the state and failure reason a status shows. A cancelled job
// that did not finish learning reports as failed with the reason cancelled.
func (j *job) reported() (State, Reason) {
	switch {
	case j.phase == phaseLearned:
		return StateLearned, ""
	case j.cancelled:
		return StateFailed, ReasonCancelled
	case j.phase == phaseFailed:
		return StateFailed, j.failure
	case j.phase == phaseRunning:
		return StateRunning, ""
	default:
		return StateQueued, ""
	}
}

// candidate is a window the job may store.
type candidate struct {
	offsetMs int
	score    float32
	vector   []float32
}

// bestCandidates keeps the examplesPerClip highest scoring candidates seen. The
// windows arrive in offset order, so on equal scores the earlier window stays.
type bestCandidates struct {
	items []candidate
}

// offer adds c unless it does not beat the weakest kept candidate.
func (b *bestCandidates) offer(c candidate) {
	if len(b.items) < examplesPerClip {
		b.items = append(b.items, c)
		return
	}
	// The weakest kept window is the lowest score and, among equal scores, the
	// latest offset: slots do not stay in offset order once one is replaced.
	worst := 0
	for i := 1; i < len(b.items); i++ {
		w := b.items[worst]
		if b.items[i].score < w.score || (b.items[i].score == w.score && b.items[i].offsetMs > w.offsetMs) {
			worst = i
		}
	}
	if c.score > b.items[worst].score {
		b.items[worst] = c
	}
}

// sorted returns the kept candidates, best first, earlier offset first on ties.
func (b *bestCandidates) sorted() []candidate {
	out := slices.Clone(b.items)
	sort.SliceStable(out, func(i, k int) bool {
		if out[i].score != out[k].score {
			return out[i].score > out[k].score
		}
		return out[i].offsetMs < out[k].offsetMs
	})
	return out
}

// work runs queued jobs one at a time until the service stops.
func (s *Service) work() {
	defer close(s.workerDone)
	for {
		select {
		case <-s.runCtx.Done():
			return
		case j := <-s.queue:
			s.runJob(j)
		}
	}
}

// runJob runs one job to its end, whatever happens inside it.
func (s *Service) runJob(j *job) {
	ctx, cancel := context.WithTimeout(s.runCtx, learnJobTimeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("noise examples: learning job panicked",
				logger.Uint64("detection_id", uint64(j.detectionID)), logger.Any("panic", r))
			s.finish(j, ReasonInferenceFailed)
		}
	}()

	s.mu.Lock()
	if j.cancelled || s.jobs[j.detectionID] != j {
		s.finishLocked(j, ReasonCancelled)
		s.mu.Unlock()
		return
	}
	j.phase = phaseRunning
	j.cancel = cancel
	s.mu.Unlock()

	space, found, reason := s.learn(ctx, j)
	if reason != "" {
		s.finish(j, reason)
		return
	}
	s.commit(j, space, found)
}

// finish records that the job ended without storing, with reason.
func (s *Service) finish(j *job, reason Reason) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finishLocked(j, reason)
}

func (s *Service) finishLocked(j *job, reason Reason) {
	j.phase = phaseFailed
	j.failure = reason
	j.cancel = nil
	if reason != ReasonCancelled {
		s.log.Info("noise examples: learning job failed",
			logger.Uint64("detection_id", uint64(j.detectionID)),
			logger.String("reason", string(reason)))
	}
}

// contextReason maps the end of a job's context to a failure reason, or empty
// when the context is still live.
func contextReason(ctx context.Context) Reason {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return ReasonTimeout
	case ctx.Err() != nil:
		return ReasonCancelled
	default:
		return ""
	}
}

// gridFor derives the window grid of a model from its spec: the window is the
// model's clip in samples and the hop is the step the live analysis takes between
// windows, so the examples come from windows like the ones the gate compares.
func (s *Service) gridFor(spec *classifier.ModelSpec) windowGrid {
	overlap := classifier.ResolveModelOverlap(modelID, *spec, s.cfg.Embedder.EffectiveBaseOverlap())
	_, _, readBytes := spec.BufferDimensions(overlap)
	frame := conf.NumChannels * conf.BytesPerSample
	window := spec.ClipSampleCount()
	hop := min(readBytes/frame, window)
	return windowGrid{window: window, hop: hop, rate: spec.EffectiveSampleRate()}
}

// learn embeds every window of the clip and returns the space of the job and the
// best candidates. A non-empty reason means the job failed.
func (s *Service) learn(ctx context.Context, j *job) (space string, found []candidate, reason Reason) {
	spec, ok := s.cfg.Embedder.ModelSpecFor(modelID)
	if !ok {
		return "", nil, ReasonModelChanged
	}
	grid := s.gridFor(&spec)
	if grid.window <= 0 || grid.hop <= 0 || grid.rate <= 0 {
		return "", nil, ReasonModelChanged
	}

	src, reason := s.decode(ctx, j, grid.rate)
	if reason != "" {
		return "", nil, reason
	}
	j.total.Store(int64(grid.count(src.SpanSamples())))

	var best bestCandidates
	err := forEachWindow(ctx, src, grid, func(offset int64, samples []float32) error {
		pred, err := s.cfg.Embedder.PredictModelTimed(ctx, modelID, [][]float32{samples})
		if err != nil {
			return fmt.Errorf("embed window: %w", err)
		}
		emb := pred.Embedding
		if emb.Space == "" || len(emb.Vector) == 0 {
			return errNoEmbedding
		}
		if space == "" {
			space = emb.Space
		} else if emb.Space != space {
			return errSpaceChanged
		}
		if emb.TopScore >= exampleScoreFloor {
			best.offer(candidate{offsetMs: grid.offsetMs(offset), score: emb.TopScore, vector: emb.Vector})
		}
		j.done.Add(1)
		return pause(ctx, max(pred.Timing.Predict, pred.Timing.LockWait))
	})
	if reason := contextReason(ctx); err != nil && reason != "" {
		return "", nil, reason
	}
	switch {
	case err == nil:
	case errors.Is(err, errSpaceChanged), errors.Is(err, errNoEmbedding), errors.Is(err, classifier.ErrModelNotLoaded):
		return "", nil, ReasonModelChanged
	default:
		s.log.Warn("noise examples: embedding a window failed",
			logger.Uint64("detection_id", uint64(j.detectionID)), logger.Error(err))
		return "", nil, ReasonInferenceFailed
	}
	if len(best.items) == 0 {
		return "", nil, ReasonNoCandidates
	}
	return space, best.sorted(), ""
}

var (
	errNoEmbedding  = errors.NewStd("model returned no embedding")
	errSpaceChanged = errors.NewStd("embedding space changed during the job")
)

// decode opens the clip, decodes it to the model's rate and closes it again. The
// decoded clip is held in memory, so the file is not needed after this returns.
func (s *Service) decode(ctx context.Context, j *job, rate int) (*transcode.MonoSource, Reason) {
	f, err := j.open(j.clipName)
	if err != nil {
		return nil, ReasonClipMissing
	}
	defer func() { _ = f.Close() }()
	src, err := transcode.DecodeMonoOpenFile(ctx, f, j.clipName, rate)
	if err != nil {
		if reason := contextReason(ctx); reason != "" {
			return nil, reason
		}
		return nil, ReasonDecodeFailed
	}
	return src, ""
}

// pause waits for d or until ctx ends, so a learning job leaves the model to live
// analysis for as long as its own inference took.
func pause(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// commit stores what a job learned. It runs under the service mutex and stores
// only while the job is still the current, uncancelled job of its detection. While
// the detection exists it needs the mark too; when the detection is gone the
// examples are stored detached, since the user trained them and nothing removes
// them but an explicit clear.
func (s *Service) commit(j *job, space string, found []candidate) {
	ctx, cancel := context.WithTimeout(s.runCtx, commitTimeout)
	defer cancel()

	s.mu.Lock()
	defer s.mu.Unlock()
	// A service that is stopping stores nothing: its database context is already
	// done, and a failed write would be reported as a store failure.
	if j.cancelled || s.jobs[j.detectionID] != j || s.runCtx.Err() != nil {
		s.finishLocked(j, ReasonCancelled)
		return
	}

	rows := make([]entities.NoiseExample, len(found))
	vectors := make([][]float32, len(found))
	now := s.cfg.Now().Unix()
	for i, c := range found {
		rows[i] = entities.NoiseExample{
			WindowOffsetMs: c.offsetMs,
			EmbeddingSpace: space,
			Dim:            len(c.vector),
			Score:          float64(c.score),
			Vector:         encodeVector(c.vector),
			CreatedAt:      now,
		}
		vectors[i] = c.vector
	}

	store := func() error {
		_, err := s.cfg.Detections.Get(ctx, j.detectionID)
		switch {
		case errors.Is(err, repository.ErrDetectionNotFound):
			if err := s.cfg.Examples.InsertDetached(ctx, rows); err != nil {
				return err
			}
			s.bank.Add(space, vectors)
			return nil
		case err != nil:
			return err
		}
		review, err := s.review(ctx, j.detectionID)
		if err != nil {
			return err
		}
		if !isMarked(review) {
			j.cancelled = true
			return nil
		}
		if err := s.cfg.Examples.ReplaceForDetection(ctx, j.detectionID, rows); err != nil {
			return err
		}
		s.bank.Replace(j.detectionID, space, vectors)
		return nil
	}
	if err := store(); err != nil {
		s.log.Warn("noise examples: storing learned examples failed",
			logger.Uint64("detection_id", uint64(j.detectionID)), logger.Error(err))
		s.finishLocked(j, ReasonStoreFailed)
		return
	}
	if j.cancelled {
		s.finishLocked(j, ReasonCancelled)
		return
	}
	j.phase = phaseLearned
	j.cancel = nil
	s.log.Info("noise examples: learned from a marked detection",
		logger.Uint64("detection_id", uint64(j.detectionID)),
		logger.String("space", space),
		logger.Int("examples", len(found)))
}
