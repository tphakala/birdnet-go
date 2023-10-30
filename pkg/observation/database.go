package observation

import (
	"errors"
	"fmt"

	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var (
	db *gorm.DB
)

type DatabaseConfig struct {
	Type     string // "sqlite", "mysql"
	Host     string
	Port     string
	User     string
	Password string
	Name     string // database name
}

func InitializeDatabase(config DatabaseConfig) error {
	var err error

	switch config.Type {
	case "sqlite":
		db, err = gorm.Open(sqlite.Open("./birdnet.db"), &gorm.Config{})
	case "mysql":
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
			config.User, config.Password, config.Host, config.Port, config.Name)
		db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
	default:
		return errors.New("unsupported database type")
	}

	if err != nil {
		return err
	}

	err = db.AutoMigrate(&Note{})
	if err != nil {
		return err
	}

	return nil
}

func SaveToDatabase(note Note) error {
	if db == nil {
		return errors.New("database not initialized")
	}

	return db.Create(&note).Error
}
