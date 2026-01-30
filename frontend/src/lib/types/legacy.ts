// Legacy Database Type Definitions
//
// Types for legacy database cleanup UI components.

/**
 * Legacy table information (MySQL)
 */
export interface LegacyTableInfo {
  name: string;
  size_bytes: number;
  row_count: number;
}

/**
 * Legacy database status returned from the API
 */
export interface LegacyStatus {
  exists: boolean;
  can_cleanup: boolean;
  reason: string;
  size_bytes: number;
  total_records: number;
  last_modified: string | null;
  location: string;
  tables: LegacyTableInfo[];
}

/**
 * Cleanup action response
 */
export interface CleanupActionResponse {
  success: boolean;
  message: string;
}
