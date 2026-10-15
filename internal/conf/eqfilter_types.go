package conf

import dspeq "github.com/tphakala/go-audio-dsp/equalizer"

// eqFilterTypes maps the filter type names stored in EqualizerFilter.Type to the
// equalizer library's filter types.
var eqFilterTypes = map[string]dspeq.FilterType{
	"LowPass":    dspeq.LowPass,
	"HighPass":   dspeq.HighPass,
	"AllPass":    dspeq.AllPass,
	"BandPass":   dspeq.BandPass,
	"BandReject": dspeq.BandReject,
	"LowShelf":   dspeq.LowShelf,
	"HighShelf":  dspeq.HighShelf,
	"Peaking":    dspeq.Peaking,
}

// EQFilterType returns the equalizer library's filter type for a filter type
// name as stored in EqualizerFilter.Type, and false for an unknown name.
func EQFilterType(name string) (dspeq.FilterType, bool) {
	t, ok := eqFilterTypes[name]
	return t, ok
}
