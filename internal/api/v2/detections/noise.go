package detections

import (
	"context"
	"net/http"
	"os"
	"strconv"

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
// Retry-After. Nothing has been written by the request in any of these cases.
func (c *Handler) respondNoiseMarkError(ctx echo.Context, err error) error {
	if refusal, ok := errors.AsType[*noisebank.RefusalError](err); ok {
		if refusal.Reason == noisebank.ReasonClearing {
			return c.respondRetryLater(ctx, err, string(refusal.Reason))
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
