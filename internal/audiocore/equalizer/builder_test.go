package equalizer_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	dspeq "github.com/tphakala/go-audio-dsp/equalizer"

	"github.com/tphakala/birdnet-go/internal/audiocore/convert"
	"github.com/tphakala/birdnet-go/internal/audiocore/equalizer"
	"github.com/tphakala/birdnet-go/internal/conf"
)

const (
	testRate      = 48000
	testSignalLen = 4096
	// responseSkip drops the filter start-up transient before measuring RMS.
	responseSkip = 4800
	// responseLen is one second of audio at testRate.
	responseLen = testRate
	// butterworthQ is the Q of a maximally flat second-order section.
	butterworthQ = conf.DefaultEQQFactor
)

// sine returns n samples of a sine wave at hz with the given amplitude.
func sine(n int, hz float64, sr int, amp float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = amp * math.Sin(2*math.Pi*hz*float64(i)/float64(sr))
	}
	return out
}

// testSignal is a deterministic mix of tones at 150, 1100 and 7000 Hz.
func testSignal(n, sr int) []float64 {
	a := sine(n, 150, sr, 0.3)
	b := sine(n, 1100, sr, 0.3)
	c := sine(n, 7000, sr, 0.3)
	for i := range a {
		a[i] += b[i] + c[i]
	}
	return a
}

// runChain copies in and runs it through chain.
func runChain(t *testing.T, chain *dspeq.FilterChain, in []float64) []float64 {
	t.Helper()
	require.NotNil(t, chain)
	out := make([]float64, len(in))
	copy(out, in)
	chain.ApplyFloat64(out)
	return out
}

// directChain builds a chain straight from the library for comparison.
func directChain(t *testing.T, sr int, bands ...dspeq.Band) *dspeq.FilterChain {
	t.Helper()
	chain := dspeq.NewFilterChain()
	for _, b := range bands {
		f, err := dspeq.NewFilter(b, sr)
		require.NoError(t, err)
		require.NoError(t, chain.AddFilter(f))
	}
	return chain
}

// rmsDB returns the RMS ratio of out to in in dB, ignoring the first skip samples.
func rmsDB(in, out []float64, skip int) float64 {
	return 20 * math.Log10(convert.CalculateRMSFloat64(out[skip:])/convert.CalculateRMSFloat64(in[skip:]))
}

func enabled(filters ...conf.EqualizerFilter) conf.EqualizerSettings {
	return conf.EqualizerSettings{Enabled: true, Filters: filters}
}

func TestBuildFilterChain_ReturnsNil(t *testing.T) {
	t.Parallel()
	good := conf.EqualizerFilter{Type: "HighPass", Frequency: 100, Q: butterworthQ, Passes: 1}
	tests := []struct {
		name     string
		settings conf.EqualizerSettings
	}{
		{"disabled with filters", conf.EqualizerSettings{Enabled: false, Filters: []conf.EqualizerFilter{good}}},
		{"enabled without filters", enabled()},
		{"every filter skipped", enabled(
			conf.EqualizerFilter{Type: "NoSuchType", Frequency: 100, Q: 1, Passes: 1},
			conf.EqualizerFilter{Type: "LowPass", Frequency: 24000, Q: butterworthQ, Passes: 1},
		)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Nil(t, equalizer.BuildFilterChain(tt.settings, testRate))
		})
	}
}

func TestBuildFilterChain_MapsFieldsToLibraryBand(t *testing.T) {
	t.Parallel()
	// Every conf filter sets Q, Width, Gain and Passes to distinct non-default
	// values, including fields its type ignores. The expected band carries only
	// the fields that type uses, so a swapped type or field fails.
	tests := []struct {
		name string
		conf conf.EqualizerFilter
		want dspeq.Band
	}{
		{"LowPass", conf.EqualizerFilter{Type: "LowPass", Frequency: 2000, Q: 1.3, Width: 700, Gain: 5, Passes: 2},
			dspeq.Band{Type: dspeq.LowPass, Frequency: 2000, Q: 1.3, Passes: 2}},
		{"HighPass", conf.EqualizerFilter{Type: "HighPass", Frequency: 900, Q: 0.9, Width: 700, Gain: 5, Passes: 3},
			dspeq.Band{Type: dspeq.HighPass, Frequency: 900, Q: 0.9, Passes: 3}},
		{"AllPass", conf.EqualizerFilter{Type: "AllPass", Frequency: 1500, Q: 2.1, Width: 700, Gain: 5, Passes: 2},
			dspeq.Band{Type: dspeq.AllPass, Frequency: 1500, Q: 2.1, Passes: 2}},
		{"BandPass", conf.EqualizerFilter{Type: "BandPass", Frequency: 3000, Q: 4.4, Width: 600, Gain: 5, Passes: 2},
			dspeq.Band{Type: dspeq.BandPass, Frequency: 3000, WidthHz: 600, Passes: 2}},
		{"BandReject", conf.EqualizerFilter{Type: "BandReject", Frequency: 2500, Q: 4.4, Width: 400, Gain: 5, Passes: 3},
			dspeq.Band{Type: dspeq.BandReject, Frequency: 2500, WidthHz: 400, Passes: 3}},
		{"LowShelf", conf.EqualizerFilter{Type: "LowShelf", Frequency: 800, Q: 0.8, Width: 700, Gain: -7, Passes: 2},
			dspeq.Band{Type: dspeq.LowShelf, Frequency: 800, Q: 0.8, GainDB: -7, Passes: 2}},
		{"HighShelf", conf.EqualizerFilter{Type: "HighShelf", Frequency: 5000, Q: 1.1, Width: 700, Gain: 6, Passes: 3},
			dspeq.Band{Type: dspeq.HighShelf, Frequency: 5000, Q: 1.1, GainDB: 6, Passes: 3}},
		{"Peaking", conf.EqualizerFilter{Type: "Peaking", Frequency: 1200, Q: 4.4, Width: 300, Gain: 8, Passes: 2},
			dspeq.Band{Type: dspeq.Peaking, Frequency: 1200, WidthHz: 300, GainDB: 8, Passes: 2}},
	}
	in := testSignal(testSignalLen, testRate)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			chain := equalizer.BuildFilterChain(enabled(tt.conf), testRate)
			require.NotNil(t, chain)
			assert.Equal(t, 1, chain.Len())

			got := runChain(t, chain, in)
			want := runChain(t, directChain(t, testRate, tt.want), in)
			assert.Equal(t, want, got, "builder output must match the library chain for the expected band")
			assert.NotEqual(t, in, got, "chain must change the signal")
		})
	}
}

func TestBuildFilterChain_PassesMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		passes       int
		wantSections int
	}{
		{"zero means one section", 0, 1},
		{"one", 1, 1},
		{"three", 3, 3},
		{"sixteen is the library maximum", 16, 16},
		{"negative is floored to one", -1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			chain := equalizer.BuildFilterChain(enabled(
				conf.EqualizerFilter{Type: "HighPass", Frequency: 100, Q: butterworthQ, Passes: tt.passes},
			), testRate)
			require.NotNil(t, chain)
			assert.Equal(t, tt.wantSections, chain.NumSections())
		})
	}
}

func TestBuildFilterChain_SkipsRejectedFilter(t *testing.T) {
	t.Parallel()
	first := conf.EqualizerFilter{Type: "HighPass", Frequency: 100, Q: butterworthQ, Passes: 1}
	last := conf.EqualizerFilter{Type: "LowPass", Frequency: 15000, Q: butterworthQ, Passes: 1}
	bad := []struct {
		name   string
		filter conf.EqualizerFilter
	}{
		{"unknown type", conf.EqualizerFilter{Type: "NoSuchType", Frequency: 500, Q: 1, Passes: 1}},
		{"band reject wider than its center", conf.EqualizerFilter{Type: "BandReject", Frequency: 100, Width: 1000, Passes: 1}},
		{"peaking wider than its center", conf.EqualizerFilter{Type: "Peaking", Frequency: 5000, Width: 10000, Gain: 3, Passes: 1}},
		{"corner at Nyquist", conf.EqualizerFilter{Type: "LowPass", Frequency: 24000, Q: butterworthQ, Passes: 1}},
		{"zero Q", conf.EqualizerFilter{Type: "LowPass", Frequency: 500, Q: 0, Passes: 1}},
		{"too many passes", conf.EqualizerFilter{Type: "HighPass", Frequency: 500, Q: butterworthQ, Passes: 17}},
		{"NaN frequency", conf.EqualizerFilter{Type: "LowPass", Frequency: math.NaN(), Q: butterworthQ, Passes: 1}},
	}
	in := testSignal(testSignalLen, testRate)
	want := runChain(t, directChain(t, testRate,
		dspeq.Band{Type: dspeq.HighPass, Frequency: 100, Q: butterworthQ, Passes: 1},
		dspeq.Band{Type: dspeq.LowPass, Frequency: 15000, Q: butterworthQ, Passes: 1},
	), in)
	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			chain := equalizer.BuildFilterChain(enabled(first, tt.filter, last), testRate)
			require.NotNil(t, chain)
			assert.Equal(t, 2, chain.Len(), "only the rejected filter is dropped")
			assert.Equal(t, want, runChain(t, chain, in), "neighbours keep their order and coefficients")
		})
	}
}

func TestBuildFilterChain_UsesSampleRate(t *testing.T) {
	t.Parallel()
	settings := enabled(conf.EqualizerFilter{Type: "HighPass", Frequency: 100, Q: butterworthQ, Passes: 1})
	band := dspeq.Band{Type: dspeq.HighPass, Frequency: 100, Q: butterworthQ, Passes: 1}
	const lowRate = 16000

	in16 := testSignal(testSignalLen, lowRate)
	got16 := runChain(t, equalizer.BuildFilterChain(settings, lowRate), in16)
	assert.Equal(t, runChain(t, directChain(t, lowRate, band), in16), got16)

	// The same signal through chains built for two rates must differ.
	in48 := testSignal(testSignalLen, testRate)
	got48 := runChain(t, equalizer.BuildFilterChain(settings, testRate), in48)
	wrongRate := runChain(t, equalizer.BuildFilterChain(settings, lowRate), in48)
	assert.NotEqual(t, got48, wrongRate, "coefficients must depend on the sample rate")
}

func TestBuildFilterChain_RebuildSequence(t *testing.T) {
	t.Parallel()
	hp := conf.EqualizerFilter{Type: "HighPass", Frequency: 100, Q: butterworthQ, Passes: 1}
	br := conf.EqualizerFilter{Type: "BandReject", Frequency: 1000, Width: 200, Passes: 1}
	brEdited := conf.EqualizerFilter{Type: "BandReject", Frequency: 2000, Width: 200, Passes: 1}
	ls := conf.EqualizerFilter{Type: "LowShelf", Frequency: 200, Q: butterworthQ, Gain: -6, Passes: 1}
	bad := conf.EqualizerFilter{Type: "BandReject", Frequency: 100, Width: 1000, Passes: 1}
	hpBand := dspeq.Band{Type: dspeq.HighPass, Frequency: 100, Q: butterworthQ, Passes: 1}
	brBand := dspeq.Band{Type: dspeq.BandReject, Frequency: 1000, WidthHz: 200, Passes: 1}
	brEditedBand := dspeq.Band{Type: dspeq.BandReject, Frequency: 2000, WidthHz: 200, Passes: 1}
	lsBand := dspeq.Band{Type: dspeq.LowShelf, Frequency: 200, Q: butterworthQ, GainDB: -6, Passes: 1}

	// Each step is a settings save; want lists the bands the builder must
	// produce for it, in order.
	steps := []struct {
		name    string
		filters []conf.EqualizerFilter
		want    []dspeq.Band
	}{
		{"initial", []conf.EqualizerFilter{hp, br}, []dspeq.Band{hpBand, brBand}},
		{"append", []conf.EqualizerFilter{hp, br, ls}, []dspeq.Band{hpBand, brBand, lsBand}},
		{"remove first", []conf.EqualizerFilter{br, ls}, []dspeq.Band{brBand, lsBand}},
		{"reorder", []conf.EqualizerFilter{ls, br}, []dspeq.Band{lsBand, brBand}},
		{"insert rejected in the middle", []conf.EqualizerFilter{ls, bad, br}, []dspeq.Band{lsBand, brBand}},
		{"edit a frequency", []conf.EqualizerFilter{ls, bad, brEdited}, []dspeq.Band{lsBand, brEditedBand}},
	}

	in := testSignal(testSignalLen, testRate)
	for _, s := range steps {
		settings := enabled(s.filters...)
		want := runChain(t, directChain(t, testRate, s.want...), in)

		first := equalizer.BuildFilterChain(settings, testRate)
		require.NotNil(t, first, s.name)
		assert.Equal(t, want, runChain(t, first, in), s.name)

		// A second build of the same settings starts from zeroed state and
		// shares nothing with the first, which has already processed audio.
		second := equalizer.BuildFilterChain(settings, testRate)
		assert.NotSame(t, first, second, s.name)
		assert.Equal(t, want, runChain(t, second, in), s.name)
	}
}

// TestBuildFilterChain_BandRejectWidthIsHz pins issue #1939: BandReject width is
// a bandwidth in Hz, not octaves, so a 500 Hz wide notch at 1 kHz must leave a
// distant tone alone and remove the centre tone.
func TestBuildFilterChain_BandRejectWidthIsHz(t *testing.T) {
	t.Parallel()
	notch := enabled(conf.EqualizerFilter{Type: "BandReject", Frequency: 1000, Width: 500, Passes: 1})

	// Each probe gets a fresh chain so it starts from zeroed filter state.
	far := sine(responseLen, 200, testRate, 0.5)
	assert.Greater(t, rmsDB(far, runChain(t, equalizer.BuildFilterChain(notch, testRate), far), responseSkip), -6.0,
		"a 200 Hz tone must not be silenced by a 1 kHz notch")

	centre := sine(responseLen, 1000, testRate, 0.5)
	assert.Less(t, rmsDB(centre, runChain(t, equalizer.BuildFilterChain(notch, testRate), centre), responseSkip), -9.5,
		"the 1 kHz tone must be attenuated by more than 3x")
}

func TestBuildFilterChainWithOverride_UsesOverride(t *testing.T) {
	t.Parallel()
	override := &conf.EqualizerSettings{
		Enabled: true,
		Filters: []conf.EqualizerFilter{
			{Type: "HighPass", Frequency: 300, Q: butterworthQ, Passes: 1},
		},
	}
	globalEQ := conf.EqualizerSettings{
		Enabled: true,
		Filters: []conf.EqualizerFilter{
			{Type: "LowPass", Frequency: 15000, Q: butterworthQ, Passes: 1},
			{Type: "HighPass", Frequency: 100, Q: butterworthQ, Passes: 1},
		},
	}
	chain := equalizer.BuildFilterChainWithOverride(override, globalEQ, "test-source", testRate)
	require.NotNil(t, chain)
	assert.Equal(t, 1, chain.Len(), "should use 1-filter override, not 2-filter global")
}

func TestBuildFilterChainWithOverride_FallsBackToGlobal(t *testing.T) {
	t.Parallel()
	globalEQ := conf.EqualizerSettings{
		Enabled: true,
		Filters: []conf.EqualizerFilter{
			{Type: "LowPass", Frequency: 15000, Q: butterworthQ, Passes: 1},
			{Type: "HighPass", Frequency: 100, Q: butterworthQ, Passes: 1},
		},
	}
	chain := equalizer.BuildFilterChainWithOverride(nil, globalEQ, "test-source", testRate)
	require.NotNil(t, chain)
	assert.Equal(t, 2, chain.Len(), "should use 2-filter global when override is nil")
}

func TestBuildFilterChainWithOverride_DisabledOverride(t *testing.T) {
	t.Parallel()
	override := &conf.EqualizerSettings{
		Enabled: false,
		Filters: []conf.EqualizerFilter{
			{Type: "HighPass", Frequency: 300, Q: butterworthQ, Passes: 1},
		},
	}
	globalEQ := conf.EqualizerSettings{
		Enabled: true,
		Filters: []conf.EqualizerFilter{
			{Type: "LowPass", Frequency: 15000, Q: butterworthQ, Passes: 1},
		},
	}
	chain := equalizer.BuildFilterChainWithOverride(override, globalEQ, "test-source", testRate)
	assert.Nil(t, chain, "disabled override should return nil chain regardless of global")
}

func TestResolveAndBuildFilterChain_UsesPerSourceOverride(t *testing.T) {
	t.Parallel()
	settings := &conf.Settings{}
	settings.Realtime.Audio.Sources = []conf.AudioSourceConfig{
		{
			Name: "Front Mic",
			Equalizer: &conf.EqualizerSettings{
				Enabled: true,
				Filters: []conf.EqualizerFilter{
					{Type: "HighPass", Frequency: 300, Q: butterworthQ, Passes: 1},
				},
			},
		},
	}
	settings.Realtime.Audio.Equalizer = conf.EqualizerSettings{
		Enabled: true,
		Filters: []conf.EqualizerFilter{
			{Type: "LowPass", Frequency: 15000, Q: butterworthQ, Passes: 1},
			{Type: "HighPass", Frequency: 100, Q: butterworthQ, Passes: 1},
		},
	}

	chain := equalizer.ResolveAndBuildFilterChain(settings, "Front Mic", testRate)
	require.NotNil(t, chain)
	assert.Equal(t, 1, chain.Len(), "should use 1-filter per-source override, not 2-filter global")
}

func TestResolveAndBuildFilterChain_FallsBackToGlobal(t *testing.T) {
	t.Parallel()
	settings := &conf.Settings{}
	settings.Realtime.Audio.Equalizer = conf.EqualizerSettings{
		Enabled: true,
		Filters: []conf.EqualizerFilter{
			{Type: "LowPass", Frequency: 15000, Q: butterworthQ, Passes: 1},
			{Type: "HighPass", Frequency: 100, Q: butterworthQ, Passes: 1},
		},
	}

	chain := equalizer.ResolveAndBuildFilterChain(settings, "Unknown Source", testRate)
	require.NotNil(t, chain)
	assert.Equal(t, 2, chain.Len(), "should use 2-filter global when no per-source override exists")
}

func TestResolveAndBuildFilterChain_DisabledGlobal(t *testing.T) {
	t.Parallel()
	settings := &conf.Settings{}
	settings.Realtime.Audio.Equalizer = conf.EqualizerSettings{
		Enabled: false,
		Filters: []conf.EqualizerFilter{
			{Type: "LowPass", Frequency: 15000, Q: butterworthQ, Passes: 1},
		},
	}

	chain := equalizer.ResolveAndBuildFilterChain(settings, "Any Source", testRate)
	assert.Nil(t, chain, "disabled global EQ should return nil chain")
}
