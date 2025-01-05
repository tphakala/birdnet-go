package weather

import "time"

const (
	RequestTimeout = 10 * time.Second
	UserAgent      = "BirdNET-Go https://github.com/tphakala/birdnet-go"
	RetryDelay     = 2 * time.Second
	MaxRetries     = 3
)
