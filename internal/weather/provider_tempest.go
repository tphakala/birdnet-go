package weather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"syscall"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	weathererrors "github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	tempestProviderName = "tempest"

	// tempestDefaultListenAddress is WeatherFlow's fixed, non-configurable
	// local UDP broadcast port. Binding to an empty host means "all
	// interfaces", which is what's needed to receive a broadcast packet.
	tempestDefaultListenAddress = ":50222"

	// tempestObsMessageType is the only UDP broadcast message type mapped to
	// WeatherData; rapid_wind, hub_status, device_status, and others are
	// ignored (see readLoop).
	tempestObsMessageType = "obs_st"

	// tempestObsFieldCount is the minimum number of fields WeatherFlow's
	// documented obs_st array must have for the indices this parser reads
	// (highest index read is 15, lightning strike count).
	tempestObsFieldCount = 16

	// tempestReadBufferSize comfortably fits a single obs_st JSON payload
	// (observed to be well under 512 bytes) with headroom to spare.
	tempestReadBufferSize = 2048

	// tempestStaleAfter bounds how old a cached observation may be before
	// FetchWeather reports the station as unreachable instead of returning
	// stale data. Tempest broadcasts obs_st roughly once per 60s, so 3x that
	// gives margin for a couple of missed/delayed broadcasts before alarming.
	tempestStaleAfter = 3 * time.Minute

	// Tempest obs_st UDP broadcast array indices (from WeatherFlow's API spec).
	// Each obs_st payload is an array of fields in a fixed order.
	tempestObsIndexTimestamp      = 0  // seconds since epoch
	tempestObsIndexWindLull       = 1  // m/s, lowest 3s wind sample in the interval
	tempestObsIndexWindSpeed      = 2  // m/s, average wind speed
	tempestObsIndexWindGust       = 3  // m/s, wind gust
	tempestObsIndexWindDirection  = 4  // degrees, average wind direction
	tempestObsIndexPressure       = 6  // MB (hPa), station pressure
	tempestObsIndexTemperature    = 7  // °C
	tempestObsIndexHumidity       = 8  // % relative humidity
	tempestObsIndexIlluminance    = 9  // lux
	tempestObsIndexUVIndex        = 10 // index
	tempestObsIndexSolarRadiation = 11 // W/m^2
	tempestObsIndexPrecipitation  = 12 // mm, rain accumulated over interval
	tempestObsIndexPrecipType     = 13 // numeric code (0=none, 1=rain, 2=hail, 3=rain+hail)
	tempestObsIndexLightningDist  = 14 // km, average strike distance over interval
	tempestObsIndexLightningCount = 15 // number of strikes in the interval
)

// tempestObsSTMessage is the subset of WeatherFlow's local UDP "obs_st"
// broadcast this provider cares about. The full message also carries
// serial_number/hub_sn/firmware_revision fields, which are not needed here.
type tempestObsSTMessage struct {
	Type string      `json:"type"`
	Obs  [][]float64 `json:"obs"`
}

// TempestExtras holds Tempest sensor readings with no WeatherData equivalent.
// It is cached and served live (see LatestTempestExtras/GetTempestExtras)
// rather than persisted to the datastore.
type TempestExtras struct {
	Illuminance       float64 // lux
	UVIndex           float64
	SolarRadiation    float64 // W/m^2
	LightningDistance float64 // average strike distance, km
	LightningCount    int     // strikes in the report interval
	WindLull          float64 // m/s, lowest 3s wind sample in the report interval
}

// tempestExtrasJSON is the on-disk shape for HourlyWeather.TempestExtrasJSON.
// Fields use `omitempty` so a field the user didn't check for persistence is
// simply absent from the JSON rather than serialized as a misleading zero.
type tempestExtrasJSON struct {
	Illuminance       *float64 `json:"illuminance,omitempty"`
	UVIndex           *float64 `json:"uv_index,omitempty"`
	SolarRadiation    *float64 `json:"solar_radiation,omitempty"`
	LightningDistance *float64 `json:"lightning_distance,omitempty"`
	LightningCount    *int     `json:"lightning_count,omitempty"`
	WindLull          *float64 `json:"wind_lull,omitempty"`
}

// SelectedJSON marshals only the fields enabled in conf.TempestExtraFields
// (see the Tempest settings UI checkboxes) into HourlyWeather.TempestExtrasJSON's
// string form. Returns nil when nothing is selected, so callers can skip
// persisting an empty/meaningless JSON object.
func (e *TempestExtras) SelectedJSON(fields conf.TempestExtraFields) (*string, error) {
	if e == nil {
		return nil, nil //nolint:nilnil // nil extras with no error means "nothing received yet", not a failure
	}

	var out tempestExtrasJSON
	if fields.Illuminance {
		out.Illuminance = &e.Illuminance
	}
	if fields.UVIndex {
		out.UVIndex = &e.UVIndex
	}
	if fields.SolarRadiation {
		out.SolarRadiation = &e.SolarRadiation
	}
	if fields.LightningDistance {
		out.LightningDistance = &e.LightningDistance
	}
	if fields.LightningCount {
		out.LightningCount = &e.LightningCount
	}
	if fields.WindLull {
		out.WindLull = &e.WindLull
	}

	if out == (tempestExtrasJSON{}) {
		return nil, nil //nolint:nilnil // no fields selected means nothing to persist, not a failure
	}

	data, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("marshal tempest extras: %w", err)
	}
	result := string(data)
	return &result, nil
}

// Start implements the Lifecycler interface: it binds the UDP listener once
// (subsequent calls are no-ops) and runs the read loop until ctx is
// cancelled. Called by Service.StartPolling with a context tied to the poll
// loop's stop channel, so the listener's lifetime matches the service's.
func (p *TempestProvider) Start(ctx context.Context) {
	p.startOnce.Do(func() {
		addr := p.listenAddress
		if addr == "" {
			addr = tempestDefaultListenAddress
		}

		udpAddr, err := net.ResolveUDPAddr("udp", addr)
		if err != nil {
			getLogger().Error("Failed to resolve Tempest listen address",
				logger.Error(err), logger.String("address", addr))
			return
		}

		conn, err := net.ListenUDP("udp", udpAddr)
		if err != nil {
			getLogger().Error("Failed to bind Tempest UDP listener",
				logger.Error(err), logger.String("address", addr))
			return
		}

		getLogger().Info("Tempest UDP listener started", logger.String("address", addr))

		// Closing the connection is the standard way to unblock the read
		// loop's blocking ReadFromUDP call on shutdown.
		go func() {
			<-ctx.Done()
			_ = conn.Close()
		}()

		go p.readLoop(conn)
	})
}

// readLoop continuously reads UDP packets and updates the cached observation.
// It returns once conn is closed (by Start's shutdown goroutine above).
func (p *TempestProvider) readLoop(conn *net.UDPConn) {
	defer func() {
		_ = conn.Close()
	}()

	buf := make([]byte, tempestReadBufferSize)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				getLogger().Info("Tempest UDP listener stopped")
				return
			}
			getLogger().Warn("Tempest UDP read error", logger.Error(err))
			continue
		}

		data, extras, parseErr := parseTempestObsST(buf[:n])
		if parseErr != nil {
			// The hub also broadcasts rapid_wind (~every 3s), hub_status, and
			// device_status messages on the same port; anything that isn't a
			// valid obs_st is expected and not logged above debug level.
			getLogger().Debug("Ignoring non-obs_st Tempest packet", logger.Error(parseErr))
			continue
		}

		p.mu.Lock()
		p.latest = data
		p.extras = extras
		p.receivedAt = time.Now()
		p.mu.Unlock()
	}
}

// parseTempestObsST parses a single obs_st UDP broadcast into WeatherData and
// its accompanying TempestExtras. See WeatherFlow's local UDP API reference
// for the obs array field order; fields with no WeatherData equivalent are
// returned via TempestExtras instead of being dropped.
func parseTempestObsST(raw []byte) (*WeatherData, *TempestExtras, error) {
	var msg tempestObsSTMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil, nil, fmt.Errorf("decode udp packet: %w", err)
	}
	if msg.Type != tempestObsMessageType {
		return nil, nil, fmt.Errorf("not an %s message (got %q)", tempestObsMessageType, msg.Type)
	}
	if len(msg.Obs) == 0 || len(msg.Obs[0]) < tempestObsFieldCount {
		return nil, nil, fmt.Errorf("obs_st message has insufficient fields")
	}
	obs := msg.Obs[0]

	precipType, icon := tempestPrecipTypeAndIcon(int(obs[tempestObsIndexPrecipType]), obs[tempestObsIndexPrecipitation])

	data := &WeatherData{
		Time: time.Unix(int64(obs[tempestObsIndexTimestamp]), 0),
		Wind: Wind{
			Speed: obs[tempestObsIndexWindSpeed],
			Gust:  obs[tempestObsIndexWindGust],
			Deg:   int(obs[tempestObsIndexWindDirection]),
		},
		Pressure: int(math.Round(obs[tempestObsIndexPressure])),
		Temperature: Temperature{
			Current: obs[tempestObsIndexTemperature],
			// obs_st carries no separate apparent-temperature field over local
			// UDP (that's an API-only value), so FeelsLike mirrors Current.
			FeelsLike: obs[tempestObsIndexTemperature],
		},
		Humidity: int(math.Round(obs[tempestObsIndexHumidity])),
		Precipitation: Precipitation{
			Amount: obs[tempestObsIndexPrecipitation],
			Type:   precipType,
		},
		WeatherMain: weatherMainFromIconCode(icon),
		Icon:        string(icon),
		// Visibility, Clouds, and Description have no equivalent in the local
		// UDP protocol (only numeric sensor values are broadcast; no textual
		// or coded sky/visibility condition), so they're left at their zero
		// values. Location is filled in by FetchWeather from the configured
		// BirdNET coordinates, since the hub doesn't broadcast its own GPS fix.
	}

	extras := &TempestExtras{
		Illuminance:       obs[tempestObsIndexIlluminance],
		UVIndex:           obs[tempestObsIndexUVIndex],
		SolarRadiation:    obs[tempestObsIndexSolarRadiation],
		LightningDistance: obs[tempestObsIndexLightningDist],
		LightningCount:    int(obs[tempestObsIndexLightningCount]),
		WindLull:          obs[tempestObsIndexWindLull],
	}

	return data, extras, nil
}

// tempestPrecipTypeAndIcon maps WeatherFlow's numeric precip_type (0=none,
// 1=rain, 2=hail, 3=rain+hail) and the rain-accumulation amount to a
// precipitation type string and a standardized icon code. Only a non-zero
// accumulation is treated as active precipitation, matching how the other
// providers only trust their precip type when a nonzero amount was reported.
func tempestPrecipTypeAndIcon(code int, amount float64) (precipType string, icon IconCode) {
	if amount <= 0 {
		return "", IconUnknown
	}
	switch code {
	case 1:
		return "rain", IconRain
	case 2, 3:
		// No dedicated hail icon exists in the standardized set; sleet is the
		// closest icy-precipitation analog.
		return "hail", IconSleet
	default:
		// No cloud-cover/condition field is available locally, so an unknown
		// precip type with a nonzero amount still can't be depicted with
		// confidence beyond "some precipitation is happening".
		return "", IconUnknown
	}
}

// LatestTempestExtras implements the TempestExtrasProvider interface,
// serving the most recently cached sensor extras.
func (p *TempestProvider) LatestTempestExtras() (*TempestExtras, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.extras == nil {
		return nil, false
	}
	extras := *p.extras
	return &extras, true
}

// FetchWeather implements the Provider interface for TempestProvider. Unlike
// the HTTP-based providers, this never makes a network call: it returns
// whatever the background UDP listener (see Start/readLoop) has most
// recently cached, or an error if nothing has been received yet or the
// cached observation is too old to trust.
func (p *TempestProvider) FetchWeather(_ context.Context, settings *conf.Settings) (*WeatherData, error) {
	p.mu.RLock()
	latest := p.latest
	receivedAt := p.receivedAt
	p.mu.RUnlock()

	if latest == nil {
		return nil, newWeatherError(
			fmt.Errorf("no Tempest observation received yet"),
			weathererrors.CategoryNotFound, "fetch_cached_observation", tempestProviderName,
		)
	}
	if age := time.Since(receivedAt); age > tempestStaleAfter {
		return nil, newWeatherError(
			fmt.Errorf("tempest station unreachable: last observation was %s ago", age.Round(time.Second)),
			weathererrors.CategoryNetwork, "fetch_cached_observation", tempestProviderName,
		)
	}

	// Copy before mutating Location so concurrent callers reading p.latest
	// under RLock never observe a partially-updated struct.
	data := *latest
	data.Location = Location{
		Latitude:  settings.BirdNET.Latitude,
		Longitude: settings.BirdNET.Longitude,
	}
	return &data, nil
}

// CheckTempestListenAddress reports whether listenAddress can be bound,
// without leaving anything listening. Used by the weather settings "test
// connection" flow's connectivity stage. busy=true (with a nil err) means the
// port is already owned by something else - most commonly the real Service's
// own long-lived Tempest listener when a user re-tests while Tempest is
// already the active provider - which callers should treat as a healthy
// sign, not a failure.
func CheckTempestListenAddress(listenAddress string) (busy bool, err error) {
	addr := listenAddress
	if addr == "" {
		addr = tempestDefaultListenAddress
	}
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return false, fmt.Errorf("resolve listen address: %w", err)
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return true, nil
		}
		return false, err
	}
	_ = conn.Close()
	return false, nil
}
