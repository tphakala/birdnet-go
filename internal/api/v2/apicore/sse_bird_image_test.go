package apicore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
)

// TestNewSSEDetectionData_AlwaysEmitsProxyURL pins the proxy boundary on the SSE feed.
//
// The stream used to publish whatever URL the image provider returned, so a detection
// could carry a raw upload.wikimedia.org address while the REST API published a proxy
// URL for the same species. That is also why a species could render on one surface and
// not another. The proxy URL is the only address whose availability this process
// controls and whose cache policy it sets.
func TestNewSSEDetectionData_AlwaysEmitsProxyURL(t *testing.T) {
	t.Parallel()

	note := &datastore.Note{ScientificName: "Turdus merula", CommonName: "Eurasian Blackbird"}

	t.Run("with provider metadata", func(t *testing.T) {
		t.Parallel()

		det := NewSSEDetectionData(note, &imageprovider.BirdImage{
			URL:            "https://upload.wikimedia.org/raw.jpg",
			ScientificName: "Turdus merula",
			AuthorName:     "Someone",
			LicenseName:    "CC BY-SA 4.0",
			SourceProvider: "wikimedia",
		})

		assert.Equal(t, imageprovider.ProxyImageURL("Turdus merula"), det.BirdImage.URL)
		assert.NotContains(t, det.BirdImage.URL, "upload.wikimedia.org")
		// Attribution still comes from the provider; only the URL is substituted.
		assert.Equal(t, "Someone", det.BirdImage.AuthorName)
		assert.Equal(t, "CC BY-SA 4.0", det.BirdImage.LicenseName)
	})

	t.Run("without provider metadata", func(t *testing.T) {
		t.Parallel()

		// The image is not resolved yet: the SSE producers no longer fetch on the
		// detection-save path, so this is now the common case for a new species. The
		// URL must still be emitted, or the client has nothing to load and shows a
		// placeholder that never recovers.
		det := NewSSEDetectionData(note, nil)

		assert.Equal(t, imageprovider.ProxyImageURL("Turdus merula"), det.BirdImage.URL)
		assert.Equal(t, "Turdus merula", det.BirdImage.ScientificName)
		assert.Empty(t, det.BirdImage.AuthorName)
	})
}
