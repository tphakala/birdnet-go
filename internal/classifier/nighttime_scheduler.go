package classifier

import (
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

const schedulerRefreshInterval = 60 * time.Second

// nighttimeScheduler precomputes whether the bat model should be active
// based on civil dusk/dawn times. A background goroutine refreshes the
// state every 60 seconds so the hot path is a single atomic load.
type nighttimeScheduler struct {
	sunCalc  *suncalc.SunCalc
	active   atomic.Bool
	stopChan chan struct{}
	warnOnce atomic.Bool
}

// newNighttimeScheduler creates a scheduler. If sunCalc is nil, the
// scheduler fails open (bat model always active).
func newNighttimeScheduler(sc *suncalc.SunCalc) *nighttimeScheduler {
	s := &nighttimeScheduler{
		sunCalc:  sc,
		stopChan: make(chan struct{}),
	}
	s.active.Store(true) // fail open: active until first refresh
	return s
}

// start begins the background refresh goroutine. The goroutine reads
// nighttimeOnly from the callback on each tick so settings changes are
// picked up without restart.
func (s *nighttimeScheduler) start(nighttimeOnlyFn func() bool) {
	// Run initial refresh immediately
	s.refresh(nighttimeOnlyFn())

	go func() {
		ticker := time.NewTicker(schedulerRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.stopChan:
				return
			case <-ticker.C:
				s.refresh(nighttimeOnlyFn())
			}
		}
	}()
}

// stop terminates the background goroutine.
func (s *nighttimeScheduler) stop() {
	select {
	case <-s.stopChan:
		// already closed
	default:
		close(s.stopChan)
	}
}

// isActive returns the precomputed bat-active state. Single atomic load.
func (s *nighttimeScheduler) isActive() bool {
	return s.active.Load()
}

// refresh recalculates the bat-active state based on current time and
// sun position. Called by the background ticker.
func (s *nighttimeScheduler) refresh(nighttimeOnly bool) {
	if !nighttimeOnly {
		s.active.Store(true)
		return
	}

	if s.sunCalc == nil {
		if s.warnOnce.CompareAndSwap(false, true) {
			GetLogger().Warn("bat nighttime scheduler has no suncalc instance, bat model will run 24/7",
				logger.String("operation", "nighttime_scheduler_refresh"))
		}
		s.active.Store(true)
		return
	}

	s.active.Store(isNighttime(s.sunCalc, time.Now()))
}

// isNighttime checks if the given time falls within the nighttime window
// [CivilDusk, next CivilDawn). Handles the midnight crossing case by
// checking both today's dusk and today's dawn independently.
func isNighttime(sc *suncalc.SunCalc, now time.Time) bool {
	todaySun, err := sc.GetSunEventTimes(now)
	if err != nil {
		GetLogger().Warn("suncalc error in nighttime check, allowing bat detection",
			logger.Error(err),
			logger.String("operation", "nighttime_check"))
		return true // fail open
	}

	// Before today's civil dawn: we are in the tail of last night
	if now.Before(todaySun.CivilDawn) {
		return true
	}

	// After today's civil dusk: night has started
	if !now.Before(todaySun.CivilDusk) {
		return true
	}

	// Between dawn and dusk: daytime
	return false
}
