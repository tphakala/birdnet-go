package weather

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sampleObsSTPacket returns a minimal but well-formed obs_st JSON payload
// matching WeatherFlow's documented field order. epoch controls obs[0] so
// staleness tests can control the reported observation time.
func sampleObsSTPacket(epoch int64) []byte {
	return []byte(fmt.Sprintf(
		`{"serial_number":"ST-00000512","type":"obs_st","hub_sn":"HB-00013030",`+
			`"obs":[[%d,0.5,1.2,2.4,180,3,1017.2,18.5,62,9000,3,275,0.6,1,1.1,2,2.8,1]],`+
			`"firmware_revision":149}`,
		epoch,
	))
}

func TestParseTempestObsST(t *testing.T) {
	epoch := time.Now().Unix()

	data, extras, err := parseTempestObsST(sampleObsSTPacket(epoch))

	require.NoError(t, err)
	require.NotNil(t, data)
	require.NotNil(t, extras)

	assert.Equal(t, epoch, data.Time.Unix())
	assert.InDelta(t, 1.2, data.Wind.Speed, 0.001)
	assert.InDelta(t, 2.4, data.Wind.Gust, 0.001)
	assert.Equal(t, 180, data.Wind.Deg)
	assert.Equal(t, 1017, data.Pressure)
	assert.InDelta(t, 18.5, data.Temperature.Current, 0.001)
	assert.InDelta(t, 18.5, data.Temperature.FeelsLike, 0.001)
	assert.Equal(t, 62, data.Humidity)
	assert.InDelta(t, 0.6, data.Precipitation.Amount, 0.001)
	assert.Equal(t, "rain", data.Precipitation.Type)
	assert.Equal(t, string(IconRain), data.Icon)
	assert.Equal(t, "Rain", data.WeatherMain)

	assert.InDelta(t, 9000, extras.Illuminance, 0.001)
	assert.InDelta(t, 3, extras.UVIndex, 0.001)
	assert.InDelta(t, 275, extras.SolarRadiation, 0.001)
	assert.InDelta(t, 1.1, extras.LightningDistance, 0.001)
	assert.Equal(t, 2, extras.LightningCount)
}

func TestParseTempestObsST_NoPrecipitation(t *testing.T) {
	// precip_type=0 (none), rain accumulation=0: icon must be Unknown, not
	// guessed as clear sky, since the local protocol has no sky-condition field.
	raw := []byte(`{"type":"obs_st","obs":[[1700000000,0,0,0,0,3,1013,15,50,100,0,0,0,0,0,0,2.8,1]]}`)

	data, _, err := parseTempestObsST(raw)

	require.NoError(t, err)
	assert.Empty(t, data.Precipitation.Type)
	assert.Equal(t, string(IconUnknown), data.Icon)
	assert.Empty(t, data.WeatherMain)
}

func TestParseTempestObsST_Hail(t *testing.T) {
	raw := []byte(`{"type":"obs_st","obs":[[1700000000,0,0,0,0,3,1013,0,80,0,0,0,1.5,2,0,0,2.8,1]]}`)

	data, _, err := parseTempestObsST(raw)

	require.NoError(t, err)
	assert.Equal(t, "hail", data.Precipitation.Type)
	assert.Equal(t, string(IconSleet), data.Icon)
}

func TestParseTempestObsST_RejectsWrongMessageType(t *testing.T) {
	raw := []byte(`{"type":"rapid_wind","ob":[1700000000,2.3,180]}`)

	_, _, err := parseTempestObsST(raw)

	require.Error(t, err)
}

func TestParseTempestObsST_RejectsMalformedJSON(t *testing.T) {
	_, _, err := parseTempestObsST([]byte(`not json`))
	require.Error(t, err)
}

func TestParseTempestObsST_RejectsInsufficientFields(t *testing.T) {
	raw := []byte(`{"type":"obs_st","obs":[[1700000000,0,0]]}`)

	_, _, err := parseTempestObsST(raw)

	require.Error(t, err)
}

func TestTempestProvider_FetchWeather_NoDataYet(t *testing.T) {
	provider := &TempestProvider{}
	settings := createTestSettings(t, "tempest")

	_, err := provider.FetchWeather(t.Context(), settings)

	require.Error(t, err)
}

func TestTempestProvider_FetchWeather_Stale(t *testing.T) {
	provider := &TempestProvider{
		latest:     &WeatherData{Temperature: Temperature{Current: 20}},
		receivedAt: time.Now().Add(-tempestStaleAfter - time.Minute),
	}
	settings := createTestSettings(t, "tempest")

	_, err := provider.FetchWeather(t.Context(), settings)

	require.Error(t, err, "an observation older than tempestStaleAfter must be treated as unreachable")
}

func TestTempestProvider_FetchWeather_FreshDataFillsLocationFromSettings(t *testing.T) {
	provider := &TempestProvider{
		latest:     &WeatherData{Temperature: Temperature{Current: 20}},
		receivedAt: time.Now(),
	}
	settings := createTestSettings(t, "tempest")

	data, err := provider.FetchWeather(t.Context(), settings)

	require.NoError(t, err)
	assert.InDelta(t, settings.BirdNET.Latitude, data.Location.Latitude, 0.0001)
	assert.InDelta(t, settings.BirdNET.Longitude, data.Location.Longitude, 0.0001)
}

func TestTempestProvider_LatestTempestExtras(t *testing.T) {
	provider := &TempestProvider{}

	_, ok := provider.LatestTempestExtras()
	assert.False(t, ok, "no extras should be reported before any observation arrives")

	provider.extras = &TempestExtras{Illuminance: 1234, UVIndex: 4}
	extras, ok := provider.LatestTempestExtras()
	require.True(t, ok)
	assert.InDelta(t, 1234, extras.Illuminance, 0.001)
	assert.InDelta(t, 4, extras.UVIndex, 0.001)
}

// TestTempestProvider_StartAndReadLoop exercises the real UDP listener
// end-to-end: Start binds a socket on an ephemeral loopback port, a real UDP
// packet is sent to it, and FetchWeather/LatestTempestExtras must reflect the
// received observation. Confirms the ctx-cancellation shutdown path too.
func TestTempestProvider_StartAndReadLoop(t *testing.T) {
	provider := &TempestProvider{listenAddress: "127.0.0.1:0"}

	// Bind first ourselves only to reserve a free port, then hand that exact
	// address to the provider so the test can target a known port.
	probe, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	require.NoError(t, err)
	addr := probe.LocalAddr().String()
	require.NoError(t, probe.Close())
	provider.listenAddress = addr

	ctx, cancel := context.WithCancel(t.Context())
	provider.Start(ctx)

	// Start's bind happens synchronously inside startOnce before the read-loop
	// goroutine is spawned, but give the listener a moment to be scheduled.
	require.Eventually(t, func() bool {
		conn, dialErr := net.Dial("udp", addr)
		if dialErr != nil {
			return false
		}
		defer func() { _ = conn.Close() }()
		_, writeErr := conn.Write(sampleObsSTPacket(time.Now().Unix()))
		return writeErr == nil
	}, 2*time.Second, 20*time.Millisecond)

	settings := createTestSettings(t, "tempest")
	require.Eventually(t, func() bool {
		_, fetchErr := provider.FetchWeather(t.Context(), settings)
		return fetchErr == nil
	}, 2*time.Second, 20*time.Millisecond, "provider should observe the sent packet")

	_, ok := provider.LatestTempestExtras()
	assert.True(t, ok)

	cancel()
}
