package httpcontroller

import (
	"crypto/rand"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/backup"
	"github.com/tphakala/birdnet-go/internal/conf"
)

const (
	keySize = 32 // 256-bit key
)

// initBackupRoutes initializes all backup-related routes
func (s *Server) initBackupRoutes() {
	// Create a group for backup routes that requires authentication
	backupGroup := s.Echo.Group("/backup")

	// Add authentication middleware
	backupGroup.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// Check for Cloudflare bypass
			if s.CloudflareAccess.IsEnabled(c) {
				return next(c)
			}

			// Check if authentication is required and user is authenticated
			if s.OAuth2Server.IsAuthenticationEnabled(s.RealIP(c)) && !s.IsAccessAllowed(c) {
				if c.Request().Header.Get("HX-Request") == "true" {
					return c.String(http.StatusUnauthorized, "Unauthorized")
				}
				return c.Redirect(http.StatusFound, "/login")
			}
			return next(c)
		}
	})

	// Register backup routes
	backupGroup.POST("/generate-key", s.handleGenerateKey)
	backupGroup.GET("/download-key", s.handleDownloadKey)
	backupGroup.POST("/import-key", s.handleImportKey)
}

// generateEncryptionKey generates a new random encryption key
func generateEncryptionKey() ([]byte, error) {
	key := make([]byte, keySize)
	_, err := rand.Read(key)
	if err != nil {
		return nil, fmt.Errorf("failed to generate random key: %w", err)
	}
	return key, nil
}

// handleGenerateKey handles the generation of a new encryption key
func (s *Server) handleGenerateKey(c echo.Context) error {
	// Create a backup manager instance
	manager := backup.NewManager(&s.Settings.Backup, log.Default())

	// Generate a new key using the manager
	keyHex, err := manager.GenerateEncryptionKey()
	if err != nil {
		s.Debug("Failed to generate encryption key: %v", err)
		return c.JSON(http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"message": "Failed to generate encryption key",
		})
	}

	// Update settings with the new key
	s.Settings.Backup.EncryptionKey = keyHex

	// Save settings to disk
	if err := conf.SaveSettings(); err != nil {
		s.Debug("Failed to save encryption key: %v", err)
		return c.JSON(http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"message": "Failed to save encryption key",
		})
	}

	// Enable encryption in settings if not already enabled
	if !s.Settings.Backup.Encryption {
		s.Settings.Backup.Encryption = true
		if err := conf.SaveSettings(); err != nil {
			s.Debug("Failed to update encryption setting: %v", err)
			// Don't return error here as the key was still generated successfully
		}
	}

	// Get the key file path to read creation time
	keyPath, err := manager.GetEncryptionKeyPath()
	if err != nil {
		s.Debug("Failed to get key path: %v", err)
		return c.JSON(http.StatusOK, map[string]interface{}{
			"success": true,
		})
	}

	// Get key file info
	info, err := os.Stat(keyPath)
	if err != nil {
		s.Debug("Failed to get key file info: %v", err)
		return c.JSON(http.StatusOK, map[string]interface{}{
			"success": true,
		})
	}

	// Read the key file to get its hash
	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		s.Debug("Failed to read key file: %v", err)
		return c.JSON(http.StatusOK, map[string]interface{}{
			"success": true,
		})
	}

	// Take first 8 characters of the key as a short identifier
	var keyHash string
	if len(keyBytes) >= 8 {
		keyHash = string(keyBytes[:8])
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"success":     true,
		"key_hash":    keyHash,
		"key_created": info.ModTime().UTC().Format("2006-01-02 15:04:05 UTC"),
	})
}

// handleDownloadKey handles the downloading of the encryption key
func (s *Server) handleDownloadKey(c echo.Context) error {
	// Create a backup manager to handle key operations
	manager := backup.NewManager(&s.Settings.Backup, log.Default())

	// Get the key file path
	keyPath, err := manager.GetEncryptionKeyPath()
	if err != nil {
		return c.String(http.StatusInternalServerError, "Failed to get encryption key path")
	}

	// Read the key file
	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return c.String(http.StatusNotFound, "No encryption key found")
		}
		return c.String(http.StatusInternalServerError, "Failed to read encryption key")
	}

	// Get key file info for creation time
	info, err := os.Stat(keyPath)
	if err != nil {
		return c.String(http.StatusInternalServerError, "Failed to get key file info")
	}

	// Get the key ID (first 8 characters)
	var keyID string
	if len(keyBytes) >= 8 {
		keyID = string(keyBytes[:8])
	}

	// Create the filename using just the key ID
	filename := fmt.Sprintf("birdnet-go_backup_key_%s.key", keyID)

	// Set headers for file download
	c.Response().Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	c.Response().Header().Set("Content-Type", "application/octet-stream")
	c.Response().Header().Set("Content-Security-Policy", "default-src 'none'")
	c.Response().Header().Set("X-Content-Type-Options", "nosniff")

	// Write the key file content with key ID and creation time
	content := fmt.Sprintf("BirdNET-Go Backup Encryption Key\nKey ID: %s\nCreated: %s UTC\nKey: %s\n",
		keyID,
		info.ModTime().UTC().Format("2006-01-02 15:04:05"),
		string(keyBytes))

	return c.String(http.StatusOK, content)
}

// handleImportKey handles the importing of an encryption key
func (s *Server) handleImportKey(c echo.Context) error {
	// Get the uploaded file
	file, err := c.FormFile("keyFile")
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"message": "No key file provided",
		})
	}

	// Open the uploaded file
	src, err := file.Open()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"message": "Failed to read uploaded file",
		})
	}
	defer src.Close()

	// Read the file content
	content := make([]byte, file.Size)
	if _, err := src.Read(content); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"message": "Failed to read key file content",
		})
	}

	// Create a backup manager instance
	manager := backup.NewManager(&s.Settings.Backup, log.Default())

	// Import the key using the manager
	if err := manager.ImportEncryptionKey(content); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"message": "Invalid key file format",
		})
	}

	// Get the key file path to read creation time
	keyPath, err := manager.GetEncryptionKeyPath()
	if err != nil {
		s.Debug("Failed to get key path: %v", err)
		return c.JSON(http.StatusOK, map[string]interface{}{
			"success": true,
		})
	}

	// Get key file info
	info, err := os.Stat(keyPath)
	if err != nil {
		s.Debug("Failed to get key file info: %v", err)
		return c.JSON(http.StatusOK, map[string]interface{}{
			"success": true,
		})
	}

	// Read the key file to get its hash
	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		s.Debug("Failed to read key file: %v", err)
		return c.JSON(http.StatusOK, map[string]interface{}{
			"success": true,
		})
	}

	// Take first 8 characters of the key as a short identifier
	var keyHash string
	if len(keyBytes) >= 8 {
		keyHash = string(keyBytes[:8])
	}

	// Enable encryption in settings if not already enabled
	if !s.Settings.Backup.Encryption {
		s.Settings.Backup.Encryption = true
		if err := conf.SaveSettings(); err != nil {
			s.Debug("Failed to update encryption setting: %v", err)
			// Don't return error here as the key was still imported successfully
		}
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"success":     true,
		"key_hash":    keyHash,
		"key_created": info.ModTime().UTC().Format("2006-01-02 15:04:05 UTC"),
	})
}
