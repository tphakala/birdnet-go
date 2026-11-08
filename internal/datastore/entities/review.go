// Package entities holds persistence-layer values shared by the legacy datastore, such as review verification statuses.
package entities

// VerificationStatus represents the review status values.
type VerificationStatus string

const (
	// VerificationCorrect indicates a detection is verified as correct.
	VerificationCorrect VerificationStatus = "correct"
	// VerificationFalsePositive indicates a detection is marked as a false positive.
	VerificationFalsePositive VerificationStatus = "false_positive"
)
