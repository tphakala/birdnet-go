package mqtt

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
)

// TestSetBirdImage_PublishesTheUpstreamURL pins the deliberate exception to the media
// proxy boundary.
//
// The REST API and the SSE stream publish a media-proxy URL, because their consumer is
// a same-origin browser. An MQTT subscriber is not: it has no origin to resolve a
// root-relative path against, and the usual consumer may be off the LAN entirely, so
// the provider's public URL is the only form it can render.
func TestSetBirdImage_PublishesTheUpstreamURL(t *testing.T) {
	t.Parallel()

	dto := &MQTTEventDTO{}
	dto.SetBirdImage(&imageprovider.BirdImage{
		URL:            "https://upload.wikimedia.org/raw.jpg",
		ScientificName: "Turdus merula",
		AuthorName:     "Someone",
		SourceProvider: "wikimedia",
	})

	require.NotNil(t, dto.BirdImage)
	assert.Equal(t, "https://upload.wikimedia.org/raw.jpg", dto.BirdImage.URL)
	assert.NotEqual(t, imageprovider.ProxyImageURL("Turdus merula"), dto.BirdImage.URL,
		"an MQTT subscriber cannot resolve a root-relative proxy URL")
	assert.Equal(t, "Someone", dto.BirdImage.AuthorName)
}

// TestSetBirdImage_OmitsTheBlockWithoutAURL asserts that an unresolved image is
// reported by omission. That is a truthful signal a subscriber can branch on, and it
// is the normal state right after a detection now that nothing fetches on the save
// path.
func TestSetBirdImage_OmitsTheBlockWithoutAURL(t *testing.T) {
	t.Parallel()

	dto := &MQTTEventDTO{}
	dto.SetBirdImage(nil)
	assert.Nil(t, dto.BirdImage)

	dto.SetBirdImage(&imageprovider.BirdImage{ScientificName: "Parus major"})
	assert.Nil(t, dto.BirdImage, "a species with no resolved image publishes no image block")
}
