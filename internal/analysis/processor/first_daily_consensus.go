// first_daily_consensus.go discards a species' first detection of the day unless
// at least two models detected it in the same pending window. A PendingDetection
// already merges every model that fired for one species on one source, so the
// check is a count over ModelContributions. Once a species has an accepted
// detection that day, normal single-model behavior resumes. Every case the rule
// cannot evaluate fails open, leaving the detection to the normal pipeline.
package processor

import (
	"context"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/speciesindex"
)

const (
	// firstDailyMinModels is how many models must detect a species' first
	// detection of the day in the same pending window.
	firstDailyMinModels = 2

	// firstDailySeedTimeout bounds the datastore query that loads the species
	// already detected today.
	firstDailySeedTimeout = 5 * time.Second

	// firstDailySeedRetry is how long a failed seed waits before it is retried;
	// the rule fails open meanwhile.
	firstDailySeedRetry = time.Minute

	// reasonFirstDailyConsensus is the discard reason logged by the flusher.
	reasonFirstDailyConsensus = "first daily detection confirmed by only one model"
)

// firstDailyConsensus holds the species accepted on one calendar day. It is only
// touched by the flusher goroutine (flushPendingDetections), so it needs no lock.
type firstDailyConsensus struct {
	day string // local date (YYYY-MM-DD) that accepted covers
	// accepted holds the speciesindex.CanonicalKey of each species with an
	// accepted detection on day. nil means today's set is not loaded, and the
	// rule fails open.
	accepted map[string]struct{}
	retryAt  time.Time // earliest time a failed seed is retried

	// modelSpecies is each loaded model's species keys, rebuilt outside
	// pendingMutex whenever the species index snapshot changes (every model
	// load, unload and reload rebuilds it), so the gate never waits on a model's
	// label lock. A flush that lands between a model swap and that rebuild still
	// uses the previous sets; the worst case is one extra first-of-day discard,
	// and the species is accepted from the next window in which two models agree.
	modelSpecies         map[string]map[string]struct{}
	modelSpeciesSnapshot *speciesindex.Snapshot
}

// prepareFirstDailyConsensus runs at the start of each flush cycle, before
// pendingMutex is taken. When the rule is enabled it refreshes the per-model
// species sets and, when the day has changed, loads the species already detected
// today from the datastore, so a restart does not treat them as unseen. Disabling
// the rule drops the state, so re-enabling reloads it rather than trusting a set
// that missed approvals.
func (p *Processor) prepareFirstDailyConsensus(now time.Time, settings *conf.Settings) {
	c := &p.firstDaily
	if !settings.Realtime.FirstDailyConsensus.Enabled {
		*c = firstDailyConsensus{}
		return
	}
	if snapshot := p.Bn.SpeciesSnapshot(); snapshot != c.modelSpeciesSnapshot {
		c.modelSpecies, c.modelSpeciesSnapshot = p.Bn.ModelSpeciesSets(), snapshot
	}
	today := now.Format(time.DateOnly)
	if c.day == today || now.Before(c.retryAt) || p.Ds == nil {
		return
	}

	ctx := p.flusherCtx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, firstDailySeedTimeout)
	defer cancel()
	rows, err := p.Ds.GetSpeciesSummaryData(ctx, today, today)
	if err != nil {
		c.accepted, c.retryAt = nil, now.Add(firstDailySeedRetry)
		if !errors.Is(err, context.Canceled) { // expected during shutdown
			GetLogger().Warn("first daily consensus: failed to load today's species, rule inactive until retry",
				logger.Error(err),
				logger.String("operation", "first_daily_consensus_seed"))
		}
		return
	}
	accepted := make(map[string]struct{}, len(rows))
	for i := range rows {
		accepted[speciesindex.CanonicalKey(rows[i].ScientificName)] = struct{}{}
	}
	c.day, c.accepted = today, accepted
}

// lacksFirstDailyConsensus reports whether item is its species' first detection
// today and fewer than firstDailyMinModels models detected it, while at least
// that many models analyzing its source can predict the species: a species only
// one of them knows could never gather a second confirmation. It runs under
// pendingMutex. Whitelisted species, and a detection dated another day (a window
// pending across midnight), fail open.
func (p *Processor) lacksFirstDailyConsensus(item *PendingDetection, settings *conf.Settings) bool {
	c := &p.firstDaily
	result := &item.Detection.Result
	if !settings.Realtime.FirstDailyConsensus.Enabled || c.accepted == nil || p.BufferMgr == nil ||
		len(item.ModelContributions) >= firstDailyMinModels || result.Date() != c.day {
		return false
	}
	key := speciesindex.CanonicalKey(result.Species.ScientificName)
	if _, seen := c.accepted[key]; seen ||
		isSpeciesExcluded(result.Species.CommonName, result.Species.ScientificName, settings.Realtime.FirstDailyConsensus.Whitelist) {
		return false
	}
	capable := 0
	for modelID := range p.BufferMgr.AnalysisBuffers(item.Source) {
		if _, knows := c.modelSpecies[modelID][key]; !knows {
			continue
		}
		capable++
		if capable == firstDailyMinModels {
			return true
		}
	}
	return false
}

// noteFirstDailyApproval records an approved detection, so the species' later
// detections today no longer need a second model.
func (p *Processor) noteFirstDailyApproval(item *PendingDetection) {
	if p.firstDaily.accepted != nil && item.Detection.Result.Date() == p.firstDaily.day {
		p.firstDaily.accepted[speciesindex.CanonicalKey(item.Detection.Result.Species.ScientificName)] = struct{}{}
	}
}
