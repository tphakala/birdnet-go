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

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/audiocore/soundlevel"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

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

	for range 50 {
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
		wg.Wait()
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
