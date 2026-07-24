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

LoggingConfig represents logging configuration

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
| `logging.modules` | any |  |
| `logging.module_levels` | any |  |

## main

| Setting | Type | Description |
|---------|------|-------------|
| `main.name` | string |  |
| `main.timeas24h` | boolean |  |

## birdnet

BirdNET configuration

| Setting | Type | Description |
|---------|------|-------------|
| `birdnet.version` | string | model version: "2.4", "3.0" |
| `birdnet.debug` | boolean | true to enable debug mode |
| `birdnet.sensitivity` | number | birdnet analysis sigmoid sensitivity |
| `birdnet.threshold` | number | threshold for prediction confidence to report |
| `birdnet.overlap` | number | birdnet analysis overlap between chunks |
| `birdnet.longitude` | number | longitude of recording location for prediction filtering |
| `birdnet.latitude` | number | latitude of recording location for prediction filtering |
| `birdnet.locationconfigured` | boolean | true when location has been explicitly configured by the user |
| `birdnet.threads` | integer | number of CPU threads to use for analysis |
| `birdnet.locale` | string | language to use for labels |
| `birdnet.rangefilter.debug` | boolean | true to enable debug mode |
| `birdnet.rangefilter.model` | string | range filter model version: "legacy" for v1, "v3" for geomodel v3.0, or empty/default for v2 |
| `birdnet.rangefilter.modelpath` | string | path to external meta model file (empty for embedded) |
| `birdnet.rangefilter.labelspath` | string | path to geomodel labels file (required when geomodel differs from classifier labels) |
| `birdnet.rangefilter.threshold` | number | rangefilter species occurrence threshold |
| `birdnet.rangefilter.passunmappedspecies` | boolean | true to pass through species absent from geomodel (score 1.0); false to filter them out (score 0.0) |
| `birdnet.modelpath` | string | path to external model file (empty for embedded) |
| `birdnet.labelpath` | string | path to external label file (empty for embedded) |
| `birdnet.usexnnpack` | boolean | true to use XNNPACK delegate for inference acceleration |
| `birdnet.onnxruntimepath` | string | path to ONNX Runtime shared library (required for ONNX models) |
| `birdnet.openvinopath` | string | path to libopenvino_c shared library (OpenVINO image variants only) |
| `birdnet.backend` | string | inference backend preference: "auto" (default), "onnx", or "openvino" |
| `birdnet.openvinodevice` | string | OpenVINO device preference: "auto" (default), "cpu", or "gpu" |

## perch

PerchConfig holds configuration for the Google Perch v2 model.

| Setting | Type | Description |
|---------|------|-------------|
| `perch.modelpath` | string | path to Perch v2 ONNX model file |
| `perch.labelpath` | string | path to Perch v2 label CSV file |
| `perch.threshold` | number | confidence threshold for detections |
| `perch.locale` | string | locale for species label translation |

## birdnetv3

BirdNETV3Config holds configuration for the BirdNET v3.0 acoustic classifier.

| Setting | Type | Description |
|---------|------|-------------|
| `birdnetv3.modelpath` | string | path to BirdNET v3.0 ONNX model file |
| `birdnetv3.labelpath` | string | path to BirdNET v3.0 label file |
| `birdnetv3.threshold` | number | confidence threshold for detections |
| `birdnetv3.locale` | string | locale for species label translation |

## bat

BatConfig holds configuration for bat detection using BirdNET v2.4 embeddings.

| Setting | Type | Description |
|---------|------|-------------|
| `bat.embeddingmodel` | string | path to BirdNET v2.4 embeddings ONNX model |
| `bat.classifiermodel` | string | path to bat species classifier ONNX model |
| `bat.labelpath` | string | path to bat species labels file |
| `bat.threshold` | number | confidence threshold for bat detections |
| `bat.locale` | string | locale for species label translation |
| `bat.nighttimeonly` | boolean | restrict bat detection to nighttime (civil dusk to civil dawn) |
| `bat.falsepositivefilter.level` | integer | Filtering aggressivity level (0-5): 0=Off, 1=Lenient, 2=Moderate, 3=Balanced, 4=Strict, 5=Maximum |
| `bat.ultrasonicfilter.enabled` | boolean | enable ultrasonic validation filter |
| `bat.ultrasonicfilter.cvthreshold` | number | detections with US frame CV below this are tagged unlikely |
| `bat.ultrasonicfilter.fftsize` | integer | FFT window size in samples (must be power of 2) |
| `bat.ultrasonicfilter.hopsize` | integer | STFT hop size in samples |
| `bat.ultrasonicfilter.frequencysplithz` | integer | boundary between audible and ultrasonic bands in Hz |

## bsg

BSGConfig holds configuration for BSG regional bird models.

| Setting | Type | Description |
|---------|------|-------------|
| `bsg.modelpath` | string | path to BSG ONNX model file |
| `bsg.labelpath` | string | path to BSG label file |
| `bsg.locale` | string | locale for species label translation |

## models

ModelsConfig holds global model enablement and management settings.

| Setting | Type | Description |
|---------|------|-------------|
| `models.enabled` | string[] | list of model IDs to load (e.g., "birdnet", "perch_v2") |
| `models.directory` | string | base directory for downloaded model files |
| `models.installed` | string[] | list of installed model IDs managed by the model gallery |

## lowmemory

LowMemoryConfig is the manual override for the runtime memory policy.

| Setting | Type | Description |
|---------|------|-------------|
| `lowmemory.mode` | string | "auto" (default), "on", "off" |

## taxonomySynonyms

| Setting | Type | Description |
|---------|------|-------------|

## realtime

RealtimeSettings contains all settings related to realtime processing.

| Setting | Type | Description |
|---------|------|-------------|
| `realtime.interval` | integer | minimum interval between log messages in seconds |
| `realtime.processingtime` | boolean | true to report processing time for each prediction |
| `realtime.audio.sources` | audio-source-config[] | Audio capture devices |
| `realtime.audio.source` | string | Legacy: migrated to Sources on load |
| `realtime.audio.ffmpegpath` | string | path to ffmpeg, runtime value |
| `realtime.audio.soxpath` | string | path to sox, runtime value |
| `realtime.audio.streamtransport` | string | preferred transport for audio streaming: "auto", "sse", or "ws" |
| `realtime.audio.export.debug` | boolean | true to enable audio export debug |
| `realtime.audio.export.enabled` | boolean | export audio clips containing indentified bird calls |
| `realtime.audio.export.path` | string | path to audio clip export directory |
| `realtime.audio.export.type` | string | audio file type, wav, mp3 or flac |
| `realtime.audio.export.bitrate` | string | bitrate for audio export |
| `realtime.audio.export.retention.debug` | boolean | true to enable retention debug |
| `realtime.audio.export.retention.policy` | string | retention policy, "none", "age" or "usage" |
| `realtime.audio.export.retention.maxage` | string | maximum age of audio clips to keep |
| `realtime.audio.export.retention.maxusage` | string | maximum disk usage percentage before cleanup |
| `realtime.audio.export.retention.minclips` | integer | minimum number of clips per species to keep |
| `realtime.audio.export.retention.keepspectrograms` | boolean | true to keep spectrograms |
| `realtime.audio.export.retention.checkinterval` | integer | cleanup check interval in minutes (default: 15) |
| `realtime.audio.export.length` | integer | audio capture length in seconds |
| `realtime.audio.export.precapture` | integer | pre-capture in seconds |
| `realtime.audio.export.gain` | number | gain in dB for audio capture |
| `realtime.audio.export.normalization.enabled` | boolean | true to enable loudness normalization |
| `realtime.audio.export.normalization.targetlufs` | number | target integrated loudness in LUFS (default: -23) |
| `realtime.audio.export.normalization.loudnessrange` | number | Deprecated: no longer applied; retained so existing configs keep loading. |
| `realtime.audio.export.normalization.truepeak` | number | true peak limit in dBTP (default: -2) |
| `realtime.audio.soundlevel.enabled` | boolean | true to enable sound level monitoring |
| `realtime.audio.soundlevel.interval` | integer | measurement interval in seconds (default: 10) |
| `realtime.audio.soundlevel.debug` | boolean | true to enable debug logging for sound level monitoring |
| `realtime.audio.soundlevel.debug_realtime_logging` | boolean | true to log debug messages for every realtime update, false to log only at configured interval |
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
| `realtime.dashboard.thumbnails.imageprovider` | string | preferred image provider: "auto", "wikimedia", "avicommons" |
| `realtime.dashboard.thumbnails.fallbackpolicy` | string | fallback policy: "none", "all" - try all available providers if preferred fails |
| `realtime.dashboard.summarylimit` | integer | limit for the number of species shown in the summary table |
| `realtime.dashboard.locale` | string | UI locale setting |
| `realtime.dashboard.spectrogram.mode` | string | Generation mode: "auto" (default), "prerender", "user-requested" |
| `realtime.dashboard.spectrogram.enabled` | boolean | DEPRECATED: Use Mode instead. Kept for backward compatibility (true = "prerender", false = "auto") |
| `realtime.dashboard.spectrogram.size` | string | Default size for all modes (see recommendations below) |
| `realtime.dashboard.spectrogram.raw` | boolean | Generate raw spectrogram without axes/legend (default: true) |
| `realtime.dashboard.spectrogram.style` | string | Visual style preset: "default", "scientific_dark", "high_contrast_dark", "scientific" |
| `realtime.dashboard.spectrogram.dynamicrange` | string | Dynamic range in dB: "80" (high contrast), "100" (standard), "120" (extended) |
| `realtime.dashboard.temperatureunit` | string | display unit for temperature: "celsius" or "fahrenheit" |
| `realtime.dashboard.colorscheme` | string | color scheme: "blue", "forest", "amber", "violet", "rose", "custom" |
| `realtime.dashboard.customcolors.primary` | string | primary hex color, e.g. "#2563eb" |
| `realtime.dashboard.customcolors.accent` | string | accent hex color, e.g. "#0284c7" |
| `realtime.dashboard.logostyle` | string | logo display style: "gradient" or "solid" |
| `realtime.dashboard.layout.elements` | dashboard-element[] |  |
| `realtime.dashboard.defaultaudiogain` | number | Default playback gain in dB (0-24) |
| `realtime.dashboard.livespectrogram` | boolean | auto-start live spectrogram on dashboard |
| `realtime.dashboard.rarity.enabled` | boolean | show the rare-species highlight icon on detections |
| `realtime.dashboard.rarity.threshold` | number | occurrence probability (0-1) at or below which a detection is flagged rare |
| `realtime.dynamicthreshold.enabled` | boolean | true to enable dynamic threshold |
| `realtime.dynamicthreshold.debug` | boolean | true to enable debug mode |
| `realtime.dynamicthreshold.trigger` | number | trigger threshold for dynamic threshold |
| `realtime.dynamicthreshold.min` | number | minimum threshold for dynamic threshold |
| `realtime.dynamicthreshold.validhours` | integer | number of hours to consider for dynamic threshold |
| `realtime.falsepositivefilter.level` | integer | Filtering aggressivity level (0-5): 0=Off, 1=Lenient, 2=Moderate, 3=Balanced, 4=Strict, 5=Maximum |
| `realtime.log.enabled` | boolean |  |
| `realtime.log.path` | string |  |
| `realtime.logdeduplication.enabled` | boolean | true to enable log deduplication |
| `realtime.logdeduplication.healthcheckintervalseconds` | integer | Health check interval in seconds (default: 60) |
| `realtime.birdweather.enabled` | boolean | true to enable birdweather uploads |
| `realtime.birdweather.debug` | boolean | true to enable debug mode |
| `realtime.birdweather.id` | string | birdweather ID |
| `realtime.birdweather.threshold` | number | threshold for prediction confidence for uploads |
| `realtime.birdweather.locationaccuracy` | number | accuracy of location in meters |
| `realtime.birdweather.retrysettings.enabled` | boolean | true to enable retry mechanism |
| `realtime.birdweather.retrysettings.maxretries` | integer | maximum number of retry attempts |
| `realtime.birdweather.retrysettings.initialdelay` | integer | initial delay before first retry in seconds |
| `realtime.birdweather.retrysettings.maxdelay` | integer | maximum delay between retries in seconds |
| `realtime.birdweather.retrysettings.backoffmultiplier` | number | multiplier for exponential backoff |
| `realtime.ebird.enabled` | boolean | true to enable eBird integration |
| `realtime.ebird.apikey` | string | eBird API key |
| `realtime.ebird.cachettl` | integer | cache time-to-live in hours (default: 24) |
| `realtime.ebird.locale` | string | locale for eBird data (e.g., "en", "es") |
| `realtime.privacyfilter.debug` | boolean | true to enable debug mode |
| `realtime.privacyfilter.enabled` | boolean | true to enable privacy filter |
| `realtime.privacyfilter.confidence` | number | confidence threshold for human detection |
| `realtime.dogbarkfilter.debug` | boolean | true to enable debug mode |
| `realtime.dogbarkfilter.enabled` | boolean | true to enable dog bark filter |
| `realtime.dogbarkfilter.confidence` | number | confidence threshold for dog bark detection |
| `realtime.dogbarkfilter.remember` | integer | how long we should remember bark for filtering? |
| `realtime.dogbarkfilter.species` | string[] | species list for filtering |
| `realtime.daylightfilter.debug` | boolean | true to enable debug logging |
| `realtime.daylightfilter.enabled` | boolean | true to enable daylight filter |
| `realtime.daylightfilter.offset` | integer | hours to adjust daylight window; positive = shrink (lenient), negative = expand (strict) |
| `realtime.daylightfilter.species` | string[] | species, families, orders, or genera to filter during daylight |
| `realtime.rtsp.streams` | stream-config[] | Stream configurations |
| `realtime.rtsp.urls` | string[] | Legacy: accepts old format, migrated on load |
| `realtime.rtsp.transport` | string | Legacy: global default, migrated on load |
| `realtime.rtsp.health.healthydatathreshold` | integer | seconds before stream considered unhealthy (default: 60) |
| `realtime.rtsp.health.monitoringinterval` | integer | health check interval in seconds (default: 30) |
| `realtime.rtsp.ffmpegParameters` | string[] | Custom FFmpeg parameters |
| `realtime.mqtt.enabled` | boolean | true to enable MQTT |
| `realtime.mqtt.debug` | boolean | true to enable MQTT debug |
| `realtime.mqtt.broker` | string | MQTT broker URL |
| `realtime.mqtt.topic` | string | MQTT topic |
| `realtime.mqtt.username` | string | MQTT username |
| `realtime.mqtt.password` | string | MQTT password |
| `realtime.mqtt.retain` | boolean | true to retain messages |
| `realtime.mqtt.retrysettings.enabled` | boolean | true to enable retry mechanism |
| `realtime.mqtt.retrysettings.maxretries` | integer | maximum number of retry attempts |
| `realtime.mqtt.retrysettings.initialdelay` | integer | initial delay before first retry in seconds |
| `realtime.mqtt.retrysettings.maxdelay` | integer | maximum delay between retries in seconds |
| `realtime.mqtt.retrysettings.backoffmultiplier` | number | multiplier for exponential backoff |
| `realtime.mqtt.tls.enabled` | boolean | true to enable TLS (auto-detected from broker URL) |
| `realtime.mqtt.tls.insecureskipverify` | boolean | true to skip certificate verification (for self-signed certs) |
| `realtime.mqtt.tls.cacert` | string | path to CA certificate file (managed internally) |
| `realtime.mqtt.tls.clientcert` | string | path to client certificate file (managed internally) |
| `realtime.mqtt.tls.clientkey` | string | path to client key file (managed internally) |
| `realtime.mqtt.homeassistant.enabled` | boolean | true to enable HA auto-discovery |
| `realtime.mqtt.homeassistant.discovery_prefix` | string | HA discovery topic prefix (default: homeassistant) |
| `realtime.mqtt.homeassistant.device_name` | string | base name for devices (default: BirdNET-Go) |
| `realtime.telemetry.enabled` | boolean | true to enable Prometheus compatible telemetry endpoint |
| `realtime.telemetry.listen` | string | IP address and port to listen on |
| `realtime.monitoring.enabled` | boolean | true to enable system resource monitoring |
| `realtime.monitoring.checkinterval` | integer | interval in seconds between resource checks |
| `realtime.monitoring.cpu.enabled` | boolean | true to enable monitoring for this resource |
| `realtime.monitoring.memory.enabled` | boolean | true to enable monitoring for this resource |
| `realtime.monitoring.disk.enabled` | boolean | true to enable disk monitoring |
| `realtime.monitoring.disk.paths` | string[] | filesystem paths to monitor |
| `realtime.species.include` | string[] | Always include these species |
| `realtime.species.exclude` | string[] | Always exclude these species |
| `realtime.species.config` | any |  |
| `realtime.weather.provider` | string | "none", "yrno", "openweather", or "wunderground" |
| `realtime.weather.pollinterval` | integer | weather data polling interval in minutes |
| `realtime.weather.debug` | boolean | true to enable debug mode |
| `realtime.weather.openweather.enabled` | boolean | true to enable OpenWeather integration, for legacy support |
| `realtime.weather.openweather.apikey` | string | OpenWeather API key |
| `realtime.weather.openweather.endpoint` | string | OpenWeather API endpoint |
| `realtime.weather.openweather.units` | string | units of measurement: standard, metric, or imperial |
| `realtime.weather.openweather.language` | string | language code for the response |
| `realtime.weather.wunderground.apikey` | string | WeatherUnderground API key |
| `realtime.weather.wunderground.stationid` | string | WeatherUnderground station ID |
| `realtime.weather.wunderground.endpoint` | string | WeatherUnderground API endpoint |
| `realtime.weather.wunderground.units` | string | units of measurement: "e" (imperial), "m" (metric), "h" (UK hybrid) |
| `realtime.speciestracking.enabled` | boolean | true to enable new species tracking |
| `realtime.speciestracking.newspecieswindowdays` | integer | Days to consider a species "new" (default: 7) |
| `realtime.speciestracking.syncintervalminutes` | integer | Interval to sync with database (default: 60) |
| `realtime.speciestracking.notificationsuppressionhours` | integer | Hours to suppress duplicate notifications (default: 168) |
| `realtime.speciestracking.yearlytracking.enabled` | boolean | true to enable yearly tracking |
| `realtime.speciestracking.yearlytracking.resetmonth` | integer | Month to reset yearly tracking (1=January, default: 1) |
| `realtime.speciestracking.yearlytracking.resetday` | integer | Day to reset yearly tracking (default: 1) |
| `realtime.speciestracking.yearlytracking.windowdays` | integer | Days to show "new this year" indicator (default: 7) |
| `realtime.speciestracking.seasonaltracking.enabled` | boolean | true to enable seasonal tracking |
| `realtime.speciestracking.seasonaltracking.windowdays` | integer | Days to show "new this season" indicator (default: 7) |
| `realtime.speciestracking.seasonaltracking.seasons` | any |  |
| `realtime.extendedcapture.enabled` | boolean |  |
| `realtime.extendedcapture.maxduration` | integer |  |
| `realtime.extendedcapture.capturebufferseconds` | integer |  |
| `realtime.extendedcapture.species` | string[] |  |

## webserver

web server configuration

| Setting | Type | Description |
|---------|------|-------------|
| `webserver.debug` | boolean | true to enable debug mode |
| `webserver.enabled` | boolean | true to enable web server |
| `webserver.port` | string | port for web server |
| `webserver.basepath` | string | reverse proxy subpath prefix (e.g., "/birdnet") |
| `webserver.allowembedding` | boolean | true to allow embedding in iframes (e.g., Home Assistant) |
| `webserver.livestream.debug` | boolean | true to enable debug mode |
| `webserver.livestream.bitrate` | integer | bitrate for live stream in kbps |
| `webserver.livestream.samplerate` | integer | sample rate for live stream in Hz |
| `webserver.livestream.segmentlength` | integer | length of each segment in seconds |
| `webserver.livestream.ffmpegloglevel` | string | log level for ffmpeg |
| `webserver.enableterminal` | boolean | Enable browser terminal (security risk) |

## security

SecurityConfig handles all security-related settings and validations for the application, including authentication, TLS, and access control.

| Setting | Type | Description |
|---------|------|-------------|
| `security.debug` | boolean | true to enable debug mode |
| `security.baseurl` | string | BaseURL is the complete external URL for this instance, including scheme, host, and optional port (e.g., "https://birdnet.example.com:5500"). Used for generating OAuth redirect URLs and notification links. Takes precedence over Host when set. Can be overridden with BIRDNET_URL environment variable. NOTE: This field is prepared for future implementation (issue #1462) |
| `security.host` | string | Host is the primary hostname used for TLS certificates, OAuth redirect URLs, and notification link generation. Required when using AutoTLS or authentication providers. Also used to generate URLs in push notifications - set this to your public hostname when using a reverse proxy. Can be overridden with BIRDNET_HOST environment variable. |
| `security.autoTls` | boolean | Deprecated: AutoTLS is replaced by TLSMode. Kept for backward-compatible config migration. Will be removed in a future version. |
| `security.tlsMode` | string | TLSMode controls TLS certificate management. Valid values:   "" (none)      - TLS disabled   "autotls"      - automatic via Let's Encrypt   "manual"       - user-provided certificates   "selfsigned"   - auto-generated self-signed certificates |
| `security.selfSignedValidity` | string | SelfSignedValidity is the validity duration for self-signed certificates. Uses Go duration format with day/month suffixes (e.g., "365d", "1y"). |
| `security.tlsport` | string | port for HTTPS (default: 8443) |
| `security.redirecttohttps` | boolean | true to redirect to HTTPS |
| `security.allowsubnetbypass.enabled` | boolean | true to enable subnet bypass |
| `security.allowsubnetbypass.subnet` | string | disable OAuth2 in subnet |
| `security.trustedproxies` | string[] | TrustedProxies lists reverse proxies (CIDR ranges or bare IPs) whose forwarded client-IP headers (CF-Connecting-IP, X-Forwarded-For, X-Real-IP) may be trusted. Loopback, link-local, and private (RFC1918/ULA) peers are always trusted in addition to these, so a default port-forwarded install and the common local cloudflared topology work without configuration. When the immediate peer is not trusted, forwarded headers are ignored and the real connection address is used, preventing source-IP spoofing on a directly exposed instance. The reserved value "cloudflare" (TrustedProxyCloudflarePreset) expands to Cloudflare's published edge ranges. Hot-reloadable. |
| `security.publicaccess.liveaudio` | boolean | allow unauthenticated users to start/listen to live audio streams |
| `security.privatemode` | boolean | PrivateMode, when true, requires the user to authenticate before any UI data is shown. Enforcement lives at the v2 API data layer, which returns 401 to unauthenticated requests; the public SPA shell is still served so it can render a login form instead of the dashboard, detections, analytics, search, about, and notifications views. Settings and system routes are additionally auth-gated at the HTTP layer. PublicAccess.LiveAudio still applies independently. Default is false to preserve guest-friendly upstream behavior. |
| `security.basicauth.enabled` | boolean | true to enable password authentication |
| `security.basicauth.password` | string | password for admin interface |
| `security.basicauth.clientid` | string | client id for OAuth2 |
| `security.basicauth.clientsecret` | string | client secret for OAuth2 |
| `security.basicauth.redirecturi` | string | redirect uri for OAuth2 |
| `security.basicauth.authcodeexp` | string | duration for authorization code |
| `security.basicauth.accesstokenexp` | string | duration for access token |
| `security.oauthProviders` | o-auth-provider-config[] | OAuthProviders is the new array-based OAuth configuration. This is the preferred format for configuring OAuth providers. |
| `security.googleAuth.enabled` | boolean | true to enable social provider |
| `security.googleAuth.clientid` | string | client id for OAuth2 |
| `security.googleAuth.clientsecret` | string | client secret for OAuth2 |
| `security.googleAuth.redirecturi` | string | redirect uri for OAuth2 |
| `security.googleAuth.userid` | string | valid user id for OAuth2 |
| `security.githubAuth.enabled` | boolean | true to enable social provider |
| `security.githubAuth.clientid` | string | client id for OAuth2 |
| `security.githubAuth.clientsecret` | string | client secret for OAuth2 |
| `security.githubAuth.redirecturi` | string | redirect uri for OAuth2 |
| `security.githubAuth.userid` | string | valid user id for OAuth2 |
| `security.microsoftAuth.enabled` | boolean | true to enable social provider |
| `security.microsoftAuth.clientid` | string | client id for OAuth2 |
| `security.microsoftAuth.clientsecret` | string | client secret for OAuth2 |
| `security.microsoftAuth.redirecturi` | string | redirect uri for OAuth2 |
| `security.microsoftAuth.userid` | string | valid user id for OAuth2 |
| `security.sessionsecret` | string | secret for session cookie |
| `security.sessionduration` | string | duration for browser session cookies |

## sentry

SentrySettings contains settings for Sentry error tracking

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

BackupConfig contains backup-related configuration

| Setting | Type | Description |
|---------|------|-------------|
| `backup.enabled` | boolean | Global flag to enable or disable the entire backup system. If false, no backups (manual or scheduled) will occur. |
| `backup.debug` | boolean | If true, enables detailed debug logging for backup operations. |
| `backup.encryption` | boolean | If true, enables encryption for backup archives. Requires EncryptionKey to be set. |
| `backup.encryption_key` | string | Base64-encoded encryption key used for AES-256-GCM encryption of backup archives. Must be kept secret and safe. |
| `backup.sanitize_config` | boolean | If true, sensitive information (like passwords, API keys) will be removed from the configuration file copy that is included in the backup archive. |
| `backup.retention.maxage` | string | Duration string for the maximum age of backups to keep (e.g., "30d" for 30 days, "6m" for 6 months, "1y" for 1 year). Backups older than this may be deleted. |
| `backup.retention.maxbackups` | integer | Maximum total number of backups to keep for a given source. If 0, no limit by count (only by age or MinBackups). |
| `backup.retention.minbackups` | integer | Minimum number of recent backups to keep for a given source, regardless of their age. This ensures a baseline number of backups are always available. |
| `backup.targets` | backup-target[] | A list of configured backup targets (destinations) where backup archives will be stored. |
| `backup.schedules` | backup-schedule-config[] | A list of schedules (e.g., daily, weekly) that define when automatic backups should run. |
| `backup.operationtimeouts.backup` | string |  |
| `backup.operationtimeouts.store` | string |  |
| `backup.operationtimeouts.cleanup` | string |  |
| `backup.operationtimeouts.delete` | string |  |

## import

ImportConfig controls the BirdNET-Pi import feature behavior.

| Setting | Type | Description |
|---------|------|-------------|
| `import.allowinappelevation` | boolean | AllowInAppElevation enables the in-app sudo elevation ladder for native imports of unreadable source data. Default true. When false, the UI only offers copy-paste remediation and never prompts for a sudo password. |

## notification

NotificationConfig is the root for notification-specific settings.

| Setting | Type | Description |
|---------|------|-------------|
| `notification.push.enabled` | boolean |  |
| `notification.push.default_timeout` | string | Duration string (e.g. "30s", "5m", "1h30m") |
| `notification.push.max_retries` | integer |  |
| `notification.push.retry_delay` | string | Duration string (e.g. "30s", "5m", "1h30m") |
| `notification.push.circuit_breaker.enabled` | boolean |  |
| `notification.push.circuit_breaker.max_failures` | integer |  |
| `notification.push.circuit_breaker.timeout` | string | Duration string (e.g. "30s", "5m", "1h30m") |
| `notification.push.circuit_breaker.half_open_max_requests` | integer |  |
| `notification.push.health_check.enabled` | boolean |  |
| `notification.push.health_check.interval` | string | Duration string (e.g. "30s", "5m", "1h30m") |
| `notification.push.health_check.timeout` | string | Duration string (e.g. "30s", "5m", "1h30m") |
| `notification.push.rate_limiting.enabled` | boolean |  |
| `notification.push.rate_limiting.requests_per_minute` | integer |  |
| `notification.push.rate_limiting.burst_size` | integer |  |
| `notification.push.providers` | push-provider-config[] |  |
| `notification.push.min_confidence_threshold` | number | Detection filtering settings |
| `notification.push.species_cooldown_minutes` | integer | 0 = disabled |
| `notification.templates.newspecies.title` | string |  |
| `notification.templates.newspecies.message` | string |  |

## alerting

AlertSettings configures the alerting rules engine.

| Setting | Type | Description |
|---------|------|-------------|
| `alerting.history_retention_days` | integer | Days to retain alert history (0 = unlimited) |

