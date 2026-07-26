package apicore

import (
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore/soundlevel"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// SSE client health monitoring and stream-type constants used by the hub.
const (
	// maxConsecutiveDrops auto-disconnects clients after this many consecutive dropped messages.
	maxConsecutiveDrops = 3

	// Stream types - used to identify what data a client wants to receive.
	// Note: StreamTypeAll shares a single consecutiveDrops counter across both
	// streams, meaning drops on one stream affect health tracking for both.
	StreamTypeDetections  = "detections"
	StreamTypeSoundLevels = "soundlevels"
	StreamTypeAll         = "all"
)

// SSEDetectionData represents the detection data sent via SSE.
// Uses explicit fields with camelCase JSON tags instead of embedding datastore.Note
// to avoid exposing internal Go struct layout, sensitive data (filesystem paths,
// RTSP credentials), and to provide a stable, well-defined API contract.
type SSEDetectionData struct {
	// Detection identity and classification
	ID             uint    `json:"id"`
	Date           string  `json:"date"` // "2024-01-15"
	Time           string  `json:"time"` // "14:30:00"
	ScientificName string  `json:"scientificName"`
	CommonName     string  `json:"commonName"`
	SpeciesCode    string  `json:"speciesCode,omitempty"`
	Confidence     float64 `json:"confidence"` // No omitempty - 0.0 is a valid confidence value

	// Location
	Latitude  float64 `json:"latitude,omitempty"`
	Longitude float64 `json:"longitude,omitempty"`

	// Audio clip (filename only, no path)
	ClipName string `json:"clipName,omitempty"`

	// Source info (safe fields only, no credentials)
	Source *SSESourceInfo `json:"source,omitempty"`

	// Time context
	BeginTime string `json:"beginTime,omitempty"`
	EndTime   string `json:"endTime,omitempty"`

	// Review status
	Verified string `json:"verified,omitempty"`
	Locked   bool   `json:"locked"`
	Unlikely bool   `json:"unlikely,omitempty"`

	// Bird image with attribution
	BirdImage SSEBirdImage `json:"birdImage"`

	// SSE event metadata
	Timestamp time.Time `json:"timestamp"`
	EventType string    `json:"eventType"`

	// Species tracking metadata
	IsNewSpecies       bool `json:"isNewSpecies,omitempty"`       // First seen within tracking window
	DaysSinceFirstSeen int  `json:"daysSinceFirstSeen,omitempty"` // Days since species was first detected
}

// SSESourceInfo describes the audio source in SSE events.
// Only exposes safe identifiers, never raw connection strings or credentials.
type SSESourceInfo struct {
	ID          string `json:"id"`
	Type        string `json:"type,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
}

// SSEBirdImage represents bird image data in SSE events with proper JSON tags.
type SSEBirdImage struct {
	URL            string `json:"url"`
	ScientificName string `json:"scientificName,omitempty"`
	LicenseName    string `json:"licenseName,omitempty"`
	LicenseURL     string `json:"licenseURL,omitempty"`
	AuthorName     string `json:"authorName,omitempty"`
	AuthorURL      string `json:"authorURL,omitempty"`
	SourceProvider string `json:"sourceProvider,omitempty"`
}

// SSESoundLevelData represents sound level data sent via SSE.
type SSESoundLevelData struct {
	soundlevel.SoundLevelData
	EventType string `json:"eventType"`
}

// SafeBaseName returns the filename component of a path, or empty string if the path is empty.
// Unlike filepath.Base("") which returns ".", this returns "" for empty inputs.
// It defines the clip-name privacy contract shared by the SSE feed and the REST
// detection responses: only the basename is exposed, never the on-disk directory
// layout, and an empty clip name stays empty (a truthful "no clip" signal).
func SafeBaseName(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Base(path)
}

// NewSSEDetectionData creates an SSEDetectionData from a datastore.Note and BirdImage.
// It sanitizes sensitive data: ClipName is stripped to filename only, and Source
// only includes safe display fields (no raw connection strings or credentials).
func NewSSEDetectionData(note *datastore.Note, birdImage *imageprovider.BirdImage) SSEDetectionData {
	det := SSEDetectionData{
		ID:             note.ID,
		Date:           note.Date,
		Time:           note.Time,
		ScientificName: note.ScientificName,
		CommonName:     note.CommonName,
		SpeciesCode:    note.SpeciesCode,
		Confidence:     note.Confidence,
		Latitude:       note.Latitude,
		Longitude:      note.Longitude,
		ClipName:       SafeBaseName(note.ClipName),
		Verified:       note.Verified,
		Locked:         note.Locked,
		Unlikely:       note.Unlikely,
		Timestamp:      time.Now(),
		EventType:      "new_detection",
	}

	// Format time fields as RFC3339 if non-zero
	if !note.BeginTime.IsZero() {
		det.BeginTime = note.BeginTime.Format(time.RFC3339)
	}
	if !note.EndTime.IsZero() {
		det.EndTime = note.EndTime.Format(time.RFC3339)
	}

	// Only expose safe source identifiers, never raw connection strings
	if note.Source.ID != "" {
		det.Source = &SSESourceInfo{
			ID:          note.Source.ID,
			DisplayName: note.Source.DisplayName,
		}
	}

	// Map bird image with proper camelCase tags
	if birdImage != nil {
		det.BirdImage = SSEBirdImage{
			URL:            birdImage.URL,
			ScientificName: birdImage.ScientificName,
			LicenseName:    birdImage.LicenseName,
			LicenseURL:     birdImage.LicenseURL,
			AuthorName:     birdImage.AuthorName,
			AuthorURL:      birdImage.AuthorURL,
			SourceProvider: birdImage.SourceProvider,
		}
	}

	return det
}

// SSEClient represents a connected SSE client.
type SSEClient struct {
	ID             string
	Channel        chan SSEDetectionData
	SoundLevelChan chan SSESoundLevelData
	PendingChan    chan any // Channel for pending detection snapshots ([]SSEPendingDetection from processor)
	Request        *http.Request
	Response       http.ResponseWriter
	Done           chan struct{} // Signal-only buffered channel to prevent blocking
	StreamType     string        // StreamTypeDetections, StreamTypeSoundLevels, or StreamTypeAll

	// Authenticated records the client's authentication state, captured ONCE at
	// connect time (mirroring StreamAudioLevel, which checks isClientAuthenticated
	// per connection). The detection stream reads this to decide whether to expose
	// the raw audio source DisplayName: unauthenticated subscribers receive only
	// the stable Source.ID, since DisplayName can embed internal host details for
	// stream sources without a user-configured name. Set before the read loop runs
	// and never mutated afterwards, so concurrent broadcasts read it race-free.
	Authenticated bool

	// Health tracking for auto-disconnect of slow/blocked clients
	// Uses atomic operations for thread-safe access during concurrent broadcasts
	consecutiveDrops atomic.Int32 // Count of consecutive failed message sends
}

// SSEManager manages SSE connections and broadcasts.
type SSEManager struct {
	clients      map[string]*SSEClient
	mutex        sync.RWMutex
	shuttingDown atomic.Bool // blocks new registrations during shutdown
}

// NewSSEManager creates a new SSE manager.
func NewSSEManager() *SSEManager {
	return &SSEManager{
		clients: make(map[string]*SSEClient),
	}
}

// AddClient adds a new SSE client. Returns false if the manager is
// shutting down and no new registrations are accepted.
func (m *SSEManager) AddClient(client *SSEClient) bool {
	if m.shuttingDown.Load() {
		return false
	}
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if m.shuttingDown.Load() {
		return false
	}
	m.clients[client.ID] = client
	GetLogger().Debug("SSE client connected",
		logger.String("client_id", client.ID),
		logger.Int("total_clients", len(m.clients)),
	)
	return true
}

// RemoveClient removes an SSE client.
func (m *SSEManager) RemoveClient(clientID string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if client, exists := m.clients[clientID]; exists {
		if client.Channel != nil {
			close(client.Channel)
		}
		if client.SoundLevelChan != nil {
			close(client.SoundLevelChan)
		}
		if client.PendingChan != nil {
			close(client.PendingChan)
		}
		close(client.Done)
		delete(m.clients, clientID)
		GetLogger().Debug("SSE client disconnected",
			logger.String("client_id", clientID),
			logger.Int("total_clients", len(m.clients)),
		)
	}
}

// BroadcastDetection sends detection data to all connected clients.
// Uses non-blocking send to prevent slow clients from blocking fast clients.
// Clients are automatically disconnected after maxConsecutiveDrops failed sends.
func (m *SSEManager) BroadcastDetection(detection *SSEDetectionData) {
	m.mutex.RLock()

	if len(m.clients) == 0 {
		m.mutex.RUnlock()
		return // No clients to broadcast to
	}

	// Collect blocked client IDs to remove them after releasing the lock
	var blockedClients []string

	for clientID, client := range m.clients {
		if client.StreamType == StreamTypeDetections || client.StreamType == StreamTypeAll {
			if client.Channel != nil {
				select {
				case client.Channel <- *detection:
					// Successfully sent to client - reset health counter atomically
					client.consecutiveDrops.Store(0)

				default:
					// Channel full - drop this update, increment counter atomically
					drops := client.consecutiveDrops.Add(1)

					// Only log when reaching disconnect threshold to avoid log spam
					if drops >= maxConsecutiveDrops {
						GetLogger().Info("SSE client disconnected after consecutive drops",
							logger.String("client_id", clientID),
							logger.Int("consecutive_drops", int(drops)),
						)
						blockedClients = append(blockedClients, clientID)
					}
				}
			}
		}
	}

	// Release the read lock before removing clients
	m.mutex.RUnlock()

	// Remove blocked clients synchronously (we're outside the lock and RemoveClient is fast)
	// Note: Low probability race if client reconnects with same ID between unlock and removal
	for _, clientID := range blockedClients {
		m.RemoveClient(clientID)
	}
}

// BroadcastSoundLevel sends sound level data to all connected clients.
// Uses non-blocking send to prevent slow clients from blocking fast clients.
// Clients are automatically disconnected after maxConsecutiveDrops failed sends.
func (m *SSEManager) BroadcastSoundLevel(soundLevel *SSESoundLevelData) {
	m.mutex.RLock()

	if len(m.clients) == 0 {
		m.mutex.RUnlock()
		return // No clients to broadcast to
	}

	// Collect blocked client IDs to remove them after releasing the lock
	var blockedClients []string

	for clientID, client := range m.clients {
		// Only send to clients that want sound level data
		if client.StreamType == StreamTypeSoundLevels || client.StreamType == StreamTypeAll {
			if client.SoundLevelChan != nil {
				select {
				case client.SoundLevelChan <- *soundLevel:
					// Successfully sent to client - reset health counter atomically
					client.consecutiveDrops.Store(0)

				default:
					// Channel full - drop this update, increment counter atomically
					drops := client.consecutiveDrops.Add(1)

					// Only log when reaching disconnect threshold to avoid log spam
					if drops >= maxConsecutiveDrops {
						GetLogger().Info("SSE client disconnected after consecutive drops",
							logger.String("client_id", clientID),
							logger.Int("consecutive_drops", int(drops)),
						)
						blockedClients = append(blockedClients, clientID)
					}
				}
			}
		}
	}

	// Release the read lock before removing clients
	m.mutex.RUnlock()

	// Remove blocked clients synchronously (we're outside the lock and RemoveClient is fast)
	// Note: Low probability race if client reconnects with same ID between unlock and removal
	for _, clientID := range blockedClients {
		m.RemoveClient(clientID)
	}
}

// BroadcastPending sends pending detection data to all connected detection stream clients.
// Uses non-blocking send to prevent slow clients from blocking the processor.
// Clients are automatically disconnected after maxConsecutiveDrops failed sends.
func (m *SSEManager) BroadcastPending(pending any) {
	m.mutex.RLock()

	if len(m.clients) == 0 {
		m.mutex.RUnlock()
		return
	}

	var blockedClients []string

	for clientID, client := range m.clients {
		// Only send to clients receiving detection data
		if client.StreamType != StreamTypeDetections && client.StreamType != StreamTypeAll {
			continue
		}
		if client.PendingChan == nil {
			continue
		}
		select {
		case client.PendingChan <- pending:
			client.consecutiveDrops.Store(0)
		default:
			drops := client.consecutiveDrops.Add(1)
			if drops >= maxConsecutiveDrops {
				GetLogger().Info("SSE client disconnected after consecutive drops",
					logger.String("client_id", clientID),
					logger.Int("consecutive_drops", int(drops)),
				)
				blockedClients = append(blockedClients, clientID)
			}
		}
	}

	m.mutex.RUnlock()

	for _, clientID := range blockedClients {
		m.RemoveClient(clientID)
	}
}

// CloseAllClients disconnects all SSE clients during shutdown.
// This must be called before echo.Shutdown() so the HTTP server
// has no active connections to wait for.
func (m *SSEManager) CloseAllClients() {
	m.shuttingDown.Store(true)
	m.mutex.Lock()
	defer m.mutex.Unlock()
	for id, client := range m.clients {
		if client.Channel != nil {
			close(client.Channel)
		}
		if client.SoundLevelChan != nil {
			close(client.SoundLevelChan)
		}
		if client.PendingChan != nil {
			close(client.PendingChan)
		}
		close(client.Done)
		delete(m.clients, id)
	}
	GetLogger().Info("SSE shutdown: closed all clients",
		logger.String("operation", "sse_close_all_clients"))
}

// GetClientCount returns the number of connected clients.
func (m *SSEManager) GetClientCount() int {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	return len(m.clients)
}

// BroadcastDetection is a helper method to broadcast detection from the controller.
// It maps the internal datastore.Note to a sanitized SSEDetectionData struct that
// only exposes safe fields with proper camelCase JSON tags.
func (c *Core) BroadcastDetection(note *datastore.Note, birdImage *imageprovider.BirdImage) error {
	if c.SSEManager == nil {
		return fmt.Errorf("SSE manager not initialized")
	}

	// Add nil checks to prevent panic
	if note == nil {
		c.LogErrorIfEnabled("SSE broadcast skipped: note is nil")
		return fmt.Errorf("note is nil")
	}
	if birdImage == nil {
		c.LogErrorIfEnabled("SSE broadcast skipped: birdImage is nil")
		return fmt.Errorf("birdImage is nil")
	}

	detection := NewSSEDetectionData(note, birdImage)

	// Add species tracking metadata if processor has tracker.
	// Compare the detection date with the species' first-seen date so the flag
	// is true only for the actual first detection, not for every detection of a
	// recently-first-seen species.
	// Snapshot processor and tracker to avoid TOCTOU race.
	if proc := c.Processor; proc != nil {
		if tracker := proc.GetNewSpeciesTracker(); tracker != nil {
			status := tracker.GetSpeciesStatus(note.ScientificName, time.Now())
			detection.IsNewSpecies = !status.FirstSeenTime.IsZero() &&
				note.Date == status.FirstSeenTime.Format(time.DateOnly)
			detection.DaysSinceFirstSeen = status.DaysSinceFirst
		}
	}

	c.SSEManager.BroadcastDetection(&detection)
	return nil
}

// BroadcastSoundLevel is a helper method to broadcast sound level data from the controller.
func (c *Core) BroadcastSoundLevel(soundLevel *soundlevel.SoundLevelData) error {
	if c.SSEManager == nil {
		return fmt.Errorf("SSE manager not initialized")
	}

	// Add nil check to prevent panic
	if soundLevel == nil {
		c.LogErrorIfEnabled("SSE broadcast skipped: soundLevel is nil")
		return fmt.Errorf("soundLevel is nil")
	}

	sseData := SSESoundLevelData{
		SoundLevelData: *soundLevel,
		EventType:      "sound_level_update",
	}

	c.SSEManager.BroadcastSoundLevel(&sseData)
	return nil
}

// BroadcastPending broadcasts pending detection snapshot from the controller.
func (c *Core) BroadcastPending(snapshot any) {
	if c.SSEManager == nil {
		return
	}
	c.SSEManager.BroadcastPending(snapshot)
}
