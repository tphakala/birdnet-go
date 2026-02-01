package repository

// Table name constants for standard schema (SQLite).
const (
	tableLabelTypes           = "label_types"
	tableTaxonomicClasses     = "taxonomic_classes"
	tableLabels               = "labels"
	tableAIModels             = "ai_models"
	tableAudioSources         = "audio_sources"
	tableDetections           = "detections"
	tableDetectionPredictions = "detection_predictions"
	tableDetectionReviews     = "detection_reviews"
	tableDetectionComments    = "detection_comments"
	tableDetectionLocks       = "detection_locks"
	// Auxiliary tables
	tableDailyEvents         = "daily_events"
	tableHourlyWeathers      = "hourly_weathers"
	tableImageCaches         = "image_caches"
	tableDynamicThresholds   = "dynamic_thresholds"
	tableThresholdEvents     = "threshold_events"
	tableNotificationHistory = "notification_histories"
)

// Table name constants for v2 prefixed schema (MySQL).
const (
	tableV2LabelTypes           = "v2_label_types"
	tableV2TaxonomicClasses     = "v2_taxonomic_classes"
	tableV2Labels               = "v2_labels"
	tableV2AIModels             = "v2_ai_models"
	tableV2AudioSources         = "v2_audio_sources"
	tableV2Detections           = "v2_detections"
	tableV2DetectionPredictions = "v2_detection_predictions"
	tableV2DetectionReviews     = "v2_detection_reviews"
	tableV2DetectionComments    = "v2_detection_comments"
	tableV2DetectionLocks       = "v2_detection_locks"
	// Auxiliary tables
	tableV2DailyEvents         = "v2_daily_events"
	tableV2HourlyWeathers      = "v2_hourly_weathers"
	tableV2ImageCaches         = "v2_image_caches"
	tableV2DynamicThresholds   = "v2_dynamic_thresholds"
	tableV2ThresholdEvents     = "v2_threshold_events"
	tableV2NotificationHistory = "v2_notification_histories"
)
