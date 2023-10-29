// config/config.go
package config

type Settings struct {
	InputAudioFile string
	RealtimeMode   bool
	ModelPath      string
	Sensitivity    float64
	Overlap        float64
	Debug          bool
	CapturePath    string
	LogPath        string
	Threshold      float64
	Locale         string
	ProcessingTime bool
}
