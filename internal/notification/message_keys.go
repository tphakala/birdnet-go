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

	// Error notifications (title keys only — messages are raw error strings)
	MsgErrorCriticalSystem = "notifications.content.error.criticalSystem"
	MsgErrorApplication    = "notifications.content.error.application"
	MsgErrorImageProvider  = "notifications.content.error.imageProvider"
	MsgErrorCategory       = "notifications.content.error.categoryError"

	// Settings change toasts
	MsgSettingsReloadingBirdnet               = "notifications.content.settings.reloadingBirdnet"
	MsgSettingsRebuildingRangeFilter          = "notifications.content.settings.rebuildingRangeFilter"
	MsgSettingsUpdatingIntervals              = "notifications.content.settings.updatingIntervals"
	MsgSettingsReconfiguringMqtt              = "notifications.content.settings.reconfiguringMqtt"
	MsgSettingsReconfiguringBirdweather       = "notifications.content.settings.reconfiguringBirdweather"
	MsgSettingsReconfiguringEbird             = "notifications.content.settings.reconfiguringEbird"
	MsgSettingsReconfiguringStreams           = "notifications.content.settings.reconfiguringStreams"
	MsgSettingsReconfiguringTelemetry         = "notifications.content.settings.reconfiguringTelemetry"
	MsgSettingsReconfiguringSpeciesTracking   = "notifications.content.settings.reconfiguringSpeciesTracking"
	MsgSettingsReconfiguringPushNotifications = "notifications.content.settings.reconfiguringPushNotifications"
	MsgSettingsReconfiguringDynamicThresholds = "notifications.content.settings.reconfiguringDynamicThresholds"
	MsgSettingsWebserverRestart               = "notifications.content.settings.webserverRestartRequired"
	MsgSettingsOauthRestart                   = "notifications.content.settings.oauthRestartRequired"
	MsgSettingsDatabaseRestart                = "notifications.content.settings.databaseRestartRequired"
	MsgSettingsLoggingRestart                 = "notifications.content.settings.loggingRestartRequired"

	// Audio settings toasts
	MsgSettingsReconfiguringSoundLevel   = "notifications.content.settings.reconfiguringSoundLevel"
	MsgSettingsReconfiguringAudioSources = "notifications.content.settings.reconfiguringAudioSources"
	MsgSettingsRebuildingExtendedCapture = "notifications.content.settings.rebuildingExtendedCapture"
	MsgSettingsExtendedCaptureRestart    = "notifications.content.settings.extendedCaptureRestartRequired"
	MsgSettingsEqualizerFailed           = "notifications.content.settings.equalizerUpdateFailed"
	MsgSettingsEqualizerUpdated          = "notifications.content.settings.equalizerUpdated"

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
	MsgErrAlertV2Required        = "errors.alert.v2Required"
	MsgErrAlertInvalidID         = "errors.alert.invalidID"
	MsgErrAlertNotFound          = "errors.alert.notFound"
	MsgErrAlertInvalidBody       = "errors.alert.invalidBody"
	MsgErrAlertNameRequired      = "errors.alert.nameRequired"
	MsgErrAlertTypesRequired     = "errors.alert.typesRequired"
	MsgErrAlertDuplicateName     = "errors.alert.duplicateName"
	MsgErrAlertInvalidJSON       = "errors.alert.invalidJSON"
	MsgErrAlertInvalidEscalation = "errors.alert.invalidEscalation"
	MsgErrAlertEngineUnavailable = "errors.alert.engineUnavailable"

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

	// Buffer overwrite notifications
	MsgBufferOverloadTitle   = "notifications.content.buffer.overloadTitle"
	MsgBufferOverloadMessage = "notifications.content.buffer.overloadMessage"

	// Error burst grouping notifications
	MsgErrorBurstTitle   = "notifications.content.error.burstTitle"
	MsgErrorBurstMessage = "notifications.content.error.burstMessage"

	// ONNX Runtime availability notifications
	MsgORTUnavailableTitle   = "notifications.content.ort.unavailableTitle"
	MsgORTUnavailableMessage = "notifications.content.ort.unavailableMessage"

	// Model region staleness notifications (coordinate change makes an installed
	// regional model variant stale; recommend-only, never auto-switches a model)
	MsgModelRegionStaleTitle         = "notifications.content.region.staleTitle"
	MsgModelRegionStaleMessage       = "notifications.content.region.staleMessage"
	MsgModelRegionStaleGlobalMessage = "notifications.content.region.staleGlobalMessage"

	// Model path reconciliation notifications (a configured model file path
	// pointed at a file that no longer exists and was repaired to the installed
	// gallery model)
	MsgModelPathReconciledTitle   = "notifications.content.modelPath.reconciledTitle"
	MsgModelPathReconciledMessage = "notifications.content.modelPath.reconciledMessage"

	// Model path substitution notifications (a configured model file path pointed
	// at a file that no longer exists; the installed gallery model was used at
	// runtime instead, but the configuration was deliberately left unchanged
	// because the path is user-owned)
	MsgModelPathSubstitutedTitle   = "notifications.content.modelPath.substitutedTitle"
	MsgModelPathSubstitutedMessage = "notifications.content.modelPath.substitutedMessage"

	// Model path unreadable notifications (a configured model file could not be
	// read for a reason OTHER than absence: a permissions change, an I/O error, a
	// half-initialised mount). The installed gallery model is used at runtime, and
	// the configuration is never rewritten, because a transient failure must not be
	// made permanent. Worded separately from the substituted pair above: telling a
	// user their file "was not found" when it is present but unreadable sends them
	// looking in the wrong place.
	MsgModelPathUnreadableTitle   = "notifications.content.modelPath.unreadableTitle"
	MsgModelPathUnreadableMessage = "notifications.content.modelPath.unreadableMessage"

	// Model path built-in fallback notification (a configured model file is
	// confirmed absent AND no installed model exists to replace it, so the built-in
	// model is used instead). Shares MsgModelPathSubstitutedTitle, since the title
	// ("was not found") is accurate for this case too; only the body differs,
	// because there is no installed model path to name.
	MsgModelPathBuiltinMessage = "notifications.content.modelPath.builtinMessage"

	// Model registration notifications (a model assigned to an audio source is
	// not receiving audio, so it produces no detections)
	MsgModelNotRegisteredTitle   = "notifications.content.modelPath.notRegisteredTitle"
	MsgModelNotRegisteredMessage = "notifications.content.modelPath.notRegisteredMessage"
)
