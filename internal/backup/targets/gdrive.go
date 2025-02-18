package targets

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/tphakala/birdnet-go/internal/backup"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

// GDriveTarget implements the backup.Target interface for Google Drive storage
type GDriveTarget struct {
	bucketName  string
	basePath    string
	credentials string
	client      *storage.Client
	debug       bool
	logger      backup.Logger
}

// NewGDriveTarget creates a new Google Drive target with the given configuration
func NewGDriveTarget(settings map[string]interface{}) (*GDriveTarget, error) {
	target := &GDriveTarget{}

	// Required settings
	bucketName, ok := settings["bucket"].(string)
	if !ok {
		return nil, fmt.Errorf("gdrive: bucket name is required")
	}
	target.bucketName = bucketName

	credentials, ok := settings["credentials"].(string)
	if !ok {
		return nil, fmt.Errorf("gdrive: credentials file path is required")
	}
	target.credentials = credentials

	// Optional settings
	if path, ok := settings["path"].(string); ok {
		target.basePath = strings.TrimRight(path, "/")
	} else {
		target.basePath = "backups"
	}

	if debug, ok := settings["debug"].(bool); ok {
		target.debug = debug
	}

	if logger, ok := settings["logger"].(backup.Logger); ok {
		target.logger = logger
	} else {
		target.logger = backup.DefaultLogger()
	}

	// Initialize the client
	ctx := context.Background()
	client, err := storage.NewClient(ctx, option.WithCredentialsFile(credentials))
	if err != nil {
		return nil, fmt.Errorf("gdrive: failed to create client: %w", err)
	}
	target.client = client

	return target, nil
}

// Name returns the name of this target
func (t *GDriveTarget) Name() string {
	return "gdrive"
}

// Store implements the backup.Target interface
func (t *GDriveTarget) Store(ctx context.Context, sourcePath string, metadata *backup.Metadata) error {
	if t.debug {
		t.logger.Printf("GDrive: Storing backup %s to bucket %s", filepath.Base(sourcePath), t.bucketName)
	}

	// Open source file
	srcFile, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("gdrive: failed to open source file: %w", err)
	}
	defer srcFile.Close()

	// Create the object path
	objectPath := path.Join(t.basePath, filepath.Base(sourcePath))
	obj := t.client.Bucket(t.bucketName).Object(objectPath)

	// Create a new bucket writer
	writer := obj.NewWriter(ctx)

	// Set metadata
	writer.Metadata = map[string]string{
		"timestamp": metadata.Timestamp.Format(time.RFC3339),
		"type":      metadata.Type,
		"source":    metadata.Source,
		"is_daily":  fmt.Sprintf("%v", metadata.IsDaily),
	}

	// Copy the data
	if _, err := io.Copy(writer, srcFile); err != nil {
		return fmt.Errorf("gdrive: failed to copy data: %w", err)
	}

	// Close the writer
	if err := writer.Close(); err != nil {
		return fmt.Errorf("gdrive: failed to close writer: %w", err)
	}

	if t.debug {
		t.logger.Printf("GDrive: Successfully stored backup %s", filepath.Base(sourcePath))
	}

	return nil
}

// List implements the backup.Target interface
func (t *GDriveTarget) List(ctx context.Context) ([]backup.BackupInfo, error) {
	if t.debug {
		t.logger.Printf("GDrive: Listing backups from bucket %s", t.bucketName)
	}

	var backups []backup.BackupInfo
	it := t.client.Bucket(t.bucketName).Objects(ctx, &storage.Query{
		Prefix: t.basePath + "/",
	})

	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("gdrive: failed to list objects: %w", err)
		}

		// Skip directories
		if strings.HasSuffix(attrs.Name, "/") {
			continue
		}

		// Parse metadata
		timestamp, _ := time.Parse(time.RFC3339, attrs.Metadata["timestamp"])
		isDaily := false
		if isDailyStr, ok := attrs.Metadata["is_daily"]; ok {
			isDaily, _ = strconv.ParseBool(isDailyStr)
		}

		backups = append(backups, backup.BackupInfo{
			Target: path.Base(attrs.Name),
			Metadata: backup.Metadata{
				Timestamp: timestamp,
				Size:      attrs.Size,
				Type:      attrs.Metadata["type"],
				Source:    attrs.Metadata["source"],
				IsDaily:   isDaily,
			},
		})
	}

	return backups, nil
}

// Delete implements the backup.Target interface
func (t *GDriveTarget) Delete(ctx context.Context, target string) error {
	if t.debug {
		t.logger.Printf("GDrive: Deleting backup %s from bucket %s", target, t.bucketName)
	}

	objectPath := path.Join(t.basePath, target)
	obj := t.client.Bucket(t.bucketName).Object(objectPath)

	if err := obj.Delete(ctx); err != nil {
		return fmt.Errorf("gdrive: failed to delete object: %w", err)
	}

	if t.debug {
		t.logger.Printf("GDrive: Successfully deleted backup %s", target)
	}

	return nil
}

// Validate checks if the target configuration is valid
func (t *GDriveTarget) Validate() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Check if credentials file exists
	if _, err := os.Stat(t.credentials); err != nil {
		return fmt.Errorf("gdrive: credentials file not found: %w", err)
	}

	// Check if we can access the bucket
	bucket := t.client.Bucket(t.bucketName)
	if _, err := bucket.Attrs(ctx); err != nil {
		return fmt.Errorf("gdrive: failed to access bucket: %w", err)
	}

	// Try to create a test object
	testObj := bucket.Object(path.Join(t.basePath, ".write_test"))
	writer := testObj.NewWriter(ctx)
	if _, err := writer.Write([]byte("test")); err != nil {
		return fmt.Errorf("gdrive: failed to write test object: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("gdrive: failed to close test object: %w", err)
	}

	// Clean up the test object
	if err := testObj.Delete(ctx); err != nil {
		t.logger.Printf("Warning: failed to delete test object: %v", err)
	}

	return nil
}
