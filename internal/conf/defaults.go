// conf/defaults.go default values for settings
package conf

import "github.com/spf13/viper"

// Sets default values for the configuration.
func setDefaultConfig() {
	viper.SetDefault("debug", false)

	// Logging configuration
	viper.SetDefault("logging.default_level", "info")
	viper.SetDefault("logging.timezone", "Local")

	// Console logging
	viper.SetDefault("logging.console.enabled", true)
	viper.SetDefault("logging.console.level", "info")

	// Main application log file
	viper.SetDefault("logging.file_output.enabled", true)
	viper.SetDefault("logging.file_output.path", "logs/application.log")
	viper.SetDefault("logging.file_output.level", "info")
	viper.SetDefault("logging.file_output.max_size", 100)
	viper.SetDefault("logging.file_output.max_age", 30)
	viper.SetDefault("logging.file_output.max_backups", 10)
	viper.SetDefault("logging.file_output.compress", true)

	// Per-module log files
	// Core processing modules
	setModuleLogDefaults("analysis", true)    // Bird detection analysis
	setModuleLogDefaults("birdnet", true)     // BirdNET model inference
	setModuleLogDefaults("audio", true)       // Audio capture/processing
	setModuleLogDefaults("datastore", true)   // Database operations
	setModuleLogDefaults("spectrogram", true) // Spectrogram generation

	// API and web modules
	setModuleLogDefaults("api", true)      // HTTP server and API (internal/api/)
	setModuleLogDefaults("access", true)   // HTTP access logs (request/response)
	setModuleLogDefaults("auth", true)     // Authentication
	setModuleLogDefaults("security", true) // Security operations

	// Integration modules
	setModuleLogDefaults("mqtt", false)        // MQTT client (disabled by default)
	setModuleLogDefaults("birdweather", false) // BirdWeather integration (disabled by default)
	setModuleLogDefaults("weather", false)     // Weather providers (disabled by default)
	setModuleLogDefaults("ebird", false)       // eBird integration (disabled by default)

	// System and support modules
	setModuleLogDefaults("backup", true)        // Backup operations
	setModuleLogDefaults("config", true)        // Configuration management
	setModuleLogDefaults("diskmanager", true)   // Disk management
	setModuleLogDefaults("events", true)        // Event bus
	setModuleLogDefaults("imageprovider", true) // Bird image provider
	setModuleLogDefaults("monitor", true)       // System monitoring
	setModuleLogDefaults("notifications", true) // Push notifications
	setModuleLogDefaults("securefs", true)      // Secure filesystem operations
	setModuleLogDefaults("support", true)       // Support/diagnostics
	setModuleLogDefaults("telemetry", true)     // Telemetry/metrics

	// Main configuration
	viper.SetDefault("main.name", "BirdNET-Go")
	viper.SetDefault("main.timeas24h", true)

	// BirdNET configuration
	viper.SetDefault("birdnet.debug", false)
	viper.SetDefault("birdnet.sensitivity", 1.0)
	viper.SetDefault("birdnet.threshold", 0.8)
	viper.SetDefault("birdnet.overlap", 0.0)
	viper.SetDefault("birdnet.threads", 0)
	viper.SetDefault("birdnet.locale", DefaultFallbackLocale)
	viper.SetDefault("birdnet.latitude", 0.000)
	viper.SetDefault("birdnet.longitude", 0.000)
	viper.SetDefault("birdnet.modelpath", "")
	viper.SetDefault("birdnet.labelpath", "")
	viper.SetDefault("birdnet.usexnnpack", true)

	// Range filter configuration
	viper.SetDefault("birdnet.rangefilter.debug", false)
	viper.SetDefault("birdnet.rangefilter.model", "latest")
	viper.SetDefault("birdnet.rangefilter.threshold", 0.01)

	// Realtime configuration
	viper.SetDefault("realtime.interval", 15)
	viper.SetDefault("realtime.processingtime", false)

	// Audio source configuration
	viper.SetDefault("realtime.audio.source", "sysdefault")
	viper.SetDefault("realtime.audio.streamtransport", "sse")

	// Sound level monitoring configuration
	viper.SetDefault("realtime.audio.soundlevel.enabled", false)
	viper.SetDefault("realtime.audio.soundlevel.interval", 10)

	// Audio capture configuration
	viper.SetDefault("realtime.audio.export.debug", false)
	viper.SetDefault("realtime.audio.export.enabled", true)
	viper.SetDefault("realtime.audio.export.path", "clips/")
	viper.SetDefault("realtime.audio.export.type", "wav")
	viper.SetDefault("realtime.audio.export.bitrate", "96k")
	viper.SetDefault("realtime.audio.export.length", 15)
	viper.SetDefault("realtime.audio.export.preCapture", 3)
	viper.SetDefault("realtime.audio.export.gain", 0.0)

	// Audio normalization configuration (EBU R128 standard)
	viper.SetDefault("realtime.audio.export.normalization.enabled", false)     // disabled by default
	viper.SetDefault("realtime.audio.export.normalization.targetLUFS", -23.0)  // EBU R128 broadcast standard
	viper.SetDefault("realtime.audio.export.normalization.loudnessRange", 7.0) // typical range for broadcast
	viper.SetDefault("realtime.audio.export.normalization.truePeak", -2.0)     // headroom to prevent clipping

	// Audio equalizer configuration
	viper.SetDefault("realtime.audio.equalizer.enabled", false)
	viper.SetDefault("realtime.audio.equalizer.filters", []map[string]any{
		{
			"type":      "HighPass",
			"frequency": 100,
			"q":         0.7,
			"passes":    0,
		},
		{
			"type":      "LowPass",
			"frequency": 15000,
			"q":         0.7,
			"passes":    0,
		},
	})

	// Dashboard thumbnails configuration
	viper.SetDefault("realtime.dashboard.thumbnails.debug", false)
	viper.SetDefault("realtime.dashboard.thumbnails.summary", false)
	viper.SetDefault("realtime.dashboard.thumbnails.recent", true)
	viper.SetDefault("realtime.dashboard.thumbnails.imageprovider", "avicommons")
	viper.SetDefault("realtime.dashboard.thumbnails.fallbackpolicy", "none")
	viper.SetDefault("realtime.dashboard.summarylimit", 30)
	viper.SetDefault("realtime.dashboard.locale", "en")               // Default UI locale
	viper.SetDefault("realtime.dashboard.temperatureunit", "celsius") // Temperature display unit: "celsius" or "fahrenheit"

	// Spectrogram pre-rendering configuration
	viper.SetDefault("realtime.dashboard.spectrogram.enabled", false)                                // Opt-in for safety
	viper.SetDefault("realtime.dashboard.spectrogram.mode", "auto")                                  // Default to auto mode (generate on demand)
	viper.SetDefault("realtime.dashboard.spectrogram.size", "sm")                                    // 400px, matches frontend RecentDetectionsCard
	viper.SetDefault("realtime.dashboard.spectrogram.raw", true)                                     // Raw spectrogram (no axes/legend)
	viper.SetDefault("realtime.dashboard.spectrogram.style", "default")                              // Visual style preset
	viper.SetDefault("realtime.dashboard.spectrogram.dynamicrange", SpectrogramDynamicRangeStandard) // Dynamic range in dB (100 = standard)

	// Retention policy configuration
	viper.SetDefault("realtime.audio.export.retention.enabled", true)
	viper.SetDefault("realtime.audio.export.retention.debug", false)
	viper.SetDefault("realtime.audio.export.retention.policy", "usage")
	viper.SetDefault("realtime.audio.export.retention.maxusage", "80%")
	viper.SetDefault("realtime.audio.export.retention.maxage", "30d")
	viper.SetDefault("realtime.audio.export.retention.minclips", 10)
	viper.SetDefault("realtime.audio.export.retention.keepspectrograms", true)
	viper.SetDefault("realtime.audio.export.retention.checkinterval", DefaultCleanupCheckInterval)

	// Dynamic threshold configuration
	viper.SetDefault("realtime.dynamicthreshold.enabled", true)
	viper.SetDefault("realtime.dynamicthreshold.debug", false)
	viper.SetDefault("realtime.dynamicthreshold.trigger", 0.90)
	viper.SetDefault("realtime.dynamicthreshold.min", 0.20)
	viper.SetDefault("realtime.dynamicthreshold.validhours", 24)

	// Log deduplication configuration
	viper.SetDefault("realtime.logdeduplication.enabled", true)
	viper.SetDefault("realtime.logdeduplication.healthcheckintervalseconds", 60)

	// False positive filter configuration
	// Level 0 = Off (no filtering, backward compatible default)
	// Level 1 = Lenient, Level 2 = Moderate, Level 3 = Balanced (original behavior)
	// Level 4 = Strict (RPi 4+ required), Level 5 = Maximum (RPi 4+ required)
	viper.SetDefault("realtime.falsepositivefilter.level", 0)

	// Log configuration
	viper.SetDefault("realtime.log.enabled", false)
	viper.SetDefault("realtime.log.path", "birdnet.txt")

	// BirdWeather configuration
	viper.SetDefault("realtime.birdweather.enabled", false)
	viper.SetDefault("realtime.birdweather.debug", false)
	viper.SetDefault("realtime.birdweather.id", "")
	viper.SetDefault("realtime.birdweather.threshold", 0.7)
	viper.SetDefault("realtime.birdweather.locationaccuracy", 0)
	viper.SetDefault("realtime.birdweather.retrysettings.enabled", true)
	viper.SetDefault("realtime.birdweather.retrysettings.maxretries", 10)
	viper.SetDefault("realtime.birdweather.retrysettings.initialdelay", 60)
	viper.SetDefault("realtime.birdweather.retrysettings.maxdelay", 3600)
	viper.SetDefault("realtime.birdweather.retrysettings.backoffmultiplier", 2.0)

	// eBird configuration
	viper.SetDefault("realtime.ebird.enabled", false)
	viper.SetDefault("realtime.ebird.apikey", "")
	viper.SetDefault("realtime.ebird.cachettl", 24) // 24 hours default
	viper.SetDefault("realtime.ebird.locale", "en")

	// OpenWeather configuration
	/*
		viper.SetDefault("realtime.OpenWeather.Enabled", false)
		viper.SetDefault("realtime.OpenWeather.Debug", false)
		viper.SetDefault("realtime.OpenWeather.APIKey", "")
		viper.SetDefault("realtime.OpenWeather.Endpoint", "https://api.openweathermap.org/data/2.5/weather")
		viper.SetDefault("realtime.OpenWeather.Interval", 60) // default to fetch every 60 minutes
		viper.SetDefault("realtime.OpenWeather.Units", "standard")
		viper.SetDefault("realtime.OpenWeather.Language", "en")
	*/

	// New weather configuration
	viper.SetDefault("realtime.weather.debug", false)
	viper.SetDefault("realtime.weather.pollinterval", 60)
	viper.SetDefault("realtime.weather.provider", "yrno")

	// OpenWeather specific configuration
	viper.SetDefault("realtime.weather.openweather.apikey", "")
	viper.SetDefault("realtime.weather.openweather.endpoint", "https://api.openweathermap.org/data/2.5/weather")
	viper.SetDefault("realtime.weather.openweather.units", "metric")
	viper.SetDefault("realtime.weather.openweather.language", "en")

	// Weather Underground specific configuration
	viper.SetDefault("realtime.weather.wunderground.apikey", "")
	viper.SetDefault("realtime.weather.wunderground.stationid", "")
	viper.SetDefault("realtime.weather.wunderground.endpoint", "https://api.weather.com/v2/pws/observations/current")
	viper.SetDefault("realtime.weather.wunderground.units", "m") // m=metric, e=imperial, h=UK hybrid

	// RTSP configuration
	viper.SetDefault("realtime.rtsp.urls", []string{})
	viper.SetDefault("realtime.rtsp.transport", "tcp")
	viper.SetDefault("realtime.rtsp.health.healthydatathreshold", 60)
	viper.SetDefault("realtime.rtsp.health.monitoringinterval", 30)
	viper.SetDefault("realtime.rtsp.ffmpegparameters", []string{})

	// MQTT configuration
	viper.SetDefault("realtime.mqtt.enabled", false)
	viper.SetDefault("realtime.mqtt.debug", false)
	viper.SetDefault("realtime.mqtt.broker", "tcp://localhost:1883")
	viper.SetDefault("realtime.mqtt.topic", "birdnet")
	viper.SetDefault("realtime.mqtt.username", "")
	viper.SetDefault("realtime.mqtt.password", "")
	viper.SetDefault("realtime.mqtt.retain", false)
	viper.SetDefault("realtime.mqtt.retrysettings.enabled", true)
	viper.SetDefault("realtime.mqtt.retrysettings.maxretries", 5)
	viper.SetDefault("realtime.mqtt.retrysettings.initialdelay", 30)
	viper.SetDefault("realtime.mqtt.retrysettings.maxdelay", 3600)
	viper.SetDefault("realtime.mqtt.retrysettings.backoffmultiplier", 2.0)

	// Home Assistant MQTT auto-discovery configuration
	viper.SetDefault("realtime.mqtt.homeassistant.enabled", false)
	viper.SetDefault("realtime.mqtt.homeassistant.discovery_prefix", "homeassistant")
	viper.SetDefault("realtime.mqtt.homeassistant.device_name", "BirdNET-Go")

	// Privacy filter configuration
	viper.SetDefault("realtime.privacyfilter.enabled", true)
	viper.SetDefault("realtime.privacyfilter.debug", false)
	viper.SetDefault("realtime.privacyfilter.confidence", 0.05)

	// Dog bark filter configuration
	viper.SetDefault("realtime.dogbarkfilter.enabled", false)
	viper.SetDefault("realtime.dogbarkfilter.debug", false)
	viper.SetDefault("realtime.dogbarkfilter.remember", 5)
	viper.SetDefault("realtime.dogbarkfilter.confidence", 0.1)
	viper.SetDefault("realtime.dogbarkfilter.species", []string{})

	// Telemetry configuration
	viper.SetDefault("realtime.telemetry.enabled", false)
	viper.SetDefault("realtime.telemetry.listen", "0.0.0.0:8090")

	// System monitoring configuration
	viper.SetDefault("realtime.monitoring.enabled", true)
	viper.SetDefault("realtime.monitoring.checkinterval", 60)
	viper.SetDefault("realtime.monitoring.criticalresendinterval", 30)
	viper.SetDefault("realtime.monitoring.hysteresispercent", 5.0)
	// CPU monitoring
	viper.SetDefault("realtime.monitoring.cpu.enabled", true)
	viper.SetDefault("realtime.monitoring.cpu.warning", 85.0)
	viper.SetDefault("realtime.monitoring.cpu.critical", 95.0)
	// Memory monitoring
	viper.SetDefault("realtime.monitoring.memory.enabled", true)
	viper.SetDefault("realtime.monitoring.memory.warning", 85.0)
	viper.SetDefault("realtime.monitoring.memory.critical", 95.0)
	// Disk monitoring
	viper.SetDefault("realtime.monitoring.disk.enabled", true)
	viper.SetDefault("realtime.monitoring.disk.warning", 85.0)
	viper.SetDefault("realtime.monitoring.disk.critical", 95.0)
	viper.SetDefault("realtime.monitoring.disk.paths", []string{"/"})

	// Species tracking configuration
	viper.SetDefault("realtime.speciestracking.enabled", true)
	viper.SetDefault("realtime.speciestracking.newspecieswindowdays", 7)
	viper.SetDefault("realtime.speciestracking.syncintervalminutes", 60)
	viper.SetDefault("realtime.speciestracking.notificationsuppressionhours", 168) // 7 days

	// Yearly tracking defaults
	viper.SetDefault("realtime.speciestracking.yearlytracking.enabled", true)
	viper.SetDefault("realtime.speciestracking.yearlytracking.resetmonth", 1)
	viper.SetDefault("realtime.speciestracking.yearlytracking.resetday", 1)
	viper.SetDefault("realtime.speciestracking.yearlytracking.windowdays", 7)

	// Seasonal tracking defaults
	viper.SetDefault("realtime.speciestracking.seasonaltracking.enabled", true)
	viper.SetDefault("realtime.speciestracking.seasonaltracking.windowdays", 7)

	// Default seasons (Northern Hemisphere)
	viper.SetDefault("realtime.speciestracking.seasonaltracking.seasons.spring.startmonth", 3)
	viper.SetDefault("realtime.speciestracking.seasonaltracking.seasons.spring.startday", 20)
	viper.SetDefault("realtime.speciestracking.seasonaltracking.seasons.summer.startmonth", 6)
	viper.SetDefault("realtime.speciestracking.seasonaltracking.seasons.summer.startday", 21)
	viper.SetDefault("realtime.speciestracking.seasonaltracking.seasons.fall.startmonth", 9)
	viper.SetDefault("realtime.speciestracking.seasonaltracking.seasons.fall.startday", 22)
	viper.SetDefault("realtime.speciestracking.seasonaltracking.seasons.winter.startmonth", 12)
	viper.SetDefault("realtime.speciestracking.seasonaltracking.seasons.winter.startday", 21)

	// Webserver configuration
	viper.SetDefault("webserver.debug", false)
	viper.SetDefault("webserver.enabled", true)
	viper.SetDefault("webserver.port", "8080")

	// Live stream configuration
	viper.SetDefault("webserver.livestream.debug", false)
	viper.SetDefault("webserver.livestream.bitrate", 128)
	viper.SetDefault("webserver.livestream.sampleRate", 48000)
	viper.SetDefault("webserver.livestream.segmentLength", 2)
	viper.SetDefault("webserver.livestream.ffmpegLogLevel", "warning")

	// File output configuration
	viper.SetDefault("output.file.enabled", true)
	viper.SetDefault("output.file.path", "output/")
	viper.SetDefault("output.file.type", "table")

	// SQLite output configuration
	viper.SetDefault("output.sqlite.enabled", true)
	viper.SetDefault("output.sqlite.path", "birdnet.db")

	// MySQL output configuration
	viper.SetDefault("output.mysql.enabled", false)
	viper.SetDefault("output.mysql.username", "birdnet")
	viper.SetDefault("output.mysql.password", "secret")
	viper.SetDefault("output.mysql.database", "birdnet")
	viper.SetDefault("output.mysql.host", "localhost")
	viper.SetDefault("output.mysql.port", 3306)

	// Security configuration
	viper.SetDefault("security.debug", false)
	viper.SetDefault("security.baseurl", "")
	viper.SetDefault("security.host", "")
	viper.SetDefault("security.autotls", false)
	viper.SetDefault("security.redirecttohttps", false)
	viper.SetDefault("security.allowsubnetbypass.enabled", false)
	viper.SetDefault("security.allowsubnetbypass.subnet", "")
	viper.SetDefault("security.sessionduration", "168h") // 7 days

	// Basic authentication configuration
	viper.SetDefault("security.basicauth.enabled", false)
	viper.SetDefault("security.basicauth.password", "")
	viper.SetDefault("security.basicauth.clientid", "birdnet-client")
	viper.SetDefault("security.basicauth.redirecturi", "/settings")
	viper.SetDefault("security.basicauth.authcodeexp", "10m")
	viper.SetDefault("security.basicauth.accesstokenexp", "1h")

	// Google OAuth2 configuration
	viper.SetDefault("security.googleauth.enabled", false)
	viper.SetDefault("security.googleauth.clientid", "")
	viper.SetDefault("security.googleauth.clientsecret", "")
	viper.SetDefault("security.googleauth.redirecturi", "/settings")
	viper.SetDefault("security.googleauth.userid", "")

	// GitHub OAuth2 configuration
	viper.SetDefault("security.githubauth.enabled", false)
	viper.SetDefault("security.githubauth.clientid", "")
	viper.SetDefault("security.githubauth.clientsecret", "")
	viper.SetDefault("security.githubauth.redirecturi", "/settings")
	viper.SetDefault("security.githubauth.userid", "")

	// Sentry configuration
	viper.SetDefault("sentry.enabled", false)
	viper.SetDefault("sentry.dsn", "")
	viper.SetDefault("sentry.samplerate", 1.0)
	viper.SetDefault("sentry.debug", false)

	// Notification push configuration
	viper.SetDefault("notification.push.enabled", false)
	viper.SetDefault("notification.push.default_timeout", "30s")
	viper.SetDefault("notification.push.max_retries", 3)
	viper.SetDefault("notification.push.retry_delay", "5s")

	// Circuit breaker configuration
	viper.SetDefault("notification.push.circuit_breaker.enabled", true)
	viper.SetDefault("notification.push.circuit_breaker.max_failures", 5)
	viper.SetDefault("notification.push.circuit_breaker.timeout", "30s")
	viper.SetDefault("notification.push.circuit_breaker.half_open_max_requests", 1)

	// Health check configuration
	viper.SetDefault("notification.push.health_check.enabled", true)
	viper.SetDefault("notification.push.health_check.interval", "60s")
	viper.SetDefault("notification.push.health_check.timeout", "10s")

	// Rate limiting configuration
	viper.SetDefault("notification.push.rate_limiting.enabled", false)
	viper.SetDefault("notification.push.rate_limiting.requests_per_minute", 60)
	viper.SetDefault("notification.push.rate_limiting.burst_size", 10)

	viper.SetDefault("notification.push.providers", []map[string]any{})

	// Notification templates
	viper.SetDefault("notification.templates.newspecies.title", "New Species: {{.CommonName}}")
	viper.SetDefault("notification.templates.newspecies.message", "First detection of {{.CommonName}} ({{.ScientificName}}) with {{.ConfidencePercent}}% confidence at {{.DetectionTime}}. {{.DetectionURL}}")

	// Alerting rules engine
	viper.SetDefault("alerting.history_retention_days", 30)
}

// setModuleLogDefaults sets default values for a module log configuration
func setModuleLogDefaults(module string, enabled bool) {
	prefix := "logging.modules." + module
	viper.SetDefault(prefix+".enabled", enabled)
	viper.SetDefault(prefix+".file_path", "logs/"+module+".log")
	viper.SetDefault(prefix+".level", "debug")
	viper.SetDefault(prefix+".console_also", false)
}
