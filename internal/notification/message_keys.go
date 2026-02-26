package notification

// Translation key constants for notification messages.
// These keys correspond to entries in the frontend i18n translation files
// (frontend/static/messages/*.json) under the "notifications.content" namespace.
//
// The frontend uses t(key, params) to translate these, falling back to the
// English Title/Message fields when translations are unavailable.

const (
	// Startup/shutdown notifications
	MsgStartupTitle   = "notifications.content.startup.title"
	MsgStartupMessage = "notifications.content.startup.message"

	MsgShutdownTitle   = "notifications.content.shutdown.title"
	MsgShutdownMessage = "notifications.content.shutdown.message"

	// Detection notifications
	MsgDetectionTitle   = "notifications.content.detection.title"
	MsgDetectionMessage = "notifications.content.detection.message"

	// Integration failure notifications
	MsgIntegrationFailedTitle   = "notifications.content.integration.failedTitle"
	MsgIntegrationFailedMessage = "notifications.content.integration.failedMessage"

	// Resource alert notifications
	MsgResourceHighUsage     = "notifications.content.resource.highUsage"
	MsgResourceCriticalUsage = "notifications.content.resource.criticalUsage"
	MsgResourceRecovered     = "notifications.content.resource.recovered"
	MsgResourceCurrentUsage  = "notifications.content.resource.currentUsage"

	// Error notifications (title keys only — messages are raw error strings)
	MsgErrorCriticalSystem = "notifications.content.error.criticalSystem"
	MsgErrorApplication    = "notifications.content.error.application"
	MsgErrorImageProvider  = "notifications.content.error.imageProvider"
	MsgErrorCategory       = "notifications.content.error.categoryError"

	// Settings change toasts
	MsgSettingsReloadingBirdnet             = "notifications.content.settings.reloadingBirdnet"
	MsgSettingsRebuildingRangeFilter        = "notifications.content.settings.rebuildingRangeFilter"
	MsgSettingsUpdatingIntervals            = "notifications.content.settings.updatingIntervals"
	MsgSettingsReconfiguringMqtt            = "notifications.content.settings.reconfiguringMqtt"
	MsgSettingsReconfiguringBirdweather     = "notifications.content.settings.reconfiguringBirdweather"
	MsgSettingsReconfiguringStreams         = "notifications.content.settings.reconfiguringStreams"
	MsgSettingsReconfiguringTelemetry       = "notifications.content.settings.reconfiguringTelemetry"
	MsgSettingsReconfiguringSpeciesTracking = "notifications.content.settings.reconfiguringSpeciesTracking"
	MsgSettingsWebserverRestart             = "notifications.content.settings.webserverRestartRequired"

	// Audio settings toasts
	MsgSettingsReconfiguringSoundLevel = "notifications.content.settings.reconfiguringSoundLevel"
	MsgSettingsAudioDeviceRestart      = "notifications.content.settings.audioDeviceRestartRequired"
	MsgSettingsEqualizerFailed         = "notifications.content.settings.equalizerUpdateFailed"
	MsgSettingsEqualizerUpdated        = "notifications.content.settings.equalizerUpdated"

	// Database migration notifications
	MsgMigrationStartedTitle     = "notifications.content.migration.startedTitle"
	MsgMigrationStartedMessage   = "notifications.content.migration.startedMessage"
	MsgMigrationPausedTitle      = "notifications.content.migration.pausedTitle"
	MsgMigrationPausedMessage    = "notifications.content.migration.pausedMessage"
	MsgMigrationCancelledTitle   = "notifications.content.migration.cancelledTitle"
	MsgMigrationCancelledMessage = "notifications.content.migration.cancelledMessage"
	MsgMigrationCompletedTitle   = "notifications.content.migration.completedTitle"
	MsgMigrationCompletedMessage = "notifications.content.migration.completedMessage"
	MsgMigrationErrorTitle       = "notifications.content.migration.errorTitle"
	MsgMigrationErrorMessage     = "notifications.content.migration.errorMessage"

	// Legacy database cleanup notifications
	MsgCleanupCompleteTitle   = "notifications.content.cleanup.completeTitle"
	MsgCleanupCompleteMessage = "notifications.content.cleanup.completeMessage"
	MsgCleanupFailedTitle     = "notifications.content.cleanup.failedTitle"
	MsgCleanupFailedMessage   = "notifications.content.cleanup.failedMessage"
)
