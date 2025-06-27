// Package backup provides functionality for backing up application data
package backup

import (
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// UpdateSettings updates the backup manager with new settings
func (m *Manager) UpdateSettings(config *conf.BackupConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Copy the backup configuration to the manager
	m.config = config
	m.logger.Info("Backup settings updated", "enabled", config.Enabled)

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
		"backup", m.getBackupTimeout(),
		"store", m.getStoreTimeout(),
		"cleanup", m.getCleanupTimeout(),
		"delete", m.getDeleteTimeout())

	return nil
}
