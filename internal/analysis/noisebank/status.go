package noisebank

import (
	"github.com/tphakala/birdnet-go/internal/errors"
)

// Reason is a machine readable key. It names why a mark is refused or the noise
// feature is unavailable for a detection, and why a learning job failed. The
// values are part of the API.
type Reason string

// Reasons a detection cannot be marked as noise.
const (
	// ReasonLegacyDatabase means the install does not use the enhanced database,
	// which is where noise examples are stored.
	ReasonLegacyDatabase Reason = "legacy_database"
	// ReasonNotReady means the service is not available in this run, for example
	// because its examples could not be loaded at startup.
	ReasonNotReady Reason = "not_ready"
	// ReasonModelUnsupported means none of the models that detected the clip has an
	// embedding to learn from.
	ReasonModelUnsupported Reason = "model_unsupported"
	// ReasonModelNotLoaded means the model that embeds the clip is not loaded.
	ReasonModelNotLoaded Reason = "model_not_loaded"
	// ReasonClipMissing means the detection has no clip or the clip cannot be opened.
	ReasonClipMissing Reason = "clip_missing"
	// ReasonClipUnusable means the clip cannot be decoded, is sampled too low for the
	// model, or is too long to hold in memory.
	ReasonClipUnusable Reason = "clip_unusable"
	// ReasonClipDenoised means the clip was denoised when it was saved, so its
	// embeddings do not describe what the model hears live.
	ReasonClipDenoised Reason = "clip_denoised"
	// ReasonBankFull means the examples of the embedding space are at their limit.
	ReasonBankFull Reason = "bank_full"
	// ReasonClearing means the learned noise is being cleared; retry shortly.
	ReasonClearing Reason = "clearing"
	// ReasonDetectionBeingDeleted means a delete of the detection is in progress.
	ReasonDetectionBeingDeleted Reason = "detection_being_deleted"
)

// Reasons a learning job fails after it was accepted.
const (
	// ReasonDecodeFailed means the clip could not be decoded when the job ran.
	ReasonDecodeFailed Reason = "decode_failed"
	// ReasonInferenceFailed means the model failed on a window or the job panicked.
	ReasonInferenceFailed Reason = "inference_failed"
	// ReasonNoCandidates means no window of the clip scored high enough to learn.
	ReasonNoCandidates Reason = "no_candidates"
	// ReasonModelChanged means the model went away, changed its embedding space or
	// returned no embedding while the job ran.
	ReasonModelChanged Reason = "model_changed"
	// ReasonTimeout means the job ran past its time limit.
	ReasonTimeout Reason = "timeout"
	// ReasonCancelled means the verdict changed or the service stopped before the
	// job stored anything.
	ReasonCancelled Reason = "cancelled"
	// ReasonStoreFailed means the examples could not be written.
	ReasonStoreFailed Reason = "store_failed"
)

// State is the progress of the learning job of a detection.
type State string

// Job states. A job that was cancelled or failed is StateFailed with the reason
// in Status.FailureReason.
const (
	// StateIdle means no job is known for the detection in this run.
	StateIdle State = "idle"
	// StateQueued means a job waits for the worker.
	StateQueued State = "queued"
	// StateRunning means the worker is embedding the clip.
	StateRunning State = "running"
	// StateLearned means the job stored its examples.
	StateLearned State = "learned"
	// StateFailed means the job ended without storing examples.
	StateFailed State = "failed"
)

// Status reports whether a detection can be marked as noise and how learning from
// it is going. Examples and InactiveExamples come from the database, so they stay
// right after a restart; the job fields describe the job of this run.
type Status struct {
	// Available is true when a mark would be accepted now, or when a job is already
	// working on the detection.
	Available bool `json:"available"`
	// UnavailableReason says why Available is false.
	UnavailableReason Reason `json:"unavailableReason"`
	// Marked is true when the stored review carries the no animal reason.
	Marked bool `json:"marked"`
	// State is the state of the detection's job.
	State State `json:"state"`
	// Examples counts the examples attached to the detection in the current
	// embedding space; they are the ones the gate uses.
	Examples int `json:"examples"`
	// InactiveExamples counts the attached examples of other embedding spaces. They
	// are kept but never matched.
	InactiveExamples int `json:"inactiveExamples"`
	// WindowsDone and WindowsTotal show the progress of a running job.
	WindowsDone  int `json:"windowsDone"`
	WindowsTotal int `json:"windowsTotal"`
	// FailureReason says why the job of a failed state failed.
	FailureReason Reason `json:"failureReason"`
	// ClipDenoised is true when the clip was denoised when it was saved.
	ClipDenoised bool `json:"clipDenoised"`
}

// RefusalError is returned by Mark when the request is refused. Nothing has been
// written when it is returned.
type RefusalError struct {
	// Reason is the key of the refusal.
	Reason Reason
}

// refusalText is the message of each refusal.
var refusalText = map[Reason]string{
	ReasonModelUnsupported:      "no model that detected this clip has an embedding to learn from",
	ReasonModelNotLoaded:        "the model that embeds this clip is not loaded",
	ReasonClipMissing:           "the detection has no clip that can be opened",
	ReasonClipUnusable:          "the clip cannot be decoded for learning",
	ReasonClipDenoised:          "the clip was denoised when it was saved",
	ReasonBankFull:              "the limit of learned noise examples is reached; clear the learned noise before marking more",
	ReasonClearing:              "the learned noise is being cleared",
	ReasonDetectionBeingDeleted: "the detection is being deleted",
}

// Error implements error.
func (e *RefusalError) Error() string {
	if text, ok := refusalText[e.Reason]; ok {
		return text
	}
	return "noise example mark refused: " + string(e.Reason)
}

var (
	// ErrBusy is returned by Mark when every slot of the learning queue is taken.
	ErrBusy = errors.NewStd("noise example learning queue is full")
	// ErrNotFound is returned when the detection does not exist.
	ErrNotFound = errors.NewStd("detection not found")
	// ErrInvalidVerdict is returned by LeaveNoise for a verdict that is not a
	// known review status.
	ErrInvalidVerdict = errors.NewStd("invalid review verdict")
)
