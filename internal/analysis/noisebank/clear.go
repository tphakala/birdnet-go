package noisebank

import (
	"context"
	"fmt"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// SpaceUnknown is the Space of Counts when the model that defines the current
// embedding space is not loaded.
const SpaceUnknown = "unknown"

// Counts is the size of the bank in the database.
type Counts struct {
	// Examples counts the stored examples of the current embedding space, which
	// are the ones the gate uses. When the space is unknown it counts every row.
	Examples int `json:"examples"`
	// InactiveExamples counts the stored examples of other embedding spaces. They
	// are kept but never matched.
	InactiveExamples int `json:"inactiveExamples"`
	// Space is SpaceUnknown when the current space cannot be told, and empty
	// otherwise.
	Space string `json:"space,omitempty"`
}

// Counts returns how many examples the database holds, split by whether the
// gate can use them.
func (s *Service) Counts(ctx context.Context) (Counts, error) {
	bySpace, err := s.cfg.Examples.CountBySpace(ctx)
	if err != nil {
		return Counts{}, fmt.Errorf("count noise examples: %w", err)
	}
	space, known := s.cfg.Embedder.EmbeddingSpaceFor(modelID)
	var counts Counts
	if !known {
		counts.Space = SpaceUnknown
	}
	for name, n := range bySpace {
		if !known || name == space {
			counts.Examples += int(n)
		} else {
			counts.InactiveExamples += int(n)
		}
	}
	return counts, nil
}

// Clear deletes every learned example of every embedding space and empties the
// bank. It touches no review: a detection that is still marked as noise keeps its
// mark and shows no examples. It returns how many rows it deleted.
//
// Only one Clear runs at a time; one that finds another running returns a
// *RefusalError with ReasonClearing and changes nothing. It works in three steps
// so that it never waits for the worker while holding the mutex, which the worker
// needs to finish a job:
//
//  1. Under the mutex it raises the barrier that makes Mark refuse, drops the
//     queued jobs and cancels the running one.
//  2. Without the mutex it waits until the worker is idle. A cancelled job stores
//     nothing at its commit.
//  3. Under the mutex it deletes every row, empties the bank, forgets the
//     finished and cancelled jobs, and lifts the barrier.
//
// ctx bounds the whole call; a caller serving a request passes a context detached
// from the request, so a client that disconnects does not stop a clear half way.
// When ctx ends before the delete starts, nothing is deleted and the error is
// ErrClearTimeout, but the jobs found in the first step stay cancelled: they
// report cancelled, and their detections can be marked again. LeaveNoise is not
// held back by the barrier, because it only removes.
func (s *Service) Clear(ctx context.Context) (int64, error) {
	idle := s.raiseClearBarrier()
	if idle == nil {
		return 0, &RefusalError{Reason: ReasonClearing}
	}

	select {
	case <-idle:
	case <-ctx.Done():
		s.mu.Lock()
		s.clearing = false
		s.mu.Unlock()
		return 0, ErrClearTimeout
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearing = false
	// Both outcomes of the wait can be ready at once; an ended context is a
	// timeout whichever the select took, and nothing has been deleted yet.
	if ctx.Err() != nil {
		return 0, ErrClearTimeout
	}
	deleted, err := s.cfg.Examples.DeleteAll(ctx)
	if err != nil {
		return 0, fmt.Errorf("clear noise examples: %w", err)
	}
	s.bank.Clear()
	for id, j := range s.jobs {
		// A cancelled job the worker took off the queue just before the wait
		// still has the queued phase; it stores nothing, so it goes too.
		if j.cancelled || j.phase == phaseLearned || j.phase == phaseFailed {
			delete(s.jobs, id)
		}
	}
	s.log.Info("noise examples cleared", logger.Int64("examples", deleted))
	return deleted, nil
}

// raiseClearBarrier is step one of Clear. It returns the channel that is closed
// when the worker is idle, or nil when a clear is already running.
func (s *Service) raiseClearBarrier() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.clearing {
		return nil
	}
	s.clearing = true
	for _, j := range s.jobs {
		j.cancelLocked()
	}
	for drained := false; !drained; {
		select {
		case j := <-s.queue:
			s.finishLocked(j, ReasonCancelled)
		default:
			drained = true
		}
	}
	return s.idle
}
