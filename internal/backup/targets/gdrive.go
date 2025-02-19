// Package targets provides backup target implementations
package targets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/tphakala/birdnet-go/internal/backup"
)

const (
	defaultGDriveTimeout   = 30 * time.Second
	defaultGDriveBasePath  = "backups"
	gdriveMetadataVersion  = 1
	gdriveTempFilePrefix   = ".tmp."
	gdriveMetadataFileExt  = ".meta"
	defaultRateLimitTokens = 10                     // Maximum concurrent operations
	defaultRateLimitReset  = 100 * time.Millisecond // Time between token resets
	tempFileMaxAge         = 24 * time.Hour         // Maximum age for temporary files
	acquireTimeout         = 30 * time.Second       // Maximum time to wait for rate limit token
	quotaCacheDuration     = time.Minute            // How long to cache quota information
)

// rateLimiter implements a token bucket rate limiter
type rateLimiter struct {
	tokens chan struct{}
	ticker *time.Ticker
	mu     sync.Mutex
}

// newRateLimiter creates a new rate limiter
func newRateLimiter(maxTokens int, resetInterval time.Duration) *rateLimiter {
	rl := &rateLimiter{
		tokens: make(chan struct{}, maxTokens),
		ticker: time.NewTicker(resetInterval),
	}

	// Initialize tokens
	for i := 0; i < maxTokens; i++ {
		rl.tokens <- struct{}{}
	}

	// Start token replenishment
	go rl.replenish()

	return rl
}

// replenish continuously replenishes tokens
func (rl *rateLimiter) replenish() {
	for range rl.ticker.C {
		rl.mu.Lock()
		select {
		case rl.tokens <- struct{}{}:
		default:
			// Channel is full
		}
		rl.mu.Unlock()
	}
}

// acquire gets a token with timeout
func (rl *rateLimiter) acquire(ctx context.Context) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, acquireTimeout)
	defer cancel()

	select {
	case <-timeoutCtx.Done():
		if ctx.Err() == context.Canceled {
			return ctx.Err()
		}
		return fmt.Errorf("timeout waiting for rate limit token")
	case <-rl.tokens:
		return nil
	}
}

// stop stops the rate limiter
func (rl *rateLimiter) stop() {
	rl.ticker.Stop()
}

// quotaInfo holds storage quota information with caching
type quotaInfo struct {
	limit      int64
	usage      int64
	available  int64
	updateTime time.Time
}

// folderCache implements a simple cache for folder IDs
type folderCache struct {
	cache map[string]string // path -> ID mapping
	mu    sync.RWMutex
}

// newFolderCache creates a new folder cache
func newFolderCache() *folderCache {
	return &folderCache{
		cache: make(map[string]string),
	}
}

// get retrieves a folder ID from cache
func (fc *folderCache) get(path string) (string, bool) {
	fc.mu.RLock()
	defer fc.mu.RUnlock()
	id, ok := fc.cache[path]
	return id, ok
}

// set stores a folder ID in cache
func (fc *folderCache) set(path, id string) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.cache[path] = id
}

// GDriveMetadataV1 represents version 1 of the backup metadata format
type GDriveMetadataV1 struct {
	Version     int       `json:"version"`
	Timestamp   time.Time `json:"timestamp"`
	Size        int64     `json:"size"`
	Type        string    `json:"type"`
	Source      string    `json:"source"`
	IsDaily     bool      `json:"is_daily"`
	ConfigHash  string    `json:"config_hash,omitempty"`
	AppVersion  string    `json:"app_version,omitempty"`
	Compression string    `json:"compression,omitempty"`
}

// GDriveTarget implements the backup.Target interface for Google Drive storage
type GDriveTarget struct {
	config      GDriveTargetConfig
	logger      backup.Logger
	service     *drive.Service
	mu          sync.Mutex
	tempFiles   map[string]bool
	tempFilesMu sync.Mutex
	rateLimiter *rateLimiter
	folderCache *folderCache
	quota       *quotaInfo
	quotaMu     sync.RWMutex
}

// GDriveTargetConfig holds configuration for the Google Drive target
type GDriveTargetConfig struct {
	CredentialsFile string        // Path to the credentials.json file
	TokenFile       string        // Path to store the token.json file
	BasePath        string        // Base path in Google Drive
	Timeout         time.Duration // Timeout for operations
	Debug           bool          // Enable debug logging
	MaxRetries      int           // Maximum number of retries
	RetryBackoff    time.Duration // Time to wait between retries
	MinSpace        int64         // Minimum required space in bytes
}

// NewGDriveTarget creates a new Google Drive target with the given configuration
func NewGDriveTarget(config *GDriveTargetConfig, logger backup.Logger) (*GDriveTarget, error) {
	// Validate required fields
	if config.CredentialsFile == "" {
		return nil, backup.NewError(backup.ErrConfig, "gdrive: credentials file is required", nil)
	}

	// Set defaults for optional fields
	if config.Timeout == 0 {
		config.Timeout = defaultGDriveTimeout
	}
	if config.BasePath == "" {
		config.BasePath = defaultGDriveBasePath
	} else {
		config.BasePath = strings.TrimRight(config.BasePath, "/")
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}
	if config.RetryBackoff == 0 {
		config.RetryBackoff = time.Second
	}
	if config.TokenFile == "" {
		config.TokenFile = "token.json"
	}

	if logger == nil {
		logger = backup.DefaultLogger()
	}

	target := &GDriveTarget{
		config:      *config,
		logger:      logger,
		tempFiles:   make(map[string]bool),
		rateLimiter: newRateLimiter(defaultRateLimitTokens, defaultRateLimitReset),
		folderCache: newFolderCache(),
	}

	// Initialize the Google Drive service
	if err := target.initializeService(); err != nil {
		target.rateLimiter.stop()
		return nil, err
	}

	return target, nil
}

// initializeService initializes the Google Drive service
func (t *GDriveTarget) initializeService() error {
	ctx := context.Background()

	b, err := os.ReadFile(t.config.CredentialsFile)
	if err != nil {
		return backup.NewError(backup.ErrConfig, "gdrive: unable to read credentials file", err)
	}

	// If modifying these scopes, delete your previously saved token.json.
	config, err := google.ConfigFromJSON(b, drive.DriveFileScope)
	if err != nil {
		return backup.NewError(backup.ErrConfig, "gdrive: unable to parse credentials", err)
	}

	client, err := t.getClient(config)
	if err != nil {
		return err
	}

	srv, err := drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return backup.NewError(backup.ErrIO, "gdrive: unable to initialize service", err)
	}

	t.service = srv
	return nil
}

// getClient retrieves a token, saves the token, then returns the generated client
func (t *GDriveTarget) getClient(config *oauth2.Config) (*http.Client, error) {
	tok, err := t.tokenFromFile()
	if err != nil {
		tok, err = t.getTokenFromWeb(config)
		if err != nil {
			return nil, err
		}
		if err := t.saveToken(tok); err != nil {
			return nil, err
		}
	}
	return config.Client(context.Background(), tok), nil
}

// getTokenFromWeb requests a token from the web
func (t *GDriveTarget) getTokenFromWeb(config *oauth2.Config) (*oauth2.Token, error) {
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	t.logger.Printf("Go to the following link in your browser then type the authorization code:\n%v\n", authURL)

	var authCode string
	if _, err := fmt.Scan(&authCode); err != nil {
		return nil, backup.NewError(backup.ErrValidation, "gdrive: unable to read authorization code", err)
	}

	tok, err := config.Exchange(context.TODO(), authCode)
	if err != nil {
		return nil, backup.NewError(backup.ErrValidation, "gdrive: unable to retrieve token from web", err)
	}
	return tok, nil
}

// tokenFromFile retrieves a token from a local file
func (t *GDriveTarget) tokenFromFile() (*oauth2.Token, error) {
	f, err := os.Open(t.config.TokenFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	tok := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(tok)
	return tok, err
}

// saveToken saves a token to a file
func (t *GDriveTarget) saveToken(token *oauth2.Token) error {
	f, err := os.OpenFile(t.config.TokenFile, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return backup.NewError(backup.ErrIO, "gdrive: unable to cache oauth token", err)
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(token)
}

// Name returns the name of this target
func (t *GDriveTarget) Name() string {
	return "gdrive"
}

// isTransientError checks if an error is likely temporary
func (t *GDriveTarget) isTransientError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "temporary") ||
		strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "internal server error")
}

// withRetry executes an operation with retry logic
func (t *GDriveTarget) withRetry(ctx context.Context, op func() error) error {
	var lastErr error
	for i := 0; i < t.config.MaxRetries; i++ {
		select {
		case <-ctx.Done():
			return backup.NewError(backup.ErrCanceled, "gdrive: operation canceled", ctx.Err())
		default:
		}

		err := op()
		if err == nil {
			return nil
		}

		lastErr = err
		if !t.isTransientError(err) {
			return err
		}

		if t.config.Debug {
			t.logger.Printf("GDrive: Retrying operation after error: %v (attempt %d/%d)", err, i+1, t.config.MaxRetries)
		}
		time.Sleep(t.config.RetryBackoff * time.Duration(i+1))
	}

	return backup.NewError(backup.ErrIO, "gdrive: operation failed after retries", lastErr)
}

// isAPIError checks if an error is a Google API error and handles specific cases
func (t *GDriveTarget) isAPIError(err error) (bool, error) {
	if err == nil {
		return false, nil
	}

	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case 401:
			// Token expired or invalid, try to refresh
			if refreshErr := t.refreshTokenIfNeeded(context.Background()); refreshErr != nil {
				return true, backup.NewError(backup.ErrSecurity, "gdrive: authentication failed and refresh failed", refreshErr)
			}
			// Token refreshed, caller should retry the operation
			return true, backup.NewError(backup.ErrSecurity, "gdrive: token refreshed, please retry", nil)
		case 403:
			if strings.Contains(apiErr.Message, "Rate Limit") {
				return true, backup.NewError(backup.ErrIO, "gdrive: rate limit exceeded", err)
			}
			return true, backup.NewError(backup.ErrSecurity, "gdrive: permission denied", err)
		case 404:
			return true, backup.NewError(backup.ErrNotFound, "gdrive: resource not found", err)
		case 429:
			return true, backup.NewError(backup.ErrIO, "gdrive: too many requests", err)
		case 500, 502, 503, 504:
			return true, backup.NewError(backup.ErrIO, "gdrive: server error", err)
		default:
			return true, backup.NewError(backup.ErrIO, fmt.Sprintf("gdrive: API error %d", apiErr.Code), err)
		}
	}
	return false, nil
}

// checkStorageQuota verifies sufficient space is available before upload
func (t *GDriveTarget) checkStorageQuota(ctx context.Context, fileSize int64) error {
	t.quotaMu.RLock()
	if t.quota != nil && time.Since(t.quota.updateTime) < quotaCacheDuration {
		// Use cached quota information
		if t.quota.limit > 0 {
			available := t.quota.limit - t.quota.usage
			if available < fileSize {
				t.quotaMu.RUnlock()
				return backup.NewError(backup.ErrInsufficientSpace,
					fmt.Sprintf("insufficient space: need %d bytes, have %d bytes", fileSize, available),
					nil)
			}
		}
		t.quotaMu.RUnlock()
		return nil
	}
	t.quotaMu.RUnlock()

	// Need to fetch new quota information
	t.quotaMu.Lock()
	defer t.quotaMu.Unlock()

	about, err := t.service.About.Get().Fields("storageQuota").Context(ctx).Do()
	if err != nil {
		if isAPI, apiErr := t.isAPIError(err); isAPI {
			return apiErr
		}
		return backup.NewError(backup.ErrIO, "gdrive: failed to get storage quota", err)
	}

	// Update cache
	t.quota = &quotaInfo{
		limit:      about.StorageQuota.Limit,
		usage:      about.StorageQuota.Usage,
		available:  about.StorageQuota.Limit - about.StorageQuota.Usage,
		updateTime: time.Now(),
	}

	// Check quota
	if t.quota.limit > 0 {
		available := t.quota.limit - t.quota.usage
		if available < fileSize {
			return backup.NewError(backup.ErrInsufficientSpace,
				fmt.Sprintf("insufficient space: need %d bytes, have %d bytes", fileSize, available),
				nil)
		}
	}

	return nil
}

// refreshTokenIfNeeded checks and refreshes the OAuth token if necessary
func (t *GDriveTarget) refreshTokenIfNeeded(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	tok, err := t.tokenFromFile()
	if err != nil {
		return backup.NewError(backup.ErrSecurity, "gdrive: failed to read token", err)
	}

	if !tok.Valid() {
		b, err := os.ReadFile(t.config.CredentialsFile)
		if err != nil {
			return backup.NewError(backup.ErrConfig, "gdrive: unable to read credentials file", err)
		}

		config, err := google.ConfigFromJSON(b, drive.DriveFileScope)
		if err != nil {
			return backup.NewError(backup.ErrConfig, "gdrive: unable to parse credentials", err)
		}

		// Try to refresh the token
		newToken, err := config.TokenSource(ctx, tok).Token()
		if err != nil {
			return backup.NewError(backup.ErrSecurity, "gdrive: failed to refresh token", err)
		}

		// Save the new token
		if err := t.saveToken(newToken); err != nil {
			return err
		}

		// Update the client
		client := config.Client(ctx, newToken)
		srv, err := drive.NewService(ctx, option.WithHTTPClient(client))
		if err != nil {
			return backup.NewError(backup.ErrIO, "gdrive: unable to create service with new token", err)
		}

		t.service = srv
	}

	return nil
}

// cleanupOrphanedFiles removes old temporary files
func (t *GDriveTarget) cleanupOrphanedFiles(ctx context.Context) error {
	if t.config.Debug {
		t.logger.Printf("🔄 GDrive: Cleaning up orphaned temporary files")
	}

	query := fmt.Sprintf("name contains '%s' and modifiedTime < '%s' and trashed=false",
		gdriveTempFilePrefix,
		time.Now().Add(-tempFileMaxAge).Format(time.RFC3339))

	files, err := t.service.Files.List().Q(query).Fields("files(id,name,modifiedTime)").Context(ctx).Do()
	if err != nil {
		if isAPI, apiErr := t.isAPIError(err); isAPI {
			return apiErr
		}
		return backup.NewError(backup.ErrIO, "gdrive: failed to list temporary files", err)
	}

	for _, file := range files.Files {
		if err := t.service.Files.Delete(file.Id).Context(ctx).Do(); err != nil {
			t.logger.Printf("⚠️ GDrive: Warning: failed to delete orphaned temp file %s: %v", file.Name, err)
		} else if t.config.Debug {
			t.logger.Printf("✅ GDrive: Deleted orphaned temp file: %s", file.Name)
		}
	}

	return nil
}

// Store implements the backup.Target interface with improved error handling and quota checking
func (t *GDriveTarget) Store(ctx context.Context, sourcePath string, metadata *backup.Metadata) error {
	if t.config.Debug {
		t.logger.Printf("🔄 GDrive: Storing backup %s", filepath.Base(sourcePath))
	}

	// Check file size
	fileInfo, err := os.Stat(sourcePath)
	if err != nil {
		return backup.NewError(backup.ErrIO, "gdrive: failed to get source file info", err)
	}

	// Check storage quota
	if err := t.checkStorageQuota(ctx, fileInfo.Size()); err != nil {
		return err
	}

	// Refresh token if needed
	if err := t.refreshTokenIfNeeded(ctx); err != nil {
		return err
	}

	// Acquire rate limit token
	if err := t.rateLimiter.acquire(ctx); err != nil {
		return backup.NewError(backup.ErrCanceled, "gdrive: operation canceled while waiting for rate limit", err)
	}

	// Create versioned metadata
	gdriveMetadata := GDriveMetadataV1{
		Version:    gdriveMetadataVersion,
		Timestamp:  metadata.Timestamp,
		Size:       metadata.Size,
		Type:       metadata.Type,
		Source:     metadata.Source,
		IsDaily:    metadata.IsDaily,
		ConfigHash: metadata.ConfigHash,
		AppVersion: metadata.AppVersion,
	}

	// Marshal metadata
	metadataBytes, err := json.Marshal(gdriveMetadata)
	if err != nil {
		return backup.NewError(backup.ErrIO, "gdrive: failed to marshal metadata", err)
	}

	return t.withRetry(ctx, func() error {
		// Ensure the backup folder exists
		folderId, err := t.ensureFolder(ctx, t.config.BasePath)
		if err != nil {
			return err
		}

		// Upload the backup file
		backupFile := &drive.File{
			Name:    filepath.Base(sourcePath),
			Parents: []string{folderId},
		}

		file, err := os.Open(sourcePath)
		if err != nil {
			return backup.NewError(backup.ErrIO, "gdrive: failed to open source file", err)
		}
		defer file.Close()

		if _, err = t.service.Files.Create(backupFile).Media(file).Context(ctx).Do(); err != nil {
			return backup.NewError(backup.ErrIO, "gdrive: failed to upload backup file", err)
		}

		// Create and upload metadata file
		metadataFile := &drive.File{
			Name:    filepath.Base(sourcePath) + gdriveMetadataFileExt,
			Parents: []string{folderId},
		}

		metadataReader := strings.NewReader(string(metadataBytes))
		if _, err = t.service.Files.Create(metadataFile).Media(metadataReader).Context(ctx).Do(); err != nil {
			return backup.NewError(backup.ErrIO, "gdrive: failed to upload metadata file", err)
		}

		if t.config.Debug {
			t.logger.Printf("✅ GDrive: Successfully stored backup %s with metadata", filepath.Base(sourcePath))
		}

		return nil
	})
}

// ensureFolder ensures the target folder exists in Google Drive with caching
func (t *GDriveTarget) ensureFolder(ctx context.Context, path string) (string, error) {
	// Check cache first
	if id, ok := t.folderCache.get(path); ok {
		// Verify the folder still exists
		_, err := t.service.Files.Get(id).Fields("id").Context(ctx).Do()
		if err == nil {
			return id, nil
		}
		// If verification fails, continue with folder creation
	}

	parts := strings.Split(path, "/")
	var parentId string = "root"
	currentPath := ""

	for _, part := range parts {
		if part == "" {
			continue
		}

		currentPath = filepath.Join(currentPath, part)

		// Check cache for partial path
		if id, ok := t.folderCache.get(currentPath); ok {
			parentId = id
			continue
		}

		query := fmt.Sprintf("name='%s' and mimeType='application/vnd.google-apps.folder' and '%s' in parents and trashed=false", part, parentId)
		files, err := t.service.Files.List().Q(query).Fields("files(id)").Context(ctx).Do()
		if err != nil {
			return "", backup.NewError(backup.ErrIO, "gdrive: failed to list folders", err)
		}

		if len(files.Files) > 0 {
			parentId = files.Files[0].Id
			t.folderCache.set(currentPath, parentId)
			continue
		}

		// Folder doesn't exist, create it
		folder := &drive.File{
			Name:     part,
			MimeType: "application/vnd.google-apps.folder",
			Parents:  []string{parentId},
		}

		file, err := t.service.Files.Create(folder).Fields("id").Context(ctx).Do()
		if err != nil {
			return "", backup.NewError(backup.ErrIO, "gdrive: failed to create folder", err)
		}

		parentId = file.Id
		t.folderCache.set(currentPath, parentId)
	}

	return parentId, nil
}

// List implements the backup.Target interface
func (t *GDriveTarget) List(ctx context.Context) ([]backup.BackupInfo, error) {
	if t.config.Debug {
		t.logger.Printf("🔄 GDrive: Listing backups")
	}

	var backups []backup.BackupInfo

	return backups, t.withRetry(ctx, func() error {
		// Get the backup folder ID
		folderId, err := t.ensureFolder(ctx, t.config.BasePath)
		if err != nil {
			return err
		}

		query := fmt.Sprintf("'%s' in parents and trashed=false", folderId)
		pageToken := ""
		for {
			fileList, err := t.service.Files.List().Q(query).
				Fields("nextPageToken, files(id, name, size, createdTime, description)").
				PageToken(pageToken).
				Context(ctx).Do()
			if err != nil {
				return backup.NewError(backup.ErrIO, "gdrive: failed to list files", err)
			}

			for _, file := range fileList.Files {
				// Skip metadata files
				if strings.HasSuffix(file.Name, gdriveMetadataFileExt) {
					continue
				}

				createdTime, err := time.Parse(time.RFC3339, file.CreatedTime)
				if err != nil {
					t.logger.Printf("Warning: failed to parse creation time for file %s: %v", file.Name, err)
					continue
				}

				backups = append(backups, backup.BackupInfo{
					Target: file.Name,
					Metadata: backup.Metadata{
						ID:        file.Id,
						Timestamp: createdTime,
						Size:      file.Size,
					},
				})
			}

			// If there are no more pages, break the loop
			if fileList.NextPageToken == "" {
				break
			}
			pageToken = fileList.NextPageToken
		}

		return nil
	})
}

// Delete implements the backup.Target interface
func (t *GDriveTarget) Delete(ctx context.Context, id string) error {
	if t.config.Debug {
		t.logger.Printf("🔄 GDrive: Deleting backup %s", id)
	}

	return t.withRetry(ctx, func() error {
		if err := t.service.Files.Delete(id).Context(ctx).Do(); err != nil {
			return backup.NewError(backup.ErrIO, "gdrive: failed to delete file", err)
		}

		// Try to delete the metadata file if it exists
		metadataQuery := fmt.Sprintf("name='%s%s' and trashed=false", id, gdriveMetadataFileExt)
		files, err := t.service.Files.List().Q(metadataQuery).Fields("files(id)").Context(ctx).Do()
		if err != nil {
			t.logger.Printf("⚠️ GDrive: Warning: failed to find metadata file for %s: %v", id, err)
			return nil
		}

		for _, file := range files.Files {
			if err := t.service.Files.Delete(file.Id).Context(ctx).Do(); err != nil {
				t.logger.Printf("⚠️ GDrive: Warning: failed to delete metadata file %s: %v", file.Id, err)
			}
		}

		if t.config.Debug {
			t.logger.Printf("✅ GDrive: Successfully deleted backup %s", id)
		}

		return nil
	})
}

// Validate performs comprehensive validation of the Google Drive target
func (t *GDriveTarget) Validate() error {
	ctx, cancel := context.WithTimeout(context.Background(), t.config.Timeout)
	defer cancel()

	return t.withRetry(ctx, func() error {
		// Try to create and remove a test folder
		testFolder := ".validation_test_" + time.Now().Format("20060102150405")
		folderId, err := t.ensureFolder(ctx, testFolder)
		if err != nil {
			return backup.NewError(backup.ErrValidation, "gdrive: failed to create test folder", err)
		}

		// Create a test file
		testFile := &drive.File{
			Name:    "test.txt",
			Parents: []string{folderId},
		}

		testContent := strings.NewReader("test")
		file, err := t.service.Files.Create(testFile).Media(testContent).Context(ctx).Do()
		if err != nil {
			return backup.NewError(backup.ErrValidation, "gdrive: failed to create test file", err)
		}

		// Clean up test file
		if err := t.service.Files.Delete(file.Id).Context(ctx).Do(); err != nil {
			t.logger.Printf("Warning: failed to delete test file: %v", err)
		}

		// Clean up test folder
		if err := t.service.Files.Delete(folderId).Context(ctx).Do(); err != nil {
			t.logger.Printf("Warning: failed to delete test folder: %v", err)
		}

		// Check available space
		quota, err := t.getQuota(context.Background())
		if err != nil {
			return backup.NewError(backup.ErrValidation, "gdrive: failed to check available space", err)
		}

		if t.config.Debug {
			t.logger.Printf("💾 GDrive: Available space: %.2f GB", float64(quota.available)/(1024*1024*1024))
		}

		return nil
	})
}

// Close cleans up resources
func (t *GDriveTarget) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Stop the rate limiter
	t.rateLimiter.stop()

	// Try to clean up any orphaned temporary files
	ctx, cancel := context.WithTimeout(context.Background(), t.config.Timeout)
	defer cancel()

	if err := t.cleanupOrphanedFiles(ctx); err != nil {
		t.logger.Printf("Warning: failed to clean up orphaned files during shutdown: %v", err)
	}

	return nil
}

// NewGDriveTargetFromMap creates a new Google Drive target from a map configuration
func NewGDriveTargetFromMap(settings map[string]interface{}) (*GDriveTarget, error) {
	config := GDriveTargetConfig{}

	// Required settings
	credentialsFile, ok := settings["credentials_file"].(string)
	if !ok {
		return nil, backup.NewError(backup.ErrConfig, "gdrive: credentials_file is required", nil)
	}
	config.CredentialsFile = credentialsFile

	// Optional settings
	if tokenFile, ok := settings["token_file"].(string); ok {
		config.TokenFile = tokenFile
	}
	if basePath, ok := settings["path"].(string); ok {
		config.BasePath = basePath
	}
	if timeout, ok := settings["timeout"].(string); ok {
		duration, err := time.ParseDuration(timeout)
		if err != nil {
			return nil, backup.NewError(backup.ErrValidation, "gdrive: invalid timeout format", err)
		}
		config.Timeout = duration
	}
	if debug, ok := settings["debug"].(bool); ok {
		config.Debug = debug
	}
	if maxRetries, ok := settings["max_retries"].(int); ok {
		config.MaxRetries = maxRetries
	}
	if retryBackoff, ok := settings["retry_backoff"].(string); ok {
		duration, err := time.ParseDuration(retryBackoff)
		if err != nil {
			return nil, backup.NewError(backup.ErrValidation, "gdrive: invalid retry_backoff format", err)
		}
		config.RetryBackoff = duration
	}
	if minSpace, ok := settings["min_space"].(int64); ok {
		config.MinSpace = minSpace
	}

	var logger backup.Logger
	if l, ok := settings["logger"].(backup.Logger); ok {
		logger = l
	}

	return NewGDriveTarget(&config, logger)
}

// getQuota retrieves the current quota information from Google Drive
func (t *GDriveTarget) getQuota(ctx context.Context) (*quotaInfo, error) {
	about, err := t.service.About.Get().Fields("storageQuota").Context(ctx).Do()
	if err != nil {
		return nil, err
	}

	t.quotaMu.Lock()
	defer t.quotaMu.Unlock()

	t.quota = &quotaInfo{
		limit:      about.StorageQuota.Limit,
		usage:      about.StorageQuota.Usage,
		available:  about.StorageQuota.Limit - about.StorageQuota.Usage,
		updateTime: time.Now(),
	}

	return t.quota, nil
}
