package processor

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/analysis/species"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
)

// stallingImageProvider parks every Fetch until released, so a caller that still
// resolves an image on its own goroutine hangs the test instead of merely running
// slowly.
//
// It parks on a channel rather than a bare select{} so the fetch can be released at
// cleanup. fetchFromProvider runs a context-free provider on its own goroutine and
// abandons it on cancellation, so a permanently-parked Fetch would outlive the test
// and fail the package's goleak check.
type stallingImageProvider struct {
	enteredOnce sync.Once
	entered     chan struct{}
	release     chan struct{}
}

func newStallingImageProvider(t *testing.T) *stallingImageProvider {
	t.Helper()
	p := &stallingImageProvider{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	t.Cleanup(func() { close(p.release) })
	return p
}

func (p *stallingImageProvider) Fetch(scientificName string) (imageprovider.BirdImage, error) {
	p.enteredOnce.Do(func() { close(p.entered) })
	<-p.release
	return imageprovider.BirdImage{}, imageprovider.ErrImageNotFound
}

// TestGetThumbnailURL_DoesNotConsultTheCache is the regression test for a stall that
// reached far beyond thumbnails.
//
// getThumbnailURL used to call BirdImageCache.Get purely as an existence predicate,
// choosing between the proxy URL and an empty string. That call is a synchronous,
// uncancellable provider fetch, and it ran under pendingMutex.RLock and, through the
// 1s-ticker caller in processor.go, under the exclusive Lock. One cold species could
// therefore stall pending-detection processing for as long as the provider took.
func TestGetThumbnailURL_DoesNotConsultTheCache(t *testing.T) {
	t.Parallel()

	stalling := newStallingImageProvider(t)
	cache := imageprovider.InitCache("wikimedia", stalling, nil, nil)
	t.Cleanup(func() { assert.NoError(t, cache.Close()) })

	p := &Processor{BirdImageCache: cache}

	done := make(chan string, 1)
	go func() { done <- p.getThumbnailURL("Turdus merula") }()

	select {
	case got := <-done:
		assert.Equal(t, imageprovider.ProxyImageURL("Turdus merula"), got)
	case <-time.After(2 * time.Second):
		t.Fatal("getThumbnailURL blocked on the image provider")
	}

	select {
	case <-stalling.entered:
		t.Fatal("getThumbnailURL reached the image provider")
	default:
	}
}

// TestGetThumbnailURL_EmitsAURLWithNoCacheConfigured asserts the URL is unconditional.
// Returning "" for an unresolved species is what let a detection reach the client with
// no image reference at all, which the frontend cannot recover from.
func TestGetThumbnailURL_EmitsAURLWithNoCacheConfigured(t *testing.T) {
	t.Parallel()

	p := &Processor{}
	assert.Equal(t, imageprovider.ProxyImageURL("Parus major"), p.getThumbnailURL("Parus major"))
}

// TestPopulateEventMetadata_DoesNotBlockOnTheProvider pins the de-blocking half of the
// change on the detection-save path: no provider fetch inside the CompositeAction,
// whose 30s timeout this file's siblings document as the reason slow work was moved out
// of it.
//
// The URL itself deliberately stays the provider's upstream address: this metadata
// becomes bg_image_url in Discord and webhook payloads, which the notification service
// fetches server-side from outside the user's network, where no BirdNET-Go URL resolves
// for a home-LAN install.
func TestPopulateEventMetadata_DoesNotBlockOnTheProvider(t *testing.T) {
	t.Parallel()

	stalling := newStallingImageProvider(t)
	cache := imageprovider.InitCache("wikimedia", stalling, nil, nil)
	t.Cleanup(func() { assert.NoError(t, cache.Close()) })

	action := &DatabaseAction{processor: &Processor{BirdImageCache: cache}}
	action.Result.Species.ScientificName = "Turdus merula"
	action.Result.Species.CommonName = "Eurasian Blackbird"

	event, err := events.NewDetectionEvent("Eurasian Blackbird", "Turdus merula", 0.9, "", true, 1)
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		action.populateEventMetadata(event, species.NoveltyStatus{})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("populateEventMetadata blocked on the image provider")
	}

	// Nothing is cached, so no image is advertised at all rather than a URL that has
	// not been resolved yet.
	assert.NotContains(t, event.GetMetadata(), "image_url")

	select {
	case <-stalling.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("a cache miss should have scheduled a background prefetch")
	}
}
