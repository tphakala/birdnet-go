// first_daily_consensus.go discards the first detection of a bird species each
// calendar day unless a second model confirms it within the same detection window.
//
// A single model crossing its threshold once is the weakest evidence the pipeline
// produces, and it is exactly the evidence that creates a "new species today"
// entry. The rule reuses the existing aggregation: a PendingDetection already
// collects every model that fired for one species on one source inside the flush
// window, so "two models agree" is a count over ModelContributions. Once a species
// has an accepted detection that day, normal single-model behaviour resumes.
//
// Every uncertainty fails open, leaving the detection to today's behaviour.
package processor

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/speciesindex"
)

const (
	// firstDailyMinModels is how many models must independently confirm the first
	// detection of a species on a given day.
	firstDailyMinModels = 2

	// firstDailyRetainedDays is how many calendar days of state are kept: a
	// detection heard just before midnight can still be pending just after it.
	firstDailyRetainedDays = 2

	// firstDailySeedTimeout bounds the once-per-day datastore query that seeds the
	// accepted-species set.
	firstDailySeedTimeout = 5 * time.Second

	// firstDailySeedRetry is how long a failed seed waits before it is retried. The
	// rule fails open for that day in the meantime.
	firstDailySeedRetry = time.Minute

	// avesClass is the taxonomic class name the genus taxonomy uses for birds.
	avesClass = "Aves"

	// reasonFirstDailyConsensus is the discard reason surfaced in the flush log and
	// the /system/events/detections aggregation.
	reasonFirstDailyConsensus = "first daily detection confirmed by only one model"
)

// firstDailyDay is one calendar day's set of species with an accepted detection,
// keyed by speciesindex.CanonicalKey so datastore rows and model labels that use
// different casing or a taxonomic alias for the same species match.
type firstDailyDay struct {
	accepted map[string]bool
	// loaded is true once the datastore seed succeeded. Until then the set only
	// holds approvals made in this process, so the gate fails open.
	loaded  bool
	retryAt time.Time
	// warned limits the seed-failure warning to once per day; retries log at Debug.
	warned bool
}

// firstDailyApproval identifies a species approved during the current flush cycle.
type firstDailyApproval struct {
	day     string
	species string
}

// firstDailySeed is the result of one datastore seed query, handed from the seed
// goroutine back to the flusher.
type firstDailySeed struct {
	day     string
	species []string
	err     error
}

// firstDailyConsensus is the rule's in-memory state, keyed by calendar day. The
// zero value is ready to use. Apart from the seed query, which runs on its own
// goroutine and only communicates through seeds, it is only touched by the flusher
// goroutine inside flushPendingDetections, so it needs no lock.
type firstDailyConsensus struct {
	days map[string]*firstDailyDay

	// approved collects this flush cycle's approvals. They are committed to days at
	// the start of the next cycle, so every entry flushed in one cycle is judged
	// against the same accepted set regardless of map iteration order.
	approved map[firstDailyApproval]struct{}

	// seeds delivers seed results; seeding is true while a query is in flight.
	seeds   chan firstDailySeed
	seeding bool

	// support answers the per-source model-support question; nil means p.Bn.
	support speciesSupport
}

// speciesSupport is the orchestrator query the gate needs, declared here so the gate
// can be exercised without loading real models.
type speciesSupport interface {
	SpeciesSharedByBirdModels(scientificName string, modelIDs []string, minModels int) (shared, ok bool)
}

// speciesSupportSource returns the model-support source: the orchestrator unless one
// was injected. A nil orchestrator reports every answer as unevaluable.
func (p *Processor) speciesSupportSource() speciesSupport {
	if p.firstDaily.support != nil {
		return p.firstDaily.support
	}
	return p.Bn
}

// day returns day's state, creating it and evicting all but the most recent
// firstDailyRetainedDays days. A day older than every retained day is evicted
// immediately; the returned value is still usable, it is just not kept.
func (c *firstDailyConsensus) day(day string) *firstDailyDay {
	if c.days == nil {
		c.days = make(map[string]*firstDailyDay, firstDailyRetainedDays+1)
	}
	d := c.days[day]
	if d == nil {
		d = &firstDailyDay{accepted: make(map[string]bool)}
		c.days[day] = d
	}
	for len(c.days) > firstDailyRetainedDays {
		// Days are YYYY-MM-DD, so lexicographic order is chronological.
		delete(c.days, slices.Min(slices.Collect(maps.Keys(c.days))))
	}
	return d
}

// prepareFirstDailyConsensus runs at the start of every flush cycle, before
// pendingMutex is taken. It applies a finished seed, commits the previous cycle's
// approvals and, when today's set is not loaded yet, starts a seed query in the
// background, so the datastore never delays a flush.
func (p *Processor) prepareFirstDailyConsensus(now time.Time, settings *conf.Settings) {
	c := &p.firstDaily
	seed, received := c.receiveSeed()
	if !settings.Realtime.FirstDailyConsensus.Enabled {
		// Re-enabling must reseed rather than trust approvals it did not observe.
		c.days, c.approved = nil, nil
		return
	}
	if received {
		c.applySeed(seed, now)
	}
	for approval := range c.approved {
		c.day(approval.day).accepted[approval.species] = true
	}
	c.approved = nil

	// The genus taxonomy is loaded lazily; make sure its first load happens here
	// rather than inside the gate, which runs under pendingMutex.
	p.getTaxonomyDB()
	p.startFirstDailySeed(now)
}

// receiveSeed returns a finished seed result without blocking.
func (c *firstDailyConsensus) receiveSeed() (seed firstDailySeed, received bool) {
	select {
	case seed = <-c.seeds:
		c.seeding = false
		return seed, true
	default:
		return firstDailySeed{}, false
	}
}

// applySeed merges a seed result into its day. It merges rather than replaces:
// persistence is asynchronous, so a detection approved moments ago may not be in
// the datastore yet.
func (c *firstDailyConsensus) applySeed(seed firstDailySeed, now time.Time) {
	d := c.day(seed.day)
	if seed.err != nil {
		d.retryAt = now.Add(firstDailySeedRetry)
		fields := []logger.Field{
			logger.String("day", seed.day),
			logger.Error(seed.err),
			logger.String("operation", "first_daily_consensus"),
		}
		const msg = "first daily consensus seed failed, rule inactive until retry"
		if d.warned {
			GetLogger().Debug(msg, fields...)
			return
		}
		// An enabled rule that silently cannot run must be visible without debug logging.
		d.warned = true
		GetLogger().Warn(msg, fields...)
		return
	}
	for _, species := range seed.species {
		d.accepted[species] = true
	}
	d.loaded = true
}

// startFirstDailySeed queries the datastore for today's species on its own
// goroutine, unless today is already loaded, a query is in flight, or a failed
// query is waiting out its retry delay. Previous days need no seed of their own: a
// day's set was seeded while it was today, and a pending detection cannot predate
// process start.
func (p *Processor) startFirstDailySeed(now time.Time) {
	c := &p.firstDaily
	if p.Ds == nil || c.seeding {
		return
	}
	day := now.Format(time.DateOnly)
	if d := c.day(day); d.loaded || now.Before(d.retryAt) {
		return
	}
	if c.seeds == nil {
		// One buffered slot: at most one query is in flight, so its send never blocks.
		c.seeds = make(chan firstDailySeed, 1)
	}
	c.seeding = true

	ctx := p.flusherCtx
	if ctx == nil {
		ctx = context.Background()
	}
	ds, seeds := p.Ds, c.seeds
	go func() {
		defer func() {
			// A panic must still report back, or seeding would stay true and the
			// rule would never load.
			if r := recover(); r != nil {
				seeds <- firstDailySeed{day: day, err: errors.Newf("first daily consensus seed panicked: %v", r).
					Component("processor").Build()}
			}
		}()
		ctx, cancel := context.WithTimeout(ctx, firstDailySeedTimeout)
		defer cancel()
		rows, err := ds.GetSpeciesSummaryData(ctx, day, day)
		seed := firstDailySeed{day: day, err: err, species: make([]string, 0, len(rows))}
		for i := range rows {
			if key := speciesindex.CanonicalKey(rows[i].ScientificName); key != "" {
				seed.species = append(seed.species, key)
			}
		}
		seeds <- seed
	}()
}

// noteAcceptedDetection records an approved detection so later detections of the
// same species that day pass without waiting for asynchronous persistence. The
// approval takes effect from the next flush cycle; see firstDailyConsensus.approved.
func (p *Processor) noteAcceptedDetection(item *PendingDetection, settings *conf.Settings) {
	if !settings.Realtime.FirstDailyConsensus.Enabled {
		return
	}
	result := &item.Detection.Result
	key := speciesindex.CanonicalKey(result.Species.ScientificName)
	if key == "" || result.Timestamp.IsZero() {
		return
	}
	if p.firstDaily.approved == nil {
		p.firstDaily.approved = make(map[firstDailyApproval]struct{})
	}
	p.firstDaily.approved[firstDailyApproval{day: result.Date(), species: key}] = struct{}{}
}

// shouldDiscardFirstDailyDetection reports whether item is the first detection of
// a bird species today with only one model behind it. It runs under pendingMutex
// and reads memory only; checks are ordered cheapest first, and the accepted-today
// lookup settles most calls once a species has been heard.
//
// A discard is terminal for this pending entry only: the species is not marked
// accepted, so the next window in which two models agree is accepted normally.
func (p *Processor) shouldDiscardFirstDailyDetection(item *PendingDetection, settings *conf.Settings) (discard bool, reason string) {
	if !settings.Realtime.FirstDailyConsensus.Enabled {
		return false, ""
	}
	result := &item.Detection.Result
	key := speciesindex.CanonicalKey(result.Species.ScientificName)
	if key == "" || result.Timestamp.IsZero() {
		return false, ""
	}
	day := result.Date()
	if d := p.firstDaily.days[day]; d == nil || !d.loaded || d.accepted[key] {
		return false, ""
	}
	if !classifier.IsBirdCapableModel(item.BestModelID) || countConfirmingModels(settings, item) >= firstDailyMinModels {
		return false, ""
	}
	// A dynamic threshold that currently lowers the bar is the processor already
	// deciding this species deserves a more permissive gate; do not override it.
	if p.dynamicThresholdLowersBar(settings, item) {
		return false, ""
	}
	// Birds only. A taxon the genus taxonomy does not know is not evidence of a
	// non-bird, but it cannot be gated reliably either, so it fails open.
	if _, genus, known := p.getTaxonomyDB().LookupGenusByScientificName(result.Species.ScientificName); !known || genus == nil || genus.Class != avesClass {
		return false, ""
	}
	// A species one of the source's bird models cannot predict could never reach
	// two confirmations. An unknown topology or an unevaluable answer (a model
	// mid-reload) also exempts it.
	modelIDs := p.sourceModelIDs(item.Source)
	if shared, ok := p.speciesSupportSource().SpeciesSharedByBirdModels(result.Species.ScientificName, modelIDs, firstDailyMinModels); !ok || !shared {
		return false, ""
	}

	GetLogger().Debug("first daily detection lacks a second model",
		logger.String("species", result.Species.CommonName),
		logger.String("scientific_name", result.Species.ScientificName),
		logger.String("source", p.getDisplayNameForSource(item.Source)),
		logger.String("best_model_id", item.BestModelID),
		logger.Int("model_count", len(item.ModelContributions)),
		logger.String("day", day),
		logger.String("operation", "first_daily_consensus"))
	return true, reasonFirstDailyConsensus
}

// countConfirmingModels counts the bird-capable models whose best score for this
// species is strictly above that model's normal threshold, the same comparison
// admission uses. The normal threshold is a positive per-species custom threshold,
// else the model's global threshold: a species entry that only configures actions
// carries a zero threshold, which must not turn any positive score into a
// confirmation. Dynamic lowering is ignored: a model that only cleared a lowered
// bar is not independent confirmation.
func countConfirmingModels(settings *conf.Settings, item *PendingDetection) int {
	species := item.Detection.Result.Species
	var customThreshold float32
	if config, exists := lookupSpeciesConfig(settings.Realtime.Species.Config, species.CommonName, species.ScientificName); exists && config.Threshold > 0 {
		customThreshold = float32(config.Threshold)
	}
	count := 0
	for modelID, contrib := range item.ModelContributions {
		if !classifier.IsBirdCapableModel(modelID) {
			continue
		}
		threshold := customThreshold
		if threshold == 0 {
			threshold = modelGlobalConfidenceThreshold(settings, modelID)
		}
		if float32(contrib.MaxConfidence) > threshold {
			count++
		}
	}
	return count
}

// dynamicThresholdLowersBar reports whether dynamic thresholding currently holds
// item's species below the best model's normal threshold. A custom per-species
// threshold opts out of dynamic adjustment, matching shouldFilterDetection.
func (p *Processor) dynamicThresholdLowersBar(settings *conf.Settings, item *PendingDetection) bool {
	species := item.Detection.Result.Species
	if !settings.Realtime.DynamicThreshold.Enabled || hasCustomThreshold(settings, species.CommonName, species.ScientificName) {
		return false
	}
	base := p.getBaseConfidenceThreshold(settings, species.CommonName, species.ScientificName, item.BestModelID)
	return p.dynamicThresholdLowered(dynamicThresholdKey(species.CommonName, species.ScientificName),
		base, settings.Realtime.DynamicThreshold.Min, time.Now())
}

// sourceModelIDs returns the IDs of the models analysing sourceID, which is the set
// that could confirm a detection on it: a model loaded but not assigned to the
// source never sees its audio. Returns nil when the topology is unknown, which
// SpeciesSharedByBirdModels reports as unevaluable.
func (p *Processor) sourceModelIDs(sourceID string) []string {
	if p.BufferMgr == nil || sourceID == "" {
		return nil
	}
	return slices.Collect(maps.Keys(p.BufferMgr.AnalysisBuffers(sourceID)))
}
