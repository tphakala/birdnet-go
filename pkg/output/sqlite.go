package output

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/glebarez/go-sqlite"
)

/*
func main() {
	const dbFilePath = "./test.db"
	const tableName = "observations"

	// Open an existing database or create a new one if it doesn't exist
	db := OpenDatabase(dbFilePath)
	defer db.Close()

	// Check if the table exists and create it if not
	if !tableExists(db, tableName) {
		createTableStructure(db)
		fmt.Println("Table initialized successfully!")
	} else {
		fmt.Println("Table already exists, skipping initialization.")
	}
}*/

const tableName = "observations"

// OpenDatabase tries to open an existing SQLite database or initializes a new one if it doesn't exist
func OpenDatabase(filePath string) *sql.DB {
	// If database file doesn't exist, initialize a new one
	if !databaseExists(filePath) {
		fmt.Println("Database not found. Creating new one...")
		initDatabase(filePath)
	}

	// Open the database connection
	db, err := sql.Open("sqlite", filePath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}

	// Check if the table exists and create it if not
	if !tableExists(db, tableName) {
		createTableStructure(db)
		fmt.Println("Table initialized successfully!")
	} else {
		fmt.Println("Table already exists, skipping initialization.")
	}

	return db
}

// databaseExists checks if the SQLite database file already exists
func databaseExists(filePath string) bool {
	_, err := os.Stat(filePath)
	return !os.IsNotExist(err)
}

// tableExists checks if the specified table already exists in the SQLite database
func tableExists(db *sql.DB, tableName string) bool {
	var name string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", tableName).Scan(&name)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		log.Fatalf("Failed to query for table: %v", err)
	}
	return true
}

// initDatabase creates a new SQLite database file
func initDatabase(filePath string) {
	file, err := os.Create(filePath)
	if err != nil {
		log.Fatalf("Failed to create database file: %v", err)
	}
	file.Close()
}

// createTableStructure sets up the table structure in the SQLite database
func createTableStructure(db *sql.DB) {
	const sqlStmt = `
	CREATE TABLE observations (
		date DATE,
		time TIME,
		scientificName VARCHAR(128),
		commonName VARCHAR(128),
		confidence FLOAT,
		latitude FLOAT,
		longitude FLOAT,
		threshold FLOAT,
		sensitivity FLOAT,
		clipName VARCHAR(128)
	);
	`
	_, err := db.Exec(sqlStmt)
	if err != nil {
		log.Fatalf("Failed to create table: %v", err)
	}

	// Define indexes for optimal query performance
	indexes := []string{
		"CREATE INDEX idx_date ON observations(date);",
		"CREATE INDEX idx_time ON observations(time);",
		"CREATE INDEX idx_commonName ON observations(commonName);",
	}
	for _, idx := range indexes {
		_, err := db.Exec(idx)
		if err != nil {
			log.Fatalf("Failed to create index: %v", err)
		}
	}
}
