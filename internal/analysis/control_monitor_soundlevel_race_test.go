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
