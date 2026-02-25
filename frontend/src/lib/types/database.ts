// Database Overview API Types
//
// Matches GET /api/v2/system/database/overview response.
// See: internal/api/v2/database_overview.go, internal/datastore/interfaces_inspector.go

/** SQLite engine-specific details */
export interface SQLiteDetails {
  engine_version: string;
  journal_mode: string;
  page_size: number;
  integrity_check: string;
  last_vacuum_at: string | null;
  wal_size_bytes: number;
  wal_checkpoints: number;
  freelist_pages: number;
  cache_size_bytes: number;
  busy_timeouts: number;
}

/** MySQL engine-specific details */
export interface MySQLDetails {
  active_connections: number;
  idle_connections: number;
  max_connections: number;
  threads_idle: number;
  total_created: number;
  connection_errors: number;
  threads_running: number;
  threads_cached: number;
  buffer_pool_hit_rate: number;
  buffer_pool_size_bytes: number;
  lock_waits: number;
  deadlocks: number;
  avg_lock_wait_ms: number;
  table_locks_waited: number;
  table_locks_immediate: number;
}

/** Per-table statistics */
export interface TableStats {
  name: string;
  row_count: number;
  size_bytes: number;
  usage_pct: number;
  engine: string; // MySQL only; empty for SQLite
}

/** Current performance snapshot */
export interface PerformanceStats {
  read_latency_avg_ms: number;
  read_latency_max_ms: number;
  write_latency_avg_ms: number;
  write_latency_max_ms: number;
  queries_per_sec: number;
  queries_last_hour: number;
  slow_query_count: number;
}

/** Hourly detection count (24h histogram) */
export interface HourlyCount {
  hour: string; // ISO 8601: "2026-02-24T14:00:00Z"
  count: number;
}

/** Top-level database overview response */
export interface DatabaseOverviewResponse {
  engine: 'sqlite' | 'mysql';
  status: string;
  location: string;
  size_bytes: number;
  total_detections: number;
  total_tables: number;
  sqlite?: SQLiteDetails;
  mysql?: MySQLDetails;
  tables: TableStats[] | null;
  performance: PerformanceStats;
  detection_rate_24h: HourlyCount[] | null;
}
