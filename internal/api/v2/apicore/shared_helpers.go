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
	HoursPerDay   = 24                 // Hours in a day
)

// Spectrogram render sizes (image width in pixels) shared across api/v2 domains.
// Widths are DFT_size + 2 (enabling fast FFT instead of brute-force DFT, ~20x
// speedup) and are 2x height to maintain a ~2:1 aspect ratio. The media handler
// renders spectrograms at these sizes and the detections handler matches
// spectrogram PNG filenames against them when deleting a clip, so the contract
// lives on the shared substrate to stay in sync.
const (
	SpectrogramSizeSm = 258  // height=129, DFT=256
	SpectrogramSizeMd = 514  // height=257, DFT=512
	SpectrogramSizeLg = 1026 // height=513, DFT=1024 (default render size)
	SpectrogramSizeXl = 2050 // height=1025, DFT=2048
)

// StatusClientClosedRequest is Nginx's non-standard HTTP status code for a
// client that closed the connection before the server responded. It is shared
// across api/v2 domains: the media handler returns it when a client cancels an
// audio/spectrogram request, and the analytics handler returns it when a client
// cancels an analytics query, so the value lives on the shared substrate.
const StatusClientClosedRequest = 499

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

// ParsePaginationLimit parses and validates a pagination "limit" query value.
// It returns defaultVal when the value is missing, non-numeric, non-positive, or
// exceeds maxVal. Shared by the analytics and dynamic-thresholds query parsing.
func ParsePaginationLimit(value string, defaultVal, maxVal int) int {
	limit, _ := strconv.Atoi(value)
	if limit <= 0 || limit > maxVal {
		return defaultVal
	}
	return limit
}

// RedactedValue is the placeholder the settings API returns in place of stored
// secrets. It lives on the shared substrate so the settings save flow (package
// api) and the integrations test-connection handlers match the same sentinel and
// cannot drift apart.
const RedactedValue = "**********"

// RestoreRedactedSecret replaces a redacted placeholder in an incoming secret
// field with the current (real) value. It is the canonical single-field restore
// primitive shared by the settings save flow (restoreRedactedSecrets) and the
// integration test-connection handlers, so both paths match the same sentinel
// against the same RedactedValue constant. A nil incoming pointer is a no-op.
func RestoreRedactedSecret(current string, incoming *string) {
	if incoming == nil {
		return
	}
	if *incoming == RedactedValue {
		*incoming = current
	}
}
