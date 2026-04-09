package api

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
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

// MarshalJSON forces FFT bins to be emitted as a numeric JSON array instead
// of the default []byte base64 encoding used by encoding/json for []uint8.
func (b SpectrogramBins) MarshalJSON() ([]byte, error) {
	values := make([]int, len(b))
	for i, v := range b {
		values[i] = int(v)
	}
	return json.Marshal(values)
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

type liveSpectrogramManager struct {
	activeConnections sync.Map
	connectionMu      sync.Mutex
	subscribers       map[string]map[chan LiveSpectrogramBatch]struct{}
	subscribersMu     sync.RWMutex
	broadcasterOnce   sync.Once
	broadcasterCancel context.CancelFunc
}

var liveSpectrogramMgr = &liveSpectrogramManager{
	subscribers: make(map[string]map[chan LiveSpectrogramBatch]struct{}),
}

func (c *Controller) SetLiveSpectrogramChan(ch chan LiveSpectrogramBatch) {
	c.liveSpectrogramChan = ch
	c.logInfoIfEnabled("Live spectrogram channel connected to API v2 controller")

	liveSpectrogramMgr.broadcasterOnce.Do(func() {
		ctx, cancel := context.WithCancel(c.ctx)
		liveSpectrogramMgr.broadcasterCancel = cancel
		go runLiveSpectrogramBroadcaster(ctx, ch)
	})
}

func runLiveSpectrogramBroadcaster(ctx context.Context, sourceChan chan LiveSpectrogramBatch) {
	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-sourceChan:
			if !ok {
				liveSpectrogramMgr.subscribersMu.Lock()
				for _, subscribers := range liveSpectrogramMgr.subscribers {
					for ch := range subscribers {
						close(ch)
					}
				}
				liveSpectrogramMgr.subscribers = make(map[string]map[chan LiveSpectrogramBatch]struct{})
				liveSpectrogramMgr.subscribersMu.Unlock()
				return
			}

			liveSpectrogramMgr.subscribersMu.RLock()
			for ch := range liveSpectrogramMgr.subscribers[data.SourceID] {
				select {
				case ch <- data:
				default:
				}
			}
			liveSpectrogramMgr.subscribersMu.RUnlock()
		}
	}
}

func subscribeToLiveSpectrogram(sourceID string) chan LiveSpectrogramBatch {
	ch := make(chan LiveSpectrogramBatch, liveSpectrogramChannelBuffer)
	liveSpectrogramMgr.subscribersMu.Lock()
	defer liveSpectrogramMgr.subscribersMu.Unlock()
	if liveSpectrogramMgr.subscribers[sourceID] == nil {
		liveSpectrogramMgr.subscribers[sourceID] = make(map[chan LiveSpectrogramBatch]struct{})
	}
	liveSpectrogramMgr.subscribers[sourceID][ch] = struct{}{}
	return ch
}

func unsubscribeFromLiveSpectrogram(sourceID string, ch chan LiveSpectrogramBatch) {
	liveSpectrogramMgr.subscribersMu.Lock()
	defer liveSpectrogramMgr.subscribersMu.Unlock()
	if subscribers := liveSpectrogramMgr.subscribers[sourceID]; subscribers != nil {
		delete(subscribers, ch)
		if len(subscribers) == 0 {
			delete(liveSpectrogramMgr.subscribers, sourceID)
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
	liveSpectrogramMgr.connectionMu.Lock()
	countPtr, loaded := liveSpectrogramMgr.activeConnections.Load(clientIP)
	var count int32
	if loaded {
		count = atomic.LoadInt32(countPtr.(*int32))
	}
	if count >= liveSpectrogramMaxConnectionsPerIP {
		liveSpectrogramMgr.connectionMu.Unlock()
		return c.HandleError(ctx, nil, fmt.Sprintf("Maximum %d live spectrogram stream connections per client reached", liveSpectrogramMaxConnectionsPerIP), http.StatusTooManyRequests)
	}
	if !loaded {
		var newCount int32 = 1
		liveSpectrogramMgr.activeConnections.Store(clientIP, &newCount)
	} else {
		atomic.AddInt32(countPtr.(*int32), 1)
	}
	liveSpectrogramMgr.connectionMu.Unlock()

	subscriberChan := subscribeToLiveSpectrogram(sourceID)
	defer func() {
		unsubscribeFromLiveSpectrogram(sourceID, subscriberChan)
		liveSpectrogramMgr.connectionMu.Lock()
		if countPtr, ok := liveSpectrogramMgr.activeConnections.Load(clientIP); ok {
			newCount := atomic.AddInt32(countPtr.(*int32), -1)
			if newCount <= 0 {
				liveSpectrogramMgr.activeConnections.Delete(clientIP)
			}
		}
		liveSpectrogramMgr.connectionMu.Unlock()
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
		case batch, ok := <-subscriberChan:
			if !ok {
				return nil
			}
			if conn, ok := resp.Writer.(WriteDeadlineSetter); ok {
				_ = conn.SetWriteDeadline(time.Now().Add(liveSpectrogramWriteDeadline))
			}
			err := c.sendSSEMessage(ctx, "spectrogram-columns", liveSpectrogramEvent{
				Type:     "spectrogram-columns",
				SourceID: batch.SourceID,
				Columns:  batch.Columns,
			})
			if isClosedNetworkError(err) {
				return nil
			}
			if err != nil {
				return nil
			}
		case <-heartbeat.C:
			err := c.sendSSEMessage(ctx, "heartbeat", liveSpectrogramEvent{
				Type:      "heartbeat",
				SourceID:  sourceID,
				Timestamp: time.Now().UnixMilli(),
			})
			if isClosedNetworkError(err) {
				return nil
			}
			if err != nil {
				return nil
			}
		}
	}
}

func (c *Controller) buildLiveSpectrogramMeta(sourceID string) LiveSpectrogramMeta {
	cfg := c.Settings.WebServer.LiveStream.Spectrogram
	meta := LiveSpectrogramMeta{
		Type:            "spectrogram-meta",
		SourceID:        sourceID,
		SampleRate:      c.Settings.WebServer.LiveStream.SampleRate,
		FFTSize:         cfg.FFTSize,
		HopSize:         cfg.HopSize,
		Window:          cfg.Window,
		BinCount:        cfg.FFTSize / 2,
		MinFrequencyHz:  0,
		MaxFrequencyHz:  c.Settings.WebServer.LiveStream.SampleRate / 2,
		BatchIntervalMs: cfg.BatchIntervalMs,
	}
	if stream := c.getHLSStream(sourceID); stream != nil && !stream.streamEpoch.IsZero() {
		meta.StreamEpochUnixMs = stream.streamEpoch.UnixMilli()
	}
	return meta
}

func isClosedNetworkError(err error) bool {
	if err == nil {
		return false
	}
	var netErr *net.OpError
	return stderrors.Is(err, context.Canceled) || stderrors.As(err, &netErr)
}
