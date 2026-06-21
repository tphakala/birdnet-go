// control_monitor_soundlevel_race_test.go is a regression guard for a data race:
// ControlMonitor.Stop() read cm.soundLevelManager without holding a lock while
// the monitor goroutine wrote the same field in handleReconfigureSoundLevel. A
// reconfigure_sound_level signal in flight during shutdown therefore raced the
// shutdown read. The fix guards every access with soundLevelManagerMu; this test
// fails under `go test -race` if that guard regresses.
package analysis

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/audiocore/soundlevel"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// raceStressIterations is how many concurrent Stop/reconfigure rounds the race
// guard runs; enough rounds for the race detector to reliably observe overlap.
const raceStressIterations = 50

// raceWaitTimeout bounds each round's wait so a regression that deadlocks the
// lifecycle fails fast in CI instead of hanging the whole job.
const raceWaitTimeout = 5 * time.Second

// waitWithTimeout waits for wg, failing the test if it does not complete within
// d (a deadlock guard for the concurrency tests below).
func waitWithTimeout(t *testing.T, wg *sync.WaitGroup, d time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatal("goroutines did not finish in time; possible deadlock")
	}
}

// TestControlMonitor_StopRacesReconfigureSoundLevel runs Stop() concurrently
// with handleReconfigureSoundLevel (and a second reconfigure, to cover the
// write/write window too) across many rounds. SoundLevel.Enabled is false so
// SoundLevelManager.Start()/Stop() early-return: the test stays cheap, starts no
// background goroutines, and isolates the race to the cm.soundLevelManager field
// access itself.
func TestControlMonitor_StopRacesReconfigureSoundLevel(t *testing.T) {
	// Not parallel: conftest.SetTestSettings mutates package-global settings.
	prev := conf.GetSettings()
	t.Cleanup(func() { conftest.SetTestSettings(prev) })

	settings := &conf.Settings{}
	settings.Realtime.Audio.SoundLevel.Enabled = false
	conftest.SetTestSettings(settings)

	for range raceStressIterations {
		cm := &ControlMonitor{
			soundLevelChan: make(chan soundlevel.SoundLevelData, 1),
		}

		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			cm.handleReconfigureSoundLevel()
		}()
		go func() {
			defer wg.Done()
			cm.handleReconfigureSoundLevel()
		}()
		go func() {
			defer wg.Done()
			cm.Stop()
		}()
		waitWithTimeout(t, &wg, raceWaitTimeout)
	}
}

// TestControlMonitor_ReconfigureAfterStopDoesNotResurrect verifies the lifecycle
// gate: once Stop() has run, a reconfigure_sound_level signal still in flight on
// the monitor goroutine must not construct (and start) a new SoundLevelManager,
// which would leak its publisher goroutines past shutdown. Unlike the -race test
// above this is deterministic: it asserts the observable effect (no manager
// created after Stop). SoundLevel.Enabled is false so that, if the gate ever
// regresses, the un-gated path still spawns nothing (Restart early-returns) and
// the test fails cleanly on the nil assertion rather than leaking goroutines.
func TestControlMonitor_ReconfigureAfterStopDoesNotResurrect(t *testing.T) {
	prev := conf.GetSettings()
	t.Cleanup(func() { conftest.SetTestSettings(prev) })

	settings := &conf.Settings{}
	settings.Realtime.Audio.SoundLevel.Enabled = false
	conftest.SetTestSettings(settings)

	cm := &ControlMonitor{
		soundLevelChan: make(chan soundlevel.SoundLevelData, 1),
	}

	cm.Stop() // marks soundLevelStopped; no manager was ever created

	cm.handleReconfigureSoundLevel() // must be a no-op after shutdown

	cm.soundLevelManagerMu.Lock()
	mgr := cm.soundLevelManager
	cm.soundLevelManagerMu.Unlock()
	assert.Nil(t, mgr, "reconfigure after Stop must not create a sound level manager")
}
