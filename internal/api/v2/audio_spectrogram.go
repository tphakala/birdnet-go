package api

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

const (
	liveSpectrogramHeartbeatInterval   = 15 * time.Second
	liveSpectrogramWriteDeadline       = 10 * time.Second
	liveSpectrogramChannelBuffer       = 32
	liveSpectrogramMaxConnectionsPerIP = 5
)

type LiveSpectrogramColumn struct {
	TUnixMs int64           `json:"tUnixMs"`
	Bins    SpectrogramBins `json:"bins"`
}

type SpectrogramBins []uint8

// MarshalJSON emits FFT bins as a numeric JSON array. encoding/json would
// base64-encode a []uint8 by default, so we override. The hand-written
// digit serializer avoids the intermediate []int allocation (~4 KB per
// column) and the reflection-based json.Marshal call — this runs at ~375
// columns/s × N subscribers in the SSE fan-out, so cutting the per-column
// allocation to a single output buffer is a meaningful win.
func (b SpectrogramBins) MarshalJSON() ([]byte, error) {
	if len(b) == 0 {
		return []byte("[]"), nil
	}
	// Upper bound: '[' + len*4 (up to "255,") + ']'.
	buf := make([]byte, 0, 2+len(b)*4)
	buf = append(buf, '[')
	for i, v := range b {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = strconv.AppendUint(buf, uint64(v), 10)
	}
	buf = append(buf, ']')
	return buf, nil
}

type LiveSpectrogramBatch struct {
	SourceID        string                  `json:"sourceId"`
	SampleRate      int                     `json:"sampleRate"`
	FFTSize         int                     `json:"fftSize"`
	HopSize         int                     `json:"hopSize"`
	Window          string                  `json:"window"`
	BatchIntervalMs int                     `json:"batchIntervalMs"`
	Columns         []LiveSpectrogramColumn `json:"columns"`
}

type LiveSpectrogramMeta struct {
	Type              string `json:"type"`
	SourceID          string `json:"sourceId"`
	StreamEpochUnixMs int64  `json:"streamEpochUnixMs"`
	SampleRate        int    `json:"sampleRate"`
	FFTSize           int    `json:"fftSize"`
	HopSize           int    `json:"hopSize"`
	Window            string `json:"window"`
	BinCount          int    `json:"binCount"`
	MinFrequencyHz    int    `json:"minFrequencyHz"`
	MaxFrequencyHz    int    `json:"maxFrequencyHz"`
	BatchIntervalMs   int    `json:"batchIntervalMs"`
}

type liveSpectrogramEvent struct {
	Type      string                  `json:"type"`
	SourceID  string                  `json:"sourceId"`
	Timestamp int64                   `json:"timestamp,omitempty"`
	Columns   []LiveSpectrogramColumn `json:"columns,omitempty"`
}

// liveSpectrogramSubscriber holds per-SSE-client state for the broadcaster.
// drops is incremented (under the subscribersMu read lock) whenever the
// bounded channel is full and the batch has to be skipped.
type liveSpectrogramSubscriber struct {
	ch       chan LiveSpectrogramBatch
	sourceID string
	clientIP string
	drops    atomic.Int64
}

// liveSpectrogramDropLogInterval controls how often per-subscriber drop
// warnings are emitted by the broadcaster. Matches the innermost consumer
// interval so the three log streams line up.
const liveSpectrogramDropLogInterval int64 = 100

// liveSpectrogramManager holds per-Controller SSE broadcaster state for the
// live spectrogram endpoint. Every Controller gets its own instance via
// newLiveSpectrogramManager so the embedded sync.Once resets on each
// Controller lifecycle — this is what lets a programmatic server restart
// rebind a fresh broadcaster goroutine to the new liveSpectrogramChan.
// A package-level singleton would leave the sync.Once permanently fired
// after the first start and silently drop data on every subsequent
// restart (Sentry flagged this on PR #2745).
type liveSpectrogramManager struct {
	activeConnections sync.Map
	connectionMu      sync.Mutex
	subscribers       map[string]map[*liveSpectrogramSubscriber]struct{}
	subscribersMu     sync.RWMutex
	broadcasterOnce   sync.Once
	broadcasterCancel context.CancelFunc
}

// newLiveSpectrogramManager constructs a zero-initialized manager with an
// empty subscribers map. Called once per Controller from api.go New().
func newLiveSpectrogramManager() *liveSpectrogramManager {
	return &liveSpectrogramManager{
		subscribers: make(map[string]map[*liveSpectrogramSubscriber]struct{}),
	}
}

func (c *Controller) SetLiveSpectrogramChan(ch chan LiveSpectrogramBatch) {
	c.liveSpectrogramChan = ch
	c.logInfoIfEnabled("Live spectrogram channel connected to API v2 controller")

	c.liveSpectrogramMgr.broadcasterOnce.Do(func() {
		ctx, cancel := context.WithCancel(c.ctx)
		c.liveSpectrogramMgr.broadcasterCancel = cancel
		go c.liveSpectrogramMgr.runBroadcaster(ctx, ch)
	})
}

// runBroadcaster reads batches from sourceChan and fans them out to every
// subscriber registered for the matching source ID. Exits when ctx is
// cancelled or sourceChan is closed (draining the subscribers map in the
// latter case).
func (m *liveSpectrogramManager) runBroadcaster(ctx context.Context, sourceChan chan LiveSpectrogramBatch) {
	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-sourceChan:
			if !ok {
				m.subscribersMu.Lock()
				for _, subscribers := range m.subscribers {
					for sub := range subscribers {
						close(sub.ch)
					}
				}
				m.subscribers = make(map[string]map[*liveSpectrogramSubscriber]struct{})
				m.subscribersMu.Unlock()
				return
			}

			m.subscribersMu.RLock()
			for sub := range m.subscribers[data.SourceID] {
				select {
				case sub.ch <- data:
				default:
					drops := sub.drops.Add(1)
					if drops == 1 || drops%liveSpectrogramDropLogInterval == 0 {
						GetLogger().Warn("live spectrogram batch dropped at subscriber",
							logger.String("source_id", privacy.SanitizeRTSPUrl(sub.sourceID)),
							logger.String("client_ip", sub.clientIP),
							logger.Int64("total_drops", drops))
					}
				}
			}
			m.subscribersMu.RUnlock()
		}
	}
}

// subscribe registers a new SSE subscriber for the given source ID and
// returns it. Safe to call concurrently with runBroadcaster.
func (m *liveSpectrogramManager) subscribe(sourceID, clientIP string) *liveSpectrogramSubscriber {
	sub := &liveSpectrogramSubscriber{
		ch:       make(chan LiveSpectrogramBatch, liveSpectrogramChannelBuffer),
		sourceID: sourceID,
		clientIP: clientIP,
	}
	m.subscribersMu.Lock()
	defer m.subscribersMu.Unlock()
	if m.subscribers[sourceID] == nil {
		m.subscribers[sourceID] = make(map[*liveSpectrogramSubscriber]struct{})
	}
	m.subscribers[sourceID][sub] = struct{}{}
	return sub
}

// unsubscribe removes a subscriber from its source's subscriber set, and
// removes the source key entirely if no subscribers remain.
func (m *liveSpectrogramManager) unsubscribe(sub *liveSpectrogramSubscriber) {
	m.subscribersMu.Lock()
	defer m.subscribersMu.Unlock()
	if subscribers := m.subscribers[sub.sourceID]; subscribers != nil {
		delete(subscribers, sub)
		if len(subscribers) == 0 {
			delete(m.subscribers, sub.sourceID)
		}
	}
}

func (c *Controller) initAudioSpectrogramRoutes() {
	c.Group.GET("/streams/spectrogram/:sourceID", c.StreamLiveSpectrogram, c.publicLiveAudioAuth)
}

func (c *Controller) StreamLiveSpectrogram(ctx echo.Context) error {
	if c.liveSpectrogramChan == nil || c.acquireLiveSpectrogram == nil || c.releaseLiveSpectrogram == nil {
		return c.HandleError(ctx, nil, "Live spectrogram stream is not available", http.StatusServiceUnavailable)
	}

	sourceID, err := c.validateAndDecodeSourceID(ctx)
	if err != nil {
		return err
	}
	if err := c.acquireLiveSpectrogram(sourceID); err != nil {
		return c.HandleError(ctx, err, "Live spectrogram stream is not available", http.StatusServiceUnavailable)
	}
	defer c.releaseLiveSpectrogram(sourceID)

	clientIP := c.extractRemoteAddr(ctx)
	mgr := c.liveSpectrogramMgr
	mgr.connectionMu.Lock()
	countPtr, loaded := mgr.activeConnections.Load(clientIP)
	var count int32
	if loaded {
		count = atomic.LoadInt32(countPtr.(*int32))
	}
	if count >= liveSpectrogramMaxConnectionsPerIP {
		mgr.connectionMu.Unlock()
		return c.HandleError(ctx, nil, fmt.Sprintf("Maximum %d live spectrogram stream connections per client reached", liveSpectrogramMaxConnectionsPerIP), http.StatusTooManyRequests)
	}
	if !loaded {
		var newCount int32 = 1
		mgr.activeConnections.Store(clientIP, &newCount)
	} else {
		atomic.AddInt32(countPtr.(*int32), 1)
	}
	mgr.connectionMu.Unlock()

	subscriber := mgr.subscribe(sourceID, clientIP)
	defer func() {
		mgr.unsubscribe(subscriber)
		mgr.connectionMu.Lock()
		if countPtr, ok := mgr.activeConnections.Load(clientIP); ok {
			newCount := atomic.AddInt32(countPtr.(*int32), -1)
			if newCount <= 0 {
				mgr.activeConnections.Delete(clientIP)
			}
		}
		mgr.connectionMu.Unlock()
	}()

	resp := ctx.Response()
	req := ctx.Request()
	resp.Header().Set(echo.HeaderContentType, "text/event-stream")
	resp.Header().Set(echo.HeaderCacheControl, "no-cache")
	resp.Header().Set(echo.HeaderConnection, "keep-alive")
	resp.Header().Set("X-Accel-Buffering", "no")
	resp.WriteHeader(http.StatusOK)
	if flusher, ok := resp.Writer.(http.Flusher); ok {
		flusher.Flush()
	}

	if err := c.sendSSEMessage(ctx, "spectrogram-meta", c.buildLiveSpectrogramMeta(sourceID)); err != nil {
		return nil
	}

	heartbeat := time.NewTicker(liveSpectrogramHeartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-req.Context().Done():
			return nil
		case <-c.ctx.Done():
			return nil
		case batch, ok := <-subscriber.ch:
			if !ok {
				return nil
			}
			if conn, ok := resp.Writer.(WriteDeadlineSetter); ok {
				_ = conn.SetWriteDeadline(time.Now().Add(liveSpectrogramWriteDeadline))
			}
			if err := c.sendSSEMessage(ctx, "spectrogram-columns", liveSpectrogramEvent{
				Type:     "spectrogram-columns",
				SourceID: batch.SourceID,
				Columns:  batch.Columns,
			}); err != nil {
				c.logLiveSpectrogramWriteError(ctx, err, "spectrogram-columns", sourceID)
				return nil
			}
		case <-heartbeat.C:
			if err := c.sendSSEMessage(ctx, "heartbeat", liveSpectrogramEvent{
				Type:      "heartbeat",
				SourceID:  sourceID,
				Timestamp: time.Now().UnixMilli(),
			}); err != nil {
				c.logLiveSpectrogramWriteError(ctx, err, "heartbeat", sourceID)
				return nil
			}
		}
	}
}

func (c *Controller) buildLiveSpectrogramMeta(sourceID string) LiveSpectrogramMeta {
	cfg := c.Settings.WebServer.LiveStream.Spectrogram
	// Use the same fallback the manager applies when creating the consumer so
	// the reported sample rate matches the FFT that's actually running.
	sampleRate := c.Settings.WebServer.LiveStream.EffectiveSampleRate()
	meta := LiveSpectrogramMeta{
		Type:            "spectrogram-meta",
		SourceID:        sourceID,
		SampleRate:      sampleRate,
		FFTSize:         cfg.FFTSize,
		HopSize:         cfg.HopSize,
		Window:          cfg.Window,
		BinCount:        cfg.FFTSize / 2,
		MinFrequencyHz:  0,
		MaxFrequencyHz:  sampleRate / 2,
		BatchIntervalMs: cfg.BatchIntervalMs,
	}
	if stream := c.getHLSStream(sourceID); stream != nil && !stream.streamEpoch.IsZero() {
		meta.StreamEpochUnixMs = stream.streamEpoch.UnixMilli()
	}
	return meta
}

// isClientDisconnectError reports whether err indicates that the SSE client has
// gone away (closed connection, reset peer, broken pipe, canceled request,
// write deadline expired). Transient write failures such as DNS lookup or
// dial-refused errors are NOT matched here — those are genuine faults and
// should be logged rather than silently swallowed.
func isClientDisconnectError(err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, io.EOF),
		errors.Is(err, io.ErrClosedPipe),
		errors.Is(err, io.ErrUnexpectedEOF),
		errors.Is(err, net.ErrClosed),
		errors.Is(err, syscall.EPIPE),
		errors.Is(err, syscall.ECONNRESET),
		errors.Is(err, os.ErrDeadlineExceeded):
		return true
	}
	// http.ErrAbortHandler surfaces when the client aborts the request.
	if errors.Is(err, http.ErrAbortHandler) {
		return true
	}
	return false
}

// logLiveSpectrogramWriteError logs unexpected SSE write errors at warn level
// while staying silent for client-disconnect noise.
func (c *Controller) logLiveSpectrogramWriteError(ctx echo.Context, err error, event, sourceID string) {
	if isClientDisconnectError(err) {
		return
	}
	c.logAPIRequest(ctx, logger.LogLevelWarn, "Live spectrogram SSE write failed",
		logger.String("event", event),
		logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)),
		logger.Error(err))
}
