package mempolicy

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	mib = 1024 * 1024
	gib = 1024 * mib
	// testNumCPU is above arenaMaxCeiling, so arenaMaxFor(testNumCPU) == ceiling.
	testNumCPU = 8
)

func TestNormalizeMode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want string
	}{
		{"auto", ModeAuto},
		{"on", ModeOn},
		{"off", ModeOff},
		{"AUTO", ModeAuto},
		{"On", ModeOn},
		{" off ", ModeOff},
		{"", ModeAuto},
		{"garbage", ModeAuto},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, normalizeMode(tt.in))
		})
	}
}

func TestDecide(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		total        int64
		mode         string
		wantActive   bool
		wantArena    int
		wantMemLimit int64 // -1 = "non-zero, don't assert exact"
	}{
		{
			name: "auto enables on 512MB box", total: 512 * mib, mode: ModeAuto,
			wantActive: true, wantArena: arenaMaxFor(testNumCPU), wantMemLimit: -1,
		},
		{
			name: "auto enables on 1GB box", total: 1 * gib, mode: ModeAuto,
			wantActive: true, wantArena: arenaMaxFor(testNumCPU), wantMemLimit: -1,
		},
		{
			name: "auto enables exactly at threshold", total: autoThresholdBytes, mode: ModeAuto,
			wantActive: true, wantArena: arenaMaxFor(testNumCPU), wantMemLimit: -1,
		},
		{
			name: "auto disabled one byte over threshold", total: autoThresholdBytes + 1, mode: ModeAuto,
			wantActive: false, wantArena: 0, wantMemLimit: 0,
		},
		{
			name: "auto disabled mid-gap 2GiB", total: 2 * gib, mode: ModeAuto,
			wantActive: false, wantArena: 0, wantMemLimit: 0,
		},
		{
			name: "auto disabled on 4GB box", total: 4 * gib, mode: ModeAuto,
			wantActive: false, wantArena: 0, wantMemLimit: 0,
		},
		{
			name: "auto disabled when RAM unknown", total: 0, mode: ModeAuto,
			wantActive: false, wantArena: 0, wantMemLimit: 0,
		},
		{
			name: "on forces active even on big box", total: 16 * gib, mode: ModeOn,
			wantActive: true, wantArena: arenaMaxFor(testNumCPU), wantMemLimit: -1,
		},
		{
			name: "off forces inactive even on tiny box", total: 256 * mib, mode: ModeOff,
			wantActive: false, wantArena: 0, wantMemLimit: 0,
		},
		{
			name: "on with unknown RAM still caps arenas, no memlimit", total: 0, mode: ModeOn,
			wantActive: true, wantArena: arenaMaxFor(testNumCPU), wantMemLimit: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := Decide(tt.total, testNumCPU, tt.mode)
			assert.Equal(t, tt.wantActive, d.Active, "Active")
			assert.Equal(t, tt.wantArena, d.ArenaMax, "ArenaMax")
			assert.Equal(t, tt.total, d.TotalRAMBytes, "TotalRAMBytes")
			switch tt.wantMemLimit {
			case 0:
				assert.Zero(t, d.GoMemLimitBytes, "GoMemLimitBytes should be unset")
			case -1:
				assert.Positive(t, d.GoMemLimitBytes, "GoMemLimitBytes should be set")
			default:
				assert.Equal(t, tt.wantMemLimit, d.GoMemLimitBytes)
			}
			assert.NotEmpty(t, d.Reason, "Reason should explain the decision")
		})
	}
}

func TestDeriveGoMemLimit(t *testing.T) {
	t.Parallel()
	// Unknown RAM => skip (0).
	assert.Zero(t, deriveGoMemLimit(0))
	// Tiny box => floored, never below the thrash floor.
	assert.Equal(t, int64(minGoMemLimitBytes), deriveGoMemLimit(256*mib))
	// 512MB box: total - nativeReserve, above the floor.
	got := deriveGoMemLimit(512 * mib)
	assert.Equal(t, int64(512*mib-nativeReserveBytes), got)
	assert.GreaterOrEqual(t, got, int64(minGoMemLimitBytes))
	// 1GB box: comfortably total - reserve.
	assert.Equal(t, int64(1*gib-nativeReserveBytes), deriveGoMemLimit(1*gib))
}

func TestArenaMaxFor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		numCPU int
		want   int
	}{
		{-2, 1},               // negative floored to 1
		{0, 1},                // unknown/zero floored to 1
		{1, 1},                // single core
		{2, 2},                // dual core, below ceiling
		{4, arenaMaxCeiling},  // exactly at ceiling
		{8, arenaMaxCeiling},  // above ceiling -> capped
		{64, arenaMaxCeiling}, // many cores -> capped
	}
	for _, tt := range tests {
		assert.Equalf(t, tt.want, arenaMaxFor(tt.numCPU), "arenaMaxFor(%d)", tt.numCPU)
	}
}

func TestApply_SetsBothControlsWhenActive(t *testing.T) {
	t.Parallel()
	var gotMemLimit int64
	var gotArena int
	memCalled, arenaCalled := false, false

	d := Decide(512*mib, testNumCPU, ModeAuto)
	require.True(t, d.Active)

	res := Apply(d, Setters{
		CurrentMemLimit: func() int64 { return math.MaxInt64 }, // not already set
		SetMemoryLimit: func(v int64) int64 {
			memCalled = true
			gotMemLimit = v
			return math.MaxInt64
		},
		SetArenaMax: func(n int) bool {
			arenaCalled = true
			gotArena = n
			return true
		},
	})

	assert.True(t, memCalled, "SetMemoryLimit must be called")
	assert.True(t, arenaCalled, "SetArenaMax must be called")
	assert.Equal(t, d.GoMemLimitBytes, gotMemLimit)
	assert.Equal(t, arenaMaxFor(testNumCPU), gotArena)
	assert.True(t, res.MemLimitApplied)
	assert.True(t, res.ArenaApplied)
}

func TestApply_NoopWhenInactive(t *testing.T) {
	t.Parallel()
	d := Decide(8*gib, testNumCPU, ModeAuto)
	require.False(t, d.Active)

	called := false
	res := Apply(d, Setters{
		CurrentMemLimit: func() int64 { return math.MaxInt64 },
		SetMemoryLimit:  func(int64) int64 { called = true; return math.MaxInt64 },
		SetArenaMax:     func(int) bool { called = true; return true },
	})
	assert.False(t, called, "no setters should run when inactive")
	assert.False(t, res.MemLimitApplied)
	assert.False(t, res.ArenaApplied)
}

func TestApply_RespectsExistingMemLimitOverride(t *testing.T) {
	t.Parallel()
	// Operator already set GOMEMLIMIT (env) -> current != MaxInt64; don't override it.
	d := Decide(512*mib, testNumCPU, ModeOn)
	require.True(t, d.Active)

	memCalled := false
	res := Apply(d, Setters{
		CurrentMemLimit: func() int64 { return 200 * mib }, // already set
		SetMemoryLimit:  func(int64) int64 { memCalled = true; return 200 * mib },
		SetArenaMax:     func(int) bool { return true },
	})
	assert.False(t, memCalled, "must not override an operator-set GOMEMLIMIT")
	assert.False(t, res.MemLimitApplied)
	assert.True(t, res.ArenaApplied, "arena cap still applies")
}

func TestApply_ArenaUnsupportedStillSetsMemLimit(t *testing.T) {
	t.Parallel()
	// Platform where setArenaMax reports failure (non-glibc / non-cgo / mallopt fail).
	d := Decide(512*mib, testNumCPU, ModeOn)
	require.True(t, d.Active)

	res := Apply(d, Setters{
		CurrentMemLimit: func() int64 { return math.MaxInt64 },
		SetMemoryLimit:  func(int64) int64 { return math.MaxInt64 },
		SetArenaMax:     func(int) bool { return false },
	})

	assert.False(t, res.ArenaApplied, "arena cap not applied when the setter reports failure")
	assert.Zero(t, res.ArenaMax, "ArenaMax stays unset when the cap did not apply")
	assert.True(t, res.MemLimitApplied, "GOMEMLIMIT still applies independently of the arena cap")
}
