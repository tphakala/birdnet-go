// source_registry.go - Thread-safe registry of audio sources with event notifications.
package audiocore

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// SourceEventType identifies what kind of change occurred to a source.
type SourceEventType int

const (
	// SourceAdded fires when a new source is successfully registered.
	SourceAdded SourceEventType = iota

	// SourceRemoved fires when a source is unregistered.
	SourceRemoved

	// SourceStateChanged fires when a source's SourceState changes.
	SourceStateChanged

	// SourceReconfigured fires when a source's configuration is updated.
	SourceReconfigured
)

// SourceEvent is the payload delivered to every registered listener.
type SourceEvent struct {
	// Type identifies the kind of change.
	Type SourceEventType

	// SourceID is the unique ID of the affected source.
	SourceID string

	// Source is a copy of the affected AudioSource at the time of the event.
	// It is always a copy — listeners must not modify it.
	Source *AudioSource
}

// SourceEventListener is called for every SourceEvent fired by a SourceRegistry.
// Implementations must not block; use a buffered channel or goroutine if needed.
type SourceEventListener func(SourceEvent)

// deviceDefault is the connection string used by the default audio device.
const (
	registryDeviceDefault            = "default"
	registryDeviceDefaultDisplayName = "Default Audio Device"
)

// SourceRegistry manages audio sources with thread-safe CRUD and event notifications.
// Use NewSourceRegistry to create an instance. There is no global singleton.
type SourceRegistry struct {
	// sources maps source ID -> *AudioSource.
	sources map[string]*AudioSource

	// connectionMap maps raw connection string -> source ID for deduplication.
	connectionMap map[string]string

	// mu guards sources and connectionMap.
	mu sync.RWMutex

	// listeners holds all registered event callbacks.
	listeners []SourceEventListener

	// listenerMu guards listeners independently so notify() can run while
	// the sources map is held under a read lock.
	listenerMu sync.RWMutex

	// log is the logger for this registry instance.
	log logger.Logger
}

// NewSourceRegistry creates and returns a new SourceRegistry backed by the
// provided logger.
func NewSourceRegistry(log logger.Logger) *SourceRegistry {
	return &SourceRegistry{
		sources:       make(map[string]*AudioSource),
		connectionMap: make(map[string]string),
		log:           log.With(logger.String("component", "source_registry")),
	}
}

// Register adds a source to the registry from the given SourceConfig.
//
// If a source with the same ConnectionString already exists, it is returned
// unchanged — no duplicate is created.
//
// The source type is detected automatically from the ConnectionString unless
// cfg.Type is already set to a non-Unknown value.
func (r *SourceRegistry) Register(cfg *SourceConfig) (*AudioSource, error) {
	connStr := cfg.ConnectionString

	// Detect source type from connection string when not supplied.
	if cfg.Type == SourceTypeUnknown || cfg.Type == "" {
		cfg.Type = detectSourceType(connStr)
	}

	safeStr := sanitizeConn(connStr, cfg.Type)

	r.mu.Lock()

	// Deduplication: return existing source for the same connection string.
	if existingID, ok := r.connectionMap[connStr]; ok {
		existing := r.sources[existingID]
		r.mu.Unlock()
		return existing, nil
	}

	// Determine ID.
	id := cfg.ID
	if id == "" {
		id = generateSourceID(cfg.Type)
	}

	// Determine display name.
	displayName := cfg.DisplayName
	if displayName == "" {
		displayName = buildDisplayName(cfg.Type, safeStr, connStr)
	}

	src := &AudioSource{
		ID:           id,
		DisplayName:  displayName,
		Type:         cfg.Type,
		SafeString:   safeStr,
		SampleRate:   cfg.SampleRate,
		BitDepth:     cfg.BitDepth,
		Channels:     cfg.Channels,
		Gain:         cfg.Gain,
		State:        SourceInactive,
		RegisteredAt: time.Now(),
		LastSeen:     time.Now(),
	}
	src.SetConnectionString(connStr)

	r.sources[id] = src
	r.connectionMap[connStr] = id
	snapshot := r.copySource(src)

	r.mu.Unlock()

	r.log.Info("registered audio source",
		logger.String("id", id),
		logger.String("display_name", displayName),
		logger.String("safe", safeStr),
		logger.String("type", cfg.Type.String()))

	r.notify(SourceEvent{Type: SourceAdded, SourceID: id, Source: snapshot})
	return src, nil
}

// Unregister removes the source with the given ID from the registry.
// Returns ErrSourceNotFound if the ID does not exist.
func (r *SourceRegistry) Unregister(sourceID string) error {
	r.mu.Lock()

	src, ok := r.sources[sourceID]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrSourceNotFound, sourceID)
	}

	connStr, _ := src.GetConnectionString()
	snapshot := r.copySource(src)

	delete(r.sources, sourceID)
	delete(r.connectionMap, connStr)

	r.mu.Unlock()

	r.log.Info("unregistered audio source",
		logger.String("id", sourceID),
		logger.String("safe", src.SafeString))

	r.notify(SourceEvent{Type: SourceRemoved, SourceID: sourceID, Source: snapshot})
	return nil
}

// Get returns the source with the given ID, or (nil, false) if not found.
func (r *SourceRegistry) Get(sourceID string) (*AudioSource, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	src, ok := r.sources[sourceID]
	return src, ok
}

// GetByConnection returns the source registered for the given connection string,
// or (nil, false) if not found.
func (r *SourceRegistry) GetByConnection(connStr string) (*AudioSource, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.connectionMap[connStr]
	if !ok {
		return nil, false
	}
	src, ok := r.sources[id]
	return src, ok
}

// List returns safe copies of all registered sources, sorted by DisplayName.
func (r *SourceRegistry) List() []*AudioSource {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*AudioSource, 0, len(r.sources))
	for _, src := range r.sources {
		result = append(result, r.copySource(src))
	}

	slices.SortFunc(result, func(a, b *AudioSource) int {
		return strings.Compare(a.DisplayName, b.DisplayName)
	})
	return result
}

// UpdateState changes the state of a source and fires SourceStateChanged.
// Returns ErrSourceNotFound if the ID does not exist.
func (r *SourceRegistry) UpdateState(sourceID string, state SourceState) error {
	r.mu.Lock()

	src, ok := r.sources[sourceID]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrSourceNotFound, sourceID)
	}
	src.State = state
	snapshot := r.copySource(src)
	r.mu.Unlock()

	r.notify(SourceEvent{Type: SourceStateChanged, SourceID: sourceID, Source: snapshot})
	return nil
}

// UpdateGain updates the Gain field of the source with the given ID.
// Returns false if the source does not exist.
func (r *SourceRegistry) UpdateGain(sourceID string, gain float64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	src, ok := r.sources[sourceID]
	if !ok {
		return false
	}
	src.Gain = gain
	return true
}

// GetGain returns the current gain (in dB) for the given source, under
// the registry read lock. Returns 0.0 and false if the source does not exist.
func (r *SourceRegistry) GetGain(sourceID string) (float64, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	src, ok := r.sources[sourceID]
	if !ok {
		return 0.0, false
	}
	return src.Gain, true
}

// AddListener registers a callback that is called for every SourceEvent.
// Callbacks are invoked synchronously; they must not block.
func (r *SourceRegistry) AddListener(l SourceEventListener) {
	r.listenerMu.Lock()
	defer r.listenerMu.Unlock()
	r.listeners = append(r.listeners, l)
}

// notify delivers the event to all registered listeners. It is called without
// the sources mutex held, so listeners may safely call back into the registry.
// Each listener call is wrapped in a recover so a panicking listener does not
// crash the goroutine that triggered the notification.
func (r *SourceRegistry) notify(e SourceEvent) {
	r.listenerMu.RLock()
	defer r.listenerMu.RUnlock()
	for _, l := range r.listeners {
		func() {
			defer func() {
				if p := recover(); p != nil {
					panicErr := fmt.Errorf("source event listener panicked: %v", p)
					r.log.Error("source event listener panicked",
						logger.Any("panic", p),
						logger.String("source_id", e.SourceID))
					_ = errors.New(panicErr).
						Component("audiocore.registry").
						Category(errors.CategoryState).
						Context("operation", "event_listener_panic").
						Context("source_id", e.SourceID).
						Priority(errors.PriorityCritical).
						Build()
				}
			}()
			l(e)
		}()
	}
}

// copySource returns a shallow copy of src that omits the private connectionString.
// The copy is safe to hand to callers and event listeners.
func (r *SourceRegistry) copySource(src *AudioSource) *AudioSource {
	c := *src
	c.SetConnectionString("") // do not leak credentials via events
	return &c
}

// --- Connection string helpers ---

// sanitizeConn returns a log-safe version of a connection string.
func sanitizeConn(conn string, t SourceType) string {
	switch t {
	case SourceTypeRTSP, SourceTypeHTTP, SourceTypeHLS, SourceTypeRTMP, SourceTypeUDP:
		return privacy.SanitizeStreamUrl(conn)
	default:
		return conn
	}
}

// detectSourceType infers the SourceType from the connection string.
func detectSourceType(conn string) SourceType {
	lower := strings.ToLower(conn)

	switch {
	case strings.HasPrefix(lower, "rtsp://"),
		strings.HasPrefix(lower, "rtsps://"),
		strings.HasPrefix(lower, "test://"):
		return SourceTypeRTSP

	case strings.HasPrefix(lower, "rtmp://"),
		strings.HasPrefix(lower, "rtmps://"):
		return SourceTypeRTMP

	// HLS before HTTP so .m3u8 URLs are classified correctly.
	case strings.HasSuffix(lower, ".m3u8"),
		strings.Contains(lower, ".m3u8?"):
		return SourceTypeHLS

	case strings.HasPrefix(lower, "http://"),
		strings.HasPrefix(lower, "https://"):
		return SourceTypeHTTP

	case strings.HasPrefix(lower, "udp://"),
		strings.HasPrefix(lower, "rtp://"):
		return SourceTypeUDP

	case strings.HasPrefix(conn, "hw:"),
		strings.HasPrefix(conn, "plughw:"),
		strings.Contains(conn, "alsa"),
		strings.Contains(conn, "pulse"),
		strings.Contains(conn, "dsnoop"),
		strings.Contains(conn, "sysdefault"),
		conn == registryDeviceDefault:
		return SourceTypeAudioCard

	case strings.Contains(lower, ".wav"),
		strings.Contains(lower, ".mp3"),
		strings.Contains(lower, ".flac"),
		strings.Contains(lower, ".m4a"),
		strings.Contains(lower, ".ogg"):
		return SourceTypeFile
	}

	return SourceTypeUnknown
}

// generateSourceID returns a short unique ID prefixed with the source type.
func generateSourceID(t SourceType) string {
	u, err := uuid.NewRandom()
	if err != nil {
		// Extremely rare; fall back to nanosecond timestamp.
		ts := fmt.Sprintf("%d", time.Now().UnixNano())
		if len(ts) > 8 {
			ts = ts[:8]
		}
		return fmt.Sprintf("%s_%s", t, ts)
	}
	return fmt.Sprintf("%s_%s", t, u.String()[:8])
}

// buildDisplayName creates a human-readable display name from the connection info.
func buildDisplayName(t SourceType, safeStr, rawConn string) string {
	switch t {
	case SourceTypeRTSP, SourceTypeHTTP, SourceTypeHLS, SourceTypeRTMP, SourceTypeUDP:
		// Use the sanitized URL so credentials are never shown.
		return safeStr

	case SourceTypeAudioCard:
		if rawConn == registryDeviceDefault {
			return registryDeviceDefaultDisplayName
		}
		return fmt.Sprintf("Audio Device (%s)", rawConn)

	case SourceTypeFile:
		base := filepath.Base(rawConn)
		if base != "" && base != "." {
			return fmt.Sprintf("Audio File: %s", base)
		}
		return "Audio File"

	default:
		return "Audio Source"
	}
}
