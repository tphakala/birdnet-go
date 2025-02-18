// conf/defaults.go default values for settings
package conf

import (
	"github.com/spf13/viper"

	"time"
)

// Sets default values for the configuration.
func setDefaultConfig() {
	viper.SetDefault("debug", false)

	// Main configuration
	viper.SetDefault("main.name", "BirdNET-Go")
	viper.SetDefault("main.timeas24h", true)
	viper.SetDefault("main.log.enabled", true)
	viper.SetDefault("main.log.path", "birdnet.log")
	viper.SetDefault("main.log.rotation", RotationDaily)
	viper.SetDefault("main.log.maxsize", 1048576)
	viper.SetDefault("main.log.rotationday", "Sunday")

	// BirdNET configuration
	viper.SetDefault("birdnet.debug", false)
	viper.SetDefault("birdnet.sensitivity", 1.0)
	viper.SetDefault("birdnet.threshold", 0.8)
	viper.SetDefault("birdnet.overlap", 0.0)
	viper.SetDefault("birdnet.threads", 0)
	viper.SetDefault("birdnet.locale", "en")
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

	// Audio export configuration
	viper.SetDefault("realtime.audio.export.debug", false)
	viper.SetDefault("realtime.audio.export.enabled", true)
	viper.SetDefault("realtime.audio.export.path", "clips/")
	viper.SetDefault("realtime.audio.export.type", "wav")
	viper.SetDefault("realtime.audio.export.bitrate", "128k")

	// Audio equalizer configuration
	viper.SetDefault("realtime.audio.equalizer.enabled", false)
	viper.SetDefault("realtime.audio.equalizer.filters", []map[string]interface{}{
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
	viper.SetDefault("realtime.dashboard.summarylimit", 30)

	// Retention policy configuration
	viper.SetDefault("realtime.audio.export.retention.enabled", true)
	viper.SetDefault("realtime.audio.export.retention.debug", false)
	viper.SetDefault("realtime.audio.export.retention.policy", "usage")
	viper.SetDefault("realtime.audio.export.retention.maxusage", "80%")
	viper.SetDefault("realtime.audio.export.retention.maxage", "30d")
	viper.SetDefault("realtime.audio.export.retention.minclips", 10)

	// Dynamic threshold configuration
	viper.SetDefault("realtime.dynamicthreshold.enabled", true)
	viper.SetDefault("realtime.dynamicthreshold.debug", false)
	viper.SetDefault("realtime.dynamicthreshold.trigger", 0.90)
	viper.SetDefault("realtime.dynamicthreshold.min", 0.20)
	viper.SetDefault("realtime.dynamicthreshold.validhours", 24)

	// Log configuration
	viper.SetDefault("realtime.log.enabled", false)
	viper.SetDefault("realtime.log.path", "birdnet.txt")

	// BirdWeather configuration
	viper.SetDefault("realtime.birdweather.enabled", false)
	viper.SetDefault("realtime.birdweather.debug", false)
	viper.SetDefault("realtime.birdweather.id", "")
	viper.SetDefault("realtime.birdweather.threshold", 0.8)
	viper.SetDefault("realtime.birdweather.locationaccuracy", 500)

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

	// RTSP configuration
	viper.SetDefault("realtime.rtsp.urls", []string{})
	viper.SetDefault("realtime.rtsp.transport", "tcp")

	// MQTT configuration
	viper.SetDefault("realtime.mqtt.enabled", false)
	viper.SetDefault("realtime.mqtt.broker", "tcp://localhost:1883")
	viper.SetDefault("realtime.mqtt.topic", "birdnet")
	viper.SetDefault("realtime.mqtt.username", "birdnet")
	viper.SetDefault("realtime.mqtt.password", "secret")

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

	// Webserver configuration
	viper.SetDefault("webserver.debug", false)
	viper.SetDefault("webserver.enabled", true)
	viper.SetDefault("webserver.port", "8080")

	// Webserver log configuration
	viper.SetDefault("webserver.log.enabled", false)
	viper.SetDefault("webserver.log.path", "webui.log")
	viper.SetDefault("webserver.log.rotation", RotationDaily)
	viper.SetDefault("webserver.log.maxsize", 1048576)
	viper.SetDefault("webserver.log.rotationday", time.Sunday)

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

	// Backup configuration
	viper.SetDefault("backup.enabled", false)
	viper.SetDefault("backup.debug", false)
	viper.SetDefault("backup.schedule", "0 0 * * *") // Daily at midnight
	viper.SetDefault("backup.encryption", false)     // Encryption disabled by default
	viper.SetDefault("backup.retention.maxage", "30d")
	viper.SetDefault("backup.retention.maxbackups", 30)
	viper.SetDefault("backup.retention.minbackups", 7)
	viper.SetDefault("backup.targets", []map[string]interface{}{
		{
			"type":    "local",
			"enabled": true,
			"settings": map[string]interface{}{
				"path": "backups/",
			},
		},
		{
			"type":    "ftp",
			"enabled": false,
			"settings": map[string]interface{}{
				"host":     "localhost",
				"port":     21,
				"username": "",
				"password": "",
				"path":     "backups/",
				"timeout":  "30s",
			},
		},
		{
			"type":    "sftp",
			"enabled": false,
			"settings": map[string]interface{}{
				"host":     "localhost",
				"port":     22,
				"username": "",
				"password": "",
				"key_file": "",
				"path":     "backups/",
				"timeout":  "30s",
			},
		},
		{
			"type":    "rsync",
			"enabled": false,
			"settings": map[string]interface{}{
				"host":     "localhost",
				"port":     22,
				"username": "",
				"key_file": "",
				"path":     "backups/",
			},
		},
		{
			"type":    "gdrive",
			"enabled": false,
			"settings": map[string]interface{}{
				"bucket":      "",
				"credentials": "credentials.json",
				"path":        "backups/",
			},
		},
	})

	// Security configuration
	viper.SetDefault("security.debug", false)
	viper.SetDefault("security.host", "")
	viper.SetDefault("security.autotls", false)
	viper.SetDefault("security.redirecttohttps", false)
	viper.SetDefault("security.allowsubnetbypass.enabled", false)
	viper.SetDefault("security.allowsubnetbypass.subnet", "")
	viper.SetDefault("security.allowcloudflarebypass.enabled", false)
	viper.SetDefault("security.allowcloudflarebypass.teamdomain", "")
	viper.SetDefault("security.allowcloudflarebypass.audience", "")

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
}
