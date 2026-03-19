package entities

// AppMetadata stores key-value application metadata such as wizard state.
type AppMetadata struct {
	Key   string `gorm:"primaryKey;size:255"`
	Value string `gorm:"size:4096"`
}
