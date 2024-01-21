package observation

import (
	"fmt"
	"log"
	"path/filepath"

	"github.com/tphakala/birdnet-go/internal/config"
	"github.com/tphakala/birdnet-go/internal/model"
	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// InitializeDatabase sets up the database connection using the provided configuration.
// It defaults to SQLite if both SQLite and MySQL are enabled.
func InitializeDatabase(ctx *config.Context) error {
	var err error

	// Prioritize SQLite initialization
	if ctx.Settings.Output.SQLite.Enabled {
		// Separate the directory and file name from the SQLite path
		dir, fileName := filepath.Split(ctx.Settings.Output.SQLite.Path)

		// Expand the directory path to an absolute path
		basePath := config.GetBasePath(dir)

		// Recombine to form the full absolute path of the log file
		absoluteFilePath := filepath.Join(basePath, fileName)

		ctx.Db, err = gorm.Open(sqlite.Open(absoluteFilePath), &gorm.Config{})
		if err != nil {
			return fmt.Errorf("failed to open SQLite database: %v", err)
		} else if ctx.Settings.Debug {
			log.Println("SQLite database connection initialized:", absoluteFilePath)
		}

		// Perform SQLite migration
		if err = ctx.Db.AutoMigrate(&model.Note{}); err != nil {
			return fmt.Errorf("failed to auto-migrate SQLite database: %v", err)
		}

		return nil
	}

	// Initialize MySQL if SQLite is not enabled
	if ctx.Settings.Output.MySQL.Enabled {
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
			ctx.Settings.Output.MySQL.Username, ctx.Settings.Output.MySQL.Password, ctx.Settings.Output.MySQL.Host, ctx.Settings.Output.MySQL.Port, ctx.Settings.Output.MySQL.Database)
		ctx.Db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
		if err != nil {
			return fmt.Errorf("failed to open MySQL database: %v", err)
		}

		// Perform MySQL migration
		if err = ctx.Db.AutoMigrate(&model.Note{}); err != nil {
			return fmt.Errorf("failed to auto-migrate MySQL database: %v", err)
		}
	}

	return nil
}

// SaveToDatabase inserts a new Note record into the database.
func SaveToDatabase(ctx *config.Context, note model.Note) error {
	// Initialize the database if it's not already connected.
	if ctx.Db == nil {
		if err := InitializeDatabase(ctx); err != nil {
			// Return the error encountered during database initialization.
			return fmt.Errorf("failed to initialize database for saving note: %v", err)
		}
	}

	// Insert the new Note record into the database.
	if err := ctx.Db.Create(&note).Error; err != nil {
		log.Printf("Failed to save note: %v\n", err)
		return err
	}

	if ctx.Settings.Debug {
		log.Printf("Saved note: %v\n", note)
	}

	return nil
}
