package conf_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// These tests are NOT parallel: they publish to the process-wide settings
// snapshot, and a sibling test doing the same would see this one's writes.

const (
	// seedLatitude is the baseline value of the field the held writer changes.
	seedLatitude = 10.0
	// heldWriterLatitude is the value the lock-holding writer publishes.
	heldWriterLatitude = 20.0
	// counterWriters and countersPerWriter size the lost-update stress test.
	counterWriters    = 8
	countersPerWriter = 200
)

// seedGlobalSettings publishes a fresh snapshot for the test and restores the
// previous one on cleanup.
func seedGlobalSettings(t *testing.T, mutate func(s *conf.Settings)) *conf.Settings {
	t.Helper()
	prev := conf.GetSettings()
	s := &conf.Settings{}
	s.BirdNET.Latitude = seedLatitude
	if mutate != nil {
		mutate(s)
	}
	conf.StoreSettings(s)
	t.Cleanup(func() { conf.StoreSettings(prev) })
	return s
}

// TestSettingsWriters_WaitForHeldWriterLock pins that every settings writer
// takes the one writer lock and reads its baseline inside it: while another
// writer is mid-cycle, none of them completes, and once the holder publishes
// its change, every writer's own change lands on top of it instead of
// replacing it. A writer that kept a private mutex would finish while the
// lock is held and would then publish a clone of the old baseline, dropping
// the holder's latitude.
func TestSettingsWriters_WaitForHeldWriterLock(t *testing.T) {
	const wantSpecies = "Parus major_Great Tit"

	tests := []struct {
		name  string
		seed  func(s *conf.Settings)
		write func(seeded *conf.Settings) bool
		check func(t *testing.T, final *conf.Settings, writeResult bool)
	}{
		{
			name: "UpdateSettings",
			write: func(_ *conf.Settings) bool {
				_, _, err := conf.UpdateSettings(func(_, updated *conf.Settings) error {
					updated.BirdNET.Longitude = 1
					return nil
				})
				return err == nil
			},
			check: func(t *testing.T, final *conf.Settings, ok bool) {
				t.Helper()
				assert.True(t, ok)
				assert.InDelta(t, 1.0, final.BirdNET.Longitude, 0)
			},
		},
		{
			name: "UpdateIncludedSpecies",
			write: func(_ *conf.Settings) bool {
				conf.UpdateIncludedSpecies([]string{wantSpecies})
				return true
			},
			check: func(t *testing.T, final *conf.Settings, _ bool) {
				t.Helper()
				assert.Equal(t, []string{wantSpecies}, final.BirdNET.RangeFilter.Species)
			},
		},
		{
			name: "ResetRangeFilterUpdateFlag",
			seed: func(s *conf.Settings) { s.BirdNET.RangeFilter.LastUpdated = time.Now() },
			write: func(_ *conf.Settings) bool {
				conf.ResetRangeFilterUpdateFlag()
				return true
			},
			check: func(t *testing.T, final *conf.Settings, _ bool) {
				t.Helper()
				assert.True(t, final.BirdNET.RangeFilter.LastUpdated.IsZero())
			},
		},
		{
			name: "ShouldUpdateRangeFilterToday",
			seed: func(s *conf.Settings) {
				s.BirdNET.RangeFilter.LastUpdated = time.Now().Add(-48 * time.Hour)
			},
			write: func(_ *conf.Settings) bool { return conf.ShouldUpdateRangeFilterToday() },
			check: func(t *testing.T, final *conf.Settings, got bool) {
				t.Helper()
				assert.True(t, got, "the first caller of the day must get true")
				assert.False(t, conf.LocalNoon(final.BirdNET.RangeFilter.LastUpdated).Before(conf.LocalNoon(time.Now())))
			},
		},
		{
			name: "CompareAndStoreSettings",
			write: func(seeded *conf.Settings) bool {
				return conf.CompareAndStoreSettings(seeded, conf.CloneSettings(seeded))
			},
			check: func(t *testing.T, final *conf.Settings, swapped bool) {
				t.Helper()
				assert.False(t, swapped, "the holder published on top of the seeded snapshot, so the swap must fail")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seeded := seedGlobalSettings(t, tt.seed)

			release := conftest.HoldSettingsWriter(t, func(updated *conf.Settings) {
				updated.BirdNET.Latitude = heldWriterLatitude
			})

			done := make(chan struct{})
			var result bool
			go func() {
				defer close(done)
				result = tt.write(seeded)
			}()

			conftest.AssertStillBlocked(t, done, tt.name)
			release()
			<-done

			final := conf.GetSettings()
			assert.InDelta(t, heldWriterLatitude, final.BirdNET.Latitude, 0,
				"the writer that held the lock must not lose its change to a writer built on the old baseline")
			tt.check(t, final, result)
		})
	}
}

// TestUpdateSettings_ConcurrentWritersLoseNoUpdate hammers UpdateSettings and
// the range filter writers together and checks that no increment is lost and
// the species list is one of the lists written. It fails only when a writer
// publishes from a baseline it read outside the lock; it cannot fail
// spuriously.
func TestUpdateSettings_ConcurrentWritersLoseNoUpdate(t *testing.T) {
	seedGlobalSettings(t, func(s *conf.Settings) { s.BirdNET.Latitude = 0 })

	lists := [][]string{
		{"Parus major_Great Tit"},
		{"Turdus merula_Common Blackbird", "Erithacus rubecula_European Robin"},
	}

	var wg sync.WaitGroup
	for range counterWriters {
		wg.Go(func() {
			for range countersPerWriter {
				_, _, err := conf.UpdateSettings(func(_, updated *conf.Settings) error {
					updated.BirdNET.Latitude++
					return nil
				})
				assert.NoError(t, err)
			}
		})
	}
	for i := range counterWriters {
		wg.Go(func() {
			for range countersPerWriter {
				conf.UpdateIncludedSpecies(lists[i%len(lists)])
				conf.ResetRangeFilterUpdateFlag()
			}
		})
	}
	wg.Wait()

	final := conf.GetSettings()
	assert.InDelta(t, float64(counterWriters*countersPerWriter), final.BirdNET.Latitude, 0,
		"every increment must survive the range filter writers")
	assert.Contains(t, lists, final.BirdNET.RangeFilter.Species)
}

// TestCompareAndStoreSettings pins that a rollback built on the compare-and-
// swap restores the old snapshot only while the snapshot it published is still
// the current one.
func TestCompareAndStoreSettings(t *testing.T) {
	tests := []struct {
		name        string
		supersede   bool
		wantSwapped bool
	}{
		{name: "published snapshot still current swaps back", supersede: false, wantSwapped: true},
		{name: "another writer published since leaves its snapshot", supersede: true, wantSwapped: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := seedGlobalSettings(t, nil)
			_, mine, err := conf.UpdateSettings(func(_, updated *conf.Settings) error {
				updated.BirdNET.Longitude = 5
				return nil
			})
			require.NoError(t, err)

			wantCurrent := before
			if tt.supersede {
				_, theirs, err := conf.UpdateSettings(func(_, updated *conf.Settings) error {
					updated.BirdNET.Threshold = 0.5
					return nil
				})
				require.NoError(t, err)
				wantCurrent = theirs
			}

			swapped := conf.CompareAndStoreSettings(mine, before)

			assert.Equal(t, tt.wantSwapped, swapped)
			assert.Same(t, wantCurrent, conf.GetSettings())
		})
	}
}

// TestUpdateSettings_ErrorPublishesNothing pins that a mutate function that
// fails, or reports nothing to do, leaves the published pointer untouched, so a
// half-mutated clone never reaches readers.
func TestUpdateSettings_ErrorPublishesNothing(t *testing.T) {
	errMutate := errors.NewStd("mutate failed")

	tests := []struct {
		name string
		err  error
	}{
		{name: "unchanged sentinel", err: conf.ErrSettingsUnchanged},
		{name: "arbitrary error", err: errMutate},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seeded := seedGlobalSettings(t, nil)

			current, updated, err := conf.UpdateSettings(func(cur, upd *conf.Settings) error {
				assert.Same(t, seeded, cur, "mutate must see the published snapshot as its baseline")
				assert.NotSame(t, cur, upd, "mutate must work on a clone")
				upd.BirdNET.Latitude = heldWriterLatitude
				return tt.err
			})

			require.ErrorIs(t, err, tt.err)
			assert.Nil(t, current)
			assert.Nil(t, updated)
			assert.Same(t, seeded, conf.GetSettings())
			assert.InDelta(t, seedLatitude, conf.GetSettings().BirdNET.Latitude, 0)
		})
	}
}

// TestUpdateSettings_NotLoaded pins that with no published snapshot there is no
// baseline: mutate is not called and the sentinel is returned.
func TestUpdateSettings_NotLoaded(t *testing.T) {
	prev := conf.GetSettings()
	conf.StoreSettings(nil)
	t.Cleanup(func() { conf.StoreSettings(prev) })

	called := false
	_, _, err := conf.UpdateSettings(func(_, _ *conf.Settings) error {
		called = true
		return nil
	})

	require.ErrorIs(t, err, conf.ErrSettingsNotLoaded)
	assert.False(t, called, "mutate must not run without a baseline")
	assert.Nil(t, conf.GetSettings())
}
