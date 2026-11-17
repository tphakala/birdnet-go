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
 * Legacy-schema notice kind reported by the migration status endpoint.
 * 'none' unless the install still runs the legacy database schema.
 */
export type LegacyNoticeKind =
  'none' | 'not_started' | 'in_progress' | 'needs_attention' | 'restart_required';

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
  can_retry_validation: boolean;
  can_cancel: boolean;
  dirty_id_count: number;
  error_message?: string;
  is_v2_only_mode?: boolean;
  legacy_notice?: LegacyNoticeKind;

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
 * State of the background prerequisites run. "failed" means the run itself did not
 * finish (time limit, shutdown, internal error); failing checks still give "completed".
 */
export type PrerequisitesRunStatus = 'idle' | 'running' | 'completed' | 'failed';

/**
 * Progress of a running prerequisites run
 */
export interface PrerequisitesProgress {
  completed_steps: number;
  total_steps: number;
  current_check_id?: string;
  current_check_name?: string;
  /** Computed by the server when the snapshot is taken */
  current_check_elapsed_ms?: number;
}

/**
 * Snapshot of the prerequisites run from the API. While a run is in progress
 * `checks` holds the checks finished so far and `can_start_migration` is false.
 */
export interface PrerequisitesResponse {
  all_passed: boolean;
  can_start_migration: boolean;
  checks: PrerequisiteCheck[];
  critical_failures: number;
  warnings: number;
  /** Completion time of the run; absent until a run completes */
  checked_at?: string;
  status: PrerequisitesRunStatus;
  run_id: number;
  started_at?: string;
  /** Present only while status is "running" */
  progress?: PrerequisitesProgress;
  /** Why a "failed" run did not finish */
  error?: string;
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
