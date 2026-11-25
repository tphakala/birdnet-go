/** Status of a diagnostics check or of a whole report. */
export type HealthStatus = 'healthy' | 'warning' | 'critical' | 'unknown' | 'skipped';

/** The area a diagnostics check belongs to. */
export type HealthCategory =
  'system' | 'audio' | 'analysis' | 'streams' | 'database' | 'network' | 'config' | 'logs';

/** One diagnostics check result. */
export interface DiagnosticsResult {
  name: string;
  category: HealthCategory;
  status: HealthStatus;
  message: string;
  details?: Record<string, unknown>;
  duration_ms: number;
  timestamp: string;
}

/** A diagnostics run: every check result plus counts. */
export interface DiagnosticsReport {
  id: string;
  status: HealthStatus;
  started_at: string;
  completed_at: string;
  duration_ms: number;
  total_checks: number;
  results: DiagnosticsResult[];
  summary: Record<string, HealthStatus>;
  count_by_status: Record<string, number>;
}
