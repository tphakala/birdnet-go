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
	MsgSettingsExtendedCaptureRestart  = "notifications.content.settings.extendedCaptureRestartRequired"
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

	// API error response keys — used in ErrorResponse.ErrorKey for frontend translation.
	// Namespace: errors.<handler>.<errorType>

	// Auth errors
	MsgErrAuthTooManyAttempts     = "errors.auth.tooManyAttempts"
	MsgErrAuthCredentialsRequired = "errors.auth.credentialsRequired"
	MsgErrAuthInvalidCredentials  = "errors.auth.invalidCredentials"
	MsgErrAuthMissingCode         = "errors.auth.missingCode"
	MsgErrAuthServiceUnavailable  = "errors.auth.serviceUnavailable"
	MsgErrAuthTimeout             = "errors.auth.timeout"
	MsgErrAuthExchangeFailed      = "errors.auth.exchangeFailed"
	MsgErrAuthSessionError        = "errors.auth.sessionError"

	// Alert CRUD errors
	MsgErrAlertV2Required    = "errors.alert.v2Required"
	MsgErrAlertInvalidID     = "errors.alert.invalidID"
	MsgErrAlertNotFound      = "errors.alert.notFound"
	MsgErrAlertInvalidBody   = "errors.alert.invalidBody"
	MsgErrAlertNameRequired  = "errors.alert.nameRequired"
	MsgErrAlertTypesRequired = "errors.alert.typesRequired"
	MsgErrAlertDuplicateName = "errors.alert.duplicateName"
	MsgErrAlertInvalidJSON   = "errors.alert.invalidJSON"

	// Detection errors
	MsgErrDetectionInvalidDate = "errors.detection.invalidDate"

	// Backup errors
	MsgErrBackupInvalidType       = "errors.backup.invalidType"
	MsgErrBackupAlreadyRunning    = "errors.backup.alreadyRunning"
	MsgErrBackupDBInfo            = "errors.backup.dbInfoFailed"
	MsgErrBackupDiskSpace         = "errors.backup.diskSpaceCheck"
	MsgErrBackupInsufficientSpace = "errors.backup.insufficientSpace"
	MsgErrBackupCreateFailed      = "errors.backup.createFailed"
	MsgErrBackupNotFound          = "errors.backup.notFound"
	MsgErrBackupNotReady          = "errors.backup.notReady"
	MsgErrBackupFileNotFound      = "errors.backup.fileNotFound"
	MsgErrBackupSQLiteOnly        = "errors.backup.sqliteOnly"
	MsgErrBackupDBNotConfigured   = "errors.backup.dbNotConfigured"
	MsgErrBackupUnsupportedType   = "errors.backup.unsupportedType"
	MsgErrBackupV2NotInit         = "errors.backup.v2NotInitialized"

	// Migration errors
	MsgErrMigrationNotConfigured = "errors.migration.notConfigured"
	MsgErrMigrationPreFlight     = "errors.migration.preFlightFailed"
	MsgErrMigrationInvalidBody   = "errors.migration.invalidBody"
	MsgErrMigrationRecordCount   = "errors.migration.recordCountFailed"
	MsgErrMigrationStartFailed   = "errors.migration.startFailed"
	MsgErrMigrationInitFailed    = "errors.migration.initFailed"
	MsgErrMigrationResumeFailed  = "errors.migration.resumeFailed"

	// Legacy cleanup errors
	MsgErrCleanupNoLegacyDB      = "errors.cleanup.noLegacyDB"
	MsgErrCleanupAccessFailed    = "errors.cleanup.accessFailed"
	MsgErrCleanupRestartRequired = "errors.cleanup.restartRequired"
	MsgErrCleanupSafetyCheck     = "errors.cleanup.safetyCheck"
	MsgErrCleanupNoLegacyTables  = "errors.cleanup.noLegacyTables"
	MsgErrCleanupRestartNeeded   = "errors.cleanup.restartNeeded"
	MsgErrCleanupAlreadyRunning  = "errors.cleanup.alreadyRunning"

	// Integration test errors
	MsgErrIntegMQTTDisabled      = "errors.integration.mqttDisabled"
	MsgErrIntegMQTTNotConfigured = "errors.integration.mqttNotConfigured"
	MsgErrIntegMQTTMetrics       = "errors.integration.mqttMetricsUnavailable"
	MsgErrIntegMQTTClientFailed  = "errors.integration.mqttClientFailed"
	MsgErrIntegBWDisabled        = "errors.integration.birdweatherDisabled"
	MsgErrIntegBWNotConfigured   = "errors.integration.birdweatherNotConfigured"
	MsgErrIntegBWClientFailed    = "errors.integration.birdweatherClientFailed"
	MsgErrIntegNoWeatherProvider = "errors.integration.noWeatherProvider"
	MsgErrIntegOWKeyRequired     = "errors.integration.openWeatherKeyRequired"
	MsgErrIntegProcessorUnavail  = "errors.integration.processorUnavailable"
	MsgErrIntegDiscoveryFailed   = "errors.integration.discoveryFailed"

	// Notification errors
	MsgErrNotifServiceUnavailable = "errors.notification.serviceUnavailable"
	MsgErrNotifIDRequired         = "errors.notification.idRequired"
	MsgErrNotifNotFound           = "errors.notification.notFound"
	MsgErrNotifHostRequired       = "errors.notification.hostRequired"
	MsgErrNotifInvalidHost        = "errors.notification.invalidHost"
	MsgErrNotifRateLimit          = "errors.notification.rateLimit"

	// Debug errors
	MsgErrDebugNotEnabled = "errors.debug.notEnabled"

	// Terminal errors
	MsgErrTerminalDisabled = "errors.terminal.disabled"
)
