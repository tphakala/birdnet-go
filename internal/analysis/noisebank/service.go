package noisebank

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore/transcode"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	// examplesPerClip is the most examples one marked clip contributes.
	examplesPerClip = 4

	// exampleScoreFloor is the raw top head score a window needs to become a
	// candidate. It is a fixed floor, independent of the detection threshold: the
	// windows of a clip are ranked by the score the model gave them, and nothing is
	// known about windows below it.
	exampleScoreFloor = 0.5

	// maxQueuedJobs is the number of learning jobs that may wait for the worker.
	maxQueuedJobs = 8

	// leaveTimeout bounds LeaveNoise, which runs detached from the request.
	leaveTimeout = 10 * time.Second

	// commitTimeout bounds the database work of a job's commit.
	commitTimeout = 30 * time.Second

	// learnJobTimeout bounds one learning job from decode to its last window; the commit has a bound of its own.
	learnJobTimeout = 10 * time.Minute

	// msPerSecond converts sample offsets to the millisecond offsets stored with
	// an example.
	msPerSecond = 1000

	// bytesPerFloat32 is the size of one stored vector component.
	bytesPerFloat32 = 4
)

// modelID is the registry ID of the model whose windows are learned.
const modelID = classifier.RegistryIDBat

// Embedder is the part of the classifier orchestrator the service uses.
type Embedder interface {
	// PredictModelTimed embeds and scores one window.
	PredictModelTimed(ctx context.Context, modelID string, sample [][]float32) (classifier.Prediction, error)
	// ModelSpecFor returns the spec of a loaded model.
	ModelSpecFor(modelID string) (classifier.ModelSpec, bool)
	// EffectiveBaseOverlap returns the configured analysis overlap.
	EffectiveBaseOverlap() time.Duration
	// EmbeddingSpaceFor returns the embedding space of a loaded model.
	EmbeddingSpaceFor(modelID string) (string, bool)
}

// DetectionStore is the part of the v2 detection repository the service uses.
type DetectionStore interface {
	// Get returns a detection, or repository.ErrDetectionNotFound.
	Get(ctx context.Context, id uint) (*entities.Detection, error)
	// GetWithRelations returns a detection with its model loaded.
	GetWithRelations(ctx context.Context, id uint) (*entities.Detection, error)
	// GetReview returns the review of a detection, or repository.ErrReviewNotFound.
	GetReview(ctx context.Context, detectionID uint) (*entities.DetectionReview, error)
	// SaveReview creates or updates the review of a detection.
	SaveReview(ctx context.Context, review *entities.DetectionReview) error
	// GetModelContributionsByDetectionIDs returns the model contributions of detections.
	GetModelContributionsByDetectionIDs(ctx context.Context, detectionIDs []uint) (map[uint][]*entities.DetectionModelContribution, error)
}

// OpenClipFunc opens the clip file a detection stored under clipName. The caller
// of the service builds it so that every open goes through the sandboxed file
// access; the service closes every file it receives.
type OpenClipFunc func(clipName string) (*os.File, error)

// Config holds the dependencies of a Service.
type Config struct {
	// Embedder runs the model over the windows of a clip.
	Embedder Embedder
	// Examples stores the noise examples.
	Examples repository.NoiseExampleRepository
	// Detections reads detections and writes reviews.
	Detections DetectionStore
	// Now returns the current time; the zero value uses time.Now.
	Now func() time.Time
}

// MarkRequest identifies the detection to mark as noise.
type MarkRequest struct {
	// DetectionID is the detection to learn from.
	DetectionID uint
	// OpenClip opens the clip of the detection. It is called once by the preflight
	// and once by the learning job; every file it returns is closed.
	OpenClip OpenClipFunc
}

// Service learns noise examples from the clips of detections the user marks as
// noise, keeps the Bank in step with the noise_examples table, and answers for
// the state of that learning. See the package comment for the rules it enforces.
type Service struct {
	cfg  Config
	bank *Bank
	log  logger.Logger

	// mu serialises Mark's critical section, LeaveNoise, GuardDelete, the commit of
	// a job and the two locked steps of Clear. It is held across the short database
	// writes of those paths, and never while waiting for the worker.
	mu       sync.Mutex
	jobs     map[uint]*job // the latest job of each detection
	guards   map[uint]int  // detections with a delete in progress
	clearing bool          // set while the learned noise is being cleared
	queue    chan *job
	// idle is closed while the worker holds no job and open while it runs one, so
	// Clear can wait for the worker without holding mu. Guarded by mu.
	idle chan struct{}

	runCtx     context.Context
	runCancel  context.CancelFunc
	workerDone chan struct{}
}

// NewService creates a Service with an empty Bank. Call Start before use.
func NewService(cfg Config) *Service {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	runCtx, runCancel := context.WithCancel(context.Background())
	idle := make(chan struct{})
	close(idle)
	return &Service{
		cfg:        cfg,
		bank:       New(),
		log:        logger.Global().Module("analysis").Module("noisebank"),
		jobs:       make(map[uint]*job),
		guards:     make(map[uint]int),
		queue:      make(chan *job, maxQueuedJobs),
		idle:       idle,
		runCtx:     runCtx,
		runCancel:  runCancel,
		workerDone: make(chan struct{}),
	}
}

// Bank returns the bank the service keeps in step with the database.
func (s *Service) Bank() *Bank { return s.bank }

// Start loads every stored example into the bank and starts the worker. It
// clears the link of examples whose detection is gone and deletes nothing. When
// it fails the service must not be used. ctx bounds only the load.
func (s *Service) Start(ctx context.Context) error {
	detached, err := s.cfg.Examples.DetachOrphans(ctx)
	if err != nil {
		return fmt.Errorf("detach orphaned noise examples: %w", err)
	}
	if detached > 0 {
		s.log.Warn("noise examples: cleared the link of examples whose detection no longer exists",
			logger.Int64("examples", detached))
	}
	rows, err := s.cfg.Examples.ListAll(ctx)
	if err != nil {
		return fmt.Errorf("load noise examples: %w", err)
	}
	loaded := make([]Example, 0, len(rows))
	skipped := 0
	for i := range rows {
		ex, ok := exampleFromRow(&rows[i])
		if !ok {
			skipped++
			continue
		}
		loaded = append(loaded, ex)
	}
	if skipped > 0 {
		s.log.Warn("noise examples: skipped stored examples with a malformed vector",
			logger.Int("examples", skipped))
	}
	s.bank.Load(loaded)
	s.log.Info("noise examples loaded", logger.Int("examples", len(loaded)))
	go s.work()
	return nil
}

// Stop cancels the running job and waits for the worker until ctx ends.
func (s *Service) Stop(ctx context.Context) error {
	s.runCancel()
	select {
	case <-s.workerDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// exampleFromRow converts a stored row to a bank example. A row whose vector
// length disagrees with its dimension is not usable.
func exampleFromRow(row *entities.NoiseExample) (Example, bool) {
	if row.Dim <= 0 || len(row.Vector) != row.Dim*bytesPerFloat32 {
		return Example{}, false
	}
	ex := Example{Space: row.EmbeddingSpace, Vector: decodeVector(row.Vector)}
	if row.DetectionID != nil {
		ex.DetectionID = *row.DetectionID
	}
	return ex, true
}

// encodeVector stores a vector as little-endian float32 values.
func encodeVector(v []float32) []byte {
	out := make([]byte, len(v)*bytesPerFloat32)
	for i, x := range v {
		binary.LittleEndian.PutUint32(out[i*bytesPerFloat32:], math.Float32bits(x))
	}
	return out
}

// decodeVector reads the values encodeVector wrote.
func decodeVector(b []byte) []float32 {
	out := make([]float32, len(b)/bytesPerFloat32)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*bytesPerFloat32:]))
	}
	return out
}

// checked is what the preflight learned about a detection.
type checked struct {
	detection *entities.Detection
	clipName  string
	reason    Reason // why the detection cannot be marked, or empty
}

// check decides whether a detection can be marked, from the database, the loaded
// model and the clip. It writes nothing and takes no lock. With probeClip false the
// clip is not opened. The returned error is ErrNotFound or a read failure; a
// refusal is in checked.reason.
func (s *Service) check(ctx context.Context, id uint, open OpenClipFunc, probeClip bool) (checked, error) {
	det, err := s.cfg.Detections.Get(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrDetectionNotFound) {
			return checked{}, ErrNotFound
		}
		return checked{}, fmt.Errorf("read detection: %w", err)
	}
	out := checked{detection: det}
	if det.ClipName != nil {
		out.clipName = *det.ClipName
	}

	supported, err := s.hasEmbeddingModel(ctx, det)
	if err != nil {
		return checked{}, err
	}
	if !supported {
		out.reason = ReasonModelUnsupported
		return out, nil
	}
	spec, ok := s.cfg.Embedder.ModelSpecFor(modelID)
	if !ok {
		out.reason = ReasonModelNotLoaded
		return out, nil
	}
	if out.clipName == "" || open == nil {
		out.reason = ReasonClipMissing
		return out, nil
	}
	if probeClip {
		if reason := s.probeClip(ctx, out.clipName, open, &spec); reason != "" {
			out.reason = reason
			return out, nil
		}
	}
	if det.ClipDenoised != nil && *det.ClipDenoised {
		out.reason = ReasonClipDenoised
	}
	return out, nil
}

// hasEmbeddingModel reports whether a model that has an embedding space produced
// hits for the detection: the bat model, from the detection's model contributions,
// or from the detection's own model when it has none.
func (s *Service) hasEmbeddingModel(ctx context.Context, det *entities.Detection) (bool, error) {
	batName := classifier.ModelRegistry[modelID].DetectionName
	contribs, err := s.cfg.Detections.GetModelContributionsByDetectionIDs(ctx, []uint{det.ID})
	if err != nil {
		return false, fmt.Errorf("read model contributions: %w", err)
	}
	if rows := contribs[det.ID]; len(rows) > 0 {
		for _, c := range rows {
			if c.HitCount > 0 && c.Model != nil && c.Model.Name == batName {
				return true, nil
			}
		}
		return false, nil
	}
	full, err := s.cfg.Detections.GetWithRelations(ctx, det.ID)
	if err != nil {
		if errors.Is(err, repository.ErrDetectionNotFound) {
			return false, ErrNotFound
		}
		return false, fmt.Errorf("read detection model: %w", err)
	}
	return full.Model != nil && full.Model.Name == batName, nil
}

// probeClip opens the clip, checks it can be decoded at a rate the model accepts
// and within the decoded size bound, and closes it.
func (s *Service) probeClip(ctx context.Context, clipName string, open OpenClipFunc, spec *classifier.ModelSpec) Reason {
	f, err := open(clipName)
	if err != nil {
		return ReasonClipMissing
	}
	defer func() { _ = f.Close() }()
	info, err := transcode.ProbeOpenFile(ctx, f, clipName)
	if err != nil || info.SampleRate < spec.MinRawSampleRate || info.DecodedBytes > transcode.MaxDecodedPCMBytes {
		return ReasonClipUnusable
	}
	return ""
}

// isMarked reports whether review is a false positive with the no animal reason.
func isMarked(review *entities.DetectionReview) bool {
	return review != nil &&
		review.Verified == entities.VerificationFalsePositive &&
		review.Reason != nil && *review.Reason == entities.ReviewReasonNoAnimal
}

// review reads the review of a detection; a detection without one has a nil
// review and no error.
func (s *Service) review(ctx context.Context, id uint) (*entities.DetectionReview, error) {
	review, err := s.cfg.Detections.GetReview(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrReviewNotFound) {
			return nil, nil //nolint:nilnil // a missing review is a normal state
		}
		return nil, fmt.Errorf("read review: %w", err)
	}
	return review, nil
}

// spaceCounts returns the examples attached to a detection, split into the
// current embedding space and all others.
func (s *Service) spaceCounts(ctx context.Context, id uint, space string) (active, inactive int, err error) {
	bySpace, err := s.cfg.Examples.CountForDetection(ctx, id)
	if err != nil {
		return 0, 0, fmt.Errorf("count noise examples: %w", err)
	}
	for name, n := range bySpace {
		if name == space {
			active += int(n)
		} else {
			inactive += int(n)
		}
	}
	return active, inactive, nil
}

// Mark makes the detection a noise example source: it writes the review
// (false_positive with the no animal reason) and queues a job that embeds the
// clip. It is one atomic call. A mark of a detection that is already marked and
// has examples in the current embedding space (in any space while no model is
// loaded), or whose job is in flight, changes nothing and returns the current
// status, whatever the state of its clip and the model. Otherwise the preflight
// outside the lock only reads and decides the refusals that need the clip and the
// model; inside the lock the detection, its review and its examples are read
// again, so a concurrent mark, verdict change or delete cannot be missed. A
// refused mark has written nothing.
//
// The error is a *RefusalError, ErrBusy, ErrNotFound or a failure to read or write.
func (s *Service) Mark(ctx context.Context, req MarkRequest) (Status, error) {
	// The model's lock is taken here, never under mu.
	space, known := s.cfg.Embedder.EmbeddingSpaceFor(modelID)
	if !known {
		space = ""
	}

	if st, done, err := s.markNoop(ctx, req.DetectionID, space, known); done || err != nil {
		return st, err
	}

	pre, err := s.check(ctx, req.DetectionID, req.OpenClip, true)
	if err != nil {
		return Status{}, err
	}
	if pre.reason != "" {
		return Status{}, &RefusalError{Reason: pre.reason}
	}
	if !known {
		// The model was not reporting a space before the preflight found it loaded.
		if space, known = s.cfg.Embedder.EmbeddingSpaceFor(modelID); !known {
			return Status{}, &RefusalError{Reason: ReasonModelNotLoaded}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if refusal := s.busyLocked(req.DetectionID); refusal != nil {
		return Status{}, refusal
	}
	_, review, attached, noop, err := s.markStateLocked(ctx, req.DetectionID, space, known)
	if err != nil {
		return Status{}, err
	}
	if noop {
		return s.statusLocked(ctx, req.DetectionID, review, space, pre)
	}
	if s.bankFullLocked(space, attached) {
		return Status{}, &RefusalError{Reason: ReasonBankFull}
	}
	if len(s.queue) >= cap(s.queue) {
		return Status{}, ErrBusy
	}

	reason := entities.ReviewReasonNoAnimal
	err = s.cfg.Detections.SaveReview(ctx, &entities.DetectionReview{
		DetectionID: req.DetectionID,
		Verified:    entities.VerificationFalsePositive,
		Reason:      &reason,
	})
	if err != nil {
		return Status{}, fmt.Errorf("save review: %w", err)
	}

	j := &job{detectionID: req.DetectionID, clipName: pre.clipName, open: req.OpenClip, phase: phaseQueued}
	s.jobs[req.DetectionID] = j
	s.queue <- j

	return s.statusLocked(ctx, req.DetectionID, &entities.DetectionReview{
		Verified: entities.VerificationFalsePositive, Reason: &reason,
	}, space, pre)
}

// busyLocked returns the refusal for a detection that cannot be marked now: the
// learned noise is being cleared, or a delete of the detection is in progress. The
// caller holds mu.
func (s *Service) busyLocked(id uint) *RefusalError {
	switch {
	case s.clearing:
		return &RefusalError{Reason: ReasonClearing}
	case s.guards[id] > 0:
		return &RefusalError{Reason: ReasonDetectionBeingDeleted}
	}
	return nil
}

// markStateLocked reads what decides a mark: that the detection exists, its review
// and the examples attached to it. noop is true when the detection is already
// marked with attached examples in the current space (in any space while the space
// is not known) or has a job in flight. The caller holds mu.
func (s *Service) markStateLocked(ctx context.Context, id uint, space string, known bool) (det *entities.Detection, review *entities.DetectionReview, attached int, noop bool, err error) {
	det, err = s.cfg.Detections.Get(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrDetectionNotFound) {
			return nil, nil, 0, false, ErrNotFound
		}
		return nil, nil, 0, false, fmt.Errorf("read detection: %w", err)
	}
	if review, err = s.review(ctx, id); err != nil {
		return nil, nil, 0, false, err
	}
	active, inactive, err := s.spaceCounts(ctx, id, space)
	if err != nil {
		return nil, nil, 0, false, err
	}
	attached = active
	if !known {
		attached = inactive
	}
	noop = s.jobs[id].active() || (isMarked(review) && attached > 0)
	return det, review, attached, noop, nil
}

// markNoop answers a repeat mark before any clip or model check: done is true with
// the current status when the detection is already marked or has a job in flight.
// It leaves a request that a clearing barrier or a delete guard would refuse to
// the caller's main path.
func (s *Service) markNoop(ctx context.Context, id uint, space string, known bool) (st Status, done bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busyLocked(id) != nil {
		return Status{}, false, nil
	}
	det, review, _, noop, err := s.markStateLocked(ctx, id, space, known)
	if err != nil || !noop {
		return Status{}, false, err
	}
	st, err = s.statusLocked(ctx, id, review, space, checked{detection: det})
	return st, true, err
}

// LeaveNoise records a verdict that takes the detection out of noise: it cancels
// the detection's job, deletes the examples attached to it and writes the review
// with the given status and no reason in one transaction, then removes the
// detection's examples from the bank. It works for any detection, marked or not,
// so every review save to a verdict other than noise can go through it. It
// runs detached from the caller's cancellation with its own timeout, so a client
// that disconnects after the request was accepted still gets the cleanup. On an
// error the database and the bank are untouched, but a job that was queued or
// running has already been cancelled and stores nothing: the detection stays
// marked, its job reports cancelled, and a later mark relearns. A finished job of
// the detection is forgotten, so its status stops saying learned.
func (s *Service) LeaveNoise(ctx context.Context, detectionID uint, verified entities.VerificationStatus) error {
	if verified != entities.VerificationCorrect && verified != entities.VerificationFalsePositive {
		return ErrInvalidVerdict
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), leaveTimeout)
	defer cancel()

	s.mu.Lock()
	defer s.mu.Unlock()
	if j := s.jobs[detectionID]; j.active() {
		j.cancelLocked()
	}
	if _, err := s.cfg.Examples.RemoveForVerdict(ctx, detectionID, verified); err != nil {
		return fmt.Errorf("leave noise: %w", err)
	}
	s.bank.Remove(detectionID)
	if j := s.jobs[detectionID]; j != nil && (j.phase == phaseLearned || j.phase == phaseFailed) {
		delete(s.jobs, detectionID)
	}
	return nil
}

// GuardDelete is called before a detection is deleted. It refuses (ok false) while
// a learning job for the detection is queued or running, because the delete also
// removes the clip the job reads. Otherwise it registers the delete, so a Mark for
// the detection is refused until release is called, and returns release. Call
// release after the whole delete, files included, on every path; it is safe to
// call more than once.
//
// The examples learned from the detection are not deleted with it: the database
// clears their link. When the detection is gone at release, the bank entries lose
// their owner too, so a later detection that gets the same ID cannot reach them.
func (s *Service) GuardDelete(detectionID uint) (release func(), ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.jobs[detectionID].active() {
		return nil, false
	}
	s.guards[detectionID]++
	var once sync.Once
	return func() {
		once.Do(func() { s.releaseDelete(detectionID) })
	}, true
}

// releaseDelete ends the delete guard of a detection and, when the detection no
// longer exists, detaches its bank entries.
func (s *Service) releaseDelete(detectionID uint) {
	ctx, cancel := context.WithTimeout(context.Background(), leaveTimeout)
	defer cancel()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.guards[detectionID]--
	if s.guards[detectionID] <= 0 {
		delete(s.guards, detectionID)
	}
	_, err := s.cfg.Detections.Get(ctx, detectionID)
	switch {
	case errors.Is(err, repository.ErrDetectionNotFound):
		s.bank.Detach(detectionID)
		delete(s.jobs, detectionID)
	case err != nil:
		// The stored link is cleared by the database either way; the next start
		// rebuilds the bank owners from it.
		s.log.Warn("noise examples: could not check a deleted detection",
			logger.Uint64("detection_id", uint64(detectionID)), logger.Error(err))
	}
}

// Status reports whether the detection can be marked and the state of learning.
// It is an error only when the detection does not exist or cannot be read. open
// is used to check the clip when no job is working on the detection.
func (s *Service) Status(ctx context.Context, detectionID uint, open OpenClipFunc) (Status, error) {
	s.mu.Lock()
	working := s.jobs[detectionID].active()
	s.mu.Unlock()

	pre, err := s.check(ctx, detectionID, open, !working)
	if err != nil {
		return Status{}, err
	}
	review, err := s.review(ctx, detectionID)
	if err != nil {
		return Status{}, err
	}
	space, _ := s.cfg.Embedder.EmbeddingSpaceFor(modelID)

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked(ctx, detectionID, review, space, pre)
}

// statusLocked builds the status of a detection; the caller holds mu. Examples
// come from the database, the job fields from the job table. While a job works on
// the detection it is available; otherwise the refusal found by the preflight
// applies, then the room left in the bank.
func (s *Service) statusLocked(ctx context.Context, id uint, review *entities.DetectionReview, space string, pre checked) (Status, error) {
	active, inactive, err := s.spaceCounts(ctx, id, space)
	if err != nil {
		return Status{}, err
	}
	st := Status{
		Available:        true,
		Marked:           isMarked(review),
		State:            StateIdle,
		Examples:         active,
		InactiveExamples: inactive,
		ClipDenoised:     pre.detection != nil && pre.detection.ClipDenoised != nil && *pre.detection.ClipDenoised,
	}
	j := s.jobs[id]
	if j != nil {
		st.State, st.FailureReason = j.reported()
		st.WindowsDone = int(j.done.Load())
		st.WindowsTotal = int(j.total.Load())
	}
	if j.active() {
		return st, nil
	}
	switch {
	case pre.reason != "":
		st.Available, st.UnavailableReason = false, pre.reason
	case space != "" && s.bankFullLocked(space, active):
		st.Available, st.UnavailableReason = false, ReasonBankFull
	}
	return st, nil
}

// bankFullLocked reports whether one more mark would take the space past its
// limit: the examples of the space that the mark would not replace (replaced is
// how many the detection already has there), plus the room every job in flight and
// the new one will fill. The caller holds mu.
func (s *Service) bankFullLocked(space string, replaced int) bool {
	return s.bank.Len(space)-replaced+(s.inFlightLocked()+1)*examplesPerClip > MaxExamplesPerSpace
}

// inFlightLocked counts the jobs that are queued or running and not cancelled.
// The caller holds mu. Each will add up to examplesPerClip examples when it
// commits, so the cap check counts them.
func (s *Service) inFlightLocked() int {
	n := 0
	for _, j := range s.jobs {
		if j.active() {
			n++
		}
	}
	return n
}
