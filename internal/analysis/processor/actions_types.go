// processor/actions_types.go
// This file contains interfaces, types, and constants for processor actions.

package processor

import (
	"context"
	"io/fs"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/analysis/jobqueue"
	"github.com/tphakala/birdnet-go/internal/analysis/species"
	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/birdweather"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/mqtt"
	"github.com/tphakala/birdnet-go/internal/securefs"
)

// Timeout and interval constants
const (
	// MQTTPublishTimeout is the timeout for MQTT publish operations
	MQTTPublishTimeout = 10 * time.Second

	// CompositeActionTimeout is the default timeout for each action in a composite action.
	// After audio export was decoupled, CompositeAction only runs the Database save, the
	// step that enqueues MQTT, and SSE
	// which are fast operations even on slow hardware.
	CompositeActionTimeout = 10 * time.Second

	// ExecuteCommandTimeout is the timeout for external command execution
	ExecuteCommandTimeout = 5 * time.Minute

	// databaseSaveRetryWindow is how long a detection whose database save failed
	// before anything was stored keeps being retried. The retry count is derived
	// from it with the shared 1 s to 30 s backoff schedule; with queue jitter
	// (plus or minus 10 percent) and up to one step timeout per attempt the real
	// span is at least about this long.
	databaseSaveRetryWindow = 5 * time.Minute

	// databaseSaveRetryInitialDelay, databaseSaveRetryMaxDelay and
	// databaseSaveRetryMultiplier are the backoff of a database save retry. They
	// must match the schedule retriesForBackoffBudget assumes.
	databaseSaveRetryInitialDelay = saveAudioDeferredInitialDelay
	databaseSaveRetryMaxDelay     = saveAudioDeferredMaxDelay
	databaseSaveRetryMultiplier   = saveAudioDeferredMultiplier

	// speciesTimeQueryTimeout bounds the per-detection query for the first/last
	// species detection times published in the MQTT payload. It is short because
	// it runs on the MQTT hot path and a failure must degrade to null fields,
	// not stall the publish.
	speciesTimeQueryTimeout = 2 * time.Second
)

// DetectionContext provides thread-safe shared state for detection pipeline actions.
// This enables downstream actions (MQTT, SSE, SaveAudio) to access data set by
// upstream actions (Database) without polling, when used with CompositeAction for
// sequential execution. An action enqueued by a composite step shares it too.
//
// The context is created once per detection in getDefaultActions(). The steps of
// the database composite (Database, the MQTT enqueue step, SSE) share it and run
// in order; SaveAudioAction runs as its own job outside the composite and waits at
// most noteIDWaitTimeout for the database outcome.
type DetectionContext struct {
	// NoteID holds the ID a successful save reported (0 when none was). Write it
	// only through markDatabaseSaved, which stores it only when the saved verdict
	// settles dbOutcome, before publishing that verdict: SaveAudioAction wakes on
	// dbOutcome, not on NoteID, so a bare Store leaves it waiting
	// noteIDWaitTimeout. Read by MqttAction, SSEAction and SaveAudioAction.
	NoteID atomic.Uint64

	// ClipSaved reports that SaveAudioAction exported the clip and no verdict has
	// since claimed it for removal. SaveAudioAction sets it after a successful
	// export; it is cleared once a skipped or failed verdict claims the held clip
	// (unconfirmedClip), whether or not the file is then removed: a failed
	// removal, or a file another export put at the same name, is only logged.
	ClipSaved atomic.Bool

	// birdImage holds the species image resolved once per detection, so the
	// Database, SSE and MQTT actions of a single detection do not each
	// repeat the lookup. Three independent lookups per detection meant three
	// SQLite reads for a cold species, on a path whose whole purpose is to stay
	// off slow work. A stored empty BirdImage is a resolved verdict of "nothing
	// available", which is why it is a pointer: nil means nobody has looked yet.
	birdImage atomic.Pointer[imageprovider.BirdImage]

	// clipDenoise is what the export stage did to the clip: clipDenoiseUnset until
	// the clip is encoded, then clipDenoiseRaw or clipDenoiseDenoised. Together with
	// NoteID it lets whichever of SaveAudioAction and DatabaseAction finishes last
	// write the denoise marker: the clip can be encoded before its row exists.
	clipDenoise atomic.Int32

	// dbOutcome is DatabaseAction's verdict on the detection, one of the dbOutcome*
	// values, set once under dbOutcomeMu. SaveAudioAction waits for it at most
	// noteIDWaitTimeout; a skipped or failed verdict that lands after the export
	// claims the held clip for removal (unconfirmedClip, see exportedClip.remove
	// for when the file is kept). dbOutcomeCh is allocated under
	// dbOutcomeMu only when a waiter blocks while the verdict is pending, and is
	// closed when the verdict is set.
	dbOutcome   atomic.Int32
	dbOutcomeMu sync.Mutex
	dbOutcomeCh chan struct{}

	// unconfirmedClip is the clip SaveAudioAction exported while the verdict was
	// still pending; it stays set after a saved or unknown verdict, which never
	// claims it, and is nil when no clip was held or once one is taken. Whichever
	// of SaveAudioAction and DatabaseAction finishes last claims it for removal
	// when the verdict proves no row: SaveAudioAction stores the clip and then
	// reads the verdict, DatabaseAction stores the verdict and then takes the
	// clip, so at least one of them sees both, and the Swap lets only one of them
	// claim it. The claimed file can still stay on disk (see exportedClip.remove).
	unconfirmedClip atomic.Pointer[exportedClip]
}

// exportedClip is a clip SaveAudioAction wrote before the database verdict was
// known: what is needed to remove exactly that file later.
type exportedClip struct {
	exportDir     string      // export directory of the settings snapshot the clip was written with
	path          string      // the path the encoder wrote (after resolveExportParams)
	relPath       string      // path relative to exportDir (relativeClipPath form), for the log lines
	written       os.FileInfo // Stat taken right after the encode, identity already read (os.SameFile(fi, fi)), for os.SameFile
	correlationID string
}

// Values of DetectionContext.dbOutcome.
const (
	// dbOutcomePending: no verdict yet.
	dbOutcomePending int32 = iota
	// dbOutcomeSaved: the row is stored; NoteID holds its ID, or 0 when the
	// repository reported none.
	dbOutcomeSaved
	// dbOutcomeSkipped: the event tracker skipped the detection before any save,
	// so nothing was stored.
	dbOutcomeSkipped
	// dbOutcomeFailed: the save ended with an error proving nothing was stored
	// (isRepeatableSaveError), or the database job never ran (the queue dropped
	// it, or enqueueing it failed).
	dbOutcomeFailed
	// dbOutcomeUnknown: the save ended without that proof, an unmarked error or a
	// panic, so the row may exist.
	dbOutcomeUnknown
)

// setDatabaseOutcome moves dbOutcome out of pending once and wakes any waiter;
// later calls are ignored. A saved verdict stores noteID first, and only when
// it is the one that settles, so lock-free readers that load dbOutcome and then
// NoteID see the ID, and a losing call changes nothing.
func (d *DetectionContext) setDatabaseOutcome(outcome int32, noteID uint64) {
	if d == nil || outcome == dbOutcomePending {
		return
	}
	d.dbOutcomeMu.Lock()
	defer d.dbOutcomeMu.Unlock()
	if d.dbOutcome.Load() != dbOutcomePending {
		return
	}
	if outcome == dbOutcomeSaved {
		d.NoteID.Store(noteID)
	}
	d.dbOutcome.Store(outcome)
	if d.dbOutcomeCh != nil {
		close(d.dbOutcomeCh)
	}
}

// markDatabaseSaved settles the outcome as saved and publishes the stored row's ID.
func (d *DetectionContext) markDatabaseSaved(noteID uint64) {
	d.setDatabaseOutcome(dbOutcomeSaved, noteID)
}

// markDatabaseSkipped settles the outcome as skipped by the event tracker and
// claims a clip already exported for it for removal (removeClipWithoutRow).
func (d *DetectionContext) markDatabaseSkipped() {
	d.setDatabaseOutcome(dbOutcomeSkipped, 0)
	d.removeClipWithoutRow()
}

// markDatabaseFailed settles the outcome as a failure that stored nothing and
// claims a clip already exported for it for removal (removeClipWithoutRow).
func (d *DetectionContext) markDatabaseFailed() {
	d.setDatabaseOutcome(dbOutcomeFailed, 0)
	d.removeClipWithoutRow()
}

// markDatabaseUnknown settles the outcome as a failure that may have stored the row.
func (d *DetectionContext) markDatabaseUnknown() {
	d.setDatabaseOutcome(dbOutcomeUnknown, 0)
}

// databaseOutcome returns the current verdict, dbOutcomePending for a nil receiver.
func (d *DetectionContext) databaseOutcome() int32 {
	if d == nil {
		return dbOutcomePending
	}
	return d.dbOutcome.Load()
}

// verdictProvesNoRow reports whether the verdict proves no row will reference
// the clip (skipped or failed). Once true it stays true.
func (d *DetectionContext) verdictProvesNoRow() bool {
	switch d.databaseOutcome() {
	case dbOutcomeSkipped, dbOutcomeFailed:
		return true
	default:
		return false
	}
}

// verdictKeepsClip reports whether the clip stays whatever happens next: a nil
// receiver, or a saved or unknown verdict. Once true it stays true.
func (d *DetectionContext) verdictKeepsClip() bool {
	return d == nil || (d.databaseOutcome() != dbOutcomePending && !d.verdictProvesNoRow())
}

// holdUnconfirmedClip offers a clip exported before the verdict was known for
// removal, claims it at once when the verdict already proves no row, and
// reports whether the verdict proves no row. It reports the verdict, not which
// side claimed the clip or whether the file was removed: DatabaseAction can
// take the clip between the Store and this side's own call, and the caller
// must stop before its success log then.
// A nil receiver holds nothing and reports false.
func (d *DetectionContext) holdUnconfirmedClip(c *exportedClip) (noRow bool) {
	if d == nil {
		return false
	}
	d.unconfirmedClip.Store(c)
	d.removeClipWithoutRow()
	return d.verdictProvesNoRow()
}

// removeClipWithoutRow takes the held clip, at most once, when the verdict
// proves no row will reference it, and hands it to exportedClip.remove, which
// keeps the file when it was replaced or cannot be removed. It does file I/O,
// so it must not run under dbOutcomeMu.
func (d *DetectionContext) removeClipWithoutRow() {
	if d == nil || !d.verdictProvesNoRow() {
		return
	}
	c := d.unconfirmedClip.Swap(nil)
	if c == nil {
		return
	}
	d.ClipSaved.Store(false)
	c.remove()
}

// remove deletes the clip through SecureFS on its export directory when the path
// still holds the file this export wrote: clip names are not unique per
// detection, and another detection's later export renames a new file onto the
// same path. The identity check and the removal run under the path lock that
// SaveAudioAction holds while it writes a clip there (writeClipLocked), so no
// other SaveAudioAction export renames onto the path in between (the clip
// import writes historical recordings without it). The lock is only tried:
// when another export or removal holds the same lock stripe, the clip is kept
// rather than blocking the caller, which can be the pending-detection flush.
// Failures are logged; the clip is then left in place.
func (c *exportedClip) remove() {
	lock := clipPathLock(c.path)
	if !lock.TryLock() {
		GetLogger().Debug("Kept audio clip: another export or removal holds its path lock stripe",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", c.correlationID),
			logger.String("clip_path", c.relPath),
			logger.String("operation", "audio_export_remove_skipped"))
		return
	}
	defer lock.Unlock()
	cur, err := os.Stat(c.path)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		c.logRemoveFailure(err)
		return
	}
	if !os.SameFile(c.written, cur) {
		GetLogger().Debug("Kept audio clip: another export replaced it",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", c.correlationID),
			logger.String("clip_path", c.relPath),
			logger.String("operation", "audio_export_remove_skipped"))
		return
	}
	sfs, err := securefs.New(c.exportDir)
	if err != nil {
		c.logRemoveFailure(err)
		return
	}
	defer func() { _ = sfs.Close() }()
	if err := sfs.Remove(c.path); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			c.logRemoveFailure(err)
		}
		return
	}
	GetLogger().Info("Removed audio clip of a detection the database did not store",
		logger.String("component", "analysis.processor.actions"),
		logger.String("detection_id", c.correlationID),
		logger.String("clip_path", c.relPath),
		logger.String("operation", "audio_export_removed_no_row"))
}

// logRemoveFailure logs a failed removal of a clip without a row.
func (c *exportedClip) logRemoveFailure(err error) {
	GetLogger().Warn("Failed to remove audio clip of a detection the database did not store",
		logger.String("component", "analysis.processor.actions"),
		logger.String("detection_id", c.correlationID),
		logger.String("clip_path", c.relPath),
		logger.Error(err),
		logger.String("operation", "audio_export_remove_failed"))
}

// databaseOutcomeDone returns a channel that closes when dbOutcome leaves
// dbOutcomePending, or nil if the receiver is nil or the outcome is already settled.
func (d *DetectionContext) databaseOutcomeDone() <-chan struct{} {
	if d == nil || d.dbOutcome.Load() != dbOutcomePending {
		return nil
	}
	d.dbOutcomeMu.Lock()
	defer d.dbOutcomeMu.Unlock()
	if d.dbOutcome.Load() != dbOutcomePending {
		return nil
	}
	if d.dbOutcomeCh == nil {
		d.dbOutcomeCh = make(chan struct{})
	}
	return d.dbOutcomeCh
}

// Values of DetectionContext.clipDenoise.
const (
	clipDenoiseUnset int32 = iota
	clipDenoiseRaw
	clipDenoiseDenoised
)

// clipDenoiseRecordTimeout bounds one marker write. The write is detached from the
// caller's cancellation (see recordClipDenoise), so this is its only bound.
const clipDenoiseRecordTimeout = 5 * time.Second

// recordClipDenoise writes the denoise marker of the detection's clip once both
// the export state and the saved row's ID are known, and does nothing before that.
// SaveAudioAction stores the state and then calls it; DatabaseAction stores the
// note ID and then calls it. Each side stores its own value before it looks at the
// other's, so whatever the order at least one call sees both and writes, and when
// both do the second write repeats the first. It does nothing without a recorder
// (the legacy store) or a clip name.
//
// The write runs on a context detached from ctx: the database action's save can
// land after its job's last attempt ended, and the row exists by then. A failure is
// logged and never fails the calling action, because the clip and the detection are
// already stored.
func (d *DetectionContext) recordClipDenoise(ctx context.Context, recorder clipDenoiseRecorder, clipName string) {
	if recorder == nil || clipName == "" {
		return
	}
	state := d.clipDenoise.Load()
	noteID := uint(d.NoteID.Load())
	if state == clipDenoiseUnset || noteID == 0 {
		return
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), clipDenoiseRecordTimeout)
	defer cancel()
	if err := recorder.RecordClipDenoised(writeCtx, noteID, clipName, state == clipDenoiseDenoised); err != nil {
		GetLogger().Error("Failed to record whether the clip was denoised",
			logger.String("component", "analysis.processor.actions"),
			logger.Uint64("note_id", uint64(noteID)),
			logger.Error(err),
			logger.String("operation", "clip_denoised_record"))
	}
}

// StoreBirdImage records the image resolution for this detection, including the
// negative result. Callers must store even an empty image, since that is what
// tells the later actions the lookup has already happened. The value is copied,
// so the caller keeps ownership of what it passes.
func (d *DetectionContext) StoreBirdImage(img *imageprovider.BirdImage) {
	if d == nil || img == nil {
		return
	}
	stored := *img
	d.birdImage.Store(&stored)
}

// LoadBirdImage returns the image resolved earlier in this detection and whether
// a resolution has happened at all.
func (d *DetectionContext) LoadBirdImage() (img imageprovider.BirdImage, resolved bool) {
	if d == nil {
		return imageprovider.BirdImage{}, false
	}
	stored := d.birdImage.Load()
	if stored == nil {
		return imageprovider.BirdImage{}, false
	}
	return *stored, true
}

// Action is the base interface for all actions that can be executed.
// The context parameter allows for cancellation and timeout propagation.
type Action interface {
	Execute(ctx context.Context, data any) error
	GetDescription() string
}

// ContextAction is deprecated but kept for backward compatibility.
// Action now includes context directly.
type ContextAction interface {
	Action
	ExecuteContext(ctx context.Context, data any) error
}

type LogAction struct {
	Settings      *conf.Settings
	Result        detection.Result // Domain model (single source of truth)
	EventTracker  *EventTracker
	Description   string
	CorrelationID string     // Detection correlation ID for log tracking
	mu            sync.Mutex // Protect concurrent access to Result
}

type DatabaseAction struct {
	Settings          *conf.Settings
	Ds                datastore.Interface           // Legacy - to be removed after migration
	Repo              datastore.DetectionRepository // New - preferred for database operations
	clipMarks         clipDenoiseRecorder           // Writes the clip denoise marker; nil on the legacy store
	Result            detection.Result              // Domain model (single source of truth)
	Results           []detection.AdditionalResult  // All finite alternates; storedAlternates trims them at save
	EventTracker      *EventTracker
	NewSpeciesTracker *species.SpeciesTracker // Add reference to new species tracker
	processor         *Processor              // Add reference to processor for source name resolution
	DetectionCtx      *DetectionContext       // Shared context for downstream actions (MQTT, SSE, SaveAudio)
	Description       string
	CorrelationID     string // Detection correlation ID for log tracking

	// Retry state. The zero value is ready to use; see ExecuteContext.
	saveSlotOnce sync.Once
	saveSlot     chan struct{} // one-slot semaphore: only one save attempt runs at a time
	saveState    atomic.Int32  // saveStatePending, saveStateDone, saveStateTerminal or saveStateSkipped
	attempts     atomic.Int32  // ExecuteContext calls, one per job attempt
	maxAttempts  atomic.Int32  // job attempts allowed; 0 means retries are not configured
	windowEnded  atomic.Bool   // the last job attempt ended while a save was still in flight
	// Fields below are read and written only while holding saveSlot.
	gated              bool                  // the event-tracker and new-species decisions are taken
	isNewSpecies       bool                  // memoized new-species verdict of the gate
	daysSinceFirstSeen int                   // memoized with isNewSpecies
	novelty            species.NoveltyStatus // memoized with isNewSpecies
	terminalErr        error                 // the failure reported by every call once saveStateTerminal
}

type SaveAudioAction struct {
	Settings *conf.Settings
	ClipName string
	pcmData  []byte // Pre-read PCM data (set by buildSaveAudioAction)
	// Deferred-read fields. These are populated instead of pcmData when the
	// requested capture range ends in the future (e.g. Extended Capture is
	// still writing the tail of the clip). Execute() reads from the capture
	// buffer once readyAt has passed, and the job queue retries the action
	// in the meantime.
	bufferMgr        *buffer.Manager
	sourceID         string
	beginTime        time.Time
	duration         int
	readyAt          time.Time
	sourceSampleRate int    // Actual source capture rate for correct export headers
	modelName        string // Detection model name (e.g. "BattyBirdNET") for export strategy
	modelVersion     string // Detection model version, paired with modelName for ResolveModelType so the
	// encode-side model-type check matches the clip-name path exactly (no name/version skew).
	// species is the detection's common name, LOWERCASED to match the casing the
	// sibling detection operations log, carried so the export log lines can name
	// it. GET /api/v2/system/events/detections attributes recorded clip paths to a
	// species by reading this field back out of the audio_export_success line (see
	// that case in internal/api/v2/system/events_aggregation.go), keyed by an
	// exact string match. Without it every bucket the endpoint returns has an
	// empty ClipPaths; with the wrong casing the paths land on a phantom
	// zero-count species row instead of the counted one.
	species string
	// clipTags is the safe, display-ready detection metadata embedded as FLAC
	// tags; see clip_tags.go for what may and may not be carried.
	clipTags clipTagMeta
	// clipRates is the detection repository the corrected clip sample rate is
	// written through after the encode; nil only for a processor built without a
	// repository (tests).
	clipRates clipSampleRateRecorder
	// clipMarks is where the denoise marker of the clip is written; nil on the
	// legacy store, which has no such column.
	clipMarks clipDenoiseRecorder
	// denoiseAttempted is set before the export-stage denoise runs, so a retried
	// export (Extended Capture defers and retries; a.pcmData survives between
	// attempts) never denoises the same PCM twice, whether the first attempt
	// succeeded, failed or timed out.
	denoiseAttempted bool
	// denoisePreset is the preset a successful export-stage denoise used, for the
	// success log; empty when the clip was written as captured.
	denoisePreset string
	// denoiseTimeout overrides exportDenoiseTimeout when positive. Test seam.
	denoiseTimeout time.Duration
	// denoise and encode are test seams; nil selects transcode.DenoisePCM16 and
	// runEncoder. They let a test see exactly what PCM each stage receives.
	denoise pcmDenoiser
	encode  clipEncoderFunc
	// plannedRate is the clip sample rate stored with the detection at save time
	// (plannedClipSampleRate); the action corrects it when the encode wrote another.
	plannedRate   int
	NoteID        uint              // Note ID for correlation logging with pre-renderer
	PreRenderer   PreRendererSubmit // Injected from processor
	DetectionCtx  *DetectionContext // Shared with DatabaseAction: the export reads its outcome and NoteID, and sets ClipSaved.
	EventTracker  *EventTracker
	Description   string
	CorrelationID string // Detection correlation ID for log tracking
}

// PreRenderJob represents a spectrogram pre-rendering task.
// This is a local DTO to avoid direct coupling to spectrogram package types.
type PreRenderJob struct {
	PCMData    []byte    // Raw PCM data from memory (s16le, mono)
	SampleRate int       // PCM sample rate in Hz
	ClipPath   string    // Full absolute path to audio clip file
	NoteID     uint      // For logging correlation
	Timestamp  time.Time // Job submission time
	ModelType  string    // AI model type ("bird", "bat", "multi") for frequency profile selection
}

// Methods to expose fields (allows prerenderer to access without importing processor)
func (j *PreRenderJob) GetPCMData() []byte      { return j.PCMData }
func (j *PreRenderJob) GetSampleRate() int      { return j.SampleRate }
func (j *PreRenderJob) GetClipPath() string     { return j.ClipPath }
func (j *PreRenderJob) GetNoteID() uint         { return j.NoteID }
func (j *PreRenderJob) GetTimestamp() time.Time { return j.Timestamp }
func (j *PreRenderJob) GetModelType() string    { return j.ModelType }

// PreRendererSubmit is an interface for submitting pre-render jobs.
// Callers create PreRenderJob instances, and the implementation adapts them
// to spectrogram-specific types at the boundary.
type PreRendererSubmit interface {
	Submit(job interface {
		GetPCMData() []byte
		GetSampleRate() int
		GetClipPath() string
		GetNoteID() uint
		GetTimestamp() time.Time
		GetModelType() string
	}) error
	Stop() // Graceful shutdown
}

type BirdWeatherAction struct {
	// Settings is the snapshot captured when the action was built; used only
	// when liveSettings is nil or returns nil.
	Settings *conf.Settings
	Result   detection.Result // Domain model (single source of truth)
	pcmData  []byte
	// BwClient is the client captured when the action was built; used only when
	// liveClient is nil.
	BwClient     *birdweather.BwClient
	EventTracker *EventTracker
	// RetryConfig is fixed for the life of the job; a settings change applies
	// to detections made after it.
	RetryConfig jobqueue.RetryConfig
	// liveSettings returns the settings snapshot in effect when an attempt runs,
	// so a queued retry follows settings changes; nil uses Settings.
	liveSettings func() *conf.Settings
	// liveClient returns the BirdWeather client in effect when an attempt runs,
	// so a retry after a reconfigure uses the new client; nil uses BwClient.
	liveClient    func() *birdweather.BwClient
	Description   string
	CorrelationID string            // Detection correlation ID for log tracking
	mu            sync.Mutex        // Protect concurrent access to Result and pcmData
	rateLimit     rateLimitDecision // Event-tracker verdict of the first attempt, reused by queue retries
}

// speciesDetectionTimeSource is the minimal datastore capability MqttAction needs to
// populate first/last species detection times. Both *datastore.DataStore (legacy) and
// *v2only.Datastore satisfy it. Kept out of datastore.Interface on purpose (matches the
// convention used by other per-species analytics queries).
type speciesDetectionTimeSource interface {
	GetSpeciesFirstAndLastDetectionTimeBefore(ctx context.Context, scientificName string, before time.Time) (first, last *time.Time, err error)
}

// clipSampleRateRecorder is the single detection repository method SaveAudioAction
// needs to correct the stored clip sample rate after the encode.
type clipSampleRateRecorder interface {
	RecordClipSampleRate(ctx context.Context, id uint, clipName string, rate int) error
}

// clipDenoiseRecorder is the single v2 detection repository method the export and
// database actions need to record whether a clip was denoised.
type clipDenoiseRecorder interface {
	RecordClipDenoised(ctx context.Context, id uint, clipName string, denoised bool) error
}

// rateLimitDecision caches the event-tracker verdict of an action's first
// attempt. EventTracker stamps the species at check time, so a queue retry that
// asked again would either be suppressed inside the species interval or re-stamp
// the tracker and suppress a later detection. Retries reuse the first verdict
// instead. Callers must hold the owning action's mutex.
type rateLimitDecision struct {
	checked bool
	allowed bool
}

// allow runs check on the first call and returns that verdict on every later call.
func (d *rateLimitDecision) allow(check func() bool) bool {
	if !d.checked {
		d.allowed = check()
		d.checked = true
	}
	return d.allowed
}

type MqttAction struct {
	// Settings is the snapshot captured when the action was built; used only
	// when liveSettings is nil or returns nil.
	Settings       *conf.Settings
	Result         detection.Result // Domain model (single source of truth)
	BirdImageCache *imageprovider.BirdImageCache
	// MqttClient is the client captured when the action was built; used only
	// when liveClient is nil.
	MqttClient   mqtt.Client
	EventTracker *EventTracker
	DetectionCtx *DetectionContext // Shared context from DatabaseAction
	// RetryConfig is fixed for the life of the job; a settings change applies
	// to detections made after it.
	RetryConfig jobqueue.RetryConfig
	// liveSettings returns the settings snapshot in effect when an attempt runs,
	// so a queued retry follows settings changes; nil uses Settings.
	liveSettings func() *conf.Settings
	// liveClient returns the MQTT client in effect when an attempt runs, so a
	// retry after a reconfigure uses the new client; nil uses MqttClient.
	liveClient    func() mqtt.Client
	Description   string
	CorrelationID string            // Detection correlation ID for log tracking
	mu            sync.Mutex        // Protect concurrent access to Result
	rateLimit     rateLimitDecision // Event-tracker verdict of the first attempt, reused by queue retries
	// gatedByDatabase is set when the action runs as a step of the database
	// composite. The composite runs it only for a detection the database step
	// stored, and that step's event-tracker verdict is the detection's only one, so
	// the action does not ask its own.
	gatedByDatabase bool
	// SpeciesTimeSource provides the species' first-ever and most-recent previous
	// detection times for the MQTT payload. Nil when the database is disabled or
	// the backend does not support the query; the payload then carries null fields.
	SpeciesTimeSource speciesDetectionTimeSource
}

// attemptSettings returns the settings snapshot for one attempt: the live
// snapshot when a resolver is set and returns one, else the captured Settings.
func (a *MqttAction) attemptSettings() *conf.Settings {
	if a.liveSettings != nil {
		if s := a.liveSettings(); s != nil {
			return s
		}
	}
	return a.Settings
}

// attemptClient returns the MQTT client for one attempt. A set resolver is
// authoritative even when it returns nil (the client was removed): falling back
// to the captured client would publish on a disconnected one.
func (a *MqttAction) attemptClient() mqtt.Client {
	if a.liveClient != nil {
		return a.liveClient()
	}
	return a.MqttClient
}

// attemptSettings returns the settings snapshot for one attempt: the live
// snapshot when a resolver is set and returns one, else the captured Settings.
func (a *BirdWeatherAction) attemptSettings() *conf.Settings {
	if a.liveSettings != nil {
		if s := a.liveSettings(); s != nil {
			return s
		}
	}
	return a.Settings
}

// attemptClient returns the BirdWeather client for one attempt. A set resolver
// is authoritative even when it returns nil (the client was removed).
func (a *BirdWeatherAction) attemptClient() *birdweather.BwClient {
	if a.liveClient != nil {
		return a.liveClient()
	}
	return a.BwClient
}

type UpdateRangeFilterAction struct {
	Bn          *classifier.Orchestrator
	Settings    *conf.Settings
	Description string
	mu          sync.Mutex // Protect concurrent access to Settings
}

type SSEAction struct {
	Settings       *conf.Settings
	Result         detection.Result // Domain model (single source of truth)
	BirdImageCache *imageprovider.BirdImageCache
	EventTracker   *EventTracker
	DetectionCtx   *DetectionContext // Shared context from DatabaseAction (provides database ID)
	Description    string
	CorrelationID  string     // Detection correlation ID for log tracking
	mu             sync.Mutex // Protect concurrent access to Result
	// gatedByDatabase is set when the action runs as a step of the database
	// composite. The composite runs it only for a detection the database step
	// stored, and that step's event-tracker verdict is the detection's only one, so
	// the action does not ask its own.
	gatedByDatabase bool
	// SSEBroadcaster is a function that broadcasts detection data
	// This allows the action to be independent of the specific API implementation
	SSEBroadcaster func(note *datastore.Note, birdImage *imageprovider.BirdImage) error
}

// CompositeAction executes multiple actions sequentially, ensuring proper dependency management.
//
// This action type was introduced to fix a critical race condition between DatabaseAction
// and SSEAction (GitHub issue #1158). The SSEAction depends on DatabaseAction completing
// first to ensure database IDs are assigned before SSE broadcasts occur.
//
// Key Features:
//   - Sequential execution: Actions execute in order, each waiting for the previous to complete
//   - Configurable timeout: Per-action timeout can be overridden (default: CompositeActionTimeout, 10 seconds)
//   - Context support: Actions implementing ContextAction get proper context propagation
//   - Panic recovery: Panics in individual actions are caught and converted to errors
//   - Thread-safe: Mutex protects the Actions slice during access
//   - Nil-safe: Handles nil actions and empty action lists gracefully
//
// Usage:
//
//	timeout := 45 * time.Second
//	composite := &CompositeAction{
//	    Actions: []Action{databaseAction, sseAction},
//	    Description: "Save to database then broadcast",
//	    Timeout: &timeout,  // Optional: override default timeout
//	}
//	err := composite.Execute(data)
//
// This pattern ensures that dependent actions execute in the correct order, preventing
// timeout errors like "database ID not assigned after 10s" that occur when actions
// execute concurrently on resource-constrained hardware.
type CompositeAction struct {
	Actions       []Action       // Actions to execute in sequence
	Description   string         // Human-readable description
	Timeout       *time.Duration // Optional: per-action timeout override (nil = use default)
	CorrelationID string         // Detection correlation ID for log tracking
	// RetryConfig is the queue retry policy of the composite job. The zero value
	// disables retries. The database composite sets one that retries only while
	// its database step reports a save that stored nothing.
	RetryConfig jobqueue.RetryConfig
	mu          sync.Mutex // Protects concurrent access to Actions
}

// GetDescription returns a human-readable description of the LogAction
func (a *LogAction) GetDescription() string {
	if a.Description != "" {
		return a.Description
	}
	return "Log bird detection to file"
}

// GetDescription returns a human-readable description of the DatabaseAction
func (a *DatabaseAction) GetDescription() string {
	if a.Description != "" {
		return a.Description
	}
	return "Save bird detection to database"
}

// GetDescription returns a human-readable description of the SaveAudioAction
func (a *SaveAudioAction) GetDescription() string {
	if a.Description != "" {
		return a.Description
	}
	return "Save audio clip to file"
}

// GetDescription returns a human-readable description of the BirdWeatherAction
func (a *BirdWeatherAction) GetDescription() string {
	if a.Description != "" {
		return a.Description
	}
	return "Upload detection to BirdWeather"
}

// GetDescription returns a human-readable description of the MqttAction
func (a *MqttAction) GetDescription() string {
	if a.Description != "" {
		return a.Description
	}
	return "Publish detection to MQTT"
}

// GetDescription returns a human-readable description of the UpdateRangeFilterAction
func (a *UpdateRangeFilterAction) GetDescription() string {
	if a.Description != "" {
		return a.Description
	}
	return "Update BirdNET range filter"
}

// GetDescription returns a human-readable description of the SSEAction
func (a *SSEAction) GetDescription() string {
	if a.Description != "" {
		return a.Description
	}
	return "Broadcast detection via Server-Sent Events"
}

// GetDescription returns a human-readable description of the CompositeAction
func (a *CompositeAction) GetDescription() string {
	if a.Description != "" {
		return a.Description
	}
	return "Composite action (sequential execution)"
}
