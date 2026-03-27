// dogbarkfilter.go
package processor

import (
	"slices"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// dogBarkFilterTimeLimit returns the current time limit for filtering
// detections after a dog bark. The value is read from live settings on
// every call so that hot-reload changes take effect immediately.
func dogBarkFilterTimeLimit() time.Duration {
	return time.Duration(conf.Setting().Realtime.DogBarkFilter.Remember) * time.Minute
}

// CheckDogBarkFilter checks if the species should be filtered based on the last dog bark timestamp.
func (p *Processor) CheckDogBarkFilter(species string, lastDogBark time.Time) bool {
	species = strings.ToLower(species)
	if slices.Contains(p.Settings.Realtime.DogBarkFilter.Species, species) {
		return time.Since(lastDogBark) <= dogBarkFilterTimeLimit()
	}
	return false
}
