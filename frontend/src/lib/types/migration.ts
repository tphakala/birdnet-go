// Migration Type Definitions
//
// Shared types for database migration UI components.
// Used by: DatabaseManagement, MigrationControlCard, PrerequisitesChecklist

/**
 * Database statistics returned from the API
 */
export interface DatabaseStats {
  type: string;
  location: string;
  size_bytes: number;
  total_detections: number;
  connected: boolean;
}

/**
 * Migration status returned from the API
 */
export interface MigrationStatus {
  state: string;
  current_phase?: string;
  phase_number?: number;
  total_phases?: number;
  total_records: number;
  migrated_records: number;
  progress_percent: number;
  records_per_second?: number;
  estimated_remaining?: string;
  worker_running: boolean;
  worker_paused: boolean;
  can_start: boolean;
  can_pause: boolean;
  can_resume: boolean;
  can_cancel: boolean;
  dirty_id_count: number;
  error_message?: string;
  is_v2_only_mode?: boolean;

  // Cleanup state fields
  cleanup_state?: string;
  cleanup_error?: string;
  cleanup_tables_remaining?: string[];
  cleanup_space_reclaimed?: number;
}

/**
 * Status values for prerequisite checks
 */
export type PrerequisiteCheckStatus = 'passed' | 'failed' | 'warning' | 'skipped' | 'error';

/**
 * Severity levels for prerequisite checks
 */
export type PrerequisiteCheckSeverity = 'critical' | 'warning';

/**
 * Single prerequisite check result
 */
export interface PrerequisiteCheck {
  id: string;
  name: string;
  description: string;
  status: PrerequisiteCheckStatus;
  message: string;
  severity: PrerequisiteCheckSeverity;
}

/**
 * Full prerequisites response from the API
 */
export interface PrerequisitesResponse {
  all_passed: boolean;
  can_start_migration: boolean;
  checks: PrerequisiteCheck[];
  critical_failures: number;
  warnings: number;
  checked_at: string;
}

/**
 * State wrapper for prerequisites data with loading/error states
 */
export interface PrerequisitesState {
  loading: boolean;
  error: string | null;
  data: PrerequisitesResponse | null;
}

/**
 * Generic API state wrapper for async data
 */
export interface ApiState<T> {
  loading: boolean;
  error: string | null;
  data: T | null;
}
