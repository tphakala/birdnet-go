// Package backup provides functionality for backing up application data
package backup

import (
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// UpdateSettings updates the backup manager with new settings
func (m *Manager) UpdateSettings(config *conf.BackupConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Copy the backup configuration to the manager
	m.config = config
	m.logger.Info("Backup settings updated", logger.Bool("enabled", config.Enabled))

	// Update encryption settings if enabled
	if config.Encryption {
		if config.EncryptionKey == "" {
			return errors.Newf("encryption enabled but no encryption key provided").
				Component("backup").
				Category(errors.CategoryConfiguration).
				Context("operation", "update_settings").
				Build()
		}

		// Optional: Update and validate encryption key if your Manager has an encryptionKey field
		// For now this is handled by the existing methods when required
	}

	// Log the updated timeout settings
	m.logger.Info("Backup timeout settings updated",
		logger.Any("backup", m.getBackupTimeout()),
		logger.Any("store", m.getStoreTimeout()),
		logger.Any("cleanup", m.getCleanupTimeout()),
		logger.Any("delete", m.getDeleteTimeout()))

	return nil
}
