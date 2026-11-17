package v2only

import (
	"context"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// detectionToResult maps a v2 Detection to a detection.Result. Timestamp is the stored
// detected_at instant in the station zone, never parsed back from formatted Date and
// Time text, so both occurrences of a repeated DST hour keep their own offset. The
// other fields match what the datastore.Interface bridge returns for the same row,
// except ClipDenoised and OriginalSpecies, which only this mapping reads (the legacy
// note has no such fields). It does not set the review and lock times, which only Get
// sets (see setReviewAndLockTimes). det must not be nil.
func (ds *Datastore) detectionToResult(ctx context.Context, det *entities.Detection) *detection.Result {
	f := ds.decodeDetection(ctx, det)

	result := &detection.Result{
		ID:        det.ID,
		Timestamp: f.instant,
		AudioSource: detection.AudioSource{
			ID:          f.sourceURI,
			DisplayName: f.sourceDisplayName,
			SafeString:  f.sourceURI,
			Type:        detection.DetermineSourceType(f.sourceURI),
		},
		BeginTime: f.beginTime,
		EndTime:   f.endTime,
		Species: detection.Species{
			ScientificName: f.scientificName,
			CommonName:     f.commonName,
			Code:           f.speciesCode,
		},
		OriginalSpecies:     f.originalSpecies,
		Confidence:          det.Confidence,
		Latitude:            f.latitude,
		Longitude:           f.longitude,
		ClipName:            f.clipName,
		SpectrogramClipName: f.spectrogramClipName,
		ClipSampleRate:      clipSampleRateValue(det.ClipSampleRate),
		ClipDenoised:        det.ClipDenoised != nil && *det.ClipDenoised,
		ProcessingTime:      f.processingTime,
		Unlikely:            det.Unlikely,
		Verified:            f.verified,
		Locked:              f.locked,
	}

	// Use the stored model when it has a name, otherwise the default, as the bridge does.
	if f.model.Name != "" {
		result.Model = f.model
	} else {
		result.Model = detection.DefaultModelInfo()
	}

	if len(det.Comments) > 0 {
		result.Comments = make([]detection.Comment, len(det.Comments))
		for i, c := range det.Comments {
			result.Comments[i] = detection.Comment{
				ID:        c.ID,
				Entry:     c.Entry,
				CreatedAt: c.CreatedAt,
				UpdatedAt: c.UpdatedAt,
			}
		}
	}

	return result
}

// detectionsToResults maps detections to results. It returns a non-nil slice on success.
// It returns ctx.Err() when ctx ended while the relations were loaded or the rows
// mapped, because those lookups skip their own errors and would leave species unresolved.
func (ds *Datastore) detectionsToResults(ctx context.Context, dets []*entities.Detection) ([]*detection.Result, error) {
	results := make([]*detection.Result, 0, len(dets))
	for _, det := range dets {
		results = append(results, ds.detectionToResult(ctx, det))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// setReviewAndLockTimes copies the review and lock times of det onto result, in the
// station zone. det must come from getDetection, which loads the full review and lock
// rows; a list read's detection has a lock without its time, which is why
// detectionToResult does not do this. A review saved once is stored with created_at
// slightly after updated_at (the two columns are stamped by two clock readings), so the
// last time is never reported before the first.
func (ds *Datastore) setReviewAndLockTimes(result *detection.Result, det *entities.Detection) {
	if det.Review != nil {
		result.FirstReviewedAt = det.Review.CreatedAt.In(ds.timezone)
		result.LastReviewedAt = det.Review.UpdatedAt.In(ds.timezone)
		if result.LastReviewedAt.Before(result.FirstReviewedAt) {
			result.LastReviewedAt = result.FirstReviewedAt
		}
	}
	if det.Lock != nil {
		result.LockedAt = det.Lock.LockedAt.In(ds.timezone)
	}
}
