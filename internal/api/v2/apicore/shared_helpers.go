// shared_helpers.go holds small helpers shared across multiple api/v2 domains.
// They live on apicore (the shared substrate) so every domain handler and the
// facade reach them through the embedded *Core without importing each other.
package apicore

import (
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/tphakala/birdnet-go/internal/classifier"
)

// BirdNET week-model constants, shared across api/v2 domains. BirdNET uses a
// custom 48-week year (4 weeks per month). These were originally defined in the
// range filter code; the range, heatmap, and support handlers all key off the
// same model, so they live on the shared substrate to stay in sync.
const (
	WeeksPerMonth = 4                  // BirdNET model: 4 weeks per month
	WeeksPerYear  = WeeksPerMonth * 12 // 48 weeks per year in BirdNET's model
	DaysPerWeek   = 7                  // Days in a calendar week
)

// GetBirdNETInstance returns the BirdNET orchestrator or an error if unavailable.
// It snapshots the processor first to avoid a TOCTOU race. Shared by the range,
// heatmap, and diagnostics handlers.
func (c *Core) GetBirdNETInstance() (*classifier.Orchestrator, error) {
	// Snapshot processor to avoid TOCTOU race
	proc := c.Processor
	if proc == nil {
		return nil, fmt.Errorf("BirdNET processor not available")
	}
	instance := proc.GetBirdNET()
	if instance == nil {
		return nil, fmt.Errorf("BirdNET instance not available")
	}
	return instance, nil
}

// CurrentLocale returns the BirdNET locale from the current settings snapshot.
// Reading it per call keeps locale changes hot-reloadable. Shared by the range
// and species handlers.
func (c *Core) CurrentLocale() string {
	locale := c.CurrentSettings().BirdNET.Locale
	return locale
}

// NormalizeForLookup prepares a string for case- and Unicode-form-insensitive
// map lookup. BirdNET labels ship in composed (NFC) form, but users typing
// on macOS or with composing keyboards may submit decomposed (NFD) bytes
// for diacritics, so normalising both sides to NFC prevents silent misses
// on species like "Lehtopöllö". Shared by the insights, search, and range
// (display de-duplication) code so the keys stay consistent across them.
func NormalizeForLookup(s string) string {
	return strings.ToLower(norm.NFC.String(s))
}

// ParseFloat64 parses a string to float64. Shared by the range and heatmap
// query-parameter parsing.
func ParseFloat64(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
}

// ParseFloat32 parses a string to float32. Shared by the range and heatmap
// query-parameter parsing.
func ParseFloat32(s string) (float32, error) {
	f, err := strconv.ParseFloat(s, 32)
	return float32(f), err
}
