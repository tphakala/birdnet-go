// Package restore provides the restore command for BirdNET-Go
package restore

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tphakala/birdnet-go/internal/backup"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// Command creates and returns the restore command
func Command(settings *conf.Settings) *cobra.Command {
	var outputPath string

	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Restore a backup archive",
		Long:  `Restore command handles decryption and restoration of backup archives.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("please specify a subcommand: decrypt")
		},
	}

	// Add decrypt subcommand
	decryptCmd := &cobra.Command{
		Use:   "decrypt [backup file]",
		Short: "Decrypt an encrypted backup archive",
		Long:  `Decrypt an encrypted backup archive using the configured encryption key.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDecrypt(settings, args[0], outputPath)
		},
	}

	// Add flags
	decryptCmd.Flags().StringVarP(&outputPath, "output", "o", "", "Output path for decrypted file (default: same directory as source with .tar.gz extension)")

	cmd.AddCommand(decryptCmd)
	return cmd
}

func runDecrypt(settings *conf.Settings, backupPath string, outputPath string) error {
	// Verify the input file exists
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		return fmt.Errorf("backup file does not exist: %s", backupPath)
	}

	// Check for metadata file
	metaPath := backupPath + ".meta"
	if _, err := os.Stat(metaPath); os.IsNotExist(err) {
		return fmt.Errorf("metadata file does not exist: %s", metaPath)
	}

	// Read the metadata file
	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		return fmt.Errorf("failed to read metadata file: %w", err)
	}

	// Parse metadata
	var metadata backup.Metadata
	if err := json.Unmarshal(metaData, &metadata); err != nil {
		return fmt.Errorf("failed to parse metadata: %w", err)
	}

	// Check if decryption is needed
	if !metadata.Encrypted {
		log.Printf("ℹ️ Backup file is not encrypted, no decryption needed: %s", backupPath)
		return nil
	}

	// Create a backup manager
	manager := backup.NewManager(&settings.Backup, log.Default())

	// Enable encryption for decryption to work
	settings.Backup.Encryption = true

	// Validate encryption is configured
	if err := manager.ValidateEncryption(); err != nil {
		return fmt.Errorf("encryption validation failed: %w", err)
	}

	// Read the encrypted file
	encryptedData, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("failed to read encrypted file: %w", err)
	}

	// Decrypt the data
	decryptedData, err := manager.DecryptData(encryptedData)
	if err != nil {
		return fmt.Errorf("failed to decrypt data: %w", err)
	}

	// If no output path specified, use the same directory as source with .tar.gz extension
	if outputPath == "" {
		dir := filepath.Dir(backupPath)
		filename := filepath.Base(backupPath)
		// Remove .enc extension if present
		if filepath.Ext(filename) == ".enc" {
			filename = filename[:len(filename)-4]
		}
		outputPath = filepath.Join(dir, filename+".tar.gz")
	}

	// Create output directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Write the decrypted data
	if err := os.WriteFile(outputPath, decryptedData, 0600); err != nil {
		return fmt.Errorf("failed to write decrypted file: %w", err)
	}

	// Update metadata for decrypted state
	metadata.Encrypted = false
	metadata.Size = int64(len(decryptedData))

	// Convert updated metadata to JSON
	updatedMetaData, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal updated metadata: %w", err)
	}

	// Write updated metadata file
	newMetaPath := outputPath + ".meta"
	if err := os.WriteFile(newMetaPath, updatedMetaData, 0600); err != nil {
		return fmt.Errorf("failed to write updated metadata file: %w", err)
	}

	log.Printf("✅ Successfully decrypted backup to: %s", outputPath)
	log.Printf("✅ Updated metadata file written to: %s", newMetaPath)
	return nil
}
