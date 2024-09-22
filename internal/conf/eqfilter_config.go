package conf

// FilterConfig defines the structure and properties of each filter type
var EqFilterConfig = map[string]EqFilterTypeConfig{
	"LowPass": {
		Parameters: []EqFilterParameter{
			{Name: "Frequency", Label: "Cutoff Frequency", Type: "number", Unit: "Hz", Min: 20, Max: 20000, Default: 15000, Tooltip: "Cutoff frequency above which the signal is attenuated"},
			{Name: "Q", Label: "Q Factor", Type: "number", Min: 0.1, Max: 10, Default: 0.707, Tooltip: "Quality factor that determines the sharpness of the filter's response"},
			{Name: "Passes", Label: "Attenuation", Type: "number", Min: 1, Max: 4, Default: 0, Tooltip: "Number of passes to apply the filter"},
		},
		Tooltip: "Low-pass filter attenuates frequencies above the cutoff frequency.",
	},
	"HighPass": {
		Parameters: []EqFilterParameter{
			{Name: "Frequency", Label: "Cutoff Frequency", Type: "number", Unit: "Hz", Min: 20, Max: 20000, Default: 100, Tooltip: "Cutoff frequency below which the signal is attenuated"},
			{Name: "Q", Label: "Q Factor", Type: "number", Min: 0.1, Max: 10, Default: 0.707, Tooltip: "Quality factor that determines the sharpness of the filter's response"},
			{Name: "Passes", Label: "Attenuation", Type: "number", Min: 1, Max: 4, Default: 0, Tooltip: "Number of passes to apply the filter"},
		},
		Tooltip: "High-pass filter attenuates frequencies below the cutoff frequency.",
	},
	/*
		"BandPass": {
			Parameters: []EqFilterParameter{
				{Name: "Frequency", Label: "Center Frequency", Type: "number", Unit: "Hz", Min: 20, Max: 20000, Default: 0, Tooltip: "Center frequency of the pass band"},
				{Name: "Width", Label: "Bandwidth", Type: "number", Unit: "Hz", Min: 1, Max: 10000, Default: 0, Tooltip: "Width of the frequency band that is allowed to pass"},
			},
			Tooltip: "Band-pass filter allows a range of frequencies to pass while attenuating others.",
		},
		"BandReject": {
			Parameters: []EqFilterParameter{
				{Name: "Frequency", Label: "Center Frequency", Type: "number", Unit: "Hz", Min: 20, Max: 20000, Default: 0, Tooltip: "Center frequency of the reject band"},
				{Name: "Width", Label: "Bandwidth", Type: "number", Unit: "Hz", Min: 1, Max: 10000, Default: 0, Tooltip: "Width of the frequency band that is attenuated"},
			},
			Tooltip: "Band-reject filter attenuates a range of frequencies while allowing others to pass.",
		},
		"LowShelf": {
			Parameters: []EqFilterParameter{
				{Name: "Frequency", Label: "Transition Frequency", Type: "number", Unit: "Hz", Min: 20, Max: 20000, Default: 0, Tooltip: "Transition frequency of the shelf filter"},
				{Name: "Q", Label: "Q Factor", Type: "number", Min: 0.1, Max: 10, Default: 0.707, Tooltip: "Quality factor that determines the transition slope"},
				{Name: "Gain", Label: "Gain", Type: "number", Unit: "dB", Min: -30, Max: 30, Default: 0, Tooltip: "Amount of boost or cut applied to frequencies below the transition frequency"},
			},
			Tooltip: "Low-shelf filter boosts or cuts frequencies below the transition frequency.",
		},
		"HighShelf": {
			Parameters: []EqFilterParameter{
				{Name: "Frequency", Label: "Transition Frequency", Type: "number", Unit: "Hz", Min: 20, Max: 20000, Default: 0, Tooltip: "Transition frequency of the shelf filter"},
				{Name: "Q", Label: "Q Factor", Type: "number", Min: 0.1, Max: 10, Default: 0.707, Tooltip: "Quality factor that determines the transition slope"},
				{Name: "Gain", Label: "Gain", Type: "number", Unit: "dB", Min: -30, Max: 30, Default: 0, Tooltip: "Amount of boost or cut applied to frequencies above the transition frequency"},
			},
			Tooltip: "High-shelf filter boosts or cuts frequencies above the transition frequency.",
		},
		"Peaking": {
			Parameters: []EqFilterParameter{
				{Name: "Frequency", Label: "Center Frequency", Type: "number", Unit: "Hz", Min: 20, Max: 20000, Default: 0, Tooltip: "Center frequency of the peak or notch"},
				{Name: "Width", Label: "Bandwidth", Type: "number", Unit: "Hz", Min: 1, Max: 10000, Default: 0, Tooltip: "Width of the peak or notch"},
				{Name: "Gain", Label: "Gain", Type: "number", Unit: "dB", Min: -30, Max: 30, Default: 0, Tooltip: "Amount of boost or cut applied to the specified frequency range"},
			},
			Tooltip: "Peaking filter boosts or cuts a range of frequencies.",
		},
	*/
}

// FilterTypeConfig defines the configuration for a specific filter type
type EqFilterTypeConfig struct {
	Parameters []EqFilterParameter
	Tooltip    string
}

// FilterParameter defines a single parameter for a filter
type EqFilterParameter struct {
	Name    string
	Label   string
	Type    string
	Unit    string
	Min     float64
	Max     float64
	Default float64
	Tooltip string
}
