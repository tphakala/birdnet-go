package config

// BirdweatherClientInterface defines what methods a BirdweatherClient must have
type BirdweatherClientInterface interface {
	UploadSoundscape(ctx *Context, timestamp, filePath string) (soundscapeID string, err error)
	PostDetection(ctx *Context, soundscapeID, timestamp, commonName, scientificName string, confidence float64) error
}
