package repository

// Table name constants for standard schema (SQLite).
const (
	tableLabels               = "labels"
	tableAIModels             = "ai_models"
	tableModelLabels          = "model_labels"
	tableAudioSources         = "audio_sources"
	tableDetections           = "detections"
	tableDetectionPredictions = "detection_predictions"
	tableDetectionReviews     = "detection_reviews"
	tableDetectionComments    = "detection_comments"
	tableDetectionLocks       = "detection_locks"
)

// Table name constants for v2 prefixed schema (MySQL).
const (
	tableV2Labels               = "v2_labels"
	tableV2AIModels             = "v2_ai_models"
	tableV2ModelLabels          = "v2_model_labels"
	tableV2AudioSources         = "v2_audio_sources"
	tableV2Detections           = "v2_detections"
	tableV2DetectionPredictions = "v2_detection_predictions"
	tableV2DetectionReviews     = "v2_detection_reviews"
	tableV2DetectionComments    = "v2_detection_comments"
	tableV2DetectionLocks       = "v2_detection_locks"
)
