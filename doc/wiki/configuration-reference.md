# Configuration Reference

> Auto-generated from source. Do not edit manually.
>
> For IDE autocomplete, add to the top of your `config.yaml`:
>
> ```yaml
> # yaml-language-server: $schema=https://raw.githubusercontent.com/tphakala/birdnet-go/main/config.schema.json
> ```

## debug

| Setting | Type | Description |
|---------|------|-------------|
| `debug` | boolean | true to enable debug mode |

## logging

| Setting | Type | Description |
|---------|------|-------------|
| `logging.level` | string | debug, info, warn, error (deprecated, use DefaultLevel) |
| `logging.timezone` | string | "Local", "UTC", or IANA timezone name like "Europe/Helsinki" |
| `logging.file` | string | optional log file path (deprecated, use FileOutput) |
| `logging.debug_webhooks` | boolean | if true, logs full webhook details (headers, body, etc.) |
| `logging.default_level` | string | default log level for all modules |
| `logging.console.enabled` | boolean | enable console output |
| `logging.console.level` | string | log level for console output |
| `logging.file_output.enabled` | boolean | enable file output |
| `logging.file_output.path` | string | log file path |
| `logging.file_output.max_size` | integer | maximum size in MB before rotation (0 = disabled) |
| `logging.file_output.max_age` | integer | maximum age in days to keep rotated logs (0 = no limit) |
| `logging.file_output.max_rotated_files` | integer | maximum number of rotated log files to keep (0 = no limit) |
| `logging.file_output.compress` | boolean | compress rotated logs with gzip |
| `logging.file_output.level` | string | log level for file output |
| `logging.modules` | object | per-module output configuration |
| `logging.module_levels` | object | per-module log levels |

## main

| Setting | Type | Description |
|---------|------|-------------|
| `main.name` | string |  |
| `main.timeAs24h` | boolean |  |

## birdnet

| Setting | Type | Description |
|---------|------|-------------|
| `birdnet.version` | string | model version: "2.4", "3.0" |
| `birdnet.debug` | boolean | true to enable debug mode |
| `birdnet.sensitivity` | number | birdnet analysis sigmoid sensitivity |
| `birdnet.threshold` | number | threshold for prediction confidence to report |
| `birdnet.overlap` | number | birdnet analysis overlap between chunks |
| `birdnet.longitude` | number | longitude of recording location for prediction filtering |
| `birdnet.latitude` | number | latitude of recording location for prediction filtering |
| `birdnet.locationConfigured` | boolean | true when location has been explicitly configured by the user |
| `birdnet.threads` | integer | number of CPU threads to use for analysis |
| `birdnet.locale` | string | language to use for labels |
| `birdnet.rangeFilter.debug` | boolean | true to enable debug mode |
| `birdnet.rangeFilter.model` | string | range filter model version: "legacy" for v1, "v3" for geomodel v3.0, or empty/default for v2 |
| `birdnet.rangeFilter.modelPath` | string | path to external meta model file (empty for embedded) |
| `birdnet.rangeFilter.labelsPath` | string | path to geomodel labels file (required when geomodel differs from classifier labels) |
| `birdnet.rangeFilter.threshold` | number | rangefilter species occurrence threshold |
| `birdnet.rangeFilter.passUnmappedSpecies` | boolean | true to pass through species absent from geomodel (score 1.0); false to filter them out (score 0.0) |
| `birdnet.rangeFilter.species` | string[] | list of included species, runtime value |
| `birdnet.rangeFilter.lastUpdated` | string | last time the species list was updated, runtime value |
| `birdnet.modelPath` | string | path to external model file (empty for embedded) |
| `birdnet.labelPath` | string | path to external label file (empty for embedded) |
| `birdnet.useXnnpack` | boolean | true to use XNNPACK delegate for inference acceleration |
| `birdnet.onnxRuntimePath` | string | path to ONNX Runtime shared library (required for ONNX models) |
| `birdnet.openVinoPath` | string | path to libopenvino_c shared library (OpenVINO image variants only) |
| `birdnet.backend` | string | inference backend preference: "auto" (default), "onnx", or "openvino" |
| `birdnet.openVinoDevice` | string | OpenVINO device preference: "auto" (default), "cpu", or "gpu" |

## perch

| Setting | Type | Description |
|---------|------|-------------|
| `perch.modelPath` | string | path to Perch v2 ONNX model file |
| `perch.labelPath` | string | path to Perch v2 label CSV file |
| `perch.threshold` | number | confidence threshold for detections |
| `perch.locale` | string | locale for species label translation |

## bat

| Setting | Type | Description |
|---------|------|-------------|
| `bat.embeddingModel` | string | path to BirdNET v2.4 embeddings ONNX model |
| `bat.classifierModel` | string | path to bat species classifier ONNX model |
| `bat.labelPath` | string | path to bat species labels file |
| `bat.threshold` | number | confidence threshold for bat detections |
| `bat.locale` | string | locale for species label translation |
| `bat.nighttimeOnly` | boolean | restrict bat detection to nighttime (civil dusk to civil dawn) |
| `bat.falsePositiveFilter.level` | integer | Filtering aggressivity level (0-5): 0=Off, 1=Lenient, 2=Moderate, 3=Balanced, 4=Strict, 5=Maximum |
| `bat.ultrasonicFilter.enabled` | boolean | enable ultrasonic validation filter |
| `bat.ultrasonicFilter.cvThreshold` | number | detections with US frame CV below this are tagged unlikely |
| `bat.ultrasonicFilter.fftSize` | integer | FFT window size in samples (must be power of 2) |
| `bat.ultrasonicFilter.hopSize` | integer | STFT hop size in samples |
| `bat.ultrasonicFilter.frequencySplitHz` | integer | boundary between audible and ultrasonic bands in Hz |

## bsg

| Setting | Type | Description |
|---------|------|-------------|
| `bsg.modelPath` | string | path to BSG ONNX model file |
| `bsg.labelPath` | string | path to BSG label file |
| `bsg.locale` | string | locale for species label translation |

## models

| Setting | Type | Description |
|---------|------|-------------|
| `models.enabled` | string[] | list of model IDs to load (e.g., "birdnet", "perch_v2") |
| `models.directory` | string | base directory for downloaded model files |
| `models.installed` | string[] | list of installed model IDs managed by the model gallery |

## lowMemory

| Setting | Type | Description |
|---------|------|-------------|
| `lowMemory.mode` | string | "auto" (default), "on", "off" |

## taxonomySynonyms

Optional scientific-name synonym overrides merged with built-ins

| Setting | Type | Description |
|---------|------|-------------|

## realtime

| Setting | Type | Description |
|---------|------|-------------|
| `realtime.interval` | integer | minimum interval between log messages in seconds |
| `realtime.processingTime` | boolean | true to report processing time for each prediction |
| `realtime.audio.sources` | audio-source-config[] | Audio capture devices |
| `realtime.audio.source` | string | Legacy: migrated to Sources on load |
| `realtime.audio.ffmpegPath` | string | path to ffmpeg, runtime value |
| `realtime.audio.ffmpegVersion` | string | ffmpeg version string, runtime value |
| `realtime.audio.ffmpegMajor` | integer | ffmpeg major version number, runtime value |
| `realtime.audio.ffmpegMinor` | integer | ffmpeg minor version number, runtime value |
| `realtime.audio.soxPath` | string | path to sox, runtime value |
| `realtime.audio.streamTransport` | string | preferred transport for audio streaming: "auto", "sse", or "ws" |
| `realtime.audio.export.debug` | boolean | true to enable audio export debug |
| `realtime.audio.export.enabled` | boolean | export audio clips containing indentified bird calls |
| `realtime.audio.export.path` | string | path to audio clip export directory |
| `realtime.audio.export.type` | string | audio file type, wav, mp3 or flac |
| `realtime.audio.export.bitrate` | string | bitrate for audio export |
| `realtime.audio.export.retention.debug` | boolean | true to enable retention debug |
| `realtime.audio.export.retention.policy` | string | retention policy, "none", "age" or "usage" |
| `realtime.audio.export.retention.maxAge` | string | maximum age of audio clips to keep |
| `realtime.audio.export.retention.maxUsage` | string | maximum disk usage percentage before cleanup |
| `realtime.audio.export.retention.minClips` | integer | minimum number of clips per species to keep |
| `realtime.audio.export.retention.keepSpectrograms` | boolean | true to keep spectrograms |
| `realtime.audio.export.retention.checkInterval` | integer | cleanup check interval in minutes (default: 15) |
| `realtime.audio.export.length` | integer | audio capture length in seconds |
| `realtime.audio.export.preCapture` | integer | pre-capture in seconds |
| `realtime.audio.export.gain` | number | gain in dB for audio capture |
| `realtime.audio.export.normalization.enabled` | boolean | true to enable loudness normalization |
| `realtime.audio.export.normalization.targetLUFS` | number | target integrated loudness in LUFS (default: -23) |
| `realtime.audio.export.normalization.loudnessRange` | number | loudness range in LU (default: 7) |
| `realtime.audio.export.normalization.truePeak` | number | true peak limit in dBTP (default: -2) |
| `realtime.audio.soundLevel.enabled` | boolean | true to enable sound level monitoring |
| `realtime.audio.soundLevel.interval` | integer | measurement interval in seconds (default: 10) |
| `realtime.audio.soundLevel.debug` | boolean | true to enable debug logging for sound level monitoring |
| `realtime.audio.soundLevel.debugRealtimeLogging` | boolean | true to log debug messages for every realtime update, false to log only at configured interval |
| `realtime.audio.equalizer.enabled` | boolean | global flag to enable/disable equalizer filters |
| `realtime.audio.equalizer.filters` | equalizer-filter[] | equalizer filter configuration |
| `realtime.audio.quietHours.enabled` | boolean | true to enable quiet hours |
| `realtime.audio.quietHours.mode` | string | "fixed" or "solar" |
| `realtime.audio.quietHours.startTime` | string | "HH:MM" format for fixed mode (e.g., "22:00") |
| `realtime.audio.quietHours.endTime` | string | "HH:MM" format for fixed mode (e.g., "06:00") |
| `realtime.audio.quietHours.startEvent` | string | "sunset" or "sunrise" for solar mode |
| `realtime.audio.quietHours.startOffset` | integer | minutes relative to start event (positive = after, negative = before) |
| `realtime.audio.quietHours.endEvent` | string | "sunset" or "sunrise" for solar mode |
| `realtime.audio.quietHours.endOffset` | integer | minutes relative to end event (positive = after, negative = before) |
| `realtime.audio.watchdog.checkInterval` | integer | tick period (default 10s) |
| `realtime.audio.watchdog.silenceThreshold` | integer | silence before alarm (default 30s) |
| `realtime.audio.watchdog.maxRetries` | integer | restart attempts before escalation (default 3) |
| `realtime.audio.watchdog.retryBackoff` | integer | wait between retries (default 5s) |
| `realtime.audio.watchdog.cooldown` | integer | alarm suppression after recovery (default 60s) |
| `realtime.audio.watchdog.escalationTimeout` | integer | time in ESCALATED before FAILED (default 60s) |
| `realtime.dashboard.thumbnails.debug` | boolean | true to enable debug mode |
| `realtime.dashboard.thumbnails.summary` | boolean | show thumbnails on summary table |
| `realtime.dashboard.thumbnails.recent` | boolean | show thumbnails on recent table |
| `realtime.dashboard.thumbnails.imageProvider` | string | preferred image provider: "auto", "wikimedia", "avicommons" |
| `realtime.dashboard.thumbnails.fallbackPolicy` | string | fallback policy: "none", "all" - try all available providers if preferred fails |
| `realtime.dashboard.summaryLimit` | integer | limit for the number of species shown in the summary table |
| `realtime.dashboard.locale` | string | UI locale setting |
| `realtime.dashboard.spectrogram.mode` | string | Generation mode: "auto" (default), "prerender", "user-requested" |
| `realtime.dashboard.spectrogram.enabled` | boolean | DEPRECATED: Use Mode instead. Kept for backward compatibility (true = "prerender", false = "auto") |
| `realtime.dashboard.spectrogram.size` | string | Default size for all modes (see recommendations below) |
| `realtime.dashboard.spectrogram.raw` | boolean | Generate raw spectrogram without axes/legend (default: true) |
| `realtime.dashboard.spectrogram.style` | string | Visual style preset: "default", "scientific_dark", "high_contrast_dark", "scientific" |
| `realtime.dashboard.spectrogram.dynamicRange` | string | Dynamic range in dB: "80" (high contrast), "100" (standard), "120" (extended) |
| `realtime.dashboard.temperatureUnit` | string | display unit for temperature: "celsius" or "fahrenheit" |
| `realtime.dashboard.colorScheme` | string | color scheme: "blue", "forest", "amber", "violet", "rose", "custom" |
| `realtime.dashboard.customColors.primary` | string | primary hex color, e.g. "#2563eb" |
| `realtime.dashboard.customColors.accent` | string | accent hex color, e.g. "#0284c7" |
| `realtime.dashboard.logoStyle` | string | logo display style: "gradient" or "solid" |
| `realtime.dashboard.layout.elements` | dashboard-element[] |  |
| `realtime.dashboard.defaultAudioGain` | number | Default playback gain in dB (0-24) |
| `realtime.dashboard.liveSpectrogram` | boolean | auto-start live spectrogram on dashboard |
| `realtime.dynamicThreshold.enabled` | boolean | true to enable dynamic threshold |
| `realtime.dynamicThreshold.debug` | boolean | true to enable debug mode |
| `realtime.dynamicThreshold.trigger` | number | trigger threshold for dynamic threshold |
| `realtime.dynamicThreshold.min` | number | minimum threshold for dynamic threshold |
| `realtime.dynamicThreshold.validHours` | integer | number of hours to consider for dynamic threshold |
| `realtime.falsePositiveFilter.level` | integer | Filtering aggressivity level (0-5): 0=Off, 1=Lenient, 2=Moderate, 3=Balanced, 4=Strict, 5=Maximum |
| `realtime.log.enabled` | boolean |  |
| `realtime.log.path` | string |  |
| `realtime.logDeduplication.enabled` | boolean | true to enable log deduplication |
| `realtime.logDeduplication.healthCheckIntervalSeconds` | integer | Health check interval in seconds (default: 60) |
| `realtime.birdweather.enabled` | boolean | true to enable birdweather uploads |
| `realtime.birdweather.debug` | boolean | true to enable debug mode |
| `realtime.birdweather.id` | string | birdweather ID |
| `realtime.birdweather.threshold` | number | threshold for prediction confidence for uploads |
| `realtime.birdweather.locationAccuracy` | number | accuracy of location in meters |
| `realtime.birdweather.retrySettings.enabled` | boolean | true to enable retry mechanism |
| `realtime.birdweather.retrySettings.maxRetries` | integer | maximum number of retry attempts |
| `realtime.birdweather.retrySettings.initialDelay` | integer | initial delay before first retry in seconds |
| `realtime.birdweather.retrySettings.maxDelay` | integer | maximum delay between retries in seconds |
| `realtime.birdweather.retrySettings.backoffMultiplier` | number | multiplier for exponential backoff |
| `realtime.ebird.enabled` | boolean | true to enable eBird integration |
| `realtime.ebird.apiKey` | string | eBird API key |
| `realtime.ebird.cacheTTL` | integer | cache time-to-live in hours (default: 24) |
| `realtime.ebird.locale` | string | locale for eBird data (e.g., "en", "es") |
| `realtime.privacyFilter.debug` | boolean | true to enable debug mode |
| `realtime.privacyFilter.enabled` | boolean | true to enable privacy filter |
| `realtime.privacyFilter.confidence` | number | confidence threshold for human detection |
| `realtime.dogBarkFilter.debug` | boolean | true to enable debug mode |
| `realtime.dogBarkFilter.enabled` | boolean | true to enable dog bark filter |
| `realtime.dogBarkFilter.confidence` | number | confidence threshold for dog bark detection |
| `realtime.dogBarkFilter.remember` | integer | how long we should remember bark for filtering? |
| `realtime.dogBarkFilter.species` | string[] | species list for filtering |
| `realtime.daylightFilter.debug` | boolean | true to enable debug logging |
| `realtime.daylightFilter.enabled` | boolean | true to enable daylight filter |
| `realtime.daylightFilter.offset` | integer | hours to adjust daylight window; positive = shrink (lenient), negative = expand (strict) |
| `realtime.daylightFilter.species` | string[] | species, families, orders, or genera to filter during daylight |
| `realtime.rtsp.streams` | stream-config[] | Stream configurations |
| `realtime.rtsp.urls` | string[] | Legacy: accepts old format, migrated on load |
| `realtime.rtsp.transport` | string | Legacy: global default, migrated on load |
| `realtime.rtsp.health.healthyDataThreshold` | integer | seconds before stream considered unhealthy (default: 60) |
| `realtime.rtsp.health.monitoringInterval` | integer | health check interval in seconds (default: 30) |
| `realtime.rtsp.ffmpegParameters` | string[] | Custom FFmpeg parameters |
| `realtime.mqtt.enabled` | boolean | true to enable MQTT |
| `realtime.mqtt.debug` | boolean | true to enable MQTT debug |
| `realtime.mqtt.broker` | string | MQTT broker URL |
| `realtime.mqtt.topic` | string | MQTT topic |
| `realtime.mqtt.username` | string | MQTT username |
| `realtime.mqtt.password` | string | MQTT password |
| `realtime.mqtt.retain` | boolean | true to retain messages |
| `realtime.mqtt.retrySettings.enabled` | boolean | true to enable retry mechanism |
| `realtime.mqtt.retrySettings.maxRetries` | integer | maximum number of retry attempts |
| `realtime.mqtt.retrySettings.initialDelay` | integer | initial delay before first retry in seconds |
| `realtime.mqtt.retrySettings.maxDelay` | integer | maximum delay between retries in seconds |
| `realtime.mqtt.retrySettings.backoffMultiplier` | number | multiplier for exponential backoff |
| `realtime.mqtt.tls.enabled` | boolean | true to enable TLS (auto-detected from broker URL) |
| `realtime.mqtt.tls.insecureSkipVerify` | boolean | true to skip certificate verification (for self-signed certs) |
| `realtime.mqtt.tls.caCert` | string | path to CA certificate file (managed internally) |
| `realtime.mqtt.tls.clientCert` | string | path to client certificate file (managed internally) |
| `realtime.mqtt.tls.clientKey` | string | path to client key file (managed internally) |
| `realtime.mqtt.homeAssistant.enabled` | boolean | true to enable HA auto-discovery |
| `realtime.mqtt.homeAssistant.discoveryPrefix` | string | HA discovery topic prefix (default: homeassistant) |
| `realtime.mqtt.homeAssistant.deviceName` | string | base name for devices (default: BirdNET-Go) |
| `realtime.telemetry.enabled` | boolean | true to enable Prometheus compatible telemetry endpoint |
| `realtime.telemetry.listen` | string | IP address and port to listen on |
| `realtime.monitoring.enabled` | boolean | true to enable system resource monitoring |
| `realtime.monitoring.checkInterval` | integer | interval in seconds between resource checks |
| `realtime.monitoring.cpu.enabled` | boolean | true to enable monitoring for this resource |
| `realtime.monitoring.memory.enabled` | boolean | true to enable monitoring for this resource |
| `realtime.monitoring.disk.enabled` | boolean | true to enable disk monitoring |
| `realtime.monitoring.disk.paths` | string[] | filesystem paths to monitor |
| `realtime.species.include` | string[] | Always include these species |
| `realtime.species.exclude` | string[] | Always exclude these species |
| `realtime.species.config` | object | Per-species configuration (keys normalized to lowercase) |
| `realtime.weather.provider` | string | "none", "yrno", "openweather", or "wunderground" |
| `realtime.weather.pollInterval` | integer | weather data polling interval in minutes |
| `realtime.weather.debug` | boolean | true to enable debug mode |
| `realtime.weather.openWeather.enabled` | boolean | true to enable OpenWeather integration, for legacy support |
| `realtime.weather.openWeather.apiKey` | string | OpenWeather API key |
| `realtime.weather.openWeather.endpoint` | string | OpenWeather API endpoint |
| `realtime.weather.openWeather.units` | string | units of measurement: standard, metric, or imperial |
| `realtime.weather.openWeather.language` | string | language code for the response |
| `realtime.weather.wunderground.apiKey` | string | WeatherUnderground API key |
| `realtime.weather.wunderground.stationId` | string | WeatherUnderground station ID |
| `realtime.weather.wunderground.endpoint` | string | WeatherUnderground API endpoint |
| `realtime.weather.wunderground.units` | string | units of measurement: "e" (imperial), "m" (metric), "h" (UK hybrid) |
| `realtime.speciesTracking.enabled` | boolean | true to enable new species tracking |
| `realtime.speciesTracking.newSpeciesWindowDays` | integer | Days to consider a species "new" (default: 14) |
| `realtime.speciesTracking.syncIntervalMinutes` | integer | Interval to sync with database (default: 60) |
| `realtime.speciesTracking.notificationSuppressionHours` | integer | Hours to suppress duplicate notifications (default: 168) |
| `realtime.speciesTracking.yearlyTracking.enabled` | boolean | true to enable yearly tracking |
| `realtime.speciesTracking.yearlyTracking.resetMonth` | integer | Month to reset yearly tracking (1=January, default: 1) |
| `realtime.speciesTracking.yearlyTracking.resetDay` | integer | Day to reset yearly tracking (default: 1) |
| `realtime.speciesTracking.yearlyTracking.windowDays` | integer | Days to show "new this year" indicator (default: 30) |
| `realtime.speciesTracking.seasonalTracking.enabled` | boolean | true to enable seasonal tracking |
| `realtime.speciesTracking.seasonalTracking.windowDays` | integer | Days to show "new this season" indicator (default: 21) |
| `realtime.speciesTracking.seasonalTracking.seasons` | object | Season definitions |
| `realtime.extendedCapture.enabled` | boolean |  |
| `realtime.extendedCapture.maxDuration` | integer |  |
| `realtime.extendedCapture.captureBufferSeconds` | integer |  |
| `realtime.extendedCapture.species` | string[] |  |

## webServer

| Setting | Type | Description |
|---------|------|-------------|
| `webServer.debug` | boolean | true to enable debug mode |
| `webServer.enabled` | boolean | true to enable web server |
| `webServer.port` | string | port for web server |
| `webServer.basePath` | string | reverse proxy subpath prefix (e.g., "/birdnet") |
| `webServer.allowEmbedding` | boolean | true to allow embedding in iframes (e.g., Home Assistant) |
| `webServer.liveStream.debug` | boolean | true to enable debug mode |
| `webServer.liveStream.bitRate` | integer | bitrate for live stream in kbps |
| `webServer.liveStream.sampleRate` | integer | sample rate for live stream in Hz |
| `webServer.liveStream.segmentLength` | integer | length of each segment in seconds |
| `webServer.liveStream.ffmpegLogLevel` | string | log level for ffmpeg |
| `webServer.enableTerminal` | boolean | Enable browser terminal (security risk) |

## security

| Setting | Type | Description |
|---------|------|-------------|
| `security.debug` | boolean | true to enable debug mode |
| `security.baseUrl` | string | BaseURL is the complete external URL for this instance, including scheme, host, and optional port (e.g., "https://birdnet.example.com:5500"). Used for generating OAuth redirect URLs and notification links. Takes precedence over Host when set. Can be overridden with BIRDNET_URL environment variable. NOTE: This field is prepared for future implementation (issue #1462) |
| `security.host` | string | Host is the primary hostname used for TLS certificates, OAuth redirect URLs, and notification link generation. Required when using AutoTLS or authentication providers. Also used to generate URLs in push notifications - set this to your public hostname when using a reverse proxy. Can be overridden with BIRDNET_HOST environment variable. |
| `security.autoTls` | boolean |  |
| `security.tlsMode` | string | TLSMode controls TLS certificate management. Valid values:   "" (none)      - TLS disabled   "autotls"      - automatic via Let's Encrypt   "manual"       - user-provided certificates   "selfsigned"   - auto-generated self-signed certificates |
| `security.selfSignedValidity` | string | SelfSignedValidity is the validity duration for self-signed certificates. Uses Go duration format with day/month suffixes (e.g., "365d", "1y"). |
| `security.tlsPort` | string | port for HTTPS (default: 8443) |
| `security.redirectToHttps` | boolean | true to redirect to HTTPS |
| `security.allowSubnetBypass.enabled` | boolean | true to enable subnet bypass |
| `security.allowSubnetBypass.subnet` | string | disable OAuth2 in subnet |
| `security.trustedProxies` | string[] | TrustedProxies lists reverse proxies (CIDR ranges or bare IPs) whose forwarded client-IP headers (CF-Connecting-IP, X-Forwarded-For, X-Real-IP) may be trusted. Loopback, link-local, and private (RFC1918/ULA) peers are always trusted in addition to these, so a default port-forwarded install and the common local cloudflared topology work without configuration. When the immediate peer is not trusted, forwarded headers are ignored and the real connection address is used, preventing source-IP spoofing on a directly exposed instance. The reserved value "cloudflare" (TrustedProxyCloudflarePreset) expands to Cloudflare's published edge ranges. Hot-reloadable. |
| `security.publicAccess.liveAudio` | boolean | allow unauthenticated users to start/listen to live audio streams |
| `security.privateMode` | boolean | PrivateMode, when true, requires the user to authenticate before any UI data is shown. Enforcement lives at the v2 API data layer, which returns 401 to unauthenticated requests; the public SPA shell is still served so it can render a login form instead of the dashboard, detections, analytics, search, about, and notifications views. Settings and system routes are additionally auth-gated at the HTTP layer. PublicAccess.LiveAudio still applies independently. Default is false to preserve guest-friendly upstream behavior. |
| `security.basicAuth.enabled` | boolean | true to enable password authentication |
| `security.basicAuth.password` | string | password for admin interface |
| `security.basicAuth.clientId` | string | client id for OAuth2 |
| `security.basicAuth.clientSecret` | string | client secret for OAuth2 |
| `security.basicAuth.redirectUri` | string | redirect uri for OAuth2 |
| `security.basicAuth.authCodeExp` | integer | duration for authorization code |
| `security.basicAuth.accessTokenExp` | integer | duration for access token |
| `security.oauthProviders` | o-auth-provider-config[] | OAuthProviders is the new array-based OAuth configuration. This is the preferred format for configuring OAuth providers. |
| `security.googleAuth.enabled` | boolean | true to enable social provider |
| `security.googleAuth.clientId` | string | client id for OAuth2 |
| `security.googleAuth.clientSecret` | string | client secret for OAuth2 |
| `security.googleAuth.redirectUri` | string | redirect uri for OAuth2 |
| `security.googleAuth.userId` | string | valid user id for OAuth2 |
| `security.githubAuth.enabled` | boolean | true to enable social provider |
| `security.githubAuth.clientId` | string | client id for OAuth2 |
| `security.githubAuth.clientSecret` | string | client secret for OAuth2 |
| `security.githubAuth.redirectUri` | string | redirect uri for OAuth2 |
| `security.githubAuth.userId` | string | valid user id for OAuth2 |
| `security.microsoftAuth.enabled` | boolean | true to enable social provider |
| `security.microsoftAuth.clientId` | string | client id for OAuth2 |
| `security.microsoftAuth.clientSecret` | string | client secret for OAuth2 |
| `security.microsoftAuth.redirectUri` | string | redirect uri for OAuth2 |
| `security.microsoftAuth.userId` | string | valid user id for OAuth2 |
| `security.sessionSecret` | string | secret for session cookie |
| `security.sessionDuration` | integer | duration for browser session cookies |

## sentry

| Setting | Type | Description |
|---------|------|-------------|
| `sentry.enabled` | boolean | true to enable Sentry error tracking (opt-in) |
| `sentry.debug` | boolean | true to enable transparent telemetry logging |

## output

| Setting | Type | Description |
|---------|------|-------------|
| `output.sqlite.enabled` | boolean |  |
| `output.sqlite.path` | string |  |
| `output.mysql.enabled` | boolean |  |
| `output.mysql.username` | string |  |
| `output.mysql.password` | string |  |
| `output.mysql.database` | string |  |
| `output.mysql.host` | string |  |
| `output.mysql.port` | string |  |

## backup

| Setting | Type | Description |
|---------|------|-------------|
| `backup.enabled` | boolean | Global flag to enable or disable the entire backup system. If false, no backups (manual or scheduled) will occur. |
| `backup.debug` | boolean | If true, enables detailed debug logging for backup operations. |
| `backup.encryption` | boolean | If true, enables encryption for backup archives. Requires EncryptionKey to be set. |
| `backup.encryptionKey` | string | Base64-encoded encryption key used for AES-256-GCM encryption of backup archives. Must be kept secret and safe. |
| `backup.sanitizeConfig` | boolean | If true, sensitive information (like passwords, API keys) will be removed from the configuration file copy that is included in the backup archive. |
| `backup.retention.maxAge` | string | Duration string for the maximum age of backups to keep (e.g., "30d" for 30 days, "6m" for 6 months, "1y" for 1 year). Backups older than this may be deleted. |
| `backup.retention.maxBackups` | integer | Maximum total number of backups to keep for a given source. If 0, no limit by count (only by age or MinBackups). |
| `backup.retention.minBackups` | integer | Minimum number of recent backups to keep for a given source, regardless of their age. This ensures a baseline number of backups are always available. |
| `backup.targets` | backup-target[] | A list of configured backup targets (destinations) where backup archives will be stored. |
| `backup.schedules` | backup-schedule-config[] | A list of schedules (e.g., daily, weekly) that define when automatic backups should run. |
| `backup.operationTimeouts.backup` | integer |  |
| `backup.operationTimeouts.store` | integer |  |
| `backup.operationTimeouts.cleanup` | integer |  |
| `backup.operationTimeouts.delete` | integer |  |

## notification

| Setting | Type | Description |
|---------|------|-------------|
| `notification.push.enabled` | boolean |  |
| `notification.push.default_timeout` | integer |  |
| `notification.push.max_retries` | integer |  |
| `notification.push.retry_delay` | integer |  |
| `notification.push.circuit_breaker.enabled` | boolean |  |
| `notification.push.circuit_breaker.max_failures` | integer |  |
| `notification.push.circuit_breaker.timeout` | integer |  |
| `notification.push.circuit_breaker.half_open_max_requests` | integer |  |
| `notification.push.health_check.enabled` | boolean |  |
| `notification.push.health_check.interval` | integer |  |
| `notification.push.health_check.timeout` | integer |  |
| `notification.push.rate_limiting.enabled` | boolean |  |
| `notification.push.rate_limiting.requests_per_minute` | integer |  |
| `notification.push.rate_limiting.burst_size` | integer |  |
| `notification.push.providers` | push-provider-config[] |  |
| `notification.push.minConfidenceThreshold` | number | 0.0-1.0, 0 = disabled |
| `notification.push.speciesCooldownMinutes` | integer | 0 = disabled |
| `notification.templates.newSpecies.title` | string |  |
| `notification.templates.newSpecies.message` | string |  |

## alerting

| Setting | Type | Description |
|---------|------|-------------|
| `alerting.historyRetentionDays` | integer | Days to retain alert history (0 = unlimited) |

