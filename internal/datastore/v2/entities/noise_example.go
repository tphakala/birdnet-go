package entities

// NoiseExample is one analysis window of a clip the user marked as containing
// no animal, kept as an embedding for the noise example gate. It outlives the
// detection it was learned from: DetectionID is provenance only and becomes
// NULL when that detection is deleted. Rows are removed only by an explicit
// user action (the verdict of the detection changed away from noise, or
// clearing the learned noise).
type NoiseExample struct {
	ID uint `gorm:"primaryKey;autoIncrement"`
	// DetectionID is the detection the example was learned from, or NULL once that
	// detection is gone. NULLs are distinct in the unique index, so detached rows
	// never collide with each other or with a later detection.
	DetectionID *uint `gorm:"uniqueIndex:idx_noise_example_window"`
	// WindowOffsetMs is the start of the window in the clip, on a grid anchored at
	// the clip start.
	WindowOffsetMs int `gorm:"not null;uniqueIndex:idx_noise_example_window"`
	// EmbeddingSpace names the embedding space the vector belongs to.
	EmbeddingSpace string `gorm:"type:varchar(96);not null;index"`
	// Dim is the number of float32 values in Vector.
	Dim int `gorm:"not null"`
	// Score is the raw top head score of the window.
	Score float64 `gorm:"not null"`
	// Vector holds Dim little-endian float32 values, as the model produced them.
	Vector    []byte `gorm:"not null"`
	CreatedAt int64  `gorm:"not null"`

	Detection *Detection `gorm:"foreignKey:DetectionID;constraint:OnDelete:SET NULL"`
}
