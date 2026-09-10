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

// newStallingCache builds a cache around a provider that never returns.
//
// Deliberately NOT imageprovider.InitCache: that reads conf.Setting(), which
// lazy-loads settings from disk when the global is unset and publishes them over
// whatever this package's other tests installed, breaking every sibling test that
// reads settings. Nothing under test here needs the parts InitCache supplies; the
// prefetch machinery it would enable is covered in the imageprovider package, where
// InitCache is the subject rather than incidental setup.
func newStallingCache(t *testing.T) *imageprovider.BirdImageCache {
	t.Helper()
	cache := &imageprovider.BirdImageCache{}
	cache.SetImageProvider(newStallingImageProvider(t))
	return cache
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
	cache := &imageprovider.BirdImageCache{}
	cache.SetImageProvider(stalling)

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

	cache := newStallingCache(t)

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
}

// TestGetBirdImageFromCache_DoesNotBlock covers the helper both the SSE and MQTT
// actions use, which runs inside the same CompositeAction. A cache miss must return
// promptly with an empty image rather than resolving one on the caller's goroutine.
func TestGetBirdImageFromCache_DoesNotBlock(t *testing.T) {
	t.Parallel()

	cache := newStallingCache(t)

	done := make(chan imageprovider.BirdImage, 1)
	go func() {
		done <- getBirdImageFromCache(&DetectionContext{}, cache, "Turdus merula", "Eurasian Blackbird", "test-correlation")
	}()

	select {
	case img := <-done:
		assert.Empty(t, img.URL, "a miss yields an empty image, not a resolved one")
	case <-time.After(2 * time.Second):
		t.Fatal("getBirdImageFromCache blocked on the image provider")
	}
}

// TestGetBirdImageFromCache_ResolvesOncePerDetection pins the shared lookup.
//
// CompositeAction runs Database, then SSE, then MQTT for one detection, and each
// of the three used to resolve the species image independently. The prefetch
// deduplicates, so what multiplied was the database read: three per detection for
// a cold species, on a path whose whole purpose is to stay off slow work.
func TestGetBirdImageFromCache_ResolvesOncePerDetection(t *testing.T) {
	t.Parallel()

	detectionCtx := &DetectionContext{}
	cache := newStallingCache(t)

	// First action resolves. The cache is empty and the provider stalls, so the
	// verdict is an empty image, which is exactly the case that must be shared:
	// the later actions have to learn "already looked, nothing there".
	first := getBirdImageFromCache(detectionCtx, cache, "Turdus merula", "Eurasian Blackbird", "test-correlation")
	assert.Empty(t, first.URL)

	stored, resolved := detectionCtx.LoadBirdImage()
	require.True(t, resolved, "the verdict must be published for the later actions")
	assert.Empty(t, stored.URL)

	// A resolved image is handed straight back, without the cache being consulted:
	// passing a nil cache would log and return empty if the lookup ran again.
	detectionCtx.StoreBirdImage(&imageprovider.BirdImage{URL: "https://example.invalid/blackbird.jpg"})
	second := getBirdImageFromCache(detectionCtx, nil, "Turdus merula", "Eurasian Blackbird", "test-correlation")
	assert.Equal(t, "https://example.invalid/blackbird.jpg", second.URL,
		"later actions in the same detection reuse the resolution instead of repeating it")
}

// TestGetBirdImageFromCache_WithoutADetectionContext keeps the helper usable
// outside a CompositeAction, where there is nothing to share through.
func TestGetBirdImageFromCache_WithoutADetectionContext(t *testing.T) {
	t.Parallel()

	img := getBirdImageFromCache(nil, newStallingCache(t), "Turdus merula", "Eurasian Blackbird", "test")
	assert.Empty(t, img.URL)
}
