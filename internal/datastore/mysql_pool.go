package datastore

import "time"

// MySQL connection pool defaults shared by both v1 and v2 stores.
// Tuned for BirdNET-Go's typical workload (periodic detections, weather
// updates, daily events). Conservative relative to MySQL's default
// max_connections (151), leaving headroom for admin tools and other clients.
//
// During migration both stores are active against the same server, so the
// combined maximum is 2 * MySQLMaxOpenConns = 50 -- well within the 151
// default limit.
const (
	MySQLMaxOpenConns    = 25
	MySQLMaxIdleConns    = 10
	MySQLConnMaxLifetime = 5 * time.Minute
	MySQLConnMaxIdleTime = 3 * time.Minute
)
