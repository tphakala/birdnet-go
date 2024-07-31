// dogbarkfilter.go
package processor

import (
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// Assuming a predefined time limit for filtering detections after a dog bark.
var DogBarkFilterTimeLimit = time.Duration(conf.Setting().Realtime.DogBarkFilter.Remember) * time.Minute

// Check if the species should be filtered based on the last dog bark timestamp.
func (p *Processor) CheckDogBarkFilter(species string, lastDogBark time.Time) bool {
	species = strings.ToLower(species)
	for _, s := range p.Settings.Realtime.DogBarkFilter.Species {
		if s == species {
			return time.Since(lastDogBark) <= DogBarkFilterTimeLimit
		}
	}
	return false
}
