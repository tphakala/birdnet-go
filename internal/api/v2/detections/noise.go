package detections

import (
	"context"
	"math"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/analysis/noisebank"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	// errorKeyNoiseLearningInProgress is the error key of a delete refused because a
	// learning job still reads the detection's clip.
	errorKeyNoiseLearningInProgress = "noise_learning_in_progress"

	// errorKeyNoiseQueueFull is the error key of a mark refused because every slot of
	// the learning queue is taken.
	errorKeyNoiseQueueFull = "noise_queue_full"

	// errorKeyNoiseClearTimeout is the error key of a clear that gave up waiting for
	// the learning worker. Nothing was deleted.
	errorKeyNoiseClearTimeout = "noise_clear_timeout"

	// clearNoiseTimeout bounds a clear of the learned noise. It runs detached from
	// the request, so the bound is its own.
	clearNoiseTimeout = 30 * time.Second

	// noiseRetryAfterSeconds is the Retry-After of a mark that can be retried shortly.
	noiseRetryAfterSeconds = "5"
)

// errNoiseLearning is the cause logged for a delete refused by the noise guard.
var errNoiseLearning = errors.NewStd("a noise learning job is working on the detection")

// errClipNotResolved is returned by the clip opener for a name that does not
// resolve inside the clip directory.
var errClipNotResolved = errors.NewStd("clip name does not resolve to a file in the clip directory")

// noiseService is what the detection handlers use of the noise example service.
// *noisebank.Service implements it.
type noiseService interface {
	Mark(ctx context.Context, req noisebank.MarkRequest) (noisebank.Status, error)
	LeaveNoise(ctx context.Context, detectionID uint, verified entities.VerificationStatus) error
	GuardDelete(detectionID uint) (release func(), ok bool)
	Status(ctx context.Context, detectionID uint, open noisebank.OpenClipFunc) (noisebank.Status, error)
	Counts(ctx context.Context) (noisebank.Counts, error)
	Clear(ctx context.Context) (int64, error)
}

// noiseExamples returns the noise example service, or nil when this install has
// none: the legacy database, a migration in progress, or a start whose examples
// could not be loaded. Everything that would touch noise examples checks for nil
// before it writes anything.
func (c *Handler) noiseExamples() noiseService {
	if c.noise != nil {
		return c.noise
	}
	if c.Processor == nil {
		return nil
	}
	// The explicit nil check keeps a nil *Service from becoming a non-nil interface.
	if svc := c.Processor.NoiseExamples(); svc != nil {
		return svc
	}
	return nil
}

// noiseUnavailableReason says why there is no noise example service: the install
// does not use the enhanced database, or it does and the service did not start.
func noiseUnavailableReason() noisebank.Reason {
	if !datastoreV2.IsEnhancedDatabase() {
		return noisebank.ReasonLegacyDatabase
	}
	return noisebank.ReasonNotReady
}

// noiseClipOpener returns the function the service uses to open a clip. Every open
// resolves the stored name inside the clip directory and goes through the sandboxed
// file access, so the service never sees a path.
func (c *Handler) noiseClipOpener() noisebank.OpenClipFunc {
	return func(clipName string) (*os.File, error) {
		rel, ok := c.resolveClipRel(clipName)
		if !ok {
			return nil, errClipNotResolved
		}
		return c.SFS.OpenRegularRel(rel)
	}
}

// respondNoiseUnavailable answers a request that needs the noise example service
// when there is none. Nothing has been written.
func (c *Handler) respondNoiseUnavailable(ctx echo.Context) error {
	reason := noiseUnavailableReason()
	return c.HandleErrorWithKey(ctx, errors.NewStd("noise examples are not available: "+string(reason)),
		"Noise examples are not available", http.StatusConflict, string(reason), nil)
}

// respondNoiseMarkError answers a failed Mark. A refusal is 409 with the reason as
// the error key, except a clearing barrier and a full queue, which are 503 with a
// Retry-After, and a range that holds no whole model window, which is 400. Nothing has been written by the request in any of these cases.
func (c *Handler) respondNoiseMarkError(ctx echo.Context, err error) error {
	if refusal, ok := errors.AsType[*noisebank.RefusalError](err); ok {
		if refusal.Reason == noisebank.ReasonClearing {
			return c.respondRetryLater(ctx, err, string(refusal.Reason))
		}
		if refusal.Reason == noisebank.ReasonRangeTooShort {
			// The request itself is wrong, whatever the state of the clip.
			return c.HandleErrorWithKey(ctx, err, "Invalid noise range", http.StatusBadRequest, string(refusal.Reason), nil)
		}
		return c.HandleErrorWithKey(ctx, err, "Detection cannot be marked as noise", http.StatusConflict, string(refusal.Reason), nil)
	}
	switch {
	case errors.Is(err, noisebank.ErrBusy):
		return c.respondRetryLater(ctx, err, errorKeyNoiseQueueFull)
	case errors.Is(err, noisebank.ErrNotFound):
		return c.HandleError(ctx, err, detectionNotFoundMessage, http.StatusNotFound)
	default:
		return c.HandleError(ctx, err, "Failed to mark detection as noise", http.StatusInternalServerError)
	}
}

// respondRetryLater answers 503 with a Retry-After header.
func (c *Handler) respondRetryLater(ctx echo.Context, err error, key string) error {
	ctx.Response().Header().Set(echo.HeaderRetryAfter, noiseRetryAfterSeconds)
	return c.HandleErrorWithKey(ctx, err, "Noise learning is busy, try again shortly", http.StatusServiceUnavailable, key, nil)
}

// reviewReasonError is the 400 cause of a review request whose reason does not fit
// its verdict.
type reviewReasonError struct{ message string }

func (e *reviewReasonError) Error() string { return e.message }

// validateReviewReason checks the reason of a review request against its verdict
// without writing anything. The reason is a pointer so absent, empty and given are
// three different requests: absent leaves a stored reason alone, empty clears it,
// no_animal marks. A reason needs a verdict, and no_animal needs false_positive.
func validateReviewReason(reason *string, verified string) error {
	if reason == nil {
		return nil
	}
	if verified == "" {
		return &reviewReasonError{message: "reason requires a verification status"}
	}
	switch *reason {
	case "":
		return nil
	case entities.ReviewReasonNoAnimal:
		if verified != VerificationStatusFalsePositive {
			return &reviewReasonError{message: "reason " + entities.ReviewReasonNoAnimal + " requires verified " + VerificationStatusFalsePositive}
		}
		return nil
	default:
		return &reviewReasonError{message: "unknown review reason: " + *reason}
	}
}

// GetDetectionNoise reports whether a detection can be marked as noise and how
// learning from it is going. It is never an error for a known detection: when the
// install has no noise example service the answer says so.
func (c *Handler) GetDetectionNoise(ctx echo.Context) error {
	idStr := ctx.Param("id")
	id, err := strconv.ParseUint(idStr, 10, detectionIDBits)
	if err != nil || id == 0 {
		return c.HandleError(ctx, err, invalidDetectionIDMessage, http.StatusBadRequest)
	}

	noise := c.noiseExamples()
	if noise == nil {
		if _, err := c.Repo.Get(ctx.Request().Context(), idStr); err != nil {
			// The v2 repository reports a missing detection with its sentinel, the
			// legacy bridge with a not-found category.
			if errors.Is(err, repository.ErrDetectionNotFound) {
				return c.HandleError(ctx, err, detectionNotFoundMessage, http.StatusNotFound)
			}
			return c.HandleErrorWithNotFound(ctx, err, detectionNotFoundMessage, "Failed to read detection")
		}
		return ctx.JSON(http.StatusOK, noisebank.Status{
			UnavailableReason: noiseUnavailableReason(),
			State:             noisebank.StateIdle,
		})
	}

	status, err := noise.Status(ctx.Request().Context(), uint(id), c.noiseClipOpener())
	if err != nil {
		if errors.Is(err, noisebank.ErrNotFound) {
			return c.HandleError(ctx, err, detectionNotFoundMessage, http.StatusNotFound)
		}
		return c.HandleError(ctx, err, "Failed to read noise learning status", http.StatusInternalServerError)
	}
	return ctx.JSON(http.StatusOK, status)
}

// noiseExamplesResponse is the answer of the bank wide count. The embedded counts
// carry "examples", "inactiveExamples" and, when the bat model is not loaded and
// the current embedding space cannot be told, "space": "unknown" (then every
// stored example is counted in "examples").
type noiseExamplesResponse struct {
	noisebank.Counts
	// Available is false when the install has no noise example service.
	Available bool `json:"available"`
	// UnavailableReason says why Available is false.
	UnavailableReason noisebank.Reason `json:"unavailableReason"`
	// Max is the most examples one embedding space may hold.
	Max int `json:"max"`
}

// GetNoiseExamples reports how many noise examples are stored, in the current
// embedding space and in others, and the limit per space. Like the status of a
// detection it is not an error when the install has no noise example service: the
// answer says so.
func (c *Handler) GetNoiseExamples(ctx echo.Context) error {
	noise := c.noiseExamples()
	if noise == nil {
		return ctx.JSON(http.StatusOK, noiseExamplesResponse{
			UnavailableReason: noiseUnavailableReason(),
			Max:               noisebank.MaxExamplesPerSpace,
		})
	}
	counts, err := noise.Counts(ctx.Request().Context())
	if err != nil {
		return c.HandleError(ctx, err, "Failed to count noise examples", http.StatusInternalServerError)
	}
	return ctx.JSON(http.StatusOK, noiseExamplesResponse{
		Counts:    counts,
		Available: true,
		Max:       noisebank.MaxExamplesPerSpace,
	})
}

// ClearNoiseExamples deletes every learned noise example of every embedding space.
// It touches no review. The clear runs detached from the request with its own
// timeout, so a client that disconnects does not leave it half way.
func (c *Handler) ClearNoiseExamples(ctx echo.Context) error {
	noise := c.noiseExamples()
	if noise == nil {
		return c.respondNoiseUnavailable(ctx)
	}
	clearCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx.Request().Context()), clearNoiseTimeout)
	defer cancel()
	deleted, err := noise.Clear(clearCtx)
	if err != nil {
		if refusal, ok := errors.AsType[*noisebank.RefusalError](err); ok {
			return c.respondRetryLater(ctx, err, string(refusal.Reason))
		}
		if errors.Is(err, noisebank.ErrClearTimeout) {
			return c.respondRetryLater(ctx, err, errorKeyNoiseClearTimeout)
		}
		return c.HandleError(ctx, err, "Failed to clear noise examples", http.StatusInternalServerError)
	}
	return ctx.JSON(http.StatusOK, map[string]int64{"deleted": deleted})
}

// validateNoiseRange checks the range of a noise mark without writing anything.
// A range only means something for a mark, so it needs the no_animal reason; its
// ends must be finite, the start not negative and the end after the start. Whether
// it is long enough for a model window and lies inside the clip is for the noise
// service to say, since it needs the model and the clip.
func validateNoiseRange(r *noisebank.ClipRange, reason *string) error {
	if r == nil {
		return nil
	}
	switch {
	case reason == nil || *reason != entities.ReviewReasonNoAnimal:
		return &reviewReasonError{message: "noise_range requires reason " + entities.ReviewReasonNoAnimal}
	case math.IsNaN(r.Start) || math.IsInf(r.Start, 0) || math.IsNaN(r.End) || math.IsInf(r.End, 0):
		return &reviewReasonError{message: "noise_range must hold finite numbers"}
	case r.Start < 0:
		return &reviewReasonError{message: "noise_range start must not be negative"}
	case r.End <= r.Start:
		return &reviewReasonError{message: "noise_range end must be after its start"}
	}
	return nil
}
